package middleware

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"filegate/config"

	"github.com/redis/go-redis/v9"
)

type rateBucket struct {
	windowStart time.Time
	count       int
	window      time.Duration
	lastSeen    time.Time
}

// RateLimitResult 表示一次限流判断的详细结果。
type RateLimitResult struct {
	Allowed    bool
	Limit      int
	Remaining  int
	ResetAfter time.Duration
	Source     string
}

// RateLimiter 是一个轻量的固定窗口限流器。
type RateLimiter struct {
	mu          sync.Mutex
	buckets     map[string]*rateBucket
	cleanupTick *time.Ticker
	stopCleanup chan struct{}
	redisClient *redis.Client
	redisPrefix string
}

const (
	defaultWindow          = time.Minute
	defaultCleanupInterval = time.Minute
	defaultLimiterPrefix   = "filegate:limit:"
)

func NewRateLimiter() *RateLimiter {
	l := &RateLimiter{
		buckets:     make(map[string]*rateBucket),
		cleanupTick: time.NewTicker(defaultCleanupInterval),
		stopCleanup: make(chan struct{}),
	}

	go l.cleanupLoop()
	return l
}

// NewRateLimiterWithRedis 创建可选 Redis 后端的限流器。
func NewRateLimiterWithRedis(cfg config.RedisConfig) (*RateLimiter, error) {
	l := NewRateLimiter()
	if !cfg.Enabled {
		return l, nil
	}

	client := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		l.Stop()
		return nil, fmt.Errorf("connect redis limiter: %w", err)
	}

	keyPrefix := cfg.KeyPrefix
	if keyPrefix == "" {
		keyPrefix = defaultLimiterPrefix
	}

	l.redisClient = client
	l.redisPrefix = keyPrefix
	return l, nil
}

func (l *RateLimiter) Stop() {
	if l == nil {
		return
	}

	select {
	case <-l.stopCleanup:
	default:
		close(l.stopCleanup)
	}

	if l.cleanupTick != nil {
		l.cleanupTick.Stop()
	}

	if l.redisClient != nil {
		_ = l.redisClient.Close()
	}
}

func (l *RateLimiter) Allow(key string, cfg config.LimitConfig) bool {
	return l.Check(key, cfg).Allowed
}

func (l *RateLimiter) Check(key string, cfg config.LimitConfig) RateLimitResult {
	if !cfg.Enabled {
		return RateLimitResult{Allowed: true, Source: "disabled"}
	}

	maxRequests := cfg.MaxRequests
	if maxRequests <= 0 {
		maxRequests = 1
	}

	window := cfg.Window
	if window <= 0 {
		window = defaultWindow
	}

	if l.redisClient != nil {
		result, err := l.allowRedis(key, maxRequests, window)
		if err == nil {
			result.Limit = maxRequests
			result.Source = "redis"
			return result
		}
		// Redis 故障时回退到本地内存限流，避免请求全部失败。
	}

	now := time.Now()

	l.mu.Lock()
	defer l.mu.Unlock()

	bucket, exists := l.buckets[key]
	if !exists {
		l.buckets[key] = &rateBucket{windowStart: now, count: 1, window: window, lastSeen: now}
		return RateLimitResult{
			Allowed:    true,
			Limit:      maxRequests,
			Remaining:  maxInt(maxRequests-1, 0),
			ResetAfter: window,
			Source:     "local",
		}
	}

	bucket.window = window
	bucket.lastSeen = now

	if now.Sub(bucket.windowStart) >= window {
		bucket.windowStart = now
		bucket.count = 1
		return RateLimitResult{
			Allowed:    true,
			Limit:      maxRequests,
			Remaining:  maxInt(maxRequests-1, 0),
			ResetAfter: window,
			Source:     "local",
		}
	}

	resetAfter := window - now.Sub(bucket.windowStart)
	if resetAfter < 0 {
		resetAfter = 0
	}

	if bucket.count >= maxRequests {
		return RateLimitResult{
			Allowed:    false,
			Limit:      maxRequests,
			Remaining:  0,
			ResetAfter: resetAfter,
			Source:     "local",
		}
	}

	bucket.count++
	return RateLimitResult{
		Allowed:    true,
		Limit:      maxRequests,
		Remaining:  maxInt(maxRequests-bucket.count, 0),
		ResetAfter: resetAfter,
		Source:     "local",
	}
}

var redisIncrScript = redis.NewScript(`
local current = redis.call("INCR", KEYS[1])
if current == 1 then
  redis.call("PEXPIRE", KEYS[1], ARGV[1])
end
local ttl = redis.call("PTTL", KEYS[1])
return {current, ttl}
`)

func (l *RateLimiter) allowRedis(key string, maxRequests int, window time.Duration) (RateLimitResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	fullKey := l.redisPrefix + key
	windowMs := window.Milliseconds()
	if windowMs <= 0 {
		windowMs = defaultWindow.Milliseconds()
	}

	values, err := redisIncrScript.Run(ctx, l.redisClient, []string{fullKey}, windowMs).Slice()
	if err != nil {
		return RateLimitResult{}, err
	}

	if len(values) != 2 {
		return RateLimitResult{}, fmt.Errorf("unexpected redis script result length: %d", len(values))
	}

	current, err := toInt64(values[0])
	if err != nil {
		return RateLimitResult{}, fmt.Errorf("decode redis current count: %w", err)
	}

	ttlMs, err := toInt64(values[1])
	if err != nil {
		return RateLimitResult{}, fmt.Errorf("decode redis ttl: %w", err)
	}

	if ttlMs < 0 {
		ttlMs = 0
	}

	remaining := int64(maxRequests) - current
	if remaining < 0 {
		remaining = 0
	}

	return RateLimitResult{
		Allowed:    current <= int64(maxRequests),
		Remaining:  int(remaining),
		ResetAfter: time.Duration(ttlMs) * time.Millisecond,
	}, nil
}

func (l *RateLimiter) cleanupLoop() {
	for {
		select {
		case <-l.cleanupTick.C:
			l.cleanupExpiredBuckets()
		case <-l.stopCleanup:
			return
		}
	}
}

func (l *RateLimiter) cleanupExpiredBuckets() {
	now := time.Now()

	l.mu.Lock()
	defer l.mu.Unlock()

	for key, bucket := range l.buckets {
		window := bucket.window
		if window <= 0 {
			window = defaultWindow
		}
		expireAfter := window * 3
		if expireAfter < 3*time.Minute {
			expireAfter = 3 * time.Minute
		}

		if now.Sub(bucket.lastSeen) >= expireAfter {
			delete(l.buckets, key)
		}
	}
}

func toInt64(value any) (int64, error) {
	switch v := value.(type) {
	case int64:
		return v, nil
	case int:
		return int64(v), nil
	case string:
		return strconv.ParseInt(v, 10, 64)
	case []byte:
		return strconv.ParseInt(string(v), 10, 64)
	default:
		return 0, fmt.Errorf("unsupported numeric type %T", value)
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}

	return b
}
