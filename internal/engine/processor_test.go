package engine

import (
	"net/url"
	"testing"

	"filegate/config"
)

func TestParseRequest_PathTransformSupportsPartialParams(t *testing.T) {
	processor := NewProcessor(&config.Config{
		FileConversionRules: []config.FileConversionRule{
			{
				Name:             "png_conversion",
				SupportedFormats: []string{"png", "webp"},
				EnableRequestParams: config.RequestParamsConfig{
					Width:   config.ParamRange{Min: 1, Max: 8192},
					Height:  config.ParamRange{Min: 1, Max: 8192},
					Quality: config.ParamRange{Min: 10, Max: 95},
					Blur:    true,
					Format:  true,
					Zip:     true,
				},
				DefaultParams: config.ConversionDefaultParams{
					Width:   800,
					Height:  600,
					Blur:    0,
					Quality: 80,
					Format:  "png",
					Zip:     false,
				},
			},
		},
	})

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
		wantBlur       int
		wantQuality    int
		wantFormat     string
	}{
		{
			name:           "width only",
			objectPath:     "images/demo.jpg@320w",
			wantSourcePath: "images/demo.jpg",
			wantWidth:      320,
			wantHeight:     600,
			wantBlur:       0,
			wantQuality:    80,
			wantFormat:     "png",
		},
		{
			name:           "width and quality",
			objectPath:     "images/demo.jpg@320w_70",
			wantSourcePath: "images/demo.jpg",
			wantWidth:      320,
			wantHeight:     600,
			wantBlur:       0,
			wantQuality:    70,
			wantFormat:     "png",
		},
		{
			name:           "width and format",
			objectPath:     "images/demo.jpg@320w.webp",
			wantSourcePath: "images/demo.jpg",
			wantWidth:      320,
			wantHeight:     600,
			wantBlur:       0,
			wantQuality:    80,
			wantFormat:     "webp",
		},
		{
			name:           "width blur quality format",
			objectPath:     "images/demo.jpg@320w_1b_70.webp",
			wantSourcePath: "images/demo.jpg",
			wantWidth:      320,
			wantHeight:     600,
			wantBlur:       1,
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
				t.Fatalf("blur = %d, want %d", opts.Blur, tt.wantBlur)
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

func TestParseRequest_BlurSyntaxValidation(t *testing.T) {
	processor := NewProcessor(&config.Config{
		FileConversionRules: []config.FileConversionRule{
			{
				Name:             "png_conversion",
				SupportedFormats: []string{"png", "webp"},
				EnableRequestParams: config.RequestParamsConfig{
					Width:   config.ParamRange{Min: 1, Max: 8192},
					Height:  config.ParamRange{Min: 1, Max: 8192},
					Quality: config.ParamRange{Min: 10, Max: 95},
					Blur:    true,
					Format:  true,
					Zip:     true,
				},
				DefaultParams: config.ConversionDefaultParams{
					Width:   800,
					Height:  600,
					Blur:    0,
					Quality: 80,
					Format:  "png",
					Zip:     false,
				},
			},
		},
	})

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
}
