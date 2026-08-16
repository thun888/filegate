package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse url %q: %v", raw, err)
	}
	return u
}

// TestImgproxyDo_BuildsProcessingOptions 验证处理选项的构造：
// quality/default format 必须下发，为 0 的 w/h/bl 不再下发。
func TestImgproxyDo_BuildsProcessingOptions(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := &imgproxyClient{
		baseURL:    mustParseURL(t, srv.URL),
		httpClient: srv.Client(),
	}

	resp, err := client.Do(context.Background(), imgproxyRequest{
		Method:            http.MethodGet,
		SourceURL:         "http://filegate.local/origin/ns/cls/a.jpg",
		Width:             800,
		Height:            0,
		Quality:           80,
		Format:            "avif",
		MaxSourceFileSize: 10 * 1024 * 1024,
	})
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	_ = resp.Body.Close()

	for _, want := range []string{"w:800", "q:80", "f:avif", "msfs:10485760"} {
		if !strings.Contains(gotPath, want) {
			t.Errorf("request path %q missing %q", gotPath, want)
		}
	}
	for _, absent := range []string{"h:", "bl:"} {
		if strings.Contains(gotPath, absent) {
			t.Errorf("request path %q should not contain %q", gotPath, absent)
		}
	}
}

// TestImgproxyDo_NoOptionsReturnsError 验证全部选项为空时直接返回错误，不再构造请求。
func TestImgproxyDo_NoOptionsReturnsError(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := &imgproxyClient{
		baseURL:    mustParseURL(t, srv.URL),
		httpClient: srv.Client(),
	}

	_, err := client.Do(context.Background(), imgproxyRequest{
		Method:    http.MethodGet,
		SourceURL: "http://filegate.local/origin/ns/cls/a.jpg",
	})
	if !errors.Is(err, ErrImgproxyNoProcessingOptions) {
		t.Fatalf("Do() error = %v, want ErrImgproxyNoProcessingOptions", err)
	}
	if called {
		t.Fatalf("imgproxy should not be called when there are no processing options")
	}
}
