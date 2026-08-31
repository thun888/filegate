package config

import (
	"fmt"
	"strings"
)

// ParseExtraParams 解析附加处理参数（extra_params），按"/"拆分为 imgproxy 处理选项段列表。
// 段首尾空白被去除，空段（如首尾或重复的"/"）被忽略；
// 段内只允许字母、数字与 _ : . - 字符，防止空格、%、? 等破坏 URL 结构的字符被拼进 imgproxy 路径。
// 空串返回 nil；无任何有效段时返回错误。
func ParseExtraParams(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	segments := make([]string, 0, strings.Count(raw, "/")+1)
	for _, seg := range strings.Split(raw, "/") {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		if !isExtraParamSegment(seg) {
			return nil, fmt.Errorf("invalid extra_params segment %q: only letters, digits and '_ : . -' are allowed", seg)
		}
		segments = append(segments, seg)
	}

	if len(segments) == 0 {
		return nil, fmt.Errorf("extra_params contains no valid segment")
	}

	return segments, nil
}

// isExtraParamSegment 判断单个选项段是否只含合法字符（字母、数字与 _ : . -）。
func isExtraParamSegment(seg string) bool {
	for i := 0; i < len(seg); i++ {
		c := seg[i]
		switch {
		case (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9'):
		case c == '_' || c == ':' || c == '.' || c == '-':
		default:
			return false
		}
	}

	return true
}
