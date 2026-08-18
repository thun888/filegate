package engine

import (
	"fmt"
	"mime"
	"net/url"
	"strconv"
	"strings"

	"github.com/thun888/filegate/config"
	"github.com/thun888/filegate/internal/utils"
)

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
type RuleLookup func(name string) config.FileConversionRule

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

	rule := p.lookupRule(classCfg.FileConversion.Rule)

	opts := TransformOptions{
		Enabled: true,
		Width:   rule.DefaultParams.Width,
		Height:  rule.DefaultParams.Height,
		Blur:    max(0.0, rule.DefaultParams.Blur),
		Quality: rule.DefaultParams.Quality,
		Format:  strings.ToLower(strings.TrimPrefix(rule.DefaultParams.Format, ".")),
	}

	// 路径转换后缀（@... 形式）按 @ → . → _ 三级拆分后逐段匹配。
	sourcePath, err := parsePathTransform(normalizedPath, rule, &opts)
	if err != nil {
		return "", TransformOptions{}, err
	}

	// 查询参数与路径后缀同名字段时覆盖后缀值；未启用的参数一律静默忽略。
	if v := strings.TrimSpace(query.Get("width")); v != "" && rule.EnableRequestParams.Width.Enabled {
		opts.Width, err = parsePositiveInt("width", v)
		if err != nil {
			return "", TransformOptions{}, err
		}
	}

	if v := strings.TrimSpace(query.Get("height")); v != "" && rule.EnableRequestParams.Height.Enabled {
		opts.Height, err = parsePositiveInt("height", v)
		if err != nil {
			return "", TransformOptions{}, err
		}
	}

	if v := strings.TrimSpace(query.Get("quality")); v != "" && rule.EnableRequestParams.Quality.Enabled {
		opts.Quality, err = parsePositiveInt("quality", v)
		if err != nil {
			return "", TransformOptions{}, err
		}
	}

	if v := strings.TrimSpace(query.Get("blur")); v != "" && rule.EnableRequestParams.Blur {
		level, err := strconv.Atoi(v)
		if err != nil || level < 0 {
			return "", TransformOptions{}, fmt.Errorf("invalid blur value %q: expected non-negative integer", v)
		}
		opts.Blur = float64(level) / 10
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

// parsePathTransform 解析路径中的转换后缀（@... 形式）。
// 语法：@<param>[_<param>...][.<format>]
//   - <digits>w → 宽度（w 可大写）
//   - <digits>h → 高度（h 可大写）
//   - <digits>b → 高斯模糊 sigma×10（整数，如 5b=0.5，b 可大写）
//   - <digits>q → 质量（q 可大写）
//   - .<ext>    → 输出格式（纯字母扩展名，如 .webp）
//
// 各参数顺序无关、可部分省略（宽度不再必填）；宽度/高度为 0 表示保持原尺寸。
// 路径不含 @ 时原样返回；解析失败返回带具体段名的错误。
func parsePathTransform(normalizedPath string, rule config.FileConversionRule, opts *TransformOptions) (string, error) {
	at := strings.LastIndex(normalizedPath, "@")
	if at < 0 {
		return normalizedPath, nil
	}

	sourcePath := normalizedPath[:at]
	if sourcePath == "" {
		return "", fmt.Errorf("invalid transformed path %q: missing source path before @", normalizedPath)
	}

	spec := normalizedPath[at+1:]
	if strings.TrimSpace(spec) == "" {
		return "", fmt.Errorf("empty transform suffix in %q", normalizedPath)
	}

	// 分离输出格式：最后一个点之后为纯字母扩展名、且其前部分为空或全是合法参数段时
	// 才视为格式；否则整个 spec 按参数段解析。
	paramsStr, format, hasFormat := splitTransformSpec(spec)
	if hasFormat && rule.EnableRequestParams.Format {
		opts.Format = strings.ToLower(format)
	}

	seen := make(map[string]struct{}, 4)
	if paramsStr != "" {
		for _, part := range strings.Split(paramsStr, "_") {
			if part == "" {
				return "", fmt.Errorf("empty transform param segment in %q", normalizedPath)
			}

			field, value, ok := matchTransformPart(part)
			if !ok {
				return "", fmt.Errorf("invalid transform param %q in %q", part, normalizedPath)
			}
			if _, dup := seen[field]; dup {
				return "", fmt.Errorf("duplicate transform param %q in %q", part, normalizedPath)
			}
			seen[field] = struct{}{}

			if err := applyTransformParam(rule, opts, field, value); err != nil {
				return "", err
			}
		}
	}

	return sourcePath, nil
}

// splitTransformSpec 将 @ 之后的转换说明按 <params>.<format> 拆分。
// 最后一个点之后必须是纯字母扩展名，且之前的部分为空或全部为合法参数段，
// 才判定存在格式；否则返回整个 spec 作为参数部分（无格式）。
func splitTransformSpec(spec string) (paramsStr, format string, hasFormat bool) {
	lastDot := strings.LastIndex(spec, ".")
	if lastDot < 0 {
		return spec, "", false
	}

	format = spec[lastDot+1:]
	if !isAlpha(format) {
		return spec, "", false
	}

	paramsStr = spec[:lastDot]
	if !transformParamsWellFormed(paramsStr) {
		return spec, "", false
	}

	return paramsStr, format, true
}

// transformParamsWellFormed 判断参数部分是否全部为合法参数段
// （空串视为合法，代表仅指定格式）。不做重复字段检查，
// 重复由主解析循环报出更精确的错误。
func transformParamsWellFormed(params string) bool {
	if params == "" {
		return true
	}

	for _, part := range strings.Split(params, "_") {
		if part == "" {
			return false
		}
		if _, _, ok := matchTransformPart(part); !ok {
			return false
		}
	}

	return true
}

// matchTransformPart 识别单个转换参数段，返回字段名与去除单位后的值。
func matchTransformPart(part string) (field, value string, ok bool) {
	if n := len(part); n >= 2 {
		body := part[:n-1]
		switch part[n-1] {
		case 'w', 'W':
			if isDigits(body) {
				return "width", body, true
			}
		case 'h', 'H':
			if isDigits(body) {
				return "height", body, true
			}
		case 'b', 'B':
			if isDigits(body) {
				return "blur", body, true
			}
		case 'q', 'Q':
			if isDigits(body) {
				return "quality", body, true
			}
		}
	}

	return "", "", false
}

// isDigits 判断字符串是否全为 ASCII 数字且非空。
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// isAlpha 判断字符串是否全为 ASCII 字母且非空。
// 输出格式限定纯字母扩展名。
func isAlpha(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') {
			return false
		}
	}
	return true
}

// applyTransformParam 将单个已识别的路径后缀参数应用到转换选项。
// 未在 enable_request_params 中启用的字段静默忽略。
func applyTransformParam(rule config.FileConversionRule, opts *TransformOptions, field, value string) error {
	switch field {
	case "width":
		if !rule.EnableRequestParams.Width.Enabled {
			return nil
		}
		w, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid width value %q in transform suffix", value)
		}
		opts.Width = w
	case "height":
		if !rule.EnableRequestParams.Height.Enabled {
			return nil
		}
		h, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid height value %q in transform suffix", value)
		}
		opts.Height = h
	case "quality":
		if !rule.EnableRequestParams.Quality.Enabled {
			return nil
		}
		q, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid quality value %q in transform suffix", value)
		}
		opts.Quality = q
	case "blur":
		if !rule.EnableRequestParams.Blur {
			return nil
		}
		level, err := strconv.Atoi(value)
		if err != nil || level < 0 {
			return fmt.Errorf("invalid blur value %q: expected non-negative integer", value)
		}
		opts.Blur = float64(level) / 10.0
	default:
		return fmt.Errorf("unknown transform field %q", field)
	}

	return nil
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
	if !r.Enabled {
		return nil
	}

	if value < r.Min || (r.Max > 0 && value > r.Max) {
		return fmt.Errorf("%s=%d out of range [%d,%d]", fieldName, value, r.Min, r.Max)
	}

	return nil
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

// normalizeBlurLevel 标准化模糊级别，确保非负。
// func normalizeBlurLevel(level int) int {
// 	if level < 0 {
// 		return 0
// 	}

// 	return level
// }
