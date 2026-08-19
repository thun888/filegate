package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/thun888/filegate/config"
)

func newRefererRequest(t *testing.T, referer string) *http.Request {
	t.Helper()
	req := httptest.NewRequest("GET", "http://gate/fs/ns1/cls1/a.jpg", nil)
	req.Header.Set("Referer", referer)
	return req
}

func TestVerifyReferer(t *testing.T) {
	tests := []struct {
		name    string
		cfg     config.ReferCheckConfig
		referer string
		wantErr bool
	}{
		{
			name:    "disabled allows anything",
			cfg:     config.ReferCheckConfig{Enabled: false},
			referer: "https://evil.com/",
			wantErr: false,
		},
		{
			name:    "empty referer denied",
			cfg:     config.ReferCheckConfig{Enabled: true, AllowedReferers: []string{"example.com"}},
			referer: "",
			wantErr: true,
		},
		{
			name:    "empty whitelist denies all",
			cfg:     config.ReferCheckConfig{Enabled: true},
			referer: "https://example.com/",
			wantErr: true,
		},
		{
			name:    "exact domain match",
			cfg:     config.ReferCheckConfig{Enabled: true, AllowedReferers: []string{"example.com"}},
			referer: "https://example.com/index.html",
			wantErr: false,
		},
		{
			name:    "domain match ignores scheme port and path",
			cfg:     config.ReferCheckConfig{Enabled: true, AllowedReferers: []string{"example.com"}},
			referer: "http://example.com:8080/a/b?c=1",
			wantErr: false,
		},
		{
			name:    "domain match is case insensitive",
			cfg:     config.ReferCheckConfig{Enabled: true, AllowedReferers: []string{"example.com"}},
			referer: "https://EXAMPLE.COM/",
			wantErr: false,
		},
		{
			name:    "trailing dot in referer is ignored",
			cfg:     config.ReferCheckConfig{Enabled: true, AllowedReferers: []string{"example.com"}},
			referer: "https://example.com./",
			wantErr: false,
		},
		{
			name:    "subdomain not allowed by exact domain",
			cfg:     config.ReferCheckConfig{Enabled: true, AllowedReferers: []string{"example.com"}},
			referer: "https://sub.example.com/",
			wantErr: true,
		},
		{
			name:    "wildcard matches subdomain",
			cfg:     config.ReferCheckConfig{Enabled: true, AllowedReferers: []string{"*.example.com"}},
			referer: "https://a.example.com/",
			wantErr: false,
		},
		{
			name:    "wildcard matches deep subdomain",
			cfg:     config.ReferCheckConfig{Enabled: true, AllowedReferers: []string{"*.example.com"}},
			referer: "https://a.b.example.com/",
			wantErr: false,
		},
		{
			name:    "wildcard does not match apex domain",
			cfg:     config.ReferCheckConfig{Enabled: true, AllowedReferers: []string{"*.example.com"}},
			referer: "https://example.com/",
			wantErr: true,
		},
		{
			name:    "wildcard does not match other domain",
			cfg:     config.ReferCheckConfig{Enabled: true, AllowedReferers: []string{"*.example.com"}},
			referer: "https://notexample.com/",
			wantErr: true,
		},
		{
			name:    "lone wildcard allows any domain",
			cfg:     config.ReferCheckConfig{Enabled: true, AllowedReferers: []string{"*"}},
			referer: "https://anything.example.org/x",
			wantErr: false,
		},
		{
			name:    "lone wildcard with whitespace allows any domain",
			cfg:     config.ReferCheckConfig{Enabled: true, AllowedReferers: []string{" * "}},
			referer: "https://anything.example.org/x",
			wantErr: false,
		},
		{
			name:    "no matching entry denied",
			cfg:     config.ReferCheckConfig{Enabled: true, AllowedReferers: []string{"example.com", "*.another.com"}},
			referer: "https://evil.com/",
			wantErr: true,
		},
		{
			name:    "url style entry no longer matches",
			cfg:     config.ReferCheckConfig{Enabled: true, AllowedReferers: []string{"http://example.com"}},
			referer: "https://example.com/",
			wantErr: true,
		},
		{
			name:    "invalid referer url denied",
			cfg:     config.ReferCheckConfig{Enabled: true, AllowedReferers: []string{"*"}},
			referer: "http://[::1",
			wantErr: true,
		},
		{
			name:    "referer without host denied",
			cfg:     config.ReferCheckConfig{Enabled: true, AllowedReferers: []string{"*"}},
			referer: "https:///path",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := VerifyReferer(newRefererRequest(t, tt.referer), tt.cfg)
			if tt.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("expected nil, got error: %v", err)
			}
		})
	}
}
