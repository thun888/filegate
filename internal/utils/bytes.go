package utils

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var byteSizePattern = regexp.MustCompile(`(?i)^([0-9]+)\s*([kmgt]?i?b?)?$`)

// ParseByteSize 解析大小字符串（如 "10MB"、"512KiB"、"1g"）为字节数。
// 支持单位：b/kb/kib/mb/mib/gb/gib/tb/tib（大小写不敏感，纯数字视为字节）。
// 空字符串视为未设置，返回 0。
func ParseByteSize(raw string) (int64, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return 0, nil
	}

	match := byteSizePattern.FindStringSubmatch(v)
	if len(match) != 3 {
		return 0, fmt.Errorf("invalid size string")
	}

	baseValue, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil {
		return 0, err
	}

	unit := strings.ToLower(match[2])
	multiplier := int64(1)
	switch unit {
	case "", "b":
		multiplier = 1
	case "k", "kb", "kib":
		multiplier = 1024
	case "m", "mb", "mib":
		multiplier = 1024 * 1024
	case "g", "gb", "gib":
		multiplier = 1024 * 1024 * 1024
	case "t", "tb", "tib":
		multiplier = 1024 * 1024 * 1024 * 1024
	default:
		return 0, fmt.Errorf("unsupported size unit %q", unit)
	}

	return baseValue * multiplier, nil
}
