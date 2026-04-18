package store

import (
	"math"
	"testing"
)

// TestUCBScore pins the behaviour of the UCB1 reference implementation that mirrors the SQL
// in ListScenariosForFiles. If this drifts from the SQL in scenarios.sql, the SQL has
// likely changed and one of them is wrong.
//
// Formula: score = wins/n + sqrt(2·ln(max(total, 1)) / n), with n==0 returning +Inf.
func TestUCBScore(t *testing.T) {
	t.Parallel()

	const epsilon = 1e-6

	tests := []struct {
		name  string
		wins  float64
		n     float64
		total float64
		want  float64 // expected score; math.Inf(+1) for newcomers
	}{
		{"never run returns +Inf", 0, 0, 0, math.Inf(+1)},
		{"never run with prior total returns +Inf", 0, 0, 100, math.Inf(+1)},

		// n==1 cases — exploration bonus dominates.
		// sqrt(2·ln(1)/1) = 0, so mean-only on a cold repo.
		{"1 run 1 win cold repo", 1, 1, 1, 1.0},
		{"1 run 0 wins cold repo", 0, 1, 1, 0.0},
		// sqrt(2·ln(100)/1) = sqrt(9.2103) ≈ 3.0351
		{"1 run 1 win busy repo", 1, 1, 100, 1.0 + math.Sqrt(2*math.Log(100)/1)},

		// n==10 — exploration bonus shrinks.
		// sqrt(2·ln(100)/10) ≈ 0.9599
		{"10 runs 10 wins", 10, 10, 100, 1.0 + math.Sqrt(2*math.Log(100)/10)},
		{"10 runs 5 wins", 5, 10, 100, 0.5 + math.Sqrt(2*math.Log(100)/10)},
		{"10 runs 0 wins", 0, 10, 100, 0.0 + math.Sqrt(2*math.Log(100)/10)},

		// Empty / sub-one total is clamped to 1 (matches GREATEST(..., 1) in SQL).
		{"n=1 total=0 clamps to ln(1)", 1, 1, 0, 1.0 + 0.0},

		// A heavily-used incumbent with a perfect win rate still loses to a newcomer
		// (newcomer is +Inf, incumbent is finite). Guarded by the n==0 branch.
		{"incumbent loses to newcomer", 1000, 1000, 1000, 1.0 + math.Sqrt(2*math.Log(1000)/1000)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := UCBScore(tc.wins, tc.n, tc.total)

			if math.IsInf(tc.want, +1) {
				if !math.IsInf(got, +1) {
					t.Fatalf("UCBScore(%g, %g, %g) = %g, want +Inf", tc.wins, tc.n, tc.total, got)
				}
				return
			}
			if math.Abs(got-tc.want) > epsilon {
				t.Fatalf("UCBScore(%g, %g, %g) = %g, want %g (±%g)",
					tc.wins, tc.n, tc.total, got, tc.want, epsilon)
			}
		})
	}
}

// TestUCBScore_ordering documents the intended ranking between common configurations so a
// reader of the test can see the algorithm's behaviour without running SQL.
func TestUCBScore_ordering(t *testing.T) {
	t.Parallel()

	// Newcomer outscores any finite score.
	newcomer := UCBScore(0, 0, 50)
	incumbent := UCBScore(10, 10, 50) // perfect record, 10 runs
	if !(newcomer > incumbent) {
		t.Fatalf("newcomer (%g) should outrank incumbent (%g)", newcomer, incumbent)
	}

	// Among incumbents with equal sample size, higher win rate ranks higher.
	high := UCBScore(8, 10, 100)
	low := UCBScore(2, 10, 100)
	if !(high > low) {
		t.Fatalf("higher win rate should outrank lower: high=%g low=%g", high, low)
	}

	// Scenario with fewer samples gets a larger exploration bonus when win rates tie.
	// Both 50%, but n=2 has more uncertainty than n=20.
	few := UCBScore(1, 2, 100)
	many := UCBScore(10, 20, 100)
	if !(few > many) {
		t.Fatalf("fewer samples should get higher score on tied win rate: few=%g many=%g", few, many)
	}
}

// TestUCBScore_panicsOnInvalidInput confirms the guard covers each invariant separately.
func TestUCBScore_panicsOnInvalidInput(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name              string
		wins, n, total    float64
	}{
		{"negative n", 0, -1, 10},
		{"negative wins", -1, 10, 10},
		{"wins exceeds n", 11, 10, 10},
		{"negative total", 0, 10, -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			defer func() {
				if r := recover(); r == nil {
					t.Fatalf("UCBScore(%g, %g, %g) should have panicked", tc.wins, tc.n, tc.total)
				}
			}()
			_ = UCBScore(tc.wins, tc.n, tc.total)
		})
	}
}
