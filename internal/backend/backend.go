package backend

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/thun888/filegate/config"
)

// Object 表示后端返回的对象数据。
type Object struct {
	Body        io.ReadCloser
	ContentType string
	Size        int64
	Headers     http.Header
}

// Backend 是所有存储后端的统一抽象。
type Backend interface {
	// Name 返回后端的唯一标识名称。
	Name() string
	// Fetch 根据对象路径从后端获取数据，失败时返回错误。
	Fetch(ctx context.Context, objectPath string) (*Object, error)
}

// NewFromConfig 根据配置创建后端实例，并按 retries/retry_delay 包装重试装饰器。
func NewFromConfig(cfg config.BackendConfig) (Backend, error) {
	var (
		b   Backend
		err error
	)
	switch strings.ToLower(strings.TrimSpace(cfg.Type)) {
	case "http":
		b, err = newHTTPBackend(cfg)
	case "s3":
		b, err = newS3Backend(cfg)
	case "fs":
		b, err = newFSBackend(cfg)
	default:
		return nil, fmt.Errorf("unsupported backend type %q for backend %q", cfg.Type, cfg.Name)
	}
	if err != nil {
		return nil, err
	}

	return newRetryBackend(b, cfg.Retries, cfg.RetryDelay), nil
}
