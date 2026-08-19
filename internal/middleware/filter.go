package middleware

import (
	"fmt"
	"path"
	"strings"

	"github.com/thun888/filegate/config"
	"github.com/thun888/filegate/internal/utils"
)

// PathFilter 提供路径黑白名单与扩展名校验能力。
type PathFilter struct {
	denyPatterns    []string            // denyPatterns 黑名单字面量列表，路径包含任一子串即被拒绝
	allowPaths      []string            // allowPaths 白名单路径前缀列表，匹配的路径将被允许
	allowExtensions map[string]struct{} // allowExtensions 允许的文件扩展名集合（如 ".jpg", ".png"）
}

// NewPathFilter 根据配置创建 PathFilter 实例。
// 它会规范化白名单路径前缀、统一扩展名为小写；黑名单条目按字面量子串匹配（非正则），
// 语义可预测：deny_patterns 写什么就只拦什么。
func NewPathFilter(cfg config.PathFilterConfig) (*PathFilter, error) {
	filter := &PathFilter{
		denyPatterns:    make([]string, 0, len(cfg.DenyPatterns)),
		allowPaths:      make([]string, 0, len(cfg.AllowPaths)),
		allowExtensions: make(map[string]struct{}, len(cfg.AllowExtensions)),
	}

	for _, pattern := range cfg.DenyPatterns {
		pattern = strings.TrimSpace(pattern)

		// 避免用户误错误配置导致所有路径都被拒绝
		if pattern == "" {
			continue
		}

		filter.denyPatterns = append(filter.denyPatterns, pattern)
	}

	for _, allowPath := range cfg.AllowPaths {
		// 将反斜杠转换为正斜杠，并去除空格
		normalized := strings.TrimSpace(strings.ReplaceAll(allowPath, "\\", "/"))
		// 去掉开头的斜杠
		normalized = strings.TrimLeft(normalized, "/")
		if normalized == "" {
			continue
		}
		// 确保以斜杠结尾，表示这是一个路径前缀
		if !strings.HasSuffix(normalized, "/") {
			normalized += "/"
		}
		filter.allowPaths = append(filter.allowPaths, normalized)
	}

	for _, ext := range cfg.AllowExtensions {
		normalized := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(ext), "."))
		if normalized != "" {
			// struct{}{}是一个空结构体，占位用
			// map键会自动去重
			filter.allowExtensions[normalized] = struct{}{}
		}
	}

	return filter, nil
}

// deny_patterns 采用字面量子串匹配（strings.Contains），刻意不使用正则。
// 历史教训：早期实现把"regexp.Compile 是否成功"当作正则/字面量的判定依据，
// 但几乎所有字面量都能编译成功 —— "../" 被编译成正则后，"." 变成通配符，
// 实际匹配任意"两个字符 + 斜杠"（如 images/1.jpg 中的 "es/"），
// 导致几乎所有带斜杠的路径都被误拒（线上 403 事故的根因）。
// 安全过滤器的默认语义必须是可预测的字面量；若将来确实需要锚定/正则匹配，
// 应新增独立的配置键（如 deny_patterns_regex）并启动期编译校验，而不是让本键承担两种语义。

// Validate 对给定的对象路径执行三重校验：黑名单字面量、白名单前缀、扩展名白名单。
// 任一校验不通过则返回描述具体原因的 error（命中黑名单时会带出具体命中的条目），
// 全部通过返回 nil。详细配置 dump 由调用方（server 层）在 FILEGATE_DEBUG=1 时输出。
func (f *PathFilter) Validate(objectPath string) error {
	normalized, err := utils.NormalizePath(objectPath)
	if err != nil {
		return err
	}

	for _, denyPattern := range f.denyPatterns {
		if strings.Contains(normalized, denyPattern) {
			return fmt.Errorf("path %q denied by pattern %q", objectPath, denyPattern)
		}
	}

	if len(f.allowPaths) > 0 {
		allowed := false
		for _, allowedPrefix := range f.allowPaths {
			if strings.HasPrefix(normalized, allowedPrefix) {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("path %q is not under allowed prefixes", objectPath)
		}
	}

	if len(f.allowExtensions) > 0 {
		ext := strings.ToLower(strings.TrimPrefix(path.Ext(normalized), "."))
		if ext == "" {
			return fmt.Errorf("path %q has no extension", objectPath)
		}
		if _, exists := f.allowExtensions[ext]; !exists {
			return fmt.Errorf("extension %q is not allowed", ext)
		}
	}

	return nil
}

// DenyPatterns 返回 deny_patterns 条目（仅用于调试输出）。
func (f *PathFilter) DenyPatterns() []string {
	out := make([]string, len(f.denyPatterns))
	copy(out, f.denyPatterns)
	return out
}

// AllowPaths 返回白名单路径前缀（仅用于调试输出）。
func (f *PathFilter) AllowPaths() []string {
	out := make([]string, len(f.allowPaths))
	copy(out, f.allowPaths)
	return out
}

// AllowExtensions 返回允许的扩展名集合（仅用于调试输出）。
func (f *PathFilter) AllowExtensions() []string {
	out := make([]string, 0, len(f.allowExtensions))
	for ext := range f.allowExtensions {
		out = append(out, ext)
	}
	return out
}
