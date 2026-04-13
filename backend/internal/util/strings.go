package util

import "unicode/utf8"

// Truncate caps s at maxLen bytes on a rune boundary. If ellipsis is true and
// the string was truncated, "..." is appended (not counted in maxLen).
func Truncate(s string, maxLen int, ellipsis bool) string {
	if len(s) <= maxLen {
		return s
	}
	// Walk backward to a rune boundary
	for maxLen > 0 && !utf8.RuneStart(s[maxLen]) {
		maxLen--
	}
	if ellipsis {
		return s[:maxLen] + "..."
	}
	return s[:maxLen]
}

// IntAbs returns the absolute value of an integer.
func IntAbs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
