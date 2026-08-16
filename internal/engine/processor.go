package engine

import (
	"fmt"
	"math"
	"mime"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/thun888/filegate/config"
	"github.com/thun888/filegate/internal/utils"
)

var transformPattern = regexp.MustCompile(`@(\d+)w(?:_(\d+)h)?(?:_([0-9]+(?:\.[0-9]+)?[bB]))?(?:_(\d+))?(?:\.([a-zA-Z0-9]+))?$`)

// TransformOptions 包含文件转换的参数选项。
type TransformOptions struct {
	Enabled bool
	Width   int
	Height  int
	Blur    float64
	Quality int
	Format  string
}

// RuleLookup 按名称查找文件转换规则。
type RuleLookup func(name string) (config.FileConversionRule, bool)

// Processor 负责解析请求路径与转换参数。
type Processor struct {
	lookupRule RuleLookup
}

// NewProcessor 创建一个新的 Processor 实例，通过回调查找规则。
func NewProcessor(lookupRule RuleLookup) *Processor {
	return &Processor{lookupRule}
}

// ParseRequest 解析请求路径和查询参数，返回源路径和转换选项。
func (p *Processor) ParseRequest(classCfg config.ClassConfig, objectPath string, query url.Values) (string, TransformOptions, error) {
	normalizedPath, err := utils.NormalizePath(objectPath)
	if err != nil {
		return "", TransformOptions{}, err
	}

	if !classCfg.FileConversion.Enabled {
		return normalizedPath, TransformOptions{}, nil
	}

	rule, _ := p.lookupRule(classCfg.FileConversion.Rule)
	// if !exists {
	// 	return "", TransformOptions{}, fmt.Errorf("file conversion rule %q not found", classCfg.FileConversion.Rule)
	// }

	opts := TransformOptions{
		Enabled: true,
		Width:   rule.DefaultParams.Width,
		Height:  rule.DefaultParams.Height,
		Blur:    max(0.0, rule.DefaultParams.Blur),
		Quality: rule.DefaultParams.Quality,
		Format:  strings.ToLower(strings.TrimPrefix(rule.DefaultParams.Format, ".")),
	}

	sourcePath := normalizedPath
	matchedTransform := false
	// 可选组未匹配也会返回空字符串，故为6个元素（完整匹配 + 5个捕获组）
	// 匹配成功时，match[0] 是完整匹配的字符串，match[1] 是宽度，match[2] 是高度，match[3] 是模糊参数，match[4] 是质量，match[5] 是格式。
	if match := transformPattern.FindStringSubmatch(normalizedPath); len(match) == 6 {
		matchedTransform = true
		sourcePath = strings.TrimSuffix(normalizedPath, match[0])
		if sourcePath == "" {
			return "", TransformOptions{}, fmt.Errorf("invalid transformed path %q", objectPath)
		}
		// imgproxy 在传入0时默认不更改
		// strconv.Atoi 在解析失败时返回0，因此这里不区分未设置和设置为0的情况，直接传递给 imgproxy 由其处理。
		opts.Width, _ = strconv.Atoi(match[1])
		// 高度组是可选组：未出现时保持 default_params.height，避免 Atoi("") 返回 0 覆盖默认值
		if match[2] != "" {
			opts.Height, _ = strconv.Atoi(match[2])
		}

		if match[3] != "" {
			opts.Blur, err = parseBlurValue(match[3])
			if err != nil {
				return "", TransformOptions{}, err
			}
		}
		if match[4] != "" {
			opts.Quality, _ = strconv.Atoi(match[4])
		}
		if match[5] != "" {
			opts.Format = strings.ToLower(match[5])
		}
	}

	if strings.Contains(normalizedPath, "@") && !matchedTransform {
		return "", TransformOptions{}, fmt.Errorf("invalid transform suffix %q: expected @<w>w[_<h>h][_blurb][_quality][.<format>]", objectPath)
	}

	if v := strings.TrimSpace(query.Get("width")); v != "" && isRangeEnabled(rule.EnableRequestParams.Width) {
		opts.Width, err = parsePositiveInt("width", v)
		if err != nil {
			return "", TransformOptions{}, err
		}
	}

	if v := strings.TrimSpace(query.Get("height")); v != "" && isRangeEnabled(rule.EnableRequestParams.Height) {
		opts.Height, err = parsePositiveInt("height", v)
		if err != nil {
			return "", TransformOptions{}, err
		}
	}

	if v := strings.TrimSpace(query.Get("quality")); v != "" && isRangeEnabled(rule.EnableRequestParams.Quality) {
		opts.Quality, err = parsePositiveInt("quality", v)
		if err != nil {
			return "", TransformOptions{}, err
		}
	}

	if v := strings.TrimSpace(query.Get("blur")); v != "" && rule.EnableRequestParams.Blur {
		opts.Blur, err = parseBlurValue(v)
		if err != nil {
			return "", TransformOptions{}, err
		}
	}

	if v := strings.TrimSpace(query.Get("format")); v != "" && rule.EnableRequestParams.Format {
		opts.Format = strings.ToLower(strings.TrimPrefix(v, "."))
	}

	// 0 表示不调整，跳过范围校验
	if opts.Width != 0 {
		if err := validateRange("width", opts.Width, rule.EnableRequestParams.Width); err != nil {
			return "", TransformOptions{}, err
		}
	}
	if opts.Height != 0 {
		if err := validateRange("height", opts.Height, rule.EnableRequestParams.Height); err != nil {
			return "", TransformOptions{}, err
		}
	}
	if err := validateRange("quality", opts.Quality, rule.EnableRequestParams.Quality); err != nil {
		return "", TransformOptions{}, err
	}
	if err := validateFormat(opts.Format, rule.SupportedFormats); err != nil {
		return "", TransformOptions{}, err
	}

	return sourcePath, opts, nil
}

// ResolveContentType 根据转换选项确定响应的内容类型。
func (p *Processor) ResolveContentType(origin string, opts TransformOptions) string {
	if opts.Enabled && opts.Format != "" {
		if contentType := mime.TypeByExtension("." + strings.ToLower(opts.Format)); contentType != "" {
			return contentType
		}
	}

	if strings.TrimSpace(origin) != "" {
		return origin
	}

	return "application/octet-stream"
}

// FormatTransformOptions 将转换选项格式化为可读字符串。
// 用于设置 HTTP 响应头
func FormatTransformOptions(opts TransformOptions) string {
	if !opts.Enabled {
		return ""
	}

	return fmt.Sprintf(
		"width=%d,height=%d,blur=%g,quality=%d,format=%s",
		opts.Width,
		opts.Height,
		opts.Blur,
		opts.Quality,
		opts.Format,
	)
}

// parsePositiveInt 解析字符串为正整数。
func parsePositiveInt(fieldName, raw string) (int, error) {
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("invalid %s value %q", fieldName, raw)
	}
	return value, nil
}

// validateRange 验证值是否在指定的参数范围内。
func validateRange(fieldName string, value int, r config.ParamRange) error {
	if !isRangeEnabled(r) {
		return nil
	}

	if value < r.Min || (r.Max > 0 && value > r.Max) {
		return fmt.Errorf("%s=%d out of range [%d,%d]", fieldName, value, r.Min, r.Max)
	}

	return nil
}

// isRangeEnabled 检查参数范围是否已启用（设置了最小值或最大值）。
func isRangeEnabled(r config.ParamRange) bool {
	return r.Min > 0 || r.Max > 0
}

// validateFormat 验证输出格式是否在支持列表中。
func validateFormat(format string, supportedFormats []string) error {
	if len(supportedFormats) == 0 || format == "" {
		return nil
	}

	target := strings.ToLower(strings.TrimPrefix(format, "."))
	for _, candidate := range supportedFormats {
		if target == strings.ToLower(strings.TrimPrefix(candidate, ".")) {
			return nil
		}
	}

	return fmt.Errorf("unsupported output format %q", format)
}

// parseBlurValue 解析模糊值字符串（格式：数字+b，如 5b、0.5b），
// 返回的值作为高斯模糊 sigma 直接传给 imgproxy。
func parseBlurValue(blur string) (float64, error) {
	v := strings.ToLower(strings.TrimSpace(blur))
	// 再次检测是否以 "b" 结尾，确保在以传入参数调用时正常

	trimmed := strings.TrimSuffix(v, "b")
	// 利用TrimSuffix在不以"b"结尾时返回原字符串的特性，来判断是否满足格式要求
	if trimmed == v {
		return 0, fmt.Errorf("invalid blur value %q: expected format <number>b", blur)
	}

	level, err := strconv.ParseFloat(trimmed, 64)
	if err != nil || level < 0 || math.IsNaN(level) || math.IsInf(level, 0) {
		return 0, fmt.Errorf("invalid blur value %q: expected format <number>b", blur)
	}

	return level, nil
}

// normalizeBlurLevel 标准化模糊级别，确保非负。
// func normalizeBlurLevel(level int) int {
// 	if level < 0 {
// 		return 0
// 	}

// 	return level
// }
