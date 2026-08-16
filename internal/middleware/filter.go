package middleware

import (
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/thun888/filegate/config"
	"github.com/thun888/filegate/internal/utils"
)

// PathFilter 提供路径黑白名单与扩展名校验能力。
type PathFilter struct {
	denyPatterns    []*regexp.Regexp    // denyPatterns 黑名单正则列表，匹配的路径将被拒绝
	allowPaths      []string            // allowPaths 白名单路径前缀列表，匹配的路径将被允许
	allowExtensions map[string]struct{} // allowExtensions 允许的文件扩展名集合（如 ".jpg", ".png"）
}

// NewPathFilter 根据配置创建 PathFilter 实例。
// 它会预编译黑名单正则、规范化白名单路径前缀、统一扩展名为小写，
// 若黑名单正则语法无效则尝试作为字面值匹配，仍失败则返回错误。
func NewPathFilter(cfg config.PathFilterConfig) (*PathFilter, error) {
	filter := &PathFilter{
		denyPatterns:    make([]*regexp.Regexp, 0, len(cfg.DenyPatterns)),
		allowPaths:      make([]string, 0, len(cfg.AllowPaths)),
		allowExtensions: make(map[string]struct{}, len(cfg.AllowExtensions)),
	}

	for _, pattern := range cfg.DenyPatterns {
		pattern = strings.TrimSpace(pattern)

		// 避免用户误错误配置导致所有路径都被拒绝
		if pattern == "" {
			continue
		}

		// 判断是否是正则表达式，若不是，则进行转义（regexp.QuoteMeta）处理，只匹配自身字面值
		re, err := regexp.Compile(pattern)
		if err != nil {
			re, err = regexp.Compile(regexp.QuoteMeta(pattern))
			if err != nil {
				return nil, fmt.Errorf("compile deny pattern %q: %w", pattern, err)
			}
		}

		filter.denyPatterns = append(filter.denyPatterns, re)
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

// Validate 对给定的对象路径执行三重校验：黑名单正则、白名单前缀、扩展名白名单。
// 任一校验不通过则返回描述具体原因的 error，全部通过返回 nil。
func (f *PathFilter) Validate(objectPath string) error {
	normalized, err := utils.NormalizePath(objectPath)
	if err != nil {
		return err
	}

	for _, denyPattern := range f.denyPatterns {
		if denyPattern.MatchString(normalized) {
			return fmt.Errorf("path %q denied by pattern", objectPath)
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
