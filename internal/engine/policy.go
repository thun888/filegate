package engine

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/thun888/filegate/config"
	"github.com/thun888/filegate/internal/backend"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	circuitMetricsOnce          sync.Once
	circuitOpenTotal            *prometheus.CounterVec
	circuitHalfOpenSuccessTotal *prometheus.CounterVec
	circuitRequestRejectedTotal *prometheus.CounterVec
)

// PolicyEngine 负责根据策略选择后端并执行访问。
type PolicyEngine struct {
	mu              sync.Mutex // 互斥锁保护以下字段
	rrState         map[string]int
	circuitBreakers map[string]*circuitBreaker
}

func NewPolicyEngine() *PolicyEngine {
	initCircuitMetrics()

	return &PolicyEngine{
		rrState:         make(map[string]int),
		circuitBreakers: make(map[string]*circuitBreaker),
	}
}

func initCircuitMetrics() {
	// sync.Once 保证指标只注册一次，避免重复注册导致的 panic
	circuitMetricsOnce.Do(func() {
		circuitOpenTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "filegate",
			Subsystem: "circuit_breaker",
			Name:      "open_total",
			Help:      "Total number of circuit breaker open transitions.",
		}, []string{"backend", "from_state"})

		circuitHalfOpenSuccessTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "filegate",
			Subsystem: "circuit_breaker",
			Name:      "half_open_success_total",
			Help:      "Total number of successful half-open probe requests.",
		}, []string{"backend"})

		circuitRequestRejectedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "filegate",
			Subsystem: "circuit_breaker",
			Name:      "request_rejected_total",
			Help:      "Total number of requests rejected by circuit breaker state.",
		}, []string{"backend", "state"})

		registerCollector(circuitOpenTotal)
		registerCollector(circuitHalfOpenSuccessTotal)
		registerCollector(circuitRequestRejectedTotal)
	})
}

func registerCollector(c prometheus.Collector) {
	if err := prometheus.Register(c); err != nil {
		if alreadyRegisteredErr, ok := err.(prometheus.AlreadyRegisteredError); ok {
			switch registered := alreadyRegisteredErr.ExistingCollector.(type) {
			case *prometheus.CounterVec:
				switch c {
				case circuitOpenTotal:
					circuitOpenTotal = registered
				case circuitHalfOpenSuccessTotal:
					circuitHalfOpenSuccessTotal = registered
				case circuitRequestRejectedTotal:
					circuitRequestRejectedTotal = registered
				}
			}
		}
	}
}

// 后端熔断器状态
type circuitState string

const (
	circuitStateClosed   circuitState = "closed"
	circuitStateOpen     circuitState = "open"
	circuitStateHalfOpen circuitState = "half_open"
)

// circuitBreaker 实现熔断器，用于在后端连续失败时快速拒绝请求，避免雪崩。
// 状态流转：closed → open → half_open → closed。
type circuitBreaker struct {
	cfg              config.CircuitBreakerConfig // 熔断器配置参数
	state            circuitState                // 当前状态：closed/open/half_open
	failureCount     int                         // 连续失败计数
	openedAt         time.Time                   // 进入 open 状态的时间
	halfOpenAt       time.Time                   // 进入 half_open 状态的时间
	halfOpenProbeRun bool                        // half_open 状态下是否已发起探测请求
}

func (e *PolicyEngine) RegisterBackend(name string, cfg config.CircuitBreakerConfig) {
	cfg = normalizeCircuitConfig(cfg)
	if cfg.FailureThreshold <= 0 {
		return
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	e.circuitBreakers[config.NormalizeKey(name)] = &circuitBreaker{
		cfg:   cfg,
		state: circuitStateClosed,
	}
}

// OrderedBackends 根据策略配置对后端列表进行排序，返回按策略确定的优先顺序排列的后端切片。
// 支持的策略: single（仅使用第一个）、fallback/priority（按配置顺序）、round_robin（轮转调度）、random（随机排序）。
// 若策略中引用的后端不存在，返回错误。
func (e *PolicyEngine) OrderedBackends(policy config.BackendPolicy, allBackends map[string]backend.Backend) ([]backend.Backend, error) {

	// 	backend_policy:
	//   - name: "policy1"
	//     strategy: fallback # single | fallback | round_robin | random | priority
	//     backends:
	//       - backend1
	//       - backend2

	candidates := make([]backend.Backend, 0, len(policy.Backends))
	for _, backendName := range policy.Backends {
		if b, ok := allBackends[config.NormalizeKey(backendName)]; ok {
			candidates = append(candidates, b)
		} else {
			return nil, fmt.Errorf("backend %q not found for policy %q", backendName, policy.Name)
		}
	}

	// if len(candidates) == 0 {
	// 	return nil, fmt.Errorf("no available backend for policy %q", policy.Name)
	// }

	switch config.NormalizeKey(policy.Strategy) {
	case "", "single":
		return candidates[:1], nil
	case "fallback", "priority":
		return candidates, nil
	case "round_robin":
		e.mu.Lock()
		key := config.NormalizeKey(policy.Name)
		start := e.rrState[key] % len(candidates)
		e.rrState[key] = (start + 1) % len(candidates)
		e.mu.Unlock()

		// 轮转
		ordered := make([]backend.Backend, 0, len(candidates))
		ordered = append(ordered, candidates[start:]...)
		ordered = append(ordered, candidates[:start]...)
		return ordered, nil
	case "random":
		perm := rand.Perm(len(candidates))

		ordered := make([]backend.Backend, 0, len(candidates))
		for _, idx := range perm {
			ordered = append(ordered, candidates[idx])
		}
		return ordered, nil
	default:
		return candidates, nil
	}
}

// FetchWithPolicy 按策略确定的后端顺序依次尝试获取对象，任一成功即返回。
// 每次请求前经过熔断器检查，成功/失败分别记录熔断状态；所有后端均失败时返回所有的错误。
func (e *PolicyEngine) FetchWithPolicy(ctx context.Context, policy config.BackendPolicy, allBackends map[string]backend.Backend, objectPath string) (*backend.Object, error) {
	ordered, err := e.OrderedBackends(policy, allBackends)
	if err != nil {
		return nil, err
	}

	var joinedErr error
	for _, b := range ordered {
		if breakerErr := e.beforeRequest(b.Name()); breakerErr != nil {
			joinedErr = errors.Join(joinedErr, fmt.Errorf("backend %q: %w", b.Name(), breakerErr))
			continue
		}

		obj, fetchErr := b.Fetch(ctx, objectPath)
		if fetchErr == nil {
			e.recordSuccess(b.Name())
			return obj, nil
		}

		e.recordFailure(b.Name())
		// 待优化，不应该直接把后端错误暴露，应仅在调试模式下才返回详细错误，生产环境下返回一个通用错误，并在日志中记录详细错误
		joinedErr = errors.Join(joinedErr, fmt.Errorf("backend %q: %w", b.Name(), fetchErr))
	}

	return nil, fmt.Errorf("all backends failed for policy %q: %w", policy.Name, joinedErr)
}

// 检查是否可以发送请求，正常时保持静默，否则记录被拒绝的请求数并返回错误
func (e *PolicyEngine) beforeRequest(backendName string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	b, exists := e.circuitBreakers[config.NormalizeKey(backendName)]
	if !exists {
		return nil
	}

	now := time.Now()

	switch b.state {
	case circuitStateClosed:
		return nil
	case circuitStateOpen:
		// time.Time 类型没有减法运算符，需要使用 Sub 方法计算时间差
		if now.Sub(b.openedAt) < b.cfg.RecoveryTimeout {
			// prometheus.CounterVec 的 WithLabelValues 方法返回一个 prometheus.Counter 接口，调用 Inc 方法增加计数
			circuitRequestRejectedTotal.WithLabelValues(config.NormalizeKey(backendName), string(circuitStateOpen)).Inc()
			return fmt.Errorf("circuit breaker is open")
		}
		// 超过恢复时间后进入半开状态，允许尝试请求
		b.state = circuitStateHalfOpen
		b.halfOpenAt = now
		b.halfOpenProbeRun = false
		// Go 里 case 语句会自动break，使用 fallthrough 关键字继续执行下一个 case
		fallthrough
	case circuitStateHalfOpen:
		if now.Sub(b.halfOpenAt) >= b.cfg.HalfOpenTimeout {
			b.state = circuitStateOpen
			b.openedAt = now
			b.halfOpenProbeRun = false
			circuitRequestRejectedTotal.WithLabelValues(config.NormalizeKey(backendName), string(circuitStateOpen)).Inc()
			return fmt.Errorf("circuit breaker is open")
		}

		if b.halfOpenProbeRun {
			circuitRequestRejectedTotal.WithLabelValues(config.NormalizeKey(backendName), string(circuitStateHalfOpen)).Inc()
			return fmt.Errorf("circuit breaker probe is in flight")
		}

		b.halfOpenProbeRun = true
		return nil
	default:
		return nil
	}
}

// recordSuccess 记录后端请求成功，将熔断器重置为关闭状态。
func (e *PolicyEngine) recordSuccess(backendName string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	b, exists := e.circuitBreakers[config.NormalizeKey(backendName)]
	if !exists {
		return
	}

	if b.state == circuitStateHalfOpen {
		circuitHalfOpenSuccessTotal.WithLabelValues(config.NormalizeKey(backendName)).Inc()
	}

	b.state = circuitStateClosed
	b.failureCount = 0
	b.halfOpenProbeRun = false
	b.openedAt = time.Time{} // 重置时间，表示未进入 open 状态，（占位符，公元1年元旦）
	b.halfOpenAt = time.Time{}
}

// recordFailure 记录后端请求失败，根据当前状态推进熔断器：
// closed 时累加失败计数，达到阈值则转为 open；
// half_open 时探测失败直接回到 open；
// open 时重置超时起点。
func (e *PolicyEngine) recordFailure(backendName string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	b, exists := e.circuitBreakers[config.NormalizeKey(backendName)]
	if !exists {
		return
	}

	now := time.Now()

	switch b.state {
	case circuitStateHalfOpen:
		circuitOpenTotal.WithLabelValues(config.NormalizeKey(backendName), string(circuitStateHalfOpen)).Inc()
		b.state = circuitStateOpen
		b.openedAt = now
		b.failureCount = b.cfg.FailureThreshold
		b.halfOpenProbeRun = false
	case circuitStateClosed:
		b.failureCount++
		if b.failureCount >= b.cfg.FailureThreshold {
			circuitOpenTotal.WithLabelValues(config.NormalizeKey(backendName), string(circuitStateClosed)).Inc()
			b.state = circuitStateOpen
			b.openedAt = now
			b.halfOpenProbeRun = false
		}
	case circuitStateOpen:
		b.openedAt = now
	}
}

// normalizeCircuitConfig 确保熔断器配置中的时间参数合理，若未设置或设置为非正数则使用默认值。
func normalizeCircuitConfig(cfg config.CircuitBreakerConfig) config.CircuitBreakerConfig {
	if cfg.RecoveryTimeout <= 0 {
		cfg.RecoveryTimeout = 30 * time.Second
	}
	if cfg.HalfOpenTimeout <= 0 {
		cfg.HalfOpenTimeout = 10 * time.Second
	}

	return cfg
}

