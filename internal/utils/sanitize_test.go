package utils

import (
	"testing"
)

func TestSanitizePath_URLDecoding(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "URL encoded dot-dot-slash %2e%2e%2f",
			input:    "%2e%2e%2f",
			expected: "../",
		},
		{
			name:     "partial encoding ..%2f",
			input:    "..%2f",
			expected: "../",
		},
		{
			name:     "partial encoding %2e%2e/",
			input:    "%2e%2e/",
			expected: "../",
		},
		{
			name:     "encoded dots with slash %2e%2e/etc/passwd",
			input:    "%2e%2e/etc/passwd",
			expected: "../etc/passwd",
		},
		{
			name:     "double encoding %252e%252e%252f",
			input:    "%252e%252e%252f",
			expected: "../",
		},
		{
			name:     "no encoding",
			input:    "images/demo.jpg",
			expected: "images/demo.jpg",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SanitizePath(tt.input)
			if err != nil {
				t.Fatalf("SanitizePath(%q) unexpected error = %v", tt.input, err)
			}
			if got != tt.expected {
				t.Errorf("SanitizePath(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestSanitizePath_WhitespaceTrimming(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "dot-dot with space",
			input:    ".. /etc/passwd",
			expected: "../etc/passwd",
		},
		{
			name:     "dot-dot with tab",
			input:    "..\t/etc/passwd",
			expected: "../etc/passwd",
		},
		{
			name:     "leading and trailing spaces",
			input:    "  images/demo.jpg  ",
			expected: "images/demo.jpg",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SanitizePath(tt.input)
			if err != nil {
				t.Fatalf("SanitizePath(%q) unexpected error = %v", tt.input, err)
			}
			if got != tt.expected {
				t.Errorf("SanitizePath(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestSanitizePath_ExcessiveEncoding(t *testing.T) {
	// 6 层编码的 %2e，超过 maxDecodeRounds=5 应报错
	input := "%25252525252e"
	_, err := SanitizePath(input)
	if err == nil {
		t.Errorf("SanitizePath(%q) expected error for excessive encoding, got nil", input)
	}
}

func TestSanitizePath_ThenNormalizePath(t *testing.T) {
	attackPaths := []string{
		"%2e%2e%2f",
		"..%2f",
		"%2e%2e/",
		"%2e%2e/etc/passwd",
		"%252e%252e%252f",
		".. /etc/passwd",
		"..\t/etc/passwd",
	}

	for _, p := range attackPaths {
		t.Run(p, func(t *testing.T) {
			sanitized, err := SanitizePath(p)
			if err != nil {
				t.Fatalf("SanitizePath(%q) unexpected error = %v", p, err)
			}
			_, err = NormalizePath(sanitized)
			if err == nil {
				t.Errorf("SanitizePath(%q) = %q, NormalizePath should reject but got no error", p, sanitized)
			}
		})
	}
}
