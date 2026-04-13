package engine

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"filegate/config"
	"filegate/internal/backend"

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

type circuitState string

const (
	circuitStateClosed   circuitState = "closed"
	circuitStateOpen     circuitState = "open"
	circuitStateHalfOpen circuitState = "half_open"
)

type circuitBreaker struct {
	cfg              config.CircuitBreakerConfig
	state            circuitState
	failureCount     int
	openedAt         time.Time
	halfOpenAt       time.Time
	halfOpenProbeRun bool
}

func (e *PolicyEngine) RegisterBackend(name string, cfg config.CircuitBreakerConfig) {
	cfg = normalizeCircuitConfig(cfg)
	if cfg.FailureThreshold <= 0 {
		return
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	e.circuitBreakers[normalizeKey(name)] = &circuitBreaker{
		cfg:   cfg,
		state: circuitStateClosed,
	}
}

func (e *PolicyEngine) OrderedBackends(policy config.BackendPolicy, all map[string]backend.Backend) ([]backend.Backend, error) {
	candidates := make([]backend.Backend, 0, len(policy.Backends))
	for _, backendName := range policy.Backends {
		if b, ok := all[normalizeKey(backendName)]; ok {
			candidates = append(candidates, b)
		}
	}

	if len(candidates) == 0 {
		return nil, fmt.Errorf("no available backend for policy %q", policy.Name)
	}

	switch normalizeKey(policy.Strategy) {
	case "", "single":
		return candidates[:1], nil
	case "fallback", "priority":
		return candidates, nil
	case "round_robin":
		e.mu.Lock()
		key := normalizeKey(policy.Name)
		start := e.rrState[key] % len(candidates)
		e.rrState[key] = (start + 1) % len(candidates)
		e.mu.Unlock()

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

func (e *PolicyEngine) FetchWithPolicy(ctx context.Context, policy config.BackendPolicy, all map[string]backend.Backend, objectPath string) (*backend.Object, error) {
	ordered, err := e.OrderedBackends(policy, all)
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
		// 待优化，不应该直接把后端错误暴露
		joinedErr = errors.Join(joinedErr, fmt.Errorf("backend %q: %w", b.Name(), fetchErr))
	}

	return nil, fmt.Errorf("all backends failed for policy %q: %w", policy.Name, joinedErr)
}

func (e *PolicyEngine) beforeRequest(backendName string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	b, exists := e.circuitBreakers[normalizeKey(backendName)]
	if !exists {
		return nil
	}

	now := time.Now()

	switch b.state {
	case circuitStateClosed:
		return nil
	case circuitStateOpen:
		if now.Sub(b.openedAt) < b.cfg.RecoveryTimeout {
			circuitRequestRejectedTotal.WithLabelValues(normalizeKey(backendName), string(circuitStateOpen)).Inc()
			return fmt.Errorf("circuit breaker is open")
		}

		b.state = circuitStateHalfOpen
		b.halfOpenAt = now
		b.halfOpenProbeRun = false
		fallthrough
	case circuitStateHalfOpen:
		if now.Sub(b.halfOpenAt) >= b.cfg.HalfOpenTimeout {
			b.state = circuitStateOpen
			b.openedAt = now
			b.halfOpenProbeRun = false
			circuitRequestRejectedTotal.WithLabelValues(normalizeKey(backendName), string(circuitStateOpen)).Inc()
			return fmt.Errorf("circuit breaker is open")
		}

		if b.halfOpenProbeRun {
			circuitRequestRejectedTotal.WithLabelValues(normalizeKey(backendName), string(circuitStateHalfOpen)).Inc()
			return fmt.Errorf("circuit breaker probe is in flight")
		}

		b.halfOpenProbeRun = true
		return nil
	default:
		return nil
	}
}

func (e *PolicyEngine) recordSuccess(backendName string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	b, exists := e.circuitBreakers[normalizeKey(backendName)]
	if !exists {
		return
	}

	if b.state == circuitStateHalfOpen {
		circuitHalfOpenSuccessTotal.WithLabelValues(normalizeKey(backendName)).Inc()
	}

	b.state = circuitStateClosed
	b.failureCount = 0
	b.halfOpenProbeRun = false
	b.openedAt = time.Time{}
	b.halfOpenAt = time.Time{}
}

func (e *PolicyEngine) recordFailure(backendName string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	b, exists := e.circuitBreakers[normalizeKey(backendName)]
	if !exists {
		return
	}

	now := time.Now()

	switch b.state {
	case circuitStateHalfOpen:
		circuitOpenTotal.WithLabelValues(normalizeKey(backendName), string(circuitStateHalfOpen)).Inc()
		b.state = circuitStateOpen
		b.openedAt = now
		b.failureCount = b.cfg.FailureThreshold
		b.halfOpenProbeRun = false
	case circuitStateClosed:
		b.failureCount++
		if b.failureCount >= b.cfg.FailureThreshold {
			circuitOpenTotal.WithLabelValues(normalizeKey(backendName), string(circuitStateClosed)).Inc()
			b.state = circuitStateOpen
			b.openedAt = now
			b.halfOpenProbeRun = false
		}
	case circuitStateOpen:
		b.openedAt = now
	}
}

func normalizeCircuitConfig(cfg config.CircuitBreakerConfig) config.CircuitBreakerConfig {
	if cfg.RecoveryTimeout <= 0 {
		cfg.RecoveryTimeout = 30 * time.Second
	}
	if cfg.HalfOpenTimeout <= 0 {
		cfg.HalfOpenTimeout = 10 * time.Second
	}

	return cfg
}

func normalizeKey(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
