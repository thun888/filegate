package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/thun888/filegate/config"
)

// TestHandleFetch_PathFilterAppliesToSourcePath 验证路径过滤器作用于转换后缀剥离后的
// 真实后端路径（sourcePath），而不是带 @100w.jpg 装饰的原始请求路径。
// 修复前：deny_patterns / allow_extensions 可被转换后缀绕过（如 secret.txt@100w.jpg），
// 且合法的转换请求（a.jpg@300w）会被扩展名白名单误拒。
func TestHandleFetch_PathFilterAppliesToSourcePath(t *testing.T) {
	gin.SetMode(gin.TestMode)

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "secret.txt"), []byte("top-secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.jpg"), []byte("img"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Backends: []config.BackendConfig{
			{Name: "local", Type: "fs", Config: config.BackendDetailConfig{RootPath: root}},
		},
		BackendPolicies: []config.BackendPolicy{
			{Name: "p1", Strategy: "single", Backends: []string{"local"}},
		},
		Namespaces: []config.NamespaceConfig{
			{
				Name:          "ns1",
				BackendPolicy: "p1",
				Class: []config.ClassConfig{
					{
						Name: "cls1",
						Security: config.SecurityConfig{
							PathFilter: config.PathFilterConfig{
								DenyPatterns:    []string{`\.txt$`},
								AllowExtensions: []string{"jpg"},
							},
						},
						// FileConversion 开启但 imgproxy 未配置时，转换后缀会被剥离，
						// 请求直接落到后端——PathFilter 必须校验剥离后的路径。
						FileConversion: config.ClassFileConversionConfig{Enabled: true, Rule: ""},
					},
				},
			},
		},
	}

	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	handler := srv.Handler()

	t.Run("deny pattern cannot be bypassed via transform suffix", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/fs/ns1/cls1/secret.txt@100w.jpg", nil)
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
		}
	})

	t.Run("allowed file with transform suffix still works", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/fs/ns1/cls1/a.jpg@300w", nil)
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
		if got := w.Body.String(); got != "img" {
			t.Fatalf("body = %q, want %q", got, "img")
		}
	})
}
