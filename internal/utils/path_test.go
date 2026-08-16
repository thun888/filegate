package utils

import (
	"testing"
)

func TestNormalizePath_TraversalAttacks(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantError bool
	}{
		// 基础穿越攻击
		{
			name:      "standard traversal ../etc/passwd",
			input:     "../etc/passwd",
			wantError: true,
		},
		{
			name:      "multi-level traversal ../../etc/passwd",
			input:     "../../etc/passwd",
			wantError: true,
		},
		{
			name:      "mid-path traversal foo/../../bar",
			input:     "foo/../../bar",
			wantError: true,
		},
		{
			name:      "deep traversal foo/../../../etc/passwd",
			input:     "foo/../../../etc/passwd",
			wantError: true,
		},
		{
			name:      "single dot-dot",
			input:     "..",
			wantError: true,
		},
		{
			name:      "dot-dot with trailing slash",
			input:     "../",
			wantError: true,
		},
		// 混合斜线攻击
		{
			name:      "backslash traversal",
			input:     "..\\etc/passwd",
			wantError: true,
		},
		{
			name:      "windows style backslash traversal",
			input:     "..\\..\\etc\\passwd",
			wantError: true,
		},
		{
			name:      "mixed separators",
			input:     "..../\\./etc/passwd",
			wantError: false, // path.Clean 不将 \ 视为分隔符，.... 为合法目录名，无实际穿越风险
		},
		// 路径规范化后穿越
		{
			name:      "dot-dot in middle path/a/../b/../../c",
			input:     "path/a/../b/../../c",
			wantError: true,
		},
		{
			name:      "dot-dot with valid prefix images/../../../etc/passwd",
			input:     "images/../../../etc/passwd",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NormalizePath(tt.input)
			if (err != nil) != tt.wantError {
				t.Errorf("NormalizePath(%q) error = %v, wantError %v", tt.input, err, tt.wantError)
			}
		})
	}
}

func TestNormalizePath_EdgeCases(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantError bool
	}{
		{
			name:      "empty string",
			input:     "",
			wantError: true,
		},
		{
			name:      "only spaces",
			input:     "   ",
			wantError: true,
		},
		{
			name:      "only dots",
			input:     "...",
			wantError: false,
		},
		{
			name:      "single dot",
			input:     ".",
			wantError: true,
		},
		{
			name:      "leading slash",
			input:     "/images/demo.jpg",
			wantError: false,
		},
		{
			name:      "trailing slash",
			input:     "images/demo.jpg/",
			wantError: false,
		},
		{
			name:      "multiple slashes",
			input:     "images///demo.jpg",
			wantError: false,
		},
		{
			name:      "dot in filename",
			input:     "images/demo.test.jpg",
			wantError: false,
		},
		{
			name:      "dotfiles",
			input:     ".hidden",
			wantError: false,
		},
		{
			name:      "dotfile in directory",
			input:     "dir/.hidden/file.txt",
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NormalizePath(tt.input)
			if (err != nil) != tt.wantError {
				t.Errorf("NormalizePath(%q) error = %v, wantError %v", tt.input, err, tt.wantError)
			}
		})
	}
}

func TestNormalizePath_ValidPaths(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple image path",
			input:    "images/demo.jpg",
			expected: "images/demo.jpg",
		},
		{
			name:     "nested path",
			input:    "a/b/c/file.txt",
			expected: "a/b/c/file.txt",
		},
		{
			name:     "single file",
			input:    "file.txt",
			expected: "file.txt",
		},
		{
			name:     "path with spaces trimmed",
			input:    "  images/demo.jpg  ",
			expected: "images/demo.jpg",
		},
		{
			name:     "backslash converted to forward slash",
			input:    "images\\demo.jpg",
			expected: "images/demo.jpg",
		},
		{
			name:     "mixed slashes normalized",
			input:    "images/sub\\demo.jpg",
			expected: "images/sub/demo.jpg",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizePath(tt.input)
			if err != nil {
				t.Errorf("NormalizePath(%q) unexpected error = %v", tt.input, err)
				return
			}
			if got != tt.expected {
				t.Errorf("NormalizePath(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestNormalizePath_SecurityPatterns(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantError bool
	}{
		{
			name:      "null byte injection",
			input:     "images/demo.jpg\x00.png",
			wantError: false, // Go handles null bytes natively
		},
		{
			name:      "unicode dot-like characters",
			input:     "images/．．/passwd",
			wantError: false, // Full-width dots are treated as regular characters
		},
		{
			name:      "path ending with dot-dot",
			input:     "images/..",
			wantError: true,
		},
		{
			name:      "path with only dot-dot segments",
			input:     "../..",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NormalizePath(tt.input)
			if (err != nil) != tt.wantError {
				t.Errorf("NormalizePath(%q) error = %v, wantError %v", tt.input, err, tt.wantError)
			}
		})
	}
}
