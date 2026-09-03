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
// 规则为可选：未选择时以类别 default_params 为基础转换。
// 请求既未选规则又无 @ 转换后缀时不转换、原样返回路径。
func (p *Processor) ParseRequest(classCfg config.ClassConfig, objectPath string, query url.Values) (string, TransformOptions, config.FileConversionRule, error) {
	normalizedPath, err := utils.NormalizePath(objectPath)
	if err != nil {
		return "", TransformOptions{}, config.FileConversionRule{}, err
	}

	// 类别未配置转换能力（无可用规则且无类别默认参数）→ 原样返回
	if len(classCfg.FileConversion.Rules) == 0 && !hasAnyDefaultParams(classCfg.FileConversion.DefaultParams) {
		return normalizedPath, TransformOptions{}, config.FileConversionRule{}, nil
	}

	queryRule := strings.TrimSpace(query.Get("rule"))

	pt, err := parsePathTransform(normalizedPath)
	if err != nil {
		return "", TransformOptions{}, config.FileConversionRule{}, err
	}

	// 无转换意图（未选规则且路径无 @ 后缀）→ 原样返回
	if queryRule == "" && pt.ruleName == "" && !strings.Contains(normalizedPath, "@") {
		return normalizedPath, TransformOptions{}, config.FileConversionRule{}, nil
	}

	if queryRule != "" && pt.ruleName != "" && config.NormalizeKey(queryRule) != config.NormalizeKey(pt.ruleName) {
		return "", TransformOptions{}, config.FileConversionRule{}, fmt.Errorf("conflicting rule selectors %q (path) and %q (query)", pt.ruleName, queryRule)
	}

	ruleName := pt.ruleName
	if ruleName == "" {
		ruleName = queryRule
	}

	rule := config.FileConversionRule{}
	if ruleName != "" {
		if !hasConversionRule(classCfg.FileConversion.Rules, ruleName) {
			return "", TransformOptions{}, config.FileConversionRule{}, fmt.Errorf("conversion rule %q is not enabled for this class", ruleName)
		}
		rule = p.lookupRule(ruleName)
	}

	params := classCfg.FileConversion.EnableRequestParams
	defaults := mergeConversionDefaults(classCfg.FileConversion.DefaultParams, rule.Params)
	opts := TransformOptions{
		Enabled: true,
		Width:   defaults.Width,
		Height:  defaults.Height,
		Blur:    max(0.0, defaults.Blur),
		Quality: defaults.Quality,
		Format:  strings.ToLower(strings.TrimPrefix(defaults.Format, ".")),
	}

	if pt.hasFormat && params.Format {
		opts.Format = strings.ToLower(pt.format)
	}
	for _, pm := range pt.params {
		if err := applyTransformParam(params, &opts, pm.field, pm.value); err != nil {
			return "", TransformOptions{}, config.FileConversionRule{}, err
		}
	}

	// 查询参数覆盖路径后缀值；未启用的参数一律静默忽略。
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

// hasConversionRule 判断规则名（大小写不敏感）是否在类别的可用规则列表中。
func hasConversionRule(rules []string, name string) bool {
	key := config.NormalizeKey(name)
	for _, r := range rules {
		if config.NormalizeKey(r) == key {
			return true
		}
	}
	return false
}

// mergeConversionDefaults 以类别 default_params 为基础，
// 用规则 params 中已设置的非零值覆盖对应字段，得到生效的基础参数。
// 0（格式为空串）视为未设置，保留类别默认值。
func mergeConversionDefaults(base, rule config.ConversionDefaultParams) config.ConversionDefaultParams {
	out := base
	if rule.Width != 0 {
		out.Width = rule.Width
	}
	if rule.Height != 0 {
		out.Height = rule.Height
	}
	if rule.Blur != 0 {
		out.Blur = rule.Blur
	}
	if rule.Quality != 0 {
		out.Quality = rule.Quality
	}
	if rule.Format != "" {
		out.Format = rule.Format
	}
	return out
}

// hasAnyDefaultParams 判断类别默认参数是否至少设置了一项（0 / 空视为未设置）。
func hasAnyDefaultParams(p config.ConversionDefaultParams) bool {
	return p.Width != 0 || p.Height != 0 || p.Blur != 0 || p.Quality != 0 || p.Format != ""
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

	if paramsStr != "" {
		parts := strings.Split(paramsStr, "_")
		seen := make(map[string]struct{}, 4)
		for i := 0; i < len(parts); {
			part := parts[i]
			if part == "" {
				return nil, fmt.Errorf("empty transform param segment in %q", normalizedPath)
			}

			if ruleSel, ok := ruleSelectorName(part); ok {
				if pt.ruleName != "" {
					return nil, fmt.Errorf("duplicate rule selector %q in %q", part, normalizedPath)
				}
				pt.ruleName = ruleNameAt(parts, i, ruleSel)
				i = ruleNameEnd(parts, i)
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
			i++
		}
	}

	return pt, nil
}

// ruleNameEnd 返回从 parts[start]（! 选择器段）起，规则名覆盖到的段下标（不含）。
// 规则名向后并入后续段，直到遇到合法参数段或另一个选择器为止。
func ruleNameEnd(parts []string, start int) int {
	end := start + 1
	for end < len(parts) {
		if _, ok := ruleSelectorName(parts[end]); ok {
			break
		}
		if _, _, ok := matchTransformPart(parts[end]); ok {
			break
		}
		end++
	}
	return end
}

// ruleNameAt 组装从 parts[start] 起的规则名：选择器段去掉 ! 前缀，
// 并以下划线并入后续属于该规则名的段。
func ruleNameAt(parts []string, start int, first string) string {
	end := ruleNameEnd(parts, start)
	if end <= start+1 {
		return first
	}
	return first + "_" + strings.Join(parts[start+1:end], "_")
}

// ruleSelectorName 判断参数段是否为 !rulename 形式的规则选择器。
func ruleSelectorName(part string) (string, bool) {
	if len(part) >= 2 && part[0] == '!' {
		return part[1:], true
	}
	return "", false
}

// splitTransformSpec 将 @ 之后的转换说明按 <params>.<format> 拆分。
// 最后一个点之后是纯字母扩展名时视为输出格式；否则整个 spec 作为参数部分（无格式）。
func splitTransformSpec(spec string) (paramsStr, format string, hasFormat bool) {
	lastDot := strings.LastIndex(spec, ".")
	if lastDot < 0 {
		return spec, "", false
	}

	format = spec[lastDot+1:]
	if !isAlpha(format) {
		return spec, "", false
	}

	return spec[:lastDot], format, true
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
