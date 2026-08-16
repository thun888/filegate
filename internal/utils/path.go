package utils

import (
	"fmt"
	"path"
	"strings"
)

// NormalizePath 将对象路径规范化为统一的正斜杠形式。
// 它会去除首尾空格、拒绝空路径、拦截 ".." 目录穿越，
// 并通过 path.Clean 清理冗余分隔符，最终返回干净的相对路径。
// 注意：URL 解码和空白裁剪应由上层 SanitizePath 处理。
func NormalizePath(objectPath string) (string, error) {
	raw := strings.TrimSpace(strings.ReplaceAll(objectPath, "\\", "/"))
	if raw == "" {
		return "", fmt.Errorf("object path is empty")
	}

	// 前置检查：在 path.Clean 解析 .. 之前拦截
	for segment := range strings.SplitSeq(raw, "/") {
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
