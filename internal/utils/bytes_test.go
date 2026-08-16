package utils

import "testing"

func TestParseByteSize(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    int64
		wantErr bool
	}{
		{name: "empty means unset", input: "", want: 0},
		{name: "plain bytes", input: "1024", want: 1024},
		{name: "kilobytes", input: "10k", want: 10 * 1024},
		{name: "kb suffix", input: "10kb", want: 10 * 1024},
		{name: "kib suffix", input: "10kib", want: 10 * 1024},
		{name: "megabytes uppercase", input: "100MB", want: 100 * 1024 * 1024},
		{name: "mib suffix", input: "100MiB", want: 100 * 1024 * 1024},
		{name: "gigabytes", input: "1G", want: 1024 * 1024 * 1024},
		{name: "whitespace tolerated", input: " 10 MB ", want: 10 * 1024 * 1024},
		{name: "invalid unit", input: "10xb", wantErr: true},
		{name: "garbage", input: "abc", wantErr: true},
		{name: "negative rejected", input: "-10", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseByteSize(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseByteSize(%q) = %d, want error", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseByteSize(%q) unexpected error = %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("ParseByteSize(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}
