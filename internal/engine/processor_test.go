package engine

import (
	"net/url"
	"strings"
	"testing"

	"github.com/thun888/filegate/config"
)

// fullRequestParams 返回所有请求参数均启用的 enable_request_params 配置。
func fullRequestParams() config.RequestParamsConfig {
	return config.RequestParamsConfig{
		Width:   config.ParamRange{Enabled: true, Min: 1, Max: 8192},
		Height:  config.ParamRange{Enabled: true, Min: 1, Max: 8192},
		Quality: config.ParamRange{Enabled: true, Min: 10, Max: 95},
		Blur:    true,
		Format:  true,
	}
}

// fullTestRule 返回带默认参数的转换规则。
func fullTestRule() config.FileConversionRule {
	return config.FileConversionRule{
		Name: "png_conversion",
		DefaultParams: config.ConversionDefaultParams{
			Width:   800,
			Height:  600,
			Blur:    0.5,
			Quality: 80,
			Format:  "png",
		},
	}
}

// processorWithRule 构造绑定指定转换规则的 Processor 测试实例。
func processorWithRule(rule config.FileConversionRule) *Processor {
	return NewProcessor((&Router{conversionRules: map[string]config.FileConversionRule{
		"png_conversion": rule,
	}}).FileConversionRule)
}

// testClassConfig 构造引用 png_conversion 规则的类别配置。
func testClassConfig(params config.RequestParamsConfig) config.ClassConfig {
	return config.ClassConfig{
		FileConversion: []config.ClassFileConversionConfig{
			{Rule: "png_conversion", EnableRequestParams: params},
		},
	}
}

// ruleQuery 返回仅选择 png_conversion 规则的查询参数。
func ruleQuery() url.Values {
	return url.Values{"rule": {"png_conversion"}}
}

// TestParseRequest_PathTransformSupportsPartialParams 测试路径变换语法对部分参数的支持。
// 验证仅指定部分参数时，其余参数自动填充默认值（default_params）。
func TestParseRequest_PathTransformSupportsPartialParams(t *testing.T) {
	processor := processorWithRule(fullTestRule())
	classCfg := testClassConfig(fullRequestParams())

	tests := []struct {
		name           string
		objectPath     string
		wantSourcePath string
		wantWidth      int
		wantHeight     int
		wantBlur       float64
		wantQuality    int
		wantFormat     string
	}{
		{
			name:           "width only fills rest from defaults",
			objectPath:     "images/demo.jpg@320w",
			wantSourcePath: "images/demo.jpg",
			wantWidth:      320,
			wantHeight:     600, // 未指定高度，使用 default_params.height
			wantBlur:       0.5, // 未指定模糊，使用 default_params.blur
			wantQuality:    80,
			wantFormat:     "png",
		},
		{
			name:           "width and quality",
			objectPath:     "images/demo.jpg@320w_70q",
			wantSourcePath: "images/demo.jpg",
			wantWidth:      320,
			wantHeight:     600,
			wantBlur:       0.5,
			wantQuality:    70,
			wantFormat:     "png",
		},
		{
			name:           "width and format",
			objectPath:     "images/demo.jpg@320w.webp",
			wantSourcePath: "images/demo.jpg",
			wantWidth:      320,
			wantHeight:     600,
			wantBlur:       0.5,
			wantQuality:    80,
			wantFormat:     "webp",
		},
		{
			name:           "width blur quality format",
			objectPath:     "images/demo.jpg@320w_10b_70q.webp",
			wantSourcePath: "images/demo.jpg",
			wantWidth:      320,
			wantHeight:     600,
			wantBlur:       1.0,
			wantQuality:    70,
			wantFormat:     "webp",
		},
		{
			name:           "integer blur sigma (5b = 0.5)",
			objectPath:     "images/demo.jpg@320w_5b_70q.webp",
			wantSourcePath: "images/demo.jpg",
			wantWidth:      320,
			wantHeight:     600,
			wantBlur:       0.5,
			wantQuality:    70,
			wantFormat:     "webp",
		},
		{
			name:           "full params",
			objectPath:     "images/demo.jpg@320w_240h_0b_70q.webp",
			wantSourcePath: "images/demo.jpg",
			wantWidth:      320,
			wantHeight:     240,
			wantBlur:       0,
			wantQuality:    70,
			wantFormat:     "webp",
		},
		{
			name:           "zero width keeps no resize",
			objectPath:     "images/demo.jpg@0w_240h",
			wantSourcePath: "images/demo.jpg",
			wantWidth:      0,
			wantHeight:     240,
			wantBlur:       0.5,
			wantQuality:    80,
			wantFormat:     "png",
		},
		{
			name:           "both zero means no resize",
			objectPath:     "images/demo.jpg@0w_0h",
			wantSourcePath: "images/demo.jpg",
			wantWidth:      0,
			wantHeight:     0,
			wantBlur:       0.5,
			wantQuality:    80,
			wantFormat:     "png",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sourcePath, opts, _, err := processor.ParseRequest(classCfg, tt.objectPath, ruleQuery())
			if err != nil {
				t.Fatalf("ParseRequest() error = %v", err)
			}

			if sourcePath != tt.wantSourcePath {
				t.Fatalf("sourcePath = %q, want %q", sourcePath, tt.wantSourcePath)
			}
			if opts.Width != tt.wantWidth {
				t.Fatalf("width = %d, want %d", opts.Width, tt.wantWidth)
			}
			if opts.Height != tt.wantHeight {
				t.Fatalf("height = %d, want %d", opts.Height, tt.wantHeight)
			}
			if opts.Blur != tt.wantBlur {
				t.Fatalf("blur = %v, want %v", opts.Blur, tt.wantBlur)
			}
			if opts.Quality != tt.wantQuality {
				t.Fatalf("quality = %d, want %d", opts.Quality, tt.wantQuality)
			}
			if opts.Format != tt.wantFormat {
				t.Fatalf("format = %q, want %q", opts.Format, tt.wantFormat)
			}
		})
	}
}

// TestParseRequest_BlurSyntaxValidation 测试模糊参数的语法校验。
// 验证旧语法（1c）与非法模糊值（非数字、小数、负数）被拒绝，
// sigma×10 整数可通过路径后缀（5b=0.5）与查询参数（blur=15=1.5）指定。
func TestParseRequest_BlurSyntaxValidation(t *testing.T) {
	processor := processorWithRule(fullTestRule())
	classCfg := testClassConfig(fullRequestParams())

	if _, _, _, err := processor.ParseRequest(classCfg, "images/demo.jpg@320w_240h_1c_70q.webp", ruleQuery()); err == nil {
		t.Fatalf("expected error for legacy blur syntax 1c")
	}

	query := ruleQuery()
	query.Set("blur", "on")
	if _, _, _, err := processor.ParseRequest(classCfg, "images/demo.jpg@320w", query); err == nil {
		t.Fatalf("expected error for query blur=on")
	}

	query = ruleQuery()
	query.Set("blur", "1.5")
	if _, _, _, err := processor.ParseRequest(classCfg, "images/demo.jpg@320w", query); err == nil {
		t.Fatalf("expected error for query blur with decimal value")
	}

	query = ruleQuery()
	query.Set("blur", "-5")
	if _, _, _, err := processor.ParseRequest(classCfg, "images/demo.jpg@320w", query); err == nil {
		t.Fatalf("expected error for query blur with negative value")
	}

	if _, opts, _, err := processor.ParseRequest(classCfg, "images/demo.jpg@320w_5b", ruleQuery()); err != nil || opts.Blur != 0.5 {
		t.Fatalf("expected blur 0.5 from path suffix 5b, got %v (err %v)", opts.Blur, err)
	}

	query = ruleQuery()
	query.Set("blur", "15")
	if _, opts, _, err := processor.ParseRequest(classCfg, "images/demo.jpg@320w", query); err != nil || opts.Blur != 1.5 {
		t.Fatalf("expected blur 1.5 from query 15, got %v (err %v)", opts.Blur, err)
	}
}

// TestParseRequest_PathTransformSplitParser 测试拆分式路径转换解析的新能力：
// 顺序无关、宽度不再必填、格式/质量/模糊可单独出现、大小写单位、文件名含 @。
func TestParseRequest_PathTransformSplitParser(t *testing.T) {
	processor := processorWithRule(fullTestRule())
	classCfg := testClassConfig(fullRequestParams())

	tests := []struct {
		name           string
		objectPath     string
		wantSourcePath string
		wantWidth      int
		wantHeight     int
		wantBlur       float64
		wantQuality    int
		wantFormat     string
	}{
		{
			name:           "order independent",
			objectPath:     "images/demo.jpg@240h_320w.webp",
			wantSourcePath: "images/demo.jpg",
			wantWidth:      320,
			wantHeight:     240,
			wantBlur:       0.5,
			wantQuality:    80,
			wantFormat:     "webp",
		},
		{
			name:           "height only without width",
			objectPath:     "images/demo.jpg@600h",
			wantSourcePath: "images/demo.jpg",
			wantWidth:      800,
			wantHeight:     600,
			wantBlur:       0.5,
			wantQuality:    80,
			wantFormat:     "png",
		},
		{
			name:           "format only",
			objectPath:     "images/demo.jpg@.webp",
			wantSourcePath: "images/demo.jpg",
			wantWidth:      800,
			wantHeight:     600,
			wantBlur:       0.5,
			wantQuality:    80,
			wantFormat:     "webp",
		},
		{
			name:           "quality only",
			objectPath:     "images/demo.jpg@70q",
			wantSourcePath: "images/demo.jpg",
			wantWidth:      800,
			wantHeight:     600,
			wantBlur:       0.5,
			wantQuality:    70,
			wantFormat:     "png",
		},
		{
			name:           "blur only",
			objectPath:     "images/demo.jpg@5b",
			wantSourcePath: "images/demo.jpg",
			wantWidth:      800,
			wantHeight:     600,
			wantBlur:       0.5,
			wantQuality:    80,
			wantFormat:     "png",
		},
		{
			name:           "uppercase unit letters",
			objectPath:     "images/demo.jpg@320W_10B_70Q",
			wantSourcePath: "images/demo.jpg",
			wantWidth:      320,
			wantHeight:     600,
			wantBlur:       1.0,
			wantQuality:    70,
			wantFormat:     "png",
		},
		{
			name:           "at in filename with valid suffix",
			objectPath:     "images/demo@v2.jpg@320w",
			wantSourcePath: "images/demo@v2.jpg",
			wantWidth:      320,
			wantHeight:     600,
			wantBlur:       0.5,
			wantQuality:    80,
			wantFormat:     "png",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sourcePath, opts, _, err := processor.ParseRequest(classCfg, tt.objectPath, ruleQuery())
			if err != nil {
				t.Fatalf("ParseRequest() error = %v", err)
			}

			if sourcePath != tt.wantSourcePath {
				t.Fatalf("sourcePath = %q, want %q", sourcePath, tt.wantSourcePath)
			}
			if opts.Width != tt.wantWidth {
				t.Fatalf("width = %d, want %d", opts.Width, tt.wantWidth)
			}
			if opts.Height != tt.wantHeight {
				t.Fatalf("height = %d, want %d", opts.Height, tt.wantHeight)
			}
			if opts.Blur != tt.wantBlur {
				t.Fatalf("blur = %v, want %v", opts.Blur, tt.wantBlur)
			}
			if opts.Quality != tt.wantQuality {
				t.Fatalf("quality = %d, want %d", opts.Quality, tt.wantQuality)
			}
			if opts.Format != tt.wantFormat {
				t.Fatalf("format = %q, want %q", opts.Format, tt.wantFormat)
			}
		})
	}
}

// TestParseRequest_PathTransformSplitErrors 测试拆分式解析的显式报错：
// 重复段、空段、未知段、多点格式、空后缀、缺源路径、非法格式字符、文件名误带 @、越界值。
func TestParseRequest_PathTransformSplitErrors(t *testing.T) {
	processor := processorWithRule(fullTestRule())
	classCfg := testClassConfig(fullRequestParams())

	invalidPaths := []struct {
		name       string
		objectPath string
		wantErr    string
	}{
		{"duplicate field", "images/demo.jpg@320w_240w", "duplicate"},
		{"empty segment", "images/demo.jpg@320w__240h", "empty transform param"},
		{"unknown segment", "images/demo.jpg@320w_2x", `"2x"`},
		{"bare number quality", "images/demo.jpg@320w_70", `"70"`},
		{"multi-dot format", "images/demo.jpg@320w_70q.tar.gz", "invalid transform param"},
		{"empty suffix", "images/demo.jpg@", "empty transform suffix"},
		{"missing source path", "@320w", "missing source path"},
		{"invalid format chars", "images/demo.jpg@320w.webp!", "invalid transform param"},
		{"at in filename", "images/user@2x.jpg", `"2x"`},
		{"legacy decimal blur", "images/demo.jpg@320w_0.5b", `"0.5b"`},
		{"width out of range", "images/demo.jpg@99999w", "out of range"},
		{"overflow width", "images/demo.jpg@999999999999999999999999w", "invalid width value"},
	}

	for _, tt := range invalidPaths {
		t.Run(tt.name, func(t *testing.T) {
			_, _, _, err := processor.ParseRequest(classCfg, tt.objectPath, ruleQuery())
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %q, want contains %q", err.Error(), tt.wantErr)
			}
		})
	}
}

// TestParseRequest_PathTransformIgnoresDisabledParams 验证路径后缀与查询参数同策略：
// 未在 enable_request_params 中启用的字段被静默忽略，保留默认值。
func TestParseRequest_PathTransformIgnoresDisabledParams(t *testing.T) {
	processor := processorWithRule(fullTestRule())
	classCfg := testClassConfig(config.RequestParamsConfig{
		Width: config.ParamRange{Enabled: true, Min: 1, Max: 8192}, // 仅宽度启用
	})

	if _, opts, _, err := processor.ParseRequest(classCfg, "images/demo.jpg@320w", ruleQuery()); err != nil || opts.Width != 320 {
		t.Fatalf("expected width-only suffix to pass, got opts=%+v err=%v", opts, err)
	}

	want := TransformOptions{Enabled: true, Width: 320, Height: 600, Blur: 0.5, Quality: 80, Format: "png"}

	for _, tt := range []struct {
		name string
		path string
	}{
		{"disabled height", "images/demo.jpg@320w_240h"},
		{"disabled quality", "images/demo.jpg@320w_70q"},
		{"disabled blur", "images/demo.jpg@320w_1b"},
		{"disabled format", "images/demo.jpg@320w.webp"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sourcePath, opts, _, err := processor.ParseRequest(classCfg, tt.path, ruleQuery())
			if err != nil {
				t.Fatalf("ParseRequest() error = %v", err)
			}
			if sourcePath != "images/demo.jpg" {
				t.Fatalf("sourcePath = %q, want %q", sourcePath, "images/demo.jpg")
			}
			if opts != want {
				t.Fatalf("opts = %+v, want %+v", opts, want)
			}
		})
	}
}

// TestParseRequest_RuleSelector 验证规则选择：路径后缀 !rulename 与查询参数 rule=
// 均可选择规则，两者冲突、重复选择器、未知规则、未指定规则各有明确行为。
func TestParseRequest_RuleSelector(t *testing.T) {
	processor := processorWithRule(fullTestRule())
	classCfg := testClassConfig(fullRequestParams())

	// 路径后缀选择器（用户示例形态：xxx.jpg@80q_!testrule.webp）
	sourcePath, opts, rule, err := processor.ParseRequest(classCfg, "images/demo.jpg@70q_!png_conversion.webp", url.Values{})
	if err != nil {
		t.Fatalf("ParseRequest() error = %v", err)
	}
	if sourcePath != "images/demo.jpg" {
		t.Fatalf("sourcePath = %q, want %q", sourcePath, "images/demo.jpg")
	}
	if opts.Quality != 70 || opts.Format != "webp" {
		t.Fatalf("opts = %+v, want quality=70 format=webp", opts)
	}
	if rule.Name != "png_conversion" {
		t.Fatalf("rule = %q, want png_conversion", rule.Name)
	}

	// 选择器大小写不敏感
	if _, _, rule, err = processor.ParseRequest(classCfg, "images/demo.jpg@!PNG_CONVERSION", url.Values{}); err != nil || rule.Name != "png_conversion" {
		t.Fatalf("expected case-insensitive selector to pass, got rule=%q err=%v", rule.Name, err)
	}

	// 查询参数选择器
	query := url.Values{"rule": {"png_conversion"}, "width": {"320"}}
	if _, opts, rule, err = processor.ParseRequest(classCfg, "images/demo.jpg", query); err != nil || opts.Width != 320 || rule.Name != "png_conversion" {
		t.Fatalf("expected query selector to apply defaults+width, got opts=%+v rule=%q err=%v", opts, rule.Name, err)
	}

	// 路径与查询冲突
	query = url.Values{"rule": {"other_rule"}}
	if _, _, _, err = processor.ParseRequest(classCfg, "images/demo.jpg@320w_!png_conversion", query); err == nil || !strings.Contains(err.Error(), "conflicting") {
		t.Fatalf("expected conflicting selectors error, got %v", err)
	}

	// 路径与查询指向同一规则：通过
	query = ruleQuery()
	if _, _, _, err = processor.ParseRequest(classCfg, "images/demo.jpg@!png_conversion_320w", query); err != nil {
		t.Fatalf("expected identical selectors to pass, got %v", err)
	}

	// 重复选择器
	if _, _, _, err = processor.ParseRequest(classCfg, "images/demo.jpg@!png_conversion_!png_conversion", url.Values{}); err == nil || !strings.Contains(err.Error(), "duplicate rule selector") {
		t.Fatalf("expected duplicate rule selector error, got %v", err)
	}

	// 规则不在类别列表中
	if _, _, _, err = processor.ParseRequest(classCfg, "images/demo.jpg@!missing", url.Values{}); err == nil || !strings.Contains(err.Error(), "not enabled for this class") {
		t.Fatalf("expected unknown rule error, got %v", err)
	}

	// 均未指定：不做转换，原样返回路径
	sourcePath, opts, _, err = processor.ParseRequest(classCfg, "images/demo.jpg@320w", url.Values{})
	if err != nil {
		t.Fatalf("ParseRequest() error = %v", err)
	}
	if sourcePath != "images/demo.jpg@320w" {
		t.Fatalf("sourcePath = %q, want %q", sourcePath, "images/demo.jpg@320w")
	}
	if opts.Enabled {
		t.Fatalf("expected no conversion, got opts=%+v", opts)
	}

	// 类别未配置任何转换：同样原样返回
	emptyCfg := config.ClassConfig{}
	sourcePath, opts, _, err = processor.ParseRequest(emptyCfg, "images/demo.jpg@320w", url.Values{})
	if err != nil {
		t.Fatalf("ParseRequest() error = %v", err)
	}
	if sourcePath != "images/demo.jpg@320w" || opts.Enabled {
		t.Fatalf("expected raw passthrough for class without conversion, got sourcePath=%q opts=%+v", sourcePath, opts)
	}
}

// TestParseRequest_MultipleRulesPerClass 验证一个类别可挂多个规则，
// 每个条目的 enable_request_params 独立生效。
func TestParseRequest_MultipleRulesPerClass(t *testing.T) {
	processor := NewProcessor((&Router{conversionRules: map[string]config.FileConversionRule{
		"thumb": {Name: "thumb", DefaultParams: config.ConversionDefaultParams{Width: 100}},
		"full":  {Name: "full", DefaultParams: config.ConversionDefaultParams{Width: 2000}},
	}}).FileConversionRule)

	classCfg := config.ClassConfig{
		FileConversion: []config.ClassFileConversionConfig{
			{Rule: "thumb", EnableRequestParams: fullRequestParams()},
			{Rule: "full"},
		},
	}

	// thumb 启用了 width 覆盖
	query := url.Values{"rule": {"thumb"}, "width": {"150"}}
	_, opts, rule, err := processor.ParseRequest(classCfg, "images/demo.jpg", query)
	if err != nil {
		t.Fatalf("ParseRequest() error = %v", err)
	}
	if opts.Width != 150 || rule.Name != "thumb" {
		t.Fatalf("opts=%+v rule=%q, want width=150 rule=thumb", opts, rule.Name)
	}

	// full 未启用 width 覆盖：查询参数被静默忽略，保持规则默认值
	query = url.Values{"rule": {"full"}, "width": {"150"}}
	_, opts, rule, err = processor.ParseRequest(classCfg, "images/demo.jpg", query)
	if err != nil {
		t.Fatalf("ParseRequest() error = %v", err)
	}
	if opts.Width != 2000 || rule.Name != "full" {
		t.Fatalf("opts=%+v rule=%q, want width=2000 rule=full", opts, rule.Name)
	}
}

// TestParseRequest_QueryOverridesPathSuffix 锁定同名查询参数覆盖路径后缀值的优先级。
func TestParseRequest_QueryOverridesPathSuffix(t *testing.T) {
	processor := processorWithRule(fullTestRule())
	classCfg := testClassConfig(fullRequestParams())

	query := ruleQuery()
	query.Set("width", "640")

	sourcePath, opts, _, err := processor.ParseRequest(classCfg, "images/demo.jpg@320w", query)
	if err != nil {
		t.Fatalf("ParseRequest() error = %v", err)
	}
	if sourcePath != "images/demo.jpg" {
		t.Fatalf("sourcePath = %q, want %q", sourcePath, "images/demo.jpg")
	}
	if opts.Width != 640 {
		t.Fatalf("width = %d, want 640 (query overrides suffix)", opts.Width)
	}
}
