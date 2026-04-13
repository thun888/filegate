package engine

import (
	"fmt"
	"mime"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"

	"filegate/config"
)

var transformPattern = regexp.MustCompile(`@(\d+)w(?:_(\d+)h)?(?:_([0-9]+[bB]))?(?:_(\d+))?(?:\.([a-zA-Z0-9]+))?$`)

type TransformOptions struct {
	Enabled bool
	Width   int
	Height  int
	Blur    int
	Quality int
	Format  string
	Zip     bool

	WidthSet  bool
	HeightSet bool
	FormatSet bool
}

// Processor 负责解析请求路径与转换参数。
type Processor struct {
	rules map[string]config.FileConversionRule
}

func NewProcessor(cfg *config.Config) *Processor {
	rules := make(map[string]config.FileConversionRule)
	if cfg != nil {
		for _, rule := range cfg.FileConversionRules {
			rules[normalizeKey(rule.Name)] = rule
		}
	}

	return &Processor{rules: rules}
}

func (p *Processor) ParseRequest(classCfg config.ClassConfig, objectPath string, query url.Values) (string, TransformOptions, error) {
	normalizedPath, err := normalizePath(objectPath)
	if err != nil {
		return "", TransformOptions{}, err
	}

	if !classCfg.FileConversion.Enabled {
		return normalizedPath, TransformOptions{}, nil
	}

	rule, exists := p.rules[normalizeKey(classCfg.FileConversion.Rule)]
	if !exists {
		return "", TransformOptions{}, fmt.Errorf("file conversion rule %q not found", classCfg.FileConversion.Rule)
	}

	opts := TransformOptions{
		Enabled: true,
		Width:   rule.DefaultParams.Width,
		Height:  rule.DefaultParams.Height,
		Blur:    normalizeBlurLevel(rule.DefaultParams.Blur),
		Quality: rule.DefaultParams.Quality,
		Format:  strings.ToLower(strings.TrimPrefix(rule.DefaultParams.Format, ".")),
		Zip:     rule.DefaultParams.Zip,
	}

	sourcePath := normalizedPath
	matchedTransform := false
	if match := transformPattern.FindStringSubmatch(normalizedPath); len(match) == 6 {
		matchedTransform = true
		sourcePath = strings.TrimSuffix(normalizedPath, match[0])
		if sourcePath == "" {
			return "", TransformOptions{}, fmt.Errorf("invalid transformed path %q", objectPath)
		}

		opts.Width, _ = strconv.Atoi(match[1])
		opts.WidthSet = true
		if match[2] != "" {
			opts.Height, _ = strconv.Atoi(match[2])
			opts.HeightSet = true
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
			opts.FormatSet = true
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
		opts.WidthSet = true
	}

	if v := strings.TrimSpace(query.Get("height")); v != "" && isRangeEnabled(rule.EnableRequestParams.Height) {
		opts.Height, err = parsePositiveInt("height", v)
		if err != nil {
			return "", TransformOptions{}, err
		}
		opts.HeightSet = true
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
		opts.FormatSet = true
	}

	if v := strings.TrimSpace(query.Get("zip")); v != "" && rule.EnableRequestParams.Zip {
		opts.Zip, err = strconv.ParseBool(v)
		if err != nil {
			return "", TransformOptions{}, fmt.Errorf("invalid zip value %q", v)
		}
	}

	if err := validateRange("width", opts.Width, rule.EnableRequestParams.Width); err != nil {
		return "", TransformOptions{}, err
	}
	if err := validateRange("height", opts.Height, rule.EnableRequestParams.Height); err != nil {
		return "", TransformOptions{}, err
	}
	if err := validateRange("quality", opts.Quality, rule.EnableRequestParams.Quality); err != nil {
		return "", TransformOptions{}, err
	}
	if err := validateFormat(opts.Format, rule.SupportedFormats); err != nil {
		return "", TransformOptions{}, err
	}

	return sourcePath, opts, nil
}

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

func FormatTransformOptions(opts TransformOptions) string {
	if !opts.Enabled {
		return ""
	}

	return fmt.Sprintf(
		"width=%d,height=%d,blur=%d,quality=%d,format=%s,zip=%t",
		opts.Width,
		opts.Height,
		opts.Blur,
		opts.Quality,
		opts.Format,
		opts.Zip,
	)
}

func parsePositiveInt(fieldName, raw string) (int, error) {
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("invalid %s value %q", fieldName, raw)
	}
	return value, nil
}

func validateRange(fieldName string, value int, r config.ParamRange) error {
	if !isRangeEnabled(r) {
		return nil
	}

	if value < r.Min || (r.Max > 0 && value > r.Max) {
		return fmt.Errorf("%s=%d out of range [%d,%d]", fieldName, value, r.Min, r.Max)
	}

	return nil
}

func isRangeEnabled(r config.ParamRange) bool {
	return r.Min > 0 || r.Max > 0
}

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

func parseBlurValue(blur string) (int, error) {
	v := strings.ToLower(strings.TrimSpace(blur))
	if !strings.HasSuffix(v, "b") {
		return 0, fmt.Errorf("invalid blur value %q: expected format <int>b", blur)
	}

	v = strings.TrimSuffix(v, "b")

	level, err := strconv.Atoi(v)
	if err != nil || level < 0 {
		return 0, fmt.Errorf("invalid blur value %q: expected format <int>b", blur)
	}

	return level, nil
}

func normalizeBlurLevel(level int) int {
	if level < 0 {
		return 0
	}

	return level
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
