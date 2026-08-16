package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/thun888/filegate/config"
	"github.com/thun888/filegate/internal/backend"
)

type failingBackend struct {
	name string
	err  error
}

func (b *failingBackend) Name() string {
	return b.name
}

func (b *failingBackend) Fetch(_ context.Context, _ string) (*backend.Object, error) {
	return nil, b.err
}

func TestPolicyEngine_CircuitBreakerOpenAfterThreshold(t *testing.T) {
	engine := NewPolicyEngine()
	engine.RegisterBackend("b1", config.CircuitBreakerConfig{
		FailureThreshold: 1,
		RecoveryTimeout:  time.Hour,
		HalfOpenTimeout:  time.Second,
	})

	policy := config.BackendPolicy{
		Name:     "p1",
		Strategy: "fallback",
		Backends: []string{"b1"},
	}

	all := map[string]backend.Backend{
		"b1": &failingBackend{name: "b1", err: errors.New("boom")},
	}

	_, err := engine.FetchWithPolicy(context.Background(), policy, all, "foo/bar.jpg")
	if err == nil {
		t.Fatalf("first request should fail")
	}

	_, err = engine.FetchWithPolicy(context.Background(), policy, all, "foo/bar.jpg")
	if err == nil {
		t.Fatalf("second request should fail with open breaker")
	}
	if !strings.Contains(err.Error(), "circuit breaker is open") {
		t.Fatalf("expected open breaker error, got %v", err)
	}
}
