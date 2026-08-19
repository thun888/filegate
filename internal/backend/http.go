package backend

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/thun888/filegate/config"
)

type httpBackend struct {
	name    string
	baseURL *url.URL
	client  *http.Client
	headers map[string]string
}

func newHTTPBackend(cfg config.BackendConfig) (Backend, error) {
	// URLPrefix合法性检查
	if strings.TrimSpace(cfg.Config.URLPrefix) == "" {
		return nil, fmt.Errorf("http backend %q requires config.url_prefix", cfg.Name)
	}

	parsed, err := url.Parse(cfg.Config.URLPrefix)
	if err != nil {
		return nil, fmt.Errorf("parse http url_prefix for backend %q: %w", cfg.Name, err)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	// 超时设置检查
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	// 创建httpBackend实例
	h := &httpBackend{
		name:    cfg.Name,
		baseURL: parsed,
		client:  &http.Client{Timeout: timeout},
		headers: make(map[string]string, len(cfg.Config.ExtraHeaders)),
	}
	// make(map[string]string, len(cfg.Config.ExtraHeaders)) 创建一个初始容量为 len(cfg.Config.ExtraHeaders) 的 map，以避免在添加元素时发生多次扩容，从而提高性能。
	// 随后填充headers
	maps.Copy(h.headers, cfg.Config.ExtraHeaders)

	return h, nil
}

func (b *httpBackend) Name() string {
	return b.name
}

func (b *httpBackend) Fetch(ctx context.Context, objectPath string) (*Object, error) {
	// 检查 objectPath 是否为空或仅包含空白字符
	if strings.TrimSpace(objectPath) == "" {
		return nil, NotRetryable(fmt.Errorf("object path is empty"))
	}
	// 构建请求
	requestURL := b.buildRequestURL(objectPath)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request for backend %q: %w", b.name, err)
	}

	for key, value := range b.headers {
		req.Header.Set(key, value)
	}
	req.Header.Set("User-Agent", "FileGate/1.0")
	// 发送请求并处理响应
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request backend %q: %w", b.name, err)
	}

	if resp.StatusCode >= http.StatusBadRequest {
		defer resp.Body.Close()
		return nil, fmt.Errorf("backend %q: %w", b.name, &StatusError{Code: resp.StatusCode})
	}
	// 调用时记得关闭 Object.Body
	return &Object{
		Body:        resp.Body,
		ContentType: resp.Header.Get("Content-Type"),
		Size:        resp.ContentLength,
		Headers:     resp.Header.Clone(),
	}, nil
}

// 拼接基础路径和对象路径
func (b *httpBackend) buildRequestURL(objectPath string) string {
	cleanPath := path.Clean("/" + strings.TrimLeft(objectPath, "/"))
	if cleanPath == "." || cleanPath == "/" {
		cleanPath = ""
	}

	u := *b.baseURL
	u.Path = u.Path + cleanPath
	return u.String()
}
