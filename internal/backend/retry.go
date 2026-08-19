package backend

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// StatusError 携带后端返回的 HTTP 状态码，供调用方按状态码区分处理
// （如重试装饰器据此判断 404 等确定性错误不值得重试）。
type StatusError struct {
	Code int
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("backend returned status %d", e.Code)
}

// notRetryableError 标记确定性错误：重试装饰器遇到它时直接放弃，不再重试。
type notRetryableError struct {
	err error
}

func (e *notRetryableError) Error() string { return e.err.Error() }
func (e *notRetryableError) Unwrap() error { return e.err }

// NotRetryable 将 err 标记为不可重试（如路径非法、对象不存在）。
func NotRetryable(err error) error {
	if err == nil {
		return nil
	}
	return &notRetryableError{err: err}
}

// retryBackend 按 retries/retry_delay 配置对后端请求做重试装饰。
// 每个后端独立生效：请求失败且错误可重试时，间隔 retryDelay 后重试，最多重试 retries 次。
type retryBackend struct {
	inner      Backend
	retries    int
	retryDelay time.Duration
}

// newRetryBackend 在 retries > 0 时用重试装饰器包装 inner，否则原样返回。
func newRetryBackend(inner Backend, retries int, retryDelay time.Duration) Backend {
	if inner == nil || retries <= 0 {
		return inner
	}
	if retryDelay < 0 {
		retryDelay = 0
	}
	return &retryBackend{inner: inner, retries: retries, retryDelay: retryDelay}
}

func (b *retryBackend) Name() string {
	return b.inner.Name()
}

func (b *retryBackend) Fetch(ctx context.Context, objectPath string) (*Object, error) {
	for attempt := 1; ; attempt++ {
		obj, err := b.inner.Fetch(ctx, objectPath)
		if err == nil {
			return obj, nil
		}

		// 放弃条件：重试次数用尽、调用方已取消/超时、错误本身不可重试
		if attempt > b.retries || ctx.Err() != nil || !isRetryable(err) {
			return nil, fmt.Errorf("backend %q failed after %d attempt(s): %w", b.Name(), attempt, err)
		}

		// 重试间隔，尊重调用方取消
		if sleepErr := sleepContext(ctx, b.retryDelay); sleepErr != nil {
			return nil, fmt.Errorf("backend %q: retry wait aborted: %w", b.Name(), sleepErr)
		}
	}
}

// isRetryable 判断错误是否值得重试：
// - 显式标记为 NotRetryable 的错误 → 不重试；
// - 携带 HTTP 状态码的错误：408/429 与 5xx 可重试，其余 4xx 属于确定性错误不重试；
// - 其余错误（网络错误、读超时等）默认可重试。
// 调用方取消由 Fetch 循环中的 ctx.Err() 单独判断，这里不处理 context 错误。
func isRetryable(err error) bool {
	if err == nil {
		return false
	}

	var nre *notRetryableError
	if errors.As(err, &nre) {
		return false
	}

	var statusErr *StatusError
	if errors.As(err, &statusErr) {
		code := statusErr.Code
		return code == http.StatusRequestTimeout || code >= http.StatusInternalServerError
	}

	return true
}

// sleepContext 等待 d 时长，期间若 ctx 被取消/超时则提前返回 ctx 的错误。
func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
