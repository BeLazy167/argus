package pipeline

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/BeLazy167/argus/backend/internal/store"
)

func TestBuildStartedComment_CarriesMarkerAndLiveLink(t *testing.T) {
	body := BuildStartedComment("https://argus.reviews", "abc-123", []string{"| **Scope** | 3 files |"})

	if !strings.HasPrefix(body, StartedCommentMarker) {
		t.Errorf("progress comment must lead with the marker so a rewrite can identify it; got %q", body)
	}
	for _, want := range []string{"watch live", "https://argus.reviews/reviews/abc-123", "| **Scope** | 3 files |"} {
		if !strings.Contains(body, want) {
			t.Errorf("progress comment missing %q:\n%s", want, body)
		}
	}
}

// The bug this whole file exists for: a review that stopped must not keep
// advertising a live run.
func TestBuildTerminalStartedComment_DropsWatchLive(t *testing.T) {
	for _, outcome := range []StartedOutcome{StartedOutcomeFailed, StartedOutcomeCancelled} {
		body := BuildTerminalStartedComment(outcome, "https://argus.reviews", "abc-123", "boom", "", true)
		if strings.Contains(body, "watch live") {
			t.Errorf("%s comment still says 'watch live':\n%s", outcome, body)
		}
		if !strings.Contains(body, "https://argus.reviews/reviews/abc-123") {
			t.Errorf("%s comment dropped the dashboard link; the partial trace is still there:\n%s", outcome, body)
		}
	}
}

func TestBuildTerminalStartedComment_FailedOffersRetry(t *testing.T) {
	body := BuildTerminalStartedComment(StartedOutcomeFailed, "https://argus.reviews", "abc-123", "provider 502", "", true)

	if !strings.Contains(body, "failed") {
		t.Errorf("failed comment must say so:\n%s", body)
	}
	if !strings.Contains(body, TriggerCheckboxUnchecked) {
		t.Errorf("failed comment must offer a re-run checkbox:\n%s", body)
	}
	if !strings.Contains(body, "provider 502") {
		t.Errorf("failed comment must quote the cause:\n%s", body)
	}
}

func TestBuildTerminalStartedComment_CancelledIsNotAttributedWhenUnknown(t *testing.T) {
	body := BuildTerminalStartedComment(StartedOutcomeCancelled, "https://argus.reviews", "abc-123", "", "", true)

	if !strings.Contains(body, "cancelled") {
		t.Errorf("cancelled comment must say so:\n%s", body)
	}
	// Guards against reintroducing an invented handle: there is no
	// Clerk-to-GitHub mapping, and triggered_by is who STARTED the review.
	if strings.Contains(body, "@") {
		t.Errorf("unattributed cancel must not render an @mention:\n%s", body)
	}
}

func TestBuildTerminalStartedComment_TruncatesLongDetail(t *testing.T) {
	// A provider error can carry an entire response body; GitHub caps a comment
	// at 65536 chars and an unbounded paste is unreadable long before that.
	detail := strings.Repeat("x", maxDetailChars*3)
	body := BuildTerminalStartedComment(StartedOutcomeFailed, "https://argus.reviews", "abc-123", detail, "", false)

	if len(body) > maxDetailChars*2 {
		t.Errorf("detail was not truncated: comment is %d chars", len(body))
	}
	if !strings.Contains(body, "…") {
		t.Errorf("truncated detail should be marked with an ellipsis:\n%s", body)
	}
}

func TestTruncateDetail_IsRuneSafe(t *testing.T) {
	// Cutting mid-rune would put a broken UTF-8 sequence inside a code fence.
	got := truncateDetail(strings.Repeat("é", maxDetailChars))
	if !utf8.ValidString(got) {
		t.Errorf("truncateDetail produced invalid UTF-8: %q", got)
	}
}

func TestFormatStageModels_GroupsSharedModels(t *testing.T) {
	got := formatStageModels([]store.ModelConfig{
		{Stage: "triage", Provider: "openai", Model: "gpt-5-mini"},
		{Stage: "review", Provider: "anthropic", Model: "opus"},
		{Stage: "scoring", Provider: "openai", Model: "gpt-5-mini"},
	})

	// triage and scoring share a model, so they collapse into one entry.
	want := "triage, scoring `openai / gpt-5-mini` · review `anthropic / opus`"
	if got != want {
		t.Errorf("formatStageModels =\n  %q\nwant\n  %q", got, want)
	}
}

// The original bug: only the review model was shown, so triage/scoring spend
// looked like it came from the review model.
func TestFormatStageModels_NamesEveryStageNotJustReview(t *testing.T) {
	got := formatStageModels([]store.ModelConfig{
		{Stage: "triage", Provider: "openai", Model: "cheap"},
		{Stage: "review", Provider: "anthropic", Model: "expensive"},
	})
	for _, want := range []string{"triage", "review", "cheap", "expensive"} {
		if !strings.Contains(got, want) {
			t.Errorf("stage models missing %q: %q", want, got)
		}
	}
}

func TestFormatStageModels_EmptyWhenUnconfigured(t *testing.T) {
	if got := formatStageModels(nil); got != "" {
		t.Errorf("no configs should render nothing, got %q", got)
	}
	// A row with no model name must not render a dangling backtick pair.
	if got := formatStageModels([]store.ModelConfig{{Stage: "review", Provider: "openai"}}); got != "" {
		t.Errorf("config without a model should render nothing, got %q", got)
	}
}

func TestJoinModels(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{"drops blanks", []string{"", "a", ""}, "a"},
		{"dedupes preserving order", []string{"b", "a", "b"}, "b, a"},
		{"collapses beyond two", []string{"a", "b", "c", "d"}, "a +3 more"},
		{"all blank", []string{"", ""}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := joinModels(tc.in); got != tc.want {
				t.Errorf("joinModels(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestFormatCostEstimate(t *testing.T) {
	cases := []struct {
		name  string
		stats store.RepoReviewStats
		want  string
	}{
		{
			"tokens and cost",
			store.RepoReviewStats{AvgTokens: 125_000, SampleSize: 7, CostAvailable: true, AvgCost: 0.42},
			"~125.0k tokens · ~$0.42 _(avg of last 7)_",
		},
		{
			// Normal for self-hosted and some OSS endpoints: tokens are known,
			// price is not. Showing "$0.00" would be a lie.
			"cost unavailable falls back to tokens",
			store.RepoReviewStats{AvgTokens: 9_000, SampleSize: 3},
			"~9.0k tokens _(avg of last 3)_",
		},
		{"no sample renders nothing", store.RepoReviewStats{}, ""},
		{
			"sample without tokens renders nothing",
			store.RepoReviewStats{SampleSize: 5},
			"",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatCostEstimate(tc.stats); got != tc.want {
				t.Errorf("formatCostEstimate = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResetTriggerCheckbox(t *testing.T) {
	ticked := BuildTriggerComment(TriggerEstimate{Files: 2}, "argus-eye")
	ticked = strings.Replace(ticked, TriggerCheckboxUnchecked, TriggerCheckboxChecked, 1)

	got := ResetTriggerCheckbox(ticked)
	if !strings.Contains(got, TriggerCheckboxUnchecked) {
		t.Errorf("refused click must leave an unticked box:\n%s", got)
	}
	if strings.Contains(got, TriggerCheckboxChecked) {
		t.Errorf("ticked box survived the reset:\n%s", got)
	}

	// Non-Argus bodies are returned untouched so the caller can skip the API
	// call by comparing.
	foreign := "- [x] Trigger Argus review"
	if got := ResetTriggerCheckbox(foreign); got != foreign {
		t.Errorf("non-Argus body must be untouched, got %q", got)
	}
}
