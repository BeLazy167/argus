package pipeline

import (
	"strings"
	"testing"
)

func TestIsArgusTriggerBody(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
		want bool
	}{
		{name: "empty body", body: "", want: false},
		{name: "no marker", body: "just some random comment on the PR", want: false},
		{name: "marker at head", body: TriggerMarker + "\nTrigger Argus review", want: true},
		{name: "marker mid-body", body: "Some preamble\n\n" + TriggerMarker + "\nrest", want: true},
		{name: "partial marker (no match)", body: "<!-- argus-trigger -->", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := IsArgusTriggerBody(tc.body); got != tc.want {
				t.Fatalf("IsArgusTriggerBody = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCheckboxToggled(t *testing.T) {
	t.Parallel()
	const argusBodyUnchecked = TriggerMarker + "\n" + TriggerCheckboxUnchecked
	const argusBodyChecked = TriggerMarker + "\n" + TriggerCheckboxChecked
	cases := []struct {
		name   string
		before string
		after  string
		want   bool
	}{
		{
			name:   "unchecked to checked triggers",
			before: argusBodyUnchecked,
			after:  argusBodyChecked,
			want:   true,
		},
		{
			name:   "checked to unchecked does not trigger",
			before: argusBodyChecked,
			after:  argusBodyUnchecked,
			want:   false,
		},
		{
			name:   "no change does not trigger",
			before: argusBodyUnchecked,
			after:  argusBodyUnchecked,
			want:   false,
		},
		{
			name:   "unrelated body edit ignored",
			before: "random comment v1",
			after:  "random comment v2",
			want:   false,
		},
		{
			name:   "non-argus comment with checkbox transition is ignored",
			before: "- [ ] some other task",
			after:  "- [x] some other task",
			want:   false,
		},
		{
			name:   "argus body but checkbox unchanged returns false",
			before: argusBodyChecked,
			after:  argusBodyChecked + "\n\nuser appended text",
			want:   false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := CheckboxToggled(tc.before, tc.after); got != tc.want {
				t.Fatalf("CheckboxToggled = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestReplaceTriggerWithRunning(t *testing.T) {
	t.Parallel()
	t.Run("non-argus body unchanged", func(t *testing.T) {
		t.Parallel()
		in := "unrelated comment"
		if got := ReplaceTriggerWithRunning(in); got != in {
			t.Fatalf("non-trigger body should be returned unchanged, got %q", got)
		}
	})
	t.Run("checked form swapped", func(t *testing.T) {
		t.Parallel()
		in := TriggerMarker + "\n" + TriggerCheckboxChecked + "\n..."
		got := ReplaceTriggerWithRunning(in)
		if strings.Contains(got, TriggerCheckboxChecked) {
			t.Fatalf("checkbox line should be removed, got %q", got)
		}
		if !strings.Contains(got, TriggerCheckboxRunning) {
			t.Fatalf("running marker missing, got %q", got)
		}
	})
	t.Run("unchecked form swapped", func(t *testing.T) {
		t.Parallel()
		in := TriggerMarker + "\n" + TriggerCheckboxUnchecked
		got := ReplaceTriggerWithRunning(in)
		if strings.Contains(got, TriggerCheckboxUnchecked) {
			t.Fatalf("checkbox line should be removed, got %q", got)
		}
		if !strings.Contains(got, TriggerCheckboxRunning) {
			t.Fatalf("running marker missing, got %q", got)
		}
	})
	t.Run("retry cycle strips stale Failed line", func(t *testing.T) {
		t.Parallel()
		// trigger -> fail (Failed appended) -> user ticks again: no duplicate hint.
		in := TriggerMarker + "\n" + TriggerCheckboxChecked + "\n" + TriggerCheckboxFailed + "\n_tip_"
		got := ReplaceTriggerWithRunning(in)
		if strings.Contains(got, TriggerCheckboxFailed) {
			t.Fatalf("stale Failed line should be stripped on retry, got %q", got)
		}
		if !strings.Contains(got, TriggerCheckboxRunning) {
			t.Fatalf("running marker missing, got %q", got)
		}
	})
}

func TestRestoreTriggerAfterFailure(t *testing.T) {
	t.Parallel()
	t.Run("non-argus body unchanged", func(t *testing.T) {
		t.Parallel()
		in := "unrelated comment"
		if got := RestoreTriggerAfterFailure(in); got != in {
			t.Fatalf("non-trigger body should be unchanged, got %q", got)
		}
	})
	t.Run("argus body with no Running marker unchanged", func(t *testing.T) {
		t.Parallel()
		in := TriggerMarker + "\n" + TriggerCheckboxUnchecked
		if got := RestoreTriggerAfterFailure(in); got != in {
			t.Fatalf("body without Running marker should be unchanged, got %q", got)
		}
	})
	t.Run("running marker replaced with unchecked + failure note", func(t *testing.T) {
		t.Parallel()
		in := TriggerMarker + "\n" + TriggerCheckboxRunning + "\n_Tip_"
		got := RestoreTriggerAfterFailure(in)
		if strings.Contains(got, TriggerCheckboxRunning) {
			t.Fatalf("Running marker should be removed, got %q", got)
		}
		if !strings.Contains(got, TriggerCheckboxUnchecked) {
			t.Fatalf("fresh unchecked checkbox missing, got %q", got)
		}
		if !strings.Contains(got, TriggerCheckboxFailed) {
			t.Fatalf("failure note missing, got %q", got)
		}
	})
}

func TestBuildTriggerComment(t *testing.T) {
	t.Parallel()
	cost := 4.20

	cases := []struct {
		name    string
		est     TriggerEstimate
		must    []string
		mustNot []string
	}{
		{
			name: "full estimate with cost",
			est: TriggerEstimate{
				Files:      52,
				DiffLines:  1234,
				AvgTokens:  180000,
				AvgCostUSD: &cost,
				SampleSize: 5,
			},
			must: []string{
				TriggerMarker,
				TriggerCheckboxUnchecked,
				"Files changed: 52",
				"Diff lines (±): 1234",
				"180.0k tokens",
				"$4.20",
				"last 5 review",
				"@argus-eye review",
			},
		},
		{
			name: "historical only - cost unavailable",
			est: TriggerEstimate{
				Files:      10,
				AvgTokens:  50000,
				SampleSize: 3,
			},
			must: []string{
				TriggerMarker,
				TriggerCheckboxUnchecked,
				"Files changed: 10",
				"50.0k tokens",
				"last 3 review",
			},
			mustNot: []string{"$"},
		},
		{
			name: "no history, no live diff: checkbox still posted",
			est:  TriggerEstimate{Files: 0},
			must: []string{
				TriggerMarker,
				TriggerCheckboxUnchecked,
				"@argus-eye review",
			},
			mustNot: []string{"Historical avg", "Files changed:", "Diff lines"},
		},
		{
			name: "live diff only (ListFiles succeeded, no history)",
			est: TriggerEstimate{
				Files:     8,
				DiffLines: 120,
			},
			must: []string{
				"Files changed: 8",
				"Diff lines (±): 120",
			},
			mustNot: []string{"Historical avg"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := BuildTriggerComment(tc.est, "argus-eye")
			for _, s := range tc.must {
				if !strings.Contains(got, s) {
					t.Errorf("body missing %q\n----\n%s\n----", s, got)
				}
			}
			for _, s := range tc.mustNot {
				if strings.Contains(got, s) {
					t.Errorf("body unexpectedly contains %q\n----\n%s\n----", s, got)
				}
			}
		})
	}
}

func TestHumanizeTokens(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   int64
		want string
	}{
		{in: 0, want: "0"},
		{in: 999, want: "999"},
		{in: 1_500, want: "1.5k"},
		{in: 125_000, want: "125.0k"},
		{in: 1_500_000, want: "1.5M"},
		{in: 10_000_000, want: "10.0M"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			if got := humanizeTokens(tc.in); got != tc.want {
				t.Fatalf("humanizeTokens(%d) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
