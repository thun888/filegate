package engine

import (
	"net/url"
	"strings"
	"testing"

	"github.com/thun888/filegate/config"
)

// fullTestRule 返回所有请求参数均启用的转换规则，供路径后缀解析测试使用。
func fullTestRule() config.FileConversionRule {
	return config.FileConversionRule{
		Name:             "png_conversion",
		SupportedFormats: []string{"png", "webp"},
		EnableRequestParams: config.RequestParamsConfig{
			Width:   config.ParamRange{Enabled: true, Min: 1, Max: 8192},
			Height:  config.ParamRange{Enabled: true, Min: 1, Max: 8192},
			Quality: config.ParamRange{Enabled: true, Min: 10, Max: 95},
			Blur:    true,
			Format:  true,
		},
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

// TestParseRequest_PathTransformSupportsPartialParams 测试路径变换语法对部分参数的支持。
// 验证仅指定部分参数时，其余参数自动填充默认值（default_params）。
func TestParseRequest_PathTransformSupportsPartialParams(t *testing.T) {
	processor := NewProcessor((&Router{conversionRules: map[string]config.FileConversionRule{
		"png_conversion": {
			Name:             "png_conversion",
			SupportedFormats: []string{"png", "webp"},
			EnableRequestParams: config.RequestParamsConfig{
				Width:   config.ParamRange{Enabled: true, Min: 1, Max: 8192},
				Height:  config.ParamRange{Enabled: true, Min: 1, Max: 8192},
				Quality: config.ParamRange{Enabled: true, Min: 10, Max: 95},
				Blur:    true,
				Format:  true,
			},
			DefaultParams: config.ConversionDefaultParams{
				Width:   800,
				Height:  600,
				Blur:    0.5,
				Quality: 80,
				Format:  "png",
			},
		},
	}}).FileConversionRule)

	classCfg := config.ClassConfig{
		FileConversion: config.ClassFileConversionConfig{
			Enabled: true,
			Rule:    "png_conversion",
		},
	}

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
			sourcePath, opts, err := processor.ParseRequest(classCfg, tt.objectPath, url.Values{})
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
	processor := NewProcessor((&Router{conversionRules: map[string]config.FileConversionRule{
		"png_conversion": {
			Name:             "png_conversion",
			SupportedFormats: []string{"png", "webp"},
			EnableRequestParams: config.RequestParamsConfig{
				Width:   config.ParamRange{Enabled: true, Min: 1, Max: 8192},
				Height:  config.ParamRange{Enabled: true, Min: 1, Max: 8192},
				Quality: config.ParamRange{Enabled: true, Min: 10, Max: 95},
				Blur:    true,
				Format:  true,
			},
			DefaultParams: config.ConversionDefaultParams{
				Width:   800,
				Height:  600,
				Quality: 80,
				Format:  "png",
			},
		},
	}}).FileConversionRule)

	classCfg := config.ClassConfig{
		FileConversion: config.ClassFileConversionConfig{
			Enabled: true,
			Rule:    "png_conversion",
		},
	}

	if _, _, err := processor.ParseRequest(classCfg, "images/demo.jpg@320w_240h_1c_70q.webp", url.Values{}); err == nil {
		t.Fatalf("expected error for legacy blur syntax 1c")
	}

	query := url.Values{}
	query.Set("blur", "on")
	if _, _, err := processor.ParseRequest(classCfg, "images/demo.jpg@320w", query); err == nil {
		t.Fatalf("expected error for query blur=on")
	}

	query = url.Values{}
	query.Set("blur", "1.5")
	if _, _, err := processor.ParseRequest(classCfg, "images/demo.jpg@320w", query); err == nil {
		t.Fatalf("expected error for query blur with decimal value")
	}

	query = url.Values{}
	query.Set("blur", "-5")
	if _, _, err := processor.ParseRequest(classCfg, "images/demo.jpg@320w", query); err == nil {
		t.Fatalf("expected error for query blur with negative value")
	}

	if _, opts, err := processor.ParseRequest(classCfg, "images/demo.jpg@320w_5b", url.Values{}); err != nil || opts.Blur != 0.5 {
		t.Fatalf("expected blur 0.5 from path suffix 5b, got %v (err %v)", opts.Blur, err)
	}

	query = url.Values{}
	query.Set("blur", "15")
	if _, opts, err := processor.ParseRequest(classCfg, "images/demo.jpg@320w", query); err != nil || opts.Blur != 1.5 {
		t.Fatalf("expected blur 1.5 from query 15, got %v (err %v)", opts.Blur, err)
	}
}

// TestParseRequest_PathTransformSplitParser 测试拆分式路径转换解析的新能力：
// 顺序无关、宽度不再必填、格式/质量/模糊可单独出现、大小写单位、文件名含 @。
func TestParseRequest_PathTransformSplitParser(t *testing.T) {
	processor := processorWithRule(fullTestRule())

	classCfg := config.ClassConfig{
		FileConversion: config.ClassFileConversionConfig{
			Enabled: true,
			Rule:    "png_conversion",
		},
	}

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
			sourcePath, opts, err := processor.ParseRequest(classCfg, tt.objectPath, url.Values{})
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

	classCfg := config.ClassConfig{
		FileConversion: config.ClassFileConversionConfig{
			Enabled: true,
			Rule:    "png_conversion",
		},
	}

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
			_, _, err := processor.ParseRequest(classCfg, tt.objectPath, url.Values{})
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
	processor := processorWithRule(config.FileConversionRule{
		Name: "png_conversion",
		EnableRequestParams: config.RequestParamsConfig{
			Width: config.ParamRange{Enabled: true, Min: 1, Max: 8192}, // 仅宽度启用
		},
		DefaultParams: config.ConversionDefaultParams{
			Width:   800,
			Height:  600,
			Blur:    0.3,
			Quality: 80,
			Format:  "png",
		},
	})

	classCfg := config.ClassConfig{
		FileConversion: config.ClassFileConversionConfig{
			Enabled: true,
			Rule:    "png_conversion",
		},
	}

	if _, opts, err := processor.ParseRequest(classCfg, "images/demo.jpg@320w", url.Values{}); err != nil || opts.Width != 320 {
		t.Fatalf("expected width-only suffix to pass, got opts=%+v err=%v", opts, err)
	}

	want := TransformOptions{Enabled: true, Width: 320, Height: 600, Blur: 0.3, Quality: 80, Format: "png"}

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
			sourcePath, opts, err := processor.ParseRequest(classCfg, tt.path, url.Values{})
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

// TestParseRequest_QueryOverridesPathSuffix 锁定同名查询参数覆盖路径后缀值的优先级。
func TestParseRequest_QueryOverridesPathSuffix(t *testing.T) {
	processor := processorWithRule(fullTestRule())

	classCfg := config.ClassConfig{
		FileConversion: config.ClassFileConversionConfig{
			Enabled: true,
			Rule:    "png_conversion",
		},
	}

	query := url.Values{}
	query.Set("width", "640")

	sourcePath, opts, err := processor.ParseRequest(classCfg, "images/demo.jpg@320w", query)
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
