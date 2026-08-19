package middleware

import (
	"testing"

	"github.com/thun888/filegate/config"
)

// TestPathFilter_DenyIsLiteralSubstring 回归测试：deny_patterns 必须按字面量子串
// 匹配，不得按正则解释。
// 修复前：regexp.Compile("../") 会成功，"." 作为通配符使 `../` 实际匹配任意
// "两个字符 + 斜杠"（如 images/1.jpg 中的 "es/"），几乎所有带斜杠的路径都被误拒，
// 这正是线上 /fs 与 /origin 全部 403 的根因。
func TestPathFilter_DenyIsLiteralSubstring(t *testing.T) {
	f, err := NewPathFilter(config.PathFilterConfig{
		DenyPatterns: []string{"../", ".git"},
	})
	if err != nil {
		t.Fatalf("NewPathFilter() error = %v", err)
	}

	// 修复前会因 "es/" 命中 `../` 正则而被误拒。
	if err := f.Validate("images/1.jpg"); err != nil {
		t.Fatalf("images/1.jpg should be allowed, got %v", err)
	}

	// 字面量 "../" 仍拦截真正包含该子串的路径。
	if err := f.Validate("a../b.jpg"); err == nil {
		t.Fatalf("a../b.jpg should be denied by literal ../")
	}

	// 字面量 ".git" 只拦截包含 ".git" 子串的路径，不误伤普通图片。
	if err := f.Validate("images/.git/config"); err == nil {
		t.Fatalf("images/.git/config should be denied")
	}
	if err := f.Validate("images/1.jpg"); err != nil {
		t.Fatalf("images/1.jpg should not be affected by .git entry, got %v", err)
	}
}

// TestPathFilter_DenyPlainPrefix 验证普通前缀条目按字面量子串匹配。
// 注意：子串语义会同时拒绝 "xprivate/..." 这类包含该子串的路径 ——
// 对 deny 列表而言多拒是可用性问题、漏拒才是安全问题，方向是安全的。
func TestPathFilter_DenyPlainPrefix(t *testing.T) {
	f, err := NewPathFilter(config.PathFilterConfig{
		DenyPatterns: []string{"private/", ".tmp"},
	})
	if err != nil {
		t.Fatalf("NewPathFilter() error = %v", err)
	}

	if err := f.Validate("private/a.jpg"); err == nil {
		t.Fatalf("private/a.jpg should be denied")
	}
	if err := f.Validate("public/a.jpg"); err != nil {
		t.Fatalf("public/a.jpg should be allowed, got %v", err)
	}
	if err := f.Validate("images/x.tmp"); err == nil {
		t.Fatalf("images/x.tmp should be denied")
	}
}

// TestPathFilter_DenyRegexMetacharactersAreLiteral 验证正则元字符
// （. * + 等）在 deny_patterns 中仅按字面量生效。
func TestPathFilter_DenyRegexMetacharactersAreLiteral(t *testing.T) {
	f, err := NewPathFilter(config.PathFilterConfig{
		DenyPatterns: []string{".", "1.jpg"},
	})
	if err != nil {
		t.Fatalf("NewPathFilter() error = %v", err)
	}

	// "." 是字面量：普通图片路径不含 "." 子串的场景不受影响。
	if err := f.Validate("images/xjpg"); err != nil {
		t.Fatalf("images/xjpg should be allowed, got %v", err)
	}

	// 含 "." 子串的路径被字面量 "." 拦截。
	if err := f.Validate("images/1.jpg"); err == nil {
		t.Fatalf("images/1.jpg should be denied by literal .")
	}
}
