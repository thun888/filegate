package server

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/thun888/filegate/config"
	"github.com/thun888/filegate/internal/engine"
)

func TestNewRouter_PrecompilesPathFilter(t *testing.T) {
	cfg := &config.Config{
		Namespaces: []config.NamespaceConfig{
			{
				Name: "ns1",
				Class: []config.ClassConfig{
					{
						Name: "img",
						Security: config.SecurityConfig{
							PathFilter: config.PathFilterConfig{
								DenyPatterns: []string{"private/"},
							},
						},
					},
					{
						Name: "doc",
						Security: config.SecurityConfig{
							PathFilter: config.PathFilterConfig{
								DenyPatterns: []string{".tmp"},
							},
						},
					},
				},
			},
		},
	}

	router, err := engine.NewRouter(cfg)
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	imgRoute, err := router.Resolve("ns1", "img")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if imgRoute.PathFilter == nil {
		t.Fatalf("missing precompiled filter for ns1:img")
	}

	if err = imgRoute.PathFilter.Validate("private/a.jpg"); err == nil {
		t.Fatalf("expected deny pattern to block private path")
	}

	if err = imgRoute.PathFilter.Validate("public/a.jpg"); err != nil {
		t.Fatalf("expected public path allowed, got %v", err)
	}
}

func TestErrorImageName(t *testing.T) {
	tests := []struct {
		statusCode int
		want       string
	}{
		{statusCode: http.StatusNotFound, want: "404.png"},
		{statusCode: http.StatusForbidden, want: "403.png"},
		{statusCode: http.StatusUnauthorized, want: ""},
		{statusCode: http.StatusTooManyRequests, want: ""},
		{statusCode: http.StatusBadGateway, want: "5xx.png"},
		{statusCode: http.StatusServiceUnavailable, want: "5xx.png"},
		{statusCode: http.StatusCreated, want: ""},
	}

	for _, tt := range tests {
		if got := errorImageName(tt.statusCode); got != tt.want {
			t.Fatalf("errorImageName(%d) = %q, want %q", tt.statusCode, got, tt.want)
		}
	}
}

func TestAbortWithError_ReturnsImageAndNoCache(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// abortWithError 依赖启动时预加载的错误图片缓存，测试中手动初始化
	errorImages = preloadErrorImages()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/missing", nil)

	abortWithError(c, http.StatusNotFound, errors.New("not found"))

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}

	contentType := w.Header().Get("Content-Type")
	if !strings.HasPrefix(contentType, "image/png") {
		t.Fatalf("content-type = %q, want image/png", contentType)
	}

	if got := w.Header().Get("Cache-Control"); got != "no-store, no-cache, must-revalidate, max-age=0" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if got := w.Header().Get("Pragma"); got != "no-cache" {
		t.Fatalf("Pragma = %q", got)
	}
	if got := w.Header().Get("Expires"); got != "0" {
		t.Fatalf("Expires = %q", got)
	}

	body := w.Body.Bytes()
	if len(body) < 8 {
		t.Fatalf("response body too short for png: %d", len(body))
	}

	pngMagic := []byte{0x89, 'P', 'N', 'G'}
	for i := range pngMagic {
		if body[i] != pngMagic[i] {
			t.Fatalf("response body is not png")
		}
	}
}

func TestAbortWithError_HEADNoBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// abortWithError 依赖启动时预加载的错误图片缓存，测试中手动初始化
	errorImages = preloadErrorImages()

	// 无论是否有对应的错误图片，HEAD 响应都必须为空响应体
	tests := []struct {
		name       string
		statusCode int
	}{
		{name: "no error image (429)", statusCode: http.StatusTooManyRequests},
		{name: "with error image (404)", statusCode: http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodHead, "/limited", nil)

			abortWithError(c, tt.statusCode, errors.New("err"))

			if w.Code != tt.statusCode {
				t.Fatalf("status = %d, want %d", w.Code, tt.statusCode)
			}
			if w.Body.Len() != 0 {
				t.Fatalf("head response should be empty body, got %d bytes", w.Body.Len())
			}
			if got := w.Header().Get("Cache-Control"); got != "no-store, no-cache, must-revalidate, max-age=0" {
				t.Fatalf("Cache-Control = %q", got)
			}
		})
	}
}
