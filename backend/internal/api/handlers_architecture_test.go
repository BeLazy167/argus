package api

import (
	"math"
	"testing"
)

func floatEq(a, b, tol float64) bool {
	return math.Abs(a-b) <= tol
}

func TestStddev(t *testing.T) {
	cases := []struct {
		name string
		in   []float64
		want float64
	}{
		{"empty", []float64{}, 0},
		{"single", []float64{7}, 0},
		{"constant", []float64{3, 3, 3, 3}, 0},
		// variance of [1,2,3,4,5] = 2, stddev = sqrt(2) ≈ 1.414213
		{"1_to_5", []float64{1, 2, 3, 4, 5}, math.Sqrt(2)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := stddev(tc.in)
			if !floatEq(got, tc.want, 1e-9) {
				t.Fatalf("stddev(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestMaxVal(t *testing.T) {
	cases := []struct {
		name string
		in   []float64
		want float64
	}{
		{"empty", []float64{}, 0},
		{"single", []float64{4.2}, 4.2},
		// Current impl starts at 0, so an all-negative slice returns 0.
		// The handler only passes non-negative metrics, so this quirk is documented here.
		{"all_negative_returns_zero", []float64{-1, -2, -3}, 0},
		{"positive", []float64{1, 5, 3}, 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := maxVal(tc.in)
			if got != tc.want {
				t.Fatalf("maxVal(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestSafeNorm(t *testing.T) {
	cases := []struct {
		name string
		val  float64
		max  float64
		want float64
	}{
		{"zero_max", 5, 0, 0},
		{"half", 5, 10, 0.5},
		{"full", 10, 10, 1},
		{"zero_val", 0, 10, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := safeNorm(tc.val, tc.max)
			if !floatEq(got, tc.want, 1e-9) {
				t.Fatalf("safeNorm(%v,%v) = %v, want %v", tc.val, tc.max, got, tc.want)
			}
		})
	}
}

func TestPercentileValue(t *testing.T) {
	cases := []struct {
		name string
		in   []float64
		pct  int
		want float64
	}{
		{"empty", []float64{}, 50, 0},
		{"single", []float64{42}, 50, 42},
		{"sorted_p0", []float64{1, 2, 3, 4, 5}, 0, 1},
		{"sorted_p50", []float64{1, 2, 3, 4, 5}, 50, 3},
		{"sorted_p100", []float64{1, 2, 3, 4, 5}, 100, 5},
		{"unsorted", []float64{5, 1, 3, 2, 4}, 50, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := percentileValue(tc.in, tc.pct)
			if got != tc.want {
				t.Fatalf("percentileValue(%v,%d) = %v, want %v", tc.in, tc.pct, got, tc.want)
			}
		})
	}
}

func TestComputePercentiles(t *testing.T) {
	// 5 files, strict-less-than rank => all distinct values, ranks are 0/25/50/75/100.
	files := []archFile{
		{Path: "a"}, {Path: "b"}, {Path: "c"}, {Path: "d"}, {Path: "e"},
	}
	fanIns := []float64{1, 2, 3, 4, 5}
	bugDens := []float64{5, 4, 3, 2, 1}
	chgFreqs := []float64{10, 10, 20, 20, 30}
	couplings := []float64{0, 0, 0, 0, 0}

	computePercentiles(files, fanIns, bugDens, chgFreqs, couplings)

	// ascending -> ranks 0,20,40,60,80 (strictly-less-than semantics)
	wantFanIn := []int{0, 20, 40, 60, 80}
	// descending -> ranks 80,60,40,20,0
	wantBug := []int{80, 60, 40, 20, 0}
	// ties share same rank (strict <): [10,10] => 0, [20,20] => 40, 30 => 80
	wantChg := []int{0, 0, 40, 40, 80}
	// all zeros => all rank 0
	wantCoup := []int{0, 0, 0, 0, 0}

	for i := range files {
		if files[i].Percentiles.FanIn != wantFanIn[i] {
			t.Errorf("file[%d] FanIn pct = %d, want %d", i, files[i].Percentiles.FanIn, wantFanIn[i])
		}
		if files[i].Percentiles.BugDensity != wantBug[i] {
			t.Errorf("file[%d] BugDensity pct = %d, want %d", i, files[i].Percentiles.BugDensity, wantBug[i])
		}
		if files[i].Percentiles.ChangeFrequency != wantChg[i] {
			t.Errorf("file[%d] ChangeFrequency pct = %d, want %d", i, files[i].Percentiles.ChangeFrequency, wantChg[i])
		}
		if files[i].Percentiles.Coupling != wantCoup[i] {
			t.Errorf("file[%d] Coupling pct = %d, want %d", i, files[i].Percentiles.Coupling, wantCoup[i])
		}
	}
}

func TestSplitEdgeKey(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"normal", "a\x00b", []string{"a", "b"}},
		{"missing_delim", "abc", nil},
		{"empty", "", nil},
		{"multi_delim", "a\x00b\x00c", []string{"a", "b\x00c"}},
		{"leading_delim", "\x00xyz", []string{"", "xyz"}},
		{"trailing_delim", "xyz\x00", []string{"xyz", ""}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := splitEdgeKey(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("splitEdgeKey(%q) len=%d, want %d (got %v)", tc.in, len(got), len(tc.want), got)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("splitEdgeKey(%q)[%d] = %q, want %q", tc.in, i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestAdaptiveWeights(t *testing.T) {
	const tol = 1e-9

	t.Run("all_zero_metrics_equal_weights", func(t *testing.T) {
		zs := []float64{0, 0, 0}
		wFI, wB, wC, wCp := adaptiveWeights(zs, zs, zs, zs)
		if !floatEq(wFI, 0.25, tol) || !floatEq(wB, 0.25, tol) || !floatEq(wC, 0.25, tol) || !floatEq(wCp, 0.25, tol) {
			t.Fatalf("expected 0.25 across the board, got %v %v %v %v", wFI, wB, wC, wCp)
		}
	})

	t.Run("single_dominant_metric", func(t *testing.T) {
		// Only fanIn has variance; other metrics are constant => stddev 0.
		fanIns := []float64{1, 2, 3, 4, 5}
		zeros := []float64{0, 0, 0, 0, 0}
		wFI, wB, wC, wCp := adaptiveWeights(fanIns, zeros, zeros, zeros)
		if !floatEq(wFI, 1.0, tol) {
			t.Fatalf("wFanIn = %v, want ≈1.0", wFI)
		}
		if !floatEq(wB, 0, tol) || !floatEq(wC, 0, tol) || !floatEq(wCp, 0, tol) {
			t.Fatalf("non-dominant weights not ≈0: %v %v %v", wB, wC, wCp)
		}
	})

	t.Run("weights_sum_to_one_mixed", func(t *testing.T) {
		fanIns := []float64{1, 2, 3}
		bugDens := []float64{0.5, 1.5, 2.5}
		chgFreqs := []float64{10, 20, 30}
		couplings := []float64{0.1, 0.2, 0.3}
		wFI, wB, wC, wCp := adaptiveWeights(fanIns, bugDens, chgFreqs, couplings)
		sum := wFI + wB + wC + wCp
		if !floatEq(sum, 1.0, tol) {
			t.Fatalf("sum of weights = %v, want 1.0", sum)
		}
	})

	t.Run("weights_sum_to_one_all_zero", func(t *testing.T) {
		zs := []float64{0, 0}
		wFI, wB, wC, wCp := adaptiveWeights(zs, zs, zs, zs)
		sum := wFI + wB + wC + wCp
		if !floatEq(sum, 1.0, tol) {
			t.Fatalf("sum of weights = %v, want 1.0", sum)
		}
	})
}
