package utils

import (
	"testing"
	"time"
)

func TestParseDuration(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    time.Duration
		wantErr bool
	}{
		{name: "standard go duration", input: "30s", want: 30 * time.Second},
		{name: "days", input: "7d", want: 7 * 24 * time.Hour},
		{name: "weeks", input: "2w", want: 14 * 24 * time.Hour},
		{name: "composite", input: "1w2d3h", want: (9 * 24 * time.Hour) + (3 * time.Hour)},
		{name: "fractional days", input: "1.5d", want: 36 * time.Hour},
		{name: "negative", input: "-2d", want: -48 * time.Hour},
		{name: "invalid empty", input: "", wantErr: true},
		{name: "invalid unit", input: "3x", wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseDuration(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseDuration(%q) error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("ParseDuration(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
