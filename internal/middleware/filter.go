package middleware

import (
	"fmt"
	"path"
	"regexp"
	"strings"

	"filegate/config"
)

// PathFilter 提供路径黑白名单与扩展名校验能力。
type PathFilter struct {
	denyPatterns    []*regexp.Regexp
	allowPaths      []string
	allowExtensions map[string]struct{}
}

func NewPathFilter(cfg config.PathFilterConfig) (*PathFilter, error) {
	filter := &PathFilter{
		denyPatterns:    make([]*regexp.Regexp, 0, len(cfg.DenyPatterns)),
		allowPaths:      make([]string, 0, len(cfg.AllowPaths)),
		allowExtensions: make(map[string]struct{}, len(cfg.AllowExtensions)),
	}

	for _, pattern := range cfg.DenyPatterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}

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
		normalized := strings.TrimSpace(strings.ReplaceAll(allowPath, "\\", "/"))
		normalized = strings.TrimLeft(normalized, "/")
		if normalized == "" {
			continue
		}
		if !strings.HasSuffix(normalized, "/") {
			normalized += "/"
		}
		filter.allowPaths = append(filter.allowPaths, normalized)
	}

	for _, ext := range cfg.AllowExtensions {
		normalized := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(ext), "."))
		if normalized != "" {
			filter.allowExtensions[normalized] = struct{}{}
		}
	}

	return filter, nil
}

func (f *PathFilter) Validate(objectPath string) error {
	normalized, err := normalizePath(objectPath)
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

func normalizePath(objectPath string) (string, error) {
	raw := strings.TrimSpace(strings.ReplaceAll(objectPath, "\\", "/"))
	if raw == "" {
		return "", fmt.Errorf("object path is empty")
	}

	segments := strings.Split(raw, "/")
	for _, segment := range segments {
		if segment == ".." {
			return "", fmt.Errorf("invalid object path %q", objectPath)
		}
	}

	clean := path.Clean("/" + strings.TrimLeft(raw, "/"))
	clean = strings.TrimPrefix(clean, "/")
	if clean == "" || clean == "." {
		return "", fmt.Errorf("invalid object path %q", objectPath)
	}

	return clean, nil
}
