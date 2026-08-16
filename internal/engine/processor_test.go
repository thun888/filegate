package engine

import (
	"net/url"
	"testing"

	"github.com/thun888/filegate/config"
)

// TestParseRequest_PathTransformSupportsPartialParams 测试路径变换语法对部分参数的支持。
// 验证仅指定部分参数时，其余参数自动填充默认值（default_params）。
func TestParseRequest_PathTransformSupportsPartialParams(t *testing.T) {
	processor := NewProcessor((&Router{conversionRules: map[string]config.FileConversionRule{
		"png_conversion": {
			Name:             "png_conversion",
			SupportedFormats: []string{"png", "webp"},
			EnableRequestParams: config.RequestParamsConfig{
				Width:   config.ParamRange{Min: 1, Max: 8192},
				Height:  config.ParamRange{Min: 1, Max: 8192},
				Quality: config.ParamRange{Min: 10, Max: 95},
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
			objectPath:     "images/demo.jpg@320w_70",
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
			objectPath:     "images/demo.jpg@320w_1b_70.webp",
			wantSourcePath: "images/demo.jpg",
			wantWidth:      320,
			wantHeight:     600,
			wantBlur:       1.0,
			wantQuality:    70,
			wantFormat:     "webp",
		},
		{
			name:           "fractional blur sigma",
			objectPath:     "images/demo.jpg@320w_0.5b_70.webp",
			wantSourcePath: "images/demo.jpg",
			wantWidth:      320,
			wantHeight:     600,
			wantBlur:       0.5,
			wantQuality:    70,
			wantFormat:     "webp",
		},
		{
			name:           "full params",
			objectPath:     "images/demo.jpg@320w_240h_0b_70.webp",
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
// 验证旧语法（1c）、无效查询参数（blur=on、blur=5）均被正确拒绝，
// 小数 sigma（0.5b）可通过路径后缀与查询参数指定。
func TestParseRequest_BlurSyntaxValidation(t *testing.T) {
	processor := NewProcessor((&Router{conversionRules: map[string]config.FileConversionRule{
		"png_conversion": {
			Name:             "png_conversion",
			SupportedFormats: []string{"png", "webp"},
			EnableRequestParams: config.RequestParamsConfig{
				Width:   config.ParamRange{Min: 1, Max: 8192},
				Height:  config.ParamRange{Min: 1, Max: 8192},
				Quality: config.ParamRange{Min: 10, Max: 95},
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

	if _, _, err := processor.ParseRequest(classCfg, "images/demo.jpg@320w_240h_1c_70.webp", url.Values{}); err == nil {
		t.Fatalf("expected error for legacy blur syntax 1c")
	}

	query := url.Values{}
	query.Set("blur", "on")
	if _, _, err := processor.ParseRequest(classCfg, "images/demo.jpg@320w", query); err == nil {
		t.Fatalf("expected error for query blur=on")
	}

	query = url.Values{}
	query.Set("blur", "5")
	if _, _, err := processor.ParseRequest(classCfg, "images/demo.jpg@320w", query); err == nil {
		t.Fatalf("expected error for query blur without b suffix")
	}

	if _, opts, err := processor.ParseRequest(classCfg, "images/demo.jpg@320w_0.5b", url.Values{}); err != nil || opts.Blur != 0.5 {
		t.Fatalf("expected blur 0.5 from path suffix, got %v (err %v)", opts.Blur, err)
	}

	query = url.Values{}
	query.Set("blur", "0.25b")
	if _, opts, err := processor.ParseRequest(classCfg, "images/demo.jpg@320w", query); err != nil || opts.Blur != 0.25 {
		t.Fatalf("expected blur 0.25 from query, got %v (err %v)", opts.Blur, err)
	}
}
