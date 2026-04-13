package util

import (
	"math"
	"strings"
	"testing"
)

func TestTruncate(t *testing.T) {
	tests := []struct {
		name     string
		s        string
		maxLen   int
		ellipsis bool
		want     string
	}{
		{"short string unchanged", "hello", 10, false, "hello"},
		{"exact length unchanged", "hello", 5, false, "hello"},
		{"over length no ellipsis", "hello world", 5, false, "hello"},
		{"over length with ellipsis", "hello world", 5, true, "hello..."},
		{"empty string", "", 10, false, ""},
		{"maxLen zero", "hello", 0, false, ""},
		{"maxLen zero with ellipsis", "hello", 0, true, "..."},
		{"utf8 multibyte no split", "héllo wörld", 6, false, "héllo"},       // "héllo" = 6 bytes (é=2), fits exactly
		{"utf8 multibyte with ellipsis", "héllo wörld", 6, true, "héllo..."}, // same — 6 bytes + ellipsis
		{"utf8 mid-rune cut", "héllo wörld", 2, false, "h"},                  // 2 bytes: é(2) won't fit at pos 1, backs up to "h"
		{"emoji no split", "hi\U0001F600there", 4, false, "hi"},
		{"emoji with ellipsis", "hi\U0001F600there", 4, true, "hi..."},
		{"very long string", strings.Repeat("a", 10000), 100, false, strings.Repeat("a", 100)},
		{"very long string with ellipsis", strings.Repeat("a", 10000), 100, true, strings.Repeat("a", 100) + "..."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Truncate(tt.s, tt.maxLen, tt.ellipsis)
			if got != tt.want {
				t.Errorf("Truncate(%q, %d, %v) = %q, want %q", tt.s, tt.maxLen, tt.ellipsis, got, tt.want)
			}
		})
	}
}

func TestIntAbs(t *testing.T) {
	tests := []struct {
		name string
		x    int
		want int
	}{
		{"positive", 42, 42},
		{"negative", -42, 42},
		{"zero", 0, 0},
		{"min int plus one", math.MinInt + 1, math.MaxInt},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IntAbs(tt.x)
			if got != tt.want {
				t.Errorf("IntAbs(%d) = %d, want %d", tt.x, got, tt.want)
			}
		})
	}
}
