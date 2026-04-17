package api

import "testing"

func TestParseLimitParam(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		raw        string
		defaultVal int
		maxLimit   int
		want       int
	}{
		{"empty → default", "", 20, 100, 20},
		{"valid in range", "50", 20, 100, 50},
		{"valid at cap", "100", 20, 100, 100},
		{"above cap clamped", "500", 20, 100, 100},
		{"zero → default", "0", 20, 100, 20},
		{"negative → default", "-5", 20, 100, 20},
		{"non-numeric → default", "abc", 20, 100, 20},
		{"mixed → default", "12x", 20, 100, 20},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := parseLimitParam(tc.raw, tc.defaultVal, tc.maxLimit); got != tc.want {
				t.Fatalf("parseLimitParam(%q, %d, %d) = %d, want %d",
					tc.raw, tc.defaultVal, tc.maxLimit, got, tc.want)
			}
		})
	}
}

// TestContainsID guards the scope primitive used by listScenarioRuns + other handlers to
// reject cross-installation access. A regression here = tenant data leak.
func TestContainsID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		ids    []int64
		target int64
		want   bool
	}{
		{"empty", nil, 42, false},
		{"hit", []int64{1, 2, 3}, 2, true},
		{"miss", []int64{1, 2, 3}, 99, false},
		{"single hit", []int64{7}, 7, true},
		{"zero target not in non-zero slice", []int64{1, 2}, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := containsID(tc.ids, tc.target); got != tc.want {
				t.Fatalf("containsID(%v, %d) = %v, want %v", tc.ids, tc.target, got, tc.want)
			}
		})
	}
}
