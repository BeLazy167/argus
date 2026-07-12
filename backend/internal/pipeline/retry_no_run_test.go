// Package pipeline — retry_no_run_test.go guards retryPREvent, the pure mapping
// used to retry a review that has no persisted pipeline_states run to rebuild
// from (it died before the first state persist). DB-less: the mapping is pure;
// the surrounding retryFromReviewRow orchestration is DB/GitHub-bound.
package pipeline

import (
	"testing"

	"github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/internal/store"
)

// TestRetryPREvent_UsesLivePRSHAsAndRepoGithubID asserts the reconstructed event
// takes its SHAs/refs from the LIVE PR fetch (not the stale review row) — so the
// commit PostReview pins matches the freshly-fetched diff even after a
// push/force-push — and that RepoID carries the GitHub repo id, not the DB
// serial.
func TestRetryPREvent_UsesLivePRSHAsAndRepoGithubID(t *testing.T) {
	// livePR is what GetPullRequest returns for the PR's CURRENT head — the PR
	// has advanced since the review row was written.
	livePR := &github.PREvent{
		InstallationID: 789, // GitHub installation id — must be preserved
		RepoFullName:   "acme/widgets",
		PRNumber:       42,
		PRTitle:        "Add widget (edited)",
		PRAuthor:       "octocat",
		HeadSHA:        "newhead", // current head
		BaseSHA:        "newbase",
		BaseRef:        "main",
		HeadRef:        "feature/x2",
		PRBody:         "body",
	}
	repo := &store.Repo{
		FullName: "acme/widgets",
		GithubID: 123456, // GitHub repo id — the value the event must carry
	}

	ev := retryPREvent(livePR, repo)

	if ev.Action != "manual" {
		t.Errorf("Action = %q, want %q", ev.Action, "manual")
	}
	// SHAs/refs must come from the live fetch, not the stored review row.
	if ev.HeadSHA != "newhead" || ev.BaseSHA != "newbase" {
		t.Errorf("SHAs not from live fetch: head=%q base=%q, want newhead/newbase", ev.HeadSHA, ev.BaseSHA)
	}
	if ev.HeadRef != "feature/x2" || ev.BaseRef != "main" {
		t.Errorf("refs not from live fetch: head=%q base=%q", ev.HeadRef, ev.BaseRef)
	}
	// RepoID must be the GitHub repo id (repo.GithubID), stamped over the
	// live event's zero value.
	if ev.RepoID != repo.GithubID {
		t.Errorf("RepoID = %d, want %d (repo.GithubID)", ev.RepoID, repo.GithubID)
	}
	if ev.RepoFullName != "acme/widgets" {
		t.Errorf("RepoFullName = %q, want %q", ev.RepoFullName, "acme/widgets")
	}
	// Fields the live fetch already set must survive.
	if ev.InstallationID != 789 {
		t.Errorf("InstallationID = %d, want 789 (preserved from live fetch)", ev.InstallationID)
	}
	if ev.PRNumber != 42 {
		t.Errorf("PRNumber = %d, want 42", ev.PRNumber)
	}
	if ev.PRTitle != "Add widget (edited)" || ev.PRAuthor != "octocat" {
		t.Errorf("title/author not carried: title=%q author=%q", ev.PRTitle, ev.PRAuthor)
	}
}

func intPtr(v int) *int       { return &v }
func strPtr(v string) *string { return &v }

// TestBuildPriorComments maps persisted comments into the PriorComment map a
// no-run retry carries so it dedups against already-posted findings. Guards the
// StartLine/EndLine collapse, severity default, and empty-input nil.
func TestBuildPriorComments(t *testing.T) {
	t.Run("empty input returns nil", func(t *testing.T) {
		if got := buildPriorComments(nil); got != nil {
			t.Errorf("buildPriorComments(nil) = %v, want nil", got)
		}
	})

	t.Run("maps fields, groups by file, defaults severity", func(t *testing.T) {
		comments := []store.ReviewComment{
			{FilePath: "a.go", StartLine: intPtr(10), EndLine: intPtr(12), Body: "range finding", Severity: strPtr("warning"), Category: strPtr("bug")},
			{FilePath: "a.go", EndLine: intPtr(20), Body: "single-line finding"}, // only EndLine → Line falls back to it; nil severity → default
			{FilePath: "b.go", StartLine: intPtr(3), EndLine: intPtr(3), Body: "other file"},
		}

		got := buildPriorComments(comments)

		if len(got["a.go"]) != 2 {
			t.Fatalf("a.go comments = %d, want 2", len(got["a.go"]))
		}
		if len(got["b.go"]) != 1 {
			t.Fatalf("b.go comments = %d, want 1", len(got["b.go"]))
		}
		first := got["a.go"][0]
		if first.Line != 10 || first.EndLine != 12 || first.Severity != "warning" || first.Category != "bug" {
			t.Errorf("range comment mismapped: %+v", first)
		}
		// EndLine-only comment: Line falls back to EndLine, severity defaults.
		second := got["a.go"][1]
		if second.Line != 20 || second.EndLine != 20 {
			t.Errorf("single-line collapse wrong: Line=%d EndLine=%d, want 20/20", second.Line, second.EndLine)
		}
		if second.Severity != "suggestion" {
			t.Errorf("default severity = %q, want %q", second.Severity, "suggestion")
		}
	})
}
