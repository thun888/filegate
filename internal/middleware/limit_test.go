package middleware

import (
	"testing"
	"time"

	"filegate/config"
)

func TestRateLimiter_CleanupExpiredBuckets(t *testing.T) {
	l := NewRateLimiter()
	defer l.Stop()

	cfg := config.LimitConfig{
		Enabled:     true,
		MaxRequests: 2,
		Window:      time.Second,
	}

	if !l.Allow("ip:ns:class", cfg) {
		t.Fatalf("first request should pass")
	}

	l.mu.Lock()
	bucket, exists := l.buckets["ip:ns:class"]
	if !exists {
		l.mu.Unlock()
		t.Fatalf("bucket should exist")
	}
	bucket.lastSeen = time.Now().Add(-5 * time.Minute)
	l.mu.Unlock()

	l.cleanupExpiredBuckets()

	l.mu.Lock()
	_, exists = l.buckets["ip:ns:class"]
	l.mu.Unlock()

	if exists {
		t.Fatalf("expired bucket should be cleaned up")
	}
}
