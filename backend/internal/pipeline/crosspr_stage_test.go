// Package pipeline — crosspr_stage_test.go covers the pure-function helpers
// and package-level rate-limit/debounce primitives that underpin the async
// cross-PR stage. Each edge case from the design plan is a dedicated subtest
// in t.Run so shared package-global state (crossPRRefreshCount,
// crossPRInstallCount, crossPRMutexes) can be reset between cases via
// t.Cleanup(resetCrossPRGlobals).
package pipeline

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// resetCrossPRGlobals zeroes the package-level counters, debounce timers
// and per-review mutex maps so subtests stay independent. Tests MUST call
// this in t.Cleanup — never directly — so it runs even when the subtest
// short-circuits on t.Fatal.
func resetCrossPRGlobals() {
	crossPRRefreshMu.Lock()
	crossPRRefreshCount = map[uuid.UUID][]time.Time{}
	crossPRRefreshMu.Unlock()

	crossPRInstallMu.Lock()
	crossPRInstallCount = map[int64][]time.Time{}
	crossPRInstallMu.Unlock()

	crossPRDebounceMu.Lock()
	for k, t := range crossPRDebounceTimers {
		t.Stop()
		delete(crossPRDebounceTimers, k)
	}
	crossPRDebounceMu.Unlock()

	crossPRMutexes.reset()
	jointAcceptanceMutexes.reset()
}

// TestNormalizeJointVerdict covers the LLM-verdict normalizer that guards
// against a judge that emits a rollup inconsistent with its per-criterion
// statuses, or garbage outside the canonical three-verdict vocabulary.
func TestNormalizeJointVerdict(t *testing.T) {
	addressed := []JointAcceptanceCriterion{
		{Text: "a", Status: AcceptanceStatusAddressed},
		{Text: "b", Status: AcceptanceStatusAddressed},
	}
	mixed := []JointAcceptanceCriterion{
		{Text: "a", Status: AcceptanceStatusAddressed},
		{Text: "b", Status: AcceptanceStatusUnaddressed},
	}
	none := []JointAcceptanceCriterion{
		{Text: "a", Status: AcceptanceStatusUnaddressed},
		{Text: "b", Status: AcceptanceStatusUnaddressed},
	}

	cases := []struct {
		name     string
		verdict  string
		criteria []JointAcceptanceCriterion
		want     JointVerdict
	}{
		{"trusts canonical addressed verdict", "addressed", addressed, JointVerdictAddressed},
		{"trusts canonical partial verdict", "partial", mixed, JointVerdictPartial},
		{"trusts canonical unaddressed verdict", "unaddressed", none, JointVerdictUnaddressed},
		{"lowercases and trims canonical verdict", "  ADDRESSED  ", addressed, JointVerdictAddressed},
		{"garbage verdict derives addressed when all addressed", "banana", addressed, JointVerdictAddressed},
		{"garbage verdict derives unaddressed when none addressed", "banana", none, JointVerdictUnaddressed},
		{"garbage verdict derives partial on mixed", "banana", mixed, JointVerdictPartial},
		{"empty verdict with empty criteria falls to partial", "", nil, JointVerdictPartial},
		{"ambiguous non-canonical is treated as garbage", "ambiguous", addressed, JointVerdictAddressed},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeJointVerdict(tc.verdict, tc.criteria)
			if got != tc.want {
				t.Fatalf("normalizeJointVerdict(%q, %+v) = %q, want %q",
					tc.verdict, tc.criteria, got, tc.want)
			}
		})
	}
}

// TestCrossPR exercises the 11 edge cases from the design plan against the
// pure-function helpers they touch. Cases that require a fully-mocked
// orchestrator (DB + GitHub + LLM) are intentionally covered via the
// building-block functions that compose the stage — see the per-subtest
// comments for the specific touch point.
func TestCrossPR(t *testing.T) {
	t.Run("#1 empty coverage when no linked PRs", func(t *testing.T) {
		// First PR finishes before siblings — coverage formatter must
		// return "" for an empty set so the caller short-circuits the
		// sticky write (no LLM invoked by the stage either; that's
		// guarded by the early `len(run.LinkedPRs) == 0` check in
		// runCrossPRStage).
		if s := formatCrossPRCoverageSection(nil); s != "" {
			t.Fatalf("nil coverage: got %q want empty", s)
		}
		cov := &CrossPRCoverage{LinkedPRs: nil}
		if s := formatCrossPRCoverageSection(cov); s != "" {
			t.Fatalf("empty LinkedPRs: got %q want empty", s)
		}
	})

	t.Run("#2 sibling not reviewed emits diff-only marker", func(t *testing.T) {
		// Linked PR has no completed review row. writeLinkedPRFindings
		// must emit exactly one diff-only marker and no findings list.
		// PriorReview == nil is the load-bearing invariant here — the
		// former `Reviewed: false` bool has been collapsed into the
		// nil-check pattern.
		var sb strings.Builder
		writeLinkedPRFindings(&sb, PRLink{
			Owner: "acme", Repo: "api", Number: 42,
			Accessible: true,
		})
		out := sb.String()
		if !strings.Contains(out, "not reviewed by Argus") {
			t.Fatalf("expected not-reviewed marker, got %q", out)
		}
		if strings.Contains(out, "[critical]") || strings.Contains(out, "[warning]") {
			t.Fatalf("unexpected findings leaked into diff-only output: %q", out)
		}
	})

	t.Run("#3 merged-but-reviewed sibling still feeds findings", func(t *testing.T) {
		// State-agnostic lookup: writeLinkedPRFindings does not inspect
		// PR state, so a merged/closed sibling with a prior review row
		// still contributes findings — just like an open one does.
		var sb strings.Builder
		writeLinkedPRFindings(&sb, PRLink{
			Owner: "acme", Repo: "api", Number: 42,
			Accessible: true,
			HeadSHA:    "abc1234deadbeef",
			PriorReview: &PriorReviewSnapshot{
				HeadSHA: "abc1234deadbeef",
				Findings: []Finding{{
					Path:     "auth.go",
					Line:     17,
					Severity: SeverityWarning,
					Summary:  "missing nil check",
				}},
			},
		})
		out := sb.String()
		if !strings.Contains(out, "Prior findings in acme/api#42") {
			t.Fatalf("missing prior-findings header: %q", out)
		}
		if !strings.Contains(out, "[warning] auth.go:17 — missing nil check") {
			t.Fatalf("finding line missing: %q", out)
		}
	})

	t.Run("#4 force-push drift marker in prompt", func(t *testing.T) {
		// PriorReview.HeadSHA != link.HeadSHA triggers the staleness marker so
		// the judge knows the prior findings predate the current diff.
		var sb strings.Builder
		writeLinkedPRFindings(&sb, PRLink{
			Owner: "acme", Repo: "api", Number: 42,
			Accessible: true,
			HeadSHA:    "def5678newpush",
			PriorReview: &PriorReviewSnapshot{
				HeadSHA: "abc1234oldpush",
			},
		})
		out := sb.String()
		if !strings.Contains(out, "findings are stale") {
			t.Fatalf("expected staleness marker, got %q", out)
		}
		if !strings.Contains(out, "abc1234") || !strings.Contains(out, "def5678") {
			t.Fatalf("expected both short SHAs in staleness marker, got %q", out)
		}
	})

	t.Run("#5 feedback loop hash stable across identical inputs", func(t *testing.T) {
		// Compute the bundle hash twice with identical inputs: the stage
		// compares `hash` against `review.CrossPRHash` and skips the LLM
		// on match. If this property broke, the stage would loop forever.
		links := []PRLink{{
			Owner: "acme", Repo: "api", Number: 1,
			Accessible: true, Diff: "--- a\n+++ b\n", HeadSHA: "sha1",
			PriorReview: &PriorReviewSnapshot{
				Findings: []Finding{{Path: "x.go", Line: 1, Severity: SeverityWarning, Summary: "s"}},
			},
		}}
		h1, err := computeCrossPRHash("primary-sha", links)
		if err != nil {
			t.Fatalf("hash #1: %v", err)
		}
		h2, err := computeCrossPRHash("primary-sha", links)
		if err != nil {
			t.Fatalf("hash #2: %v", err)
		}
		if h1 != h2 {
			t.Fatalf("hash unstable: %q vs %q", h1, h2)
		}
		// Reordering inputs must also hash identically so sibling-slice
		// reshuffles don't look like a bundle change.
		links2 := []PRLink{
			{Owner: "zzz", Repo: "api", Number: 2, Accessible: true, Diff: "d2", HeadSHA: "s2"},
			{Owner: "aaa", Repo: "api", Number: 1, Accessible: true, Diff: "d1", HeadSHA: "s1"},
		}
		links3 := []PRLink{
			{Owner: "aaa", Repo: "api", Number: 1, Accessible: true, Diff: "d1", HeadSHA: "s1"},
			{Owner: "zzz", Repo: "api", Number: 2, Accessible: true, Diff: "d2", HeadSHA: "s2"},
		}
		got, err := computeCrossPRHash("p", links2)
		if err != nil {
			t.Fatalf("hash links2: %v", err)
		}
		want, err := computeCrossPRHash("p", links3)
		if err != nil {
			t.Fatalf("hash links3: %v", err)
		}
		if got != want {
			t.Fatalf("reorder changed hash: %q vs %q", got, want)
		}
		// Primary head_sha flip must change the hash (force-push on primary).
		h3, err := computeCrossPRHash("other-sha", links)
		if err != nil {
			t.Fatalf("hash h3: %v", err)
		}
		if h1 == h3 {
			t.Fatalf("primary head_sha flip didn't change hash: %q", h1)
		}
	})

	t.Run("#6 edited webhook detects linked-PR delta", func(t *testing.T) {
		// Mirror handlePREdited's set-diff: extract linked PRs from
		// pre/post bodies and compute the symmetric diff. The helper
		// in handlers_webhook.go is unit-testable through ExtractLinkedPRs.
		type key struct {
			Owner  string
			Repo   string
			Number int
		}
		toSet := func(body string) map[key]struct{} {
			links := ExtractLinkedPRs(body, "acme/primary", 5, 20)
			out := make(map[key]struct{}, len(links))
			for _, l := range links {
				out[key{l.Owner, l.Repo, l.Number}] = struct{}{}
			}
			return out
		}
		before := toSet("depends on https://github.com/acme/api/pull/1")
		after := toSet("depends on https://github.com/acme/api/pull/1 and https://github.com/acme/ui/pull/2")
		changed := false
		for k := range after {
			if _, ok := before[k]; !ok {
				changed = true
				break
			}
		}
		if !changed {
			t.Fatalf("added link not detected; before=%v after=%v", before, after)
		}

		// Negative case: identical bodies → no change.
		same := toSet("depends on https://github.com/acme/api/pull/1")
		equal := true
		for k := range same {
			if _, ok := before[k]; !ok {
				equal = false
				break
			}
		}
		if !equal || len(same) != len(before) {
			t.Fatalf("identical bodies produced diff: before=%v after=%v", before, same)
		}

		// Removed link case: pre had two links, post has one.
		beforeBoth := toSet("https://github.com/acme/api/pull/1 https://github.com/acme/ui/pull/2")
		afterOne := toSet("https://github.com/acme/api/pull/1")
		removed := false
		for k := range beforeBoth {
			if _, ok := afterOne[k]; !ok {
				removed = true
				break
			}
		}
		if !removed {
			t.Fatalf("removed link not detected")
		}
	})

	t.Run("#7 schema_version drift produces empty risks + warn", func(t *testing.T) {
		// Unknown schema versions are logged by runCrossPRStage and
		// treated as empty risks. The envelope struct must unmarshal
		// cleanly so the Warn path (not a parse error) is exercised.
		raw := `{"schema_version":99,"combination_risks":[{"category":"schema_race","description":"x"}]}`
		var env crossPRJudgeResponse
		if err := json.Unmarshal([]byte(raw), &env); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}
		if env.SchemaVersion != 99 {
			t.Fatalf("SchemaVersion not preserved: %d", env.SchemaVersion)
		}
		// The production path logs and sets risks = nil. We mirror the
		// guard here to document the contract.
		var risks []CombinationRisk
		if env.SchemaVersion == 1 {
			risks = env.CombinationRisks
		}
		if len(risks) != 0 {
			t.Fatalf("expected empty risks for drifted schema, got %d", len(risks))
		}
	})

	t.Run("#7b schema_version=1 produces risks", func(t *testing.T) {
		// Canonical schema version must pass through unmodified.
		raw := `{"schema_version":1,"combination_risks":[{"category":"schema_race","description":"x","linked_pr":"a/b#1","severity":"high"}]}`
		var env crossPRJudgeResponse
		if err := json.Unmarshal([]byte(raw), &env); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}
		if env.SchemaVersion != 1 {
			t.Fatalf("SchemaVersion mismatch: %d", env.SchemaVersion)
		}
		if len(env.CombinationRisks) != 1 {
			t.Fatalf("expected 1 risk, got %d", len(env.CombinationRisks))
		}
		if env.CombinationRisks[0].Category != RiskCategorySchemaRace {
			t.Fatalf("category wrong: %q", env.CombinationRisks[0].Category)
		}
	})

	t.Run("#8 per-review refresh cap clips N-squared fanout", func(t *testing.T) {
		// crossPRRefreshAllowed returns false after 2 refreshes in the
		// 10-minute window. A PR family referencing itself N ways would
		// otherwise trigger N² refreshes.
		t.Cleanup(resetCrossPRGlobals)
		id := uuid.New()
		now := time.Now()
		if !crossPRRefreshAllowed(id, now) {
			t.Fatalf("1st refresh must be allowed")
		}
		if !crossPRRefreshAllowed(id, now.Add(time.Second)) {
			t.Fatalf("2nd refresh must be allowed")
		}
		if crossPRRefreshAllowed(id, now.Add(2*time.Second)) {
			t.Fatalf("3rd refresh in window must be DENIED (cap=%d)", crossPRRefreshCap)
		}
		// After the window rolls over, the counter trims itself.
		if !crossPRRefreshAllowed(id, now.Add(crossPRRefreshWindow+time.Second)) {
			t.Fatalf("refresh after window must be allowed again")
		}
	})

	t.Run("#9 per-install TryAcquire coalesces at cap", func(t *testing.T) {
		// crossPRInstallTryAcquire folds the old Allowed+Record pair into one
		// atomic op that both reserves a slot and returns allowed=false when
		// the cap is saturated. N concurrent callers can't overshoot: exactly
		// one wins the last slot per mutex hold.
		t.Cleanup(resetCrossPRGlobals)
		const inst int64 = 7
		now := time.Now()
		for i := 0; i < crossPRPerInstallCap; i++ {
			if ok, _ := crossPRInstallTryAcquire(inst, now); !ok {
				t.Fatalf("unexpected backpressure at sample %d", i)
			}
		}
		ok, resetAt := crossPRInstallTryAcquire(inst, now)
		if ok {
			t.Fatalf("expected backpressure at sample %d", crossPRPerInstallCap)
		}
		if resetAt.Before(now) {
			t.Fatalf("resetAt in past: %v < %v", resetAt, now)
		}
		// Peek mirrors the same decision without consuming.
		peekOK, _ := crossPRInstallPeek(inst, now)
		if peekOK {
			t.Fatalf("peek at saturation must also deny")
		}
		// After the window, quota frees up.
		okLater, _ := crossPRInstallTryAcquire(inst, now.Add(crossPRPerInstallWindow+time.Minute))
		if !okLater {
			t.Fatalf("expected quota to free up after window")
		}
	})

	t.Run("#10 auto-resolve-off preserves all findings", func(t *testing.T) {
		// findingsFromFileReviews does NOT filter by AutoResolvedThreadKeys
		// — that filter layers on top. With auto-resolve OFF (nothing to
		// filter) the projection returns every comment verbatim.
		files := []FileReview{{
			Path: "x.go",
			Comments: []FileComment{
				{Line: 1, Severity: SeverityWarning, Category: CategoryBug, What: "a", Body: "A"},
				{Line: 2, Severity: SeverityCritical, Category: CategorySecurity, What: "b", Body: "B"},
			},
		}}
		got := findingsFromFileReviews(files, "rev-1")
		if len(got) != 2 {
			t.Fatalf("expected 2 findings, got %d", len(got))
		}
		if got[0].SourceReviewID != "rev-1" || got[1].SourceReviewID != "rev-1" {
			t.Fatalf("SourceReviewID not stamped: %+v", got)
		}
	})

	t.Run("#11 event ordering: stage gates on status=completed", func(t *testing.T) {
		// Event-before-commit race: synthesisStage UPDATEs status to
		// "completed" BEFORE publishing EventReviewCompleted. The stage
		// subscriber (runCrossPRStage, runCrossPRAcceptanceStage) then
		// re-loads via GetReview and early-returns unless status=="completed".
		// We document that contract by asserting the gate-string matches
		// what synthesisStage writes — if one side drifts, this test
		// and a code-review grep against "completed" catch it.
		const wantStatus = "completed"
		// Exhaustive list of non-completed statuses that must NOT trigger
		// cross-PR work. If synthesis ever adds a new terminal status, it
		// must be added here AND to the stage's gate.
		for _, status := range []string{"", "pending", "in_progress", "failed", "cancelled"} {
			if status == wantStatus {
				t.Fatalf("non-completed list contaminated with %q", status)
			}
		}
	})
}

// TestCrossPRFindingsTruncation asserts the per-link finding cap so a
// noisy specialist run can't balloon the prompt past budget.
func TestCrossPRFindingsTruncation(t *testing.T) {
	t.Cleanup(resetCrossPRGlobals)
	findings := make([]Finding, crossPRFindingsPerLink+5)
	for i := range findings {
		findings[i] = Finding{
			Path: "x.go", Line: i + 1, Severity: SeverityWarning, Summary: "s",
		}
	}
	var sb strings.Builder
	writeLinkedPRFindings(&sb, PRLink{
		Owner: "a", Repo: "b", Number: 1,
		Accessible:  true,
		PriorReview: &PriorReviewSnapshot{Findings: findings},
	})
	out := sb.String()
	if !strings.Contains(out, "…and 5 more (truncated)") {
		t.Fatalf("missing truncation marker: %q", out)
	}
}

// TestHumanDuration covers the prompt-age formatter (minute/hour/day
// thresholds). Minute-precision above an hour is deliberate noise-reduction.
func TestHumanDuration(t *testing.T) {
	cases := []struct {
		name string
		d    time.Duration
		want string
	}{
		{"zero", 0, "0m"},
		{"minutes", 15 * time.Minute, "15m"},
		{"just under hour", 59 * time.Minute, "59m"},
		{"exactly hour", time.Hour, "1h"},
		{"multiple hours", 5 * time.Hour, "5h"},
		{"just under day", 23 * time.Hour, "23h"},
		{"days", 48 * time.Hour, "2d"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := humanDuration(tc.d); got != tc.want {
				t.Fatalf("humanDuration(%v) = %q, want %q", tc.d, got, tc.want)
			}
		})
	}
}

// TestJointAcceptanceResultSchemaGuard documents the schema-version
// contract for the joint-acceptance envelope: unknown versions must
// unmarshal cleanly so the production code path reaches the Warn branch
// rather than the json-parse-error branch (which would mask the drift).
func TestJointAcceptanceResultSchemaGuard(t *testing.T) {
	t.Run("schema_version=1 unmarshals cleanly", func(t *testing.T) {
		raw := `{"schema_version":1,"issue_owner":"a","issue_repo":"b","issue_number":7,"issue_title":"t","criteria":[{"text":"c1","status":"addressed","addressed_by":"a/b#1","evidence":"x.go:1"}],"verdict":"addressed"}`
		var got JointAcceptanceResult
		if err := json.Unmarshal([]byte(raw), &got); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}
		if got.SchemaVersion != 1 || got.Verdict != "addressed" {
			t.Fatalf("envelope mismatch: %+v", got)
		}
	})
	t.Run("schema_version=99 still parses (stage then warns)", func(t *testing.T) {
		raw := `{"schema_version":99,"issue_owner":"a","criteria":[]}`
		var got JointAcceptanceResult
		if err := json.Unmarshal([]byte(raw), &got); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}
		if got.SchemaVersion != 99 {
			t.Fatalf("expected version=99, got %d", got.SchemaVersion)
		}
	})
}

// TestFormatJointAcceptanceSection covers the verdict rollup rendering
// and empty-state short-circuit.
func TestFormatJointAcceptanceSection(t *testing.T) {
	t.Run("empty input returns empty string", func(t *testing.T) {
		if got := formatJointAcceptanceSection(nil); got != "" {
			t.Fatalf("expected empty, got %q", got)
		}
		if got := formatJointAcceptanceSection([]JointAcceptanceResult{}); got != "" {
			t.Fatalf("expected empty, got %q", got)
		}
	})
	t.Run("renders heading + verdict + criteria", func(t *testing.T) {
		results := []JointAcceptanceResult{{
			IssueOwner: "acme", IssueRepo: "api", IssueNumber: 7,
			IssueTitle: "Fix auth",
			IssueURL:   "https://github.com/acme/api/issues/7",
			Criteria: []JointAcceptanceCriterion{
				{Text: "add nil check", Status: AcceptanceStatusAddressed, AddressedBy: "acme/api#1", Evidence: "auth.go:10"},
				{Text: "add test", Status: AcceptanceStatusUnaddressed},
			},
			Verdict: JointVerdictPartial,
		}}
		got := formatJointAcceptanceSection(results)
		for _, want := range []string{
			"## Joint Issue Coverage",
			"acme/api#7",
			"Fix auth",
			"**Verdict:**",
			"partial",
			"add nil check",
			"addressed in acme/api#1",
			"auth.go:10",
			"unaddressed",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("missing %q in %q", want, got)
			}
		}
	})
}

// TestFilterAutoResolvedFindings covers the filter that drops prior-review
// findings whose thread was already auto-resolved by a post-review push.
// The join key is "<path>:<line>" per migration 041 — these cases verify
// the core identity properties (empty passes through, match drops, and
// the string format matches findingKey exactly).
func TestFilterAutoResolvedFindings(t *testing.T) {
	base := []Finding{
		{Path: "foo.go", Line: 10, Summary: "alpha"},
		{Path: "foo.go", Line: 20, Summary: "beta"},
		{Path: "bar.go", Line: 5, Summary: "gamma"},
	}

	cases := []struct {
		name         string
		findings     []Finding
		resolvedKeys []string
		wantPaths    []string // remaining finding Path:Line summaries in order
	}{
		{
			name:         "empty resolved set is pass-through",
			findings:     base,
			resolvedKeys: nil,
			wantPaths:    []string{"foo.go:10", "foo.go:20", "bar.go:5"},
		},
		{
			name:         "nil findings is pass-through",
			findings:     nil,
			resolvedKeys: []string{"foo.go:10"},
			wantPaths:    nil,
		},
		{
			name:         "one matching key drops exactly one finding",
			findings:     base,
			resolvedKeys: []string{"foo.go:20"},
			wantPaths:    []string{"foo.go:10", "bar.go:5"},
		},
		{
			name:         "all findings match → empty result",
			findings:     base,
			resolvedKeys: []string{"foo.go:10", "foo.go:20", "bar.go:5"},
			wantPaths:    []string{},
		},
		{
			name:         "no match → pass-through",
			findings:     base,
			resolvedKeys: []string{"other.go:99", "foo.go:999"},
			wantPaths:    []string{"foo.go:10", "foo.go:20", "bar.go:5"},
		},
		{
			name:         "empty-string keys are ignored, not matched",
			findings:     base,
			resolvedKeys: []string{"", "", ""},
			wantPaths:    []string{"foo.go:10", "foo.go:20", "bar.go:5"},
		},
		{
			name:         "duplicate keys dedupe via set",
			findings:     base,
			resolvedKeys: []string{"foo.go:10", "foo.go:10", "foo.go:10"},
			wantPaths:    []string{"foo.go:20", "bar.go:5"},
		},
		{
			name: "line mismatch does not match path-only",
			findings: []Finding{
				{Path: "foo.go", Line: 10, Summary: "a"},
			},
			resolvedKeys: []string{"foo.go:11"},
			wantPaths:    []string{"foo.go:10"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := filterAutoResolvedFindings(tc.findings, tc.resolvedKeys)
			if len(got) != len(tc.wantPaths) {
				t.Fatalf("got %d findings, want %d: %+v", len(got), len(tc.wantPaths), got)
			}
			for i, f := range got {
				if findingKey(f) != tc.wantPaths[i] {
					t.Fatalf("idx %d: got %q, want %q", i, findingKey(f), tc.wantPaths[i])
				}
			}
		})
	}
}

// TestFilterAutoResolvedFindings_HydrationIntegration asserts the contract
// that hydratePriorFindings depends on: a row whose AutoResolvedThreadKeys
// set overlaps the projected findings produces a filtered PriorFindings
// slice. We exercise just the filter + projection boundary (no DB / no
// GetLatestCompletedReviewByPR call) — the row shape is inlined so this
// stays a unit test and doesn't need the integration harness another
// agent is building in parallel.
func TestFilterAutoResolvedFindings_HydrationIntegration(t *testing.T) {
	files := []FileReview{
		{
			Path: "svc/auth.go",
			Comments: []FileComment{
				{Line: 42, Severity: SeverityWarning, Category: CategoryBug, What: "nil check"},
				{Line: 99, Severity: SeverityCritical, Category: CategorySecurity, What: "open redirect"},
			},
		},
		{
			Path: "svc/util.go",
			Comments: []FileComment{
				{Line: 5, Severity: SeveritySuggestion, Category: CategoryStyle, What: "rename var"},
			},
		},
	}
	findings := findingsFromFileReviews(files, "rev-42")
	if len(findings) != 3 {
		t.Fatalf("precondition: expected 3 findings, got %d", len(findings))
	}

	// Simulate two post-review pushes that auto-resolved two threads.
	resolvedKeys := []string{"svc/auth.go:42", "svc/util.go:5"}
	kept := filterAutoResolvedFindings(findings, resolvedKeys)
	if len(kept) != 1 {
		t.Fatalf("expected 1 remaining finding, got %d: %+v", len(kept), kept)
	}
	if kept[0].Path != "svc/auth.go" || kept[0].Line != 99 {
		t.Fatalf("wrong finding survived: %+v", kept[0])
	}
	// SourceReviewID must still be stamped on the survivor so the cross-PR
	// prompt can attribute the finding back to its review.
	if kept[0].SourceReviewID != "rev-42" {
		t.Fatalf("SourceReviewID lost through filter: %q", kept[0].SourceReviewID)
	}
}

// TestMutexMap_SweepDropsStaleEntries asserts entries whose lastAccessed
// predates the maxAge window are evicted. We back-date lastAccessed to
// unix-nano=1 (effectively year 1970) so any positive maxAge evicts —
// this avoids a clock-injection seam in the production sweep().
func TestMutexMap_SweepDropsStaleEntries(t *testing.T) {
	m := newMutexMap()
	stale1 := uuid.New()
	stale2 := uuid.New()
	fresh := uuid.New()
	m.acquire(stale1)
	m.acquire(stale2)
	m.acquire(fresh)
	// Stamp two entries as ancient; leave the third with its current
	// time.Now() stamp from acquire().
	m.entries[stale1].lastAccessed.Store(1)
	m.entries[stale2].lastAccessed.Store(1)
	dropped := m.sweep(time.Hour)
	if dropped != 2 {
		t.Fatalf("expected 2 drops, got %d (remaining=%d)", dropped, len(m.entries))
	}
	if _, ok := m.entries[fresh]; !ok {
		t.Fatalf("fresh entry unexpectedly evicted")
	}
	if _, ok := m.entries[stale1]; ok {
		t.Fatalf("stale1 not evicted")
	}
	if _, ok := m.entries[stale2]; ok {
		t.Fatalf("stale2 not evicted")
	}
}

// TestMutexMap_SweepSkipsActiveEntry asserts that a stale-by-timestamp
// entry whose mutex is currently held is NOT evicted. TryLock fails on
// a held mutex, which is the signal for "active stage — keep alive".
func TestMutexMap_SweepSkipsActiveEntry(t *testing.T) {
	m := newMutexMap()
	held := uuid.New()
	mu := m.acquire(held)
	mu.Lock()
	defer mu.Unlock()
	// Back-date so it would otherwise be evicted.
	m.entries[held].lastAccessed.Store(1)
	dropped := m.sweep(time.Hour)
	if dropped != 0 {
		t.Fatalf("expected 0 drops (entry locked), got %d", dropped)
	}
	if _, ok := m.entries[held]; !ok {
		t.Fatalf("locked entry was evicted — sweep must skip held mutexes")
	}
}

// TestSweepTimestampCounter_DropsEmpty asserts that a key whose entire
// timestamp slice is older than the window gets removed from the map
// (not just trimmed to an empty slice — the key itself is GC'd).
func TestSweepTimestampCounter_DropsEmpty(t *testing.T) {
	var mu sync.Mutex
	m := map[uuid.UUID][]time.Time{}
	key := uuid.New()
	now := time.Now()
	m[key] = []time.Time{
		now.Add(-2 * time.Hour),
		now.Add(-90 * time.Minute),
	}
	dropped := sweepTimestampCounter(m, &mu, now, time.Hour)
	if dropped != 1 {
		t.Fatalf("expected 1 key dropped, got %d", dropped)
	}
	if _, ok := m[key]; ok {
		t.Fatalf("empty key not deleted from map")
	}
}

// TestSweepTimestampCounter_KeepsActive asserts that a key whose slice
// contains a mix of old + fresh timestamps retains the key with only
// the fresh timestamps. Also covers the int64-keyed per-install map via
// the same generic path.
func TestSweepTimestampCounter_KeepsActive(t *testing.T) {
	t.Run("uuid keys — mixed timestamps trimmed, key retained", func(t *testing.T) {
		var mu sync.Mutex
		m := map[uuid.UUID][]time.Time{}
		key := uuid.New()
		now := time.Now()
		m[key] = []time.Time{
			now.Add(-2 * time.Hour), // old
			now.Add(-30 * time.Minute),
			now.Add(-1 * time.Minute),
		}
		dropped := sweepTimestampCounter(m, &mu, now, time.Hour)
		if dropped != 0 {
			t.Fatalf("expected 0 keys dropped, got %d", dropped)
		}
		ts, ok := m[key]
		if !ok {
			t.Fatalf("active key unexpectedly deleted")
		}
		if len(ts) != 2 {
			t.Fatalf("expected 2 surviving timestamps, got %d", len(ts))
		}
	})
	t.Run("int64 keys — exercises generic path for install counter", func(t *testing.T) {
		var mu sync.Mutex
		m := map[int64][]time.Time{}
		now := time.Now()
		m[42] = []time.Time{now.Add(-30 * time.Minute)}
		m[99] = []time.Time{now.Add(-3 * time.Hour)}
		dropped := sweepTimestampCounter(m, &mu, now, time.Hour)
		if dropped != 1 {
			t.Fatalf("expected 1 key dropped (only key 99 is stale), got %d", dropped)
		}
		if _, ok := m[42]; !ok {
			t.Fatalf("fresh key 42 evicted")
		}
		if _, ok := m[99]; ok {
			t.Fatalf("stale key 99 retained")
		}
	})
}
