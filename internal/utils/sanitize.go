package utils

import (
	"fmt"
	"net/url"
	"strings"
)

const maxDecodeRounds = 5

// SanitizePath 对 URL 路径进行安全清洗。
// 它执行多轮 URL 解码（防御双重编码攻击），并裁剪每段的首尾空白字符
// （空格、制表符等），防止利用空白字符绕过路径穿越检查。
// 若解码轮次耗尽（超过 maxDecodeRounds）仍未稳定，返回错误以拒绝可疑请求。
func SanitizePath(raw string) (string, error) {
	decoded := raw
	for i := 0; i < maxDecodeRounds; i++ {
		d, err := url.PathUnescape(decoded)
		if err != nil || d == decoded {
			break
		}
		if i == maxDecodeRounds-1 {
			return "", fmt.Errorf("path %q has excessive encoding layers", raw)
		}
		decoded = d
	}

	segments := strings.Split(decoded, "/")
	for i, seg := range segments {
		segments[i] = strings.TrimSpace(seg)
	}

	return strings.Join(segments, "/"), nil
}
