package backend

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/thun888/filegate/config"
)

// fakeBackend 用于测试重试装饰器的可编程后端。
type fakeBackend struct {
	name     string
	attempts int
	failures int   // 前 failures 次调用返回错误
	err      error // 失败时返回的错误（nil 表示返回通用错误）
}

func (f *fakeBackend) Name() string { return f.name }

func (f *fakeBackend) Fetch(_ context.Context, _ string) (*Object, error) {
	f.attempts++
	if f.attempts <= f.failures {
		if f.err != nil {
			return nil, f.err
		}
		return nil, errors.New("transient failure")
	}
	return &Object{Body: http.NoBody}, nil
}

func newRetryWrapper(inner Backend, retries int, delay time.Duration) *retryBackend {
	return &retryBackend{inner: inner, retries: retries, retryDelay: delay}
}

func TestRetryBackend_SucceedsAfterRetry(t *testing.T) {
	inner := &fakeBackend{name: "test", failures: 2}
	b := newRetryWrapper(inner, 3, time.Millisecond)

	obj, err := b.Fetch(context.Background(), "a.jpg")
	if err != nil {
		t.Fatalf("Fetch() error = %v, want nil", err)
	}
	if obj == nil {
		t.Fatal("Fetch() returned nil object")
	}
	if inner.attempts != 3 {
		t.Fatalf("attempts = %d, want 3", inner.attempts)
	}
}

func TestRetryBackend_ExhaustsRetries(t *testing.T) {
	inner := &fakeBackend{name: "test", failures: 99}
	b := newRetryWrapper(inner, 2, time.Millisecond)

	_, err := b.Fetch(context.Background(), "a.jpg")
	if err == nil {
		t.Fatal("Fetch() error = nil, want error")
	}
	if inner.attempts != 3 {
		t.Fatalf("attempts = %d, want 3 (retries + 1)", inner.attempts)
	}
	if !strings.Contains(err.Error(), "3 attempt(s)") {
		t.Fatalf("error %q should mention exhausted attempts", err)
	}
}

func TestRetryBackend_StopsOnNotRetryable(t *testing.T) {
	inner := &fakeBackend{
		name:     "test",
		failures: 99,
		err:      NotRetryable(errors.New("file not found")),
	}
	b := newRetryWrapper(inner, 3, time.Millisecond)

	_, err := b.Fetch(context.Background(), "a.jpg")
	if err == nil {
		t.Fatal("Fetch() error = nil, want error")
	}
	if inner.attempts != 1 {
		t.Fatalf("attempts = %d, want 1 (non-retryable error)", inner.attempts)
	}
}

func TestRetryBackend_StopsWhenContextCanceled(t *testing.T) {
	inner := &fakeBackend{name: "test", failures: 99}
	// 重试间隔远大于取消延时，确保第一次重试等待期间 ctx 被取消
	b := newRetryWrapper(inner, 3, time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(5 * time.Millisecond)
		cancel()
	}()

	_, err := b.Fetch(ctx, "a.jpg")
	if err == nil {
		t.Fatal("Fetch() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "retry wait aborted") {
		t.Fatalf("error = %v, want retry wait aborted", err)
	}
	if inner.attempts != 1 {
		t.Fatalf("attempts = %d, want 1 (canceled during retry wait)", inner.attempts)
	}
}

func TestSleepContext_ReturnsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(5 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := sleepContext(ctx, time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("sleepContext() error = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("sleepContext() took %v, want prompt return on cancel", elapsed)
	}
}

func TestIsRetryable(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"generic network error", errors.New("connection refused"), true},
		{"context canceled", context.Canceled, true}, // 由 Fetch 循环的 ctx.Err() 判断
		{"not retryable marker", NotRetryable(errors.New("file not found")), false},
		{"nested not retryable marker", fmt.Errorf("backend %q: %w", "x", NotRetryable(errors.New("bad path"))), false},
		{"status 400", &StatusError{Code: http.StatusBadRequest}, false},
		{"status 403", &StatusError{Code: http.StatusForbidden}, false},
		{"status 404", &StatusError{Code: http.StatusNotFound}, false},
		{"status 408", &StatusError{Code: http.StatusRequestTimeout}, true},
		{"status 429", &StatusError{Code: http.StatusTooManyRequests}, true},
		{"status 500", &StatusError{Code: http.StatusInternalServerError}, true},
		{"status 503", &StatusError{Code: http.StatusServiceUnavailable}, true},
		{"wrapped status 404", fmt.Errorf("backend %q: %w", "x", &StatusError{Code: http.StatusNotFound}), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isRetryable(tc.err); got != tc.want {
				t.Fatalf("isRetryable(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestNewFromConfig_RetriesHTTPBackend 端到端验证：配置 retries 后，
// 5xx 会被重试，404 不会被重试。
func TestNewFromConfig_RetriesHTTPBackend(t *testing.T) {
	t.Run("5xx retried", func(t *testing.T) {
		requests := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests++
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()

		b, err := NewFromConfig(config.BackendConfig{
			Name:       "http500",
			Type:       "http",
			Timeout:    time.Second,
			Retries:    2,
			RetryDelay: time.Millisecond,
			Config:     config.BackendDetailConfig{URLPrefix: srv.URL},
		})
		if err != nil {
			t.Fatalf("NewFromConfig() error = %v", err)
		}

		_, err = b.Fetch(context.Background(), "a.jpg")
		if err == nil {
			t.Fatal("Fetch() error = nil, want error")
		}
		if requests != 3 {
			t.Fatalf("backend requests = %d, want 3", requests)
		}
	})

	t.Run("404 not retried", func(t *testing.T) {
		requests := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests++
			http.NotFound(w, r)
		}))
		defer srv.Close()

		b, err := NewFromConfig(config.BackendConfig{
			Name:       "http404",
			Type:       "http",
			Timeout:    time.Second,
			Retries:    3,
			RetryDelay: time.Millisecond,
			Config:     config.BackendDetailConfig{URLPrefix: srv.URL},
		})
		if err != nil {
			t.Fatalf("NewFromConfig() error = %v", err)
		}

		_, err = b.Fetch(context.Background(), "a.jpg")
		if err == nil {
			t.Fatal("Fetch() error = nil, want error")
		}
		if requests != 1 {
			t.Fatalf("backend requests = %d, want 1 (404 not retried)", requests)
		}
	})

	t.Run("retries zero means no wrapper", func(t *testing.T) {
		requests := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests++
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()

		b, err := NewFromConfig(config.BackendConfig{
			Name:    "httpNoRetry",
			Type:    "http",
			Timeout: time.Second,
			Config:  config.BackendDetailConfig{URLPrefix: srv.URL},
		})
		if err != nil {
			t.Fatalf("NewFromConfig() error = %v", err)
		}

		_, err = b.Fetch(context.Background(), "a.jpg")
		if err == nil {
			t.Fatal("Fetch() error = nil, want error")
		}
		if requests != 1 {
			t.Fatalf("backend requests = %d, want 1 (no retry configured)", requests)
		}
	})
}

// fakeStatusError 模拟携带 HTTP 状态码的错误（如 AWS SDK 的 ResponseError）。
type fakeStatusError struct{ code int }

func (e *fakeStatusError) Error() string       { return fmt.Sprintf("status %d", e.code) }
func (e *fakeStatusError) HTTPStatusCode() int { return e.code }

func TestToBackendError(t *testing.T) {
	if err := toBackendError(&fakeStatusError{code: http.StatusNotFound}); err == nil {
		t.Fatal("toBackendError() = nil, want error")
	} else {
		var se *StatusError
		if !errors.As(err, &se) || se.Code != http.StatusNotFound {
			t.Fatalf("toBackendError() = %v, want *StatusError{Code: 404}", err)
		}
	}

	plain := errors.New("connection refused")
	if err := toBackendError(plain); err != plain {
		t.Fatalf("toBackendError() should return the original error for non-status errors, got %v", err)
	}
}
