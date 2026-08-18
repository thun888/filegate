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

// transformParam 是路径后缀中单个已识别的参数段。
type transformParam struct {
	field string
	value string
}

// pathTransform 是路径转换后缀的结构化解析结果。
type pathTransform struct {
	sourcePath string
	ruleName   string
	format     string
	hasFormat  bool
	params     []transformParam
}

// ParseRequest 解析请求路径和查询参数，返回源路径、转换选项与被选中的规则。
// 规则通过路径后缀 !rulename 或查询参数 rule= 选择，两者冲突时报错；
// 均未指定时不转换、原样返回路径。
func (p *Processor) ParseRequest(classCfg config.ClassConfig, objectPath string, query url.Values) (string, TransformOptions, config.FileConversionRule, error) {
	normalizedPath, err := utils.NormalizePath(objectPath)
	if err != nil {
		return "", TransformOptions{}, config.FileConversionRule{}, err
	}

	if len(classCfg.FileConversion) == 0 {
		return normalizedPath, TransformOptions{}, config.FileConversionRule{}, nil
	}

	queryRule := strings.TrimSpace(query.Get("rule"))
	if queryRule == "" && !strings.Contains(normalizedPath, "!") {
		return normalizedPath, TransformOptions{}, config.FileConversionRule{}, nil
	}

	pt, err := parsePathTransform(normalizedPath)
	if err != nil {
		return "", TransformOptions{}, config.FileConversionRule{}, err
	}

	if queryRule != "" && pt.ruleName != "" && config.NormalizeKey(queryRule) != config.NormalizeKey(pt.ruleName) {
		return "", TransformOptions{}, config.FileConversionRule{}, fmt.Errorf("conflicting rule selectors %q (path) and %q (query)", pt.ruleName, queryRule)
	}

	ruleName := pt.ruleName
	if ruleName == "" {
		ruleName = queryRule
	}
	if ruleName == "" {
		return normalizedPath, TransformOptions{}, config.FileConversionRule{}, nil
	}

	entry, ok := findConversionEntry(classCfg.FileConversion, ruleName)
	if !ok {
		return "", TransformOptions{}, config.FileConversionRule{}, fmt.Errorf("conversion rule %q is not enabled for this class", ruleName)
	}

	rule := p.lookupRule(entry.Rule)
	opts := TransformOptions{
		Enabled: true,
		Width:   rule.DefaultParams.Width,
		Height:  rule.DefaultParams.Height,
		Blur:    max(0.0, rule.DefaultParams.Blur),
		Quality: rule.DefaultParams.Quality,
		Format:  strings.ToLower(strings.TrimPrefix(rule.DefaultParams.Format, ".")),
	}

	if pt.hasFormat && entry.EnableRequestParams.Format {
		opts.Format = strings.ToLower(pt.format)
	}
	for _, pm := range pt.params {
		if err := applyTransformParam(entry.EnableRequestParams, &opts, pm.field, pm.value); err != nil {
			return "", TransformOptions{}, config.FileConversionRule{}, err
		}
	}

	// 查询参数覆盖路径后缀值；未启用的参数一律静默忽略。
	params := entry.EnableRequestParams
	if v := strings.TrimSpace(query.Get("width")); v != "" && params.Width.Enabled {
		if opts.Width, err = parsePositiveInt("width", v); err != nil {
			return "", TransformOptions{}, config.FileConversionRule{}, err
		}
	}
	if v := strings.TrimSpace(query.Get("height")); v != "" && params.Height.Enabled {
		if opts.Height, err = parsePositiveInt("height", v); err != nil {
			return "", TransformOptions{}, config.FileConversionRule{}, err
		}
	}
	if v := strings.TrimSpace(query.Get("quality")); v != "" && params.Quality.Enabled {
		if opts.Quality, err = parsePositiveInt("quality", v); err != nil {
			return "", TransformOptions{}, config.FileConversionRule{}, err
		}
	}
	if v := strings.TrimSpace(query.Get("blur")); v != "" && params.Blur {
		level, err := strconv.Atoi(v)
		if err != nil || level < 0 {
			return "", TransformOptions{}, config.FileConversionRule{}, fmt.Errorf("invalid blur value %q: expected non-negative integer", v)
		}
		opts.Blur = float64(level) / 10
	}
	if v := strings.TrimSpace(query.Get("format")); v != "" && params.Format {
		opts.Format = strings.ToLower(strings.TrimPrefix(v, "."))
	}

	// 0 表示不调整，跳过范围校验
	if opts.Width != 0 {
		if err := validateRange("width", opts.Width, params.Width); err != nil {
			return "", TransformOptions{}, config.FileConversionRule{}, err
		}
	}
	if opts.Height != 0 {
		if err := validateRange("height", opts.Height, params.Height); err != nil {
			return "", TransformOptions{}, config.FileConversionRule{}, err
		}
	}
	if opts.Quality != 0 {
		if err := validateRange("quality", opts.Quality, params.Quality); err != nil {
			return "", TransformOptions{}, config.FileConversionRule{}, err
		}
	}

	return pt.sourcePath, opts, rule, nil
}

// findConversionEntry 在类别的转换配置中按名称（大小写不敏感）查找条目。
func findConversionEntry(entries []config.ClassFileConversionConfig, name string) (config.ClassFileConversionConfig, bool) {
	key := config.NormalizeKey(name)
	for _, e := range entries {
		if config.NormalizeKey(e.Rule) == key {
			return e, true
		}
	}
	return config.ClassFileConversionConfig{}, false
}

// parsePathTransform 解析路径中的转换后缀（@... 形式）。
// 语法：@[<param>|!<rulename>][_...][.<format>]
//   - !<rulename> → 规则选择器
//   - <digits>w → 宽度（w 可大写）
//   - <digits>h → 高度（h 可大写）
//   - <digits>b → 高斯模糊 sigma×10（整数，如 5b=0.5，b 可大写）
//   - <digits>q → 质量（q 可大写）
//   - .<ext>    → 输出格式（纯字母扩展名，如 .webp）
//
// 各段顺序无关、可部分省略；宽度/高度为 0 表示保持原尺寸。
// 路径不含 @ 时源路径原样返回；解析失败返回带具体段名的错误。
func parsePathTransform(normalizedPath string) (*pathTransform, error) {
	at := strings.LastIndex(normalizedPath, "@")
	if at < 0 {
		return &pathTransform{sourcePath: normalizedPath}, nil
	}

	sourcePath := normalizedPath[:at]
	if sourcePath == "" {
		return nil, fmt.Errorf("invalid transformed path %q: missing source path before @", normalizedPath)
	}

	spec := normalizedPath[at+1:]
	if strings.TrimSpace(spec) == "" {
		return nil, fmt.Errorf("empty transform suffix in %q", normalizedPath)
	}

	pt := &pathTransform{sourcePath: sourcePath}

	// 分离输出格式：最后一个点之后为纯字母扩展名、且其前部分为空或全是合法参数段时
	// 才视为格式；否则整个 spec 按参数段解析。
	paramsStr, format, hasFormat := splitTransformSpec(spec)
	pt.format = format
	pt.hasFormat = hasFormat

	seen := make(map[string]struct{}, 4)
	for _, part := range strings.Split(paramsStr, "_") {
		if part == "" {
			return nil, fmt.Errorf("empty transform param segment in %q", normalizedPath)
		}

		if name, ok := ruleSelectorName(part); ok {
			if pt.ruleName != "" {
				return nil, fmt.Errorf("duplicate rule selector %q in %q", part, normalizedPath)
			}
			pt.ruleName = name
			continue
		}

		field, value, ok := matchTransformPart(part)
		if !ok {
			return nil, fmt.Errorf("invalid transform param %q in %q", part, normalizedPath)
		}
		if _, dup := seen[field]; dup {
			return nil, fmt.Errorf("duplicate transform param %q in %q", part, normalizedPath)
		}
		seen[field] = struct{}{}
		pt.params = append(pt.params, transformParam{field, value})
	}

	return pt, nil
}

// ruleSelectorName 判断参数段是否为 !rulename 形式的规则选择器。
func ruleSelectorName(part string) (string, bool) {
	if len(part) >= 2 && part[0] == '!' {
		return part[1:], true
	}
	return "", false
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
		if _, ok := ruleSelectorName(part); ok {
			continue
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
func applyTransformParam(params config.RequestParamsConfig, opts *TransformOptions, field, value string) error {
	switch field {
	case "width":
		if !params.Width.Enabled {
			return nil
		}
		w, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid width value %q in transform suffix", value)
		}
		opts.Width = w
	case "height":
		if !params.Height.Enabled {
			return nil
		}
		h, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid height value %q in transform suffix", value)
		}
		opts.Height = h
	case "quality":
		if !params.Quality.Enabled {
			return nil
		}
		q, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid quality value %q in transform suffix", value)
		}
		opts.Quality = q
	case "blur":
		if !params.Blur {
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
