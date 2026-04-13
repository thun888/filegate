package backend

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"filegate/config"
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
	Name() string
	Fetch(ctx context.Context, objectPath string) (*Object, error)
}

// NewFromConfig 根据配置创建后端实例。
func NewFromConfig(cfg config.BackendConfig) (Backend, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Type)) {
	case "http":
		return newHTTPBackend(cfg)
	case "s3":
		return newS3Backend(cfg)
	case "fs":
		return newFSBackend(cfg)
	default:
		return nil, fmt.Errorf("unsupported backend type %q for backend %q", cfg.Type, cfg.Name)
	}
}
