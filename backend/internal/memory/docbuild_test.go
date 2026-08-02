package memory

import (
	"log/slog"
	"strings"
	"testing"
)

// TestDocBuildersInvariants pins the backend-neutral Doc shape per writer:
// container tag, type-column/metadata identity, customId determinism, and the
// custom_id metadata mirror where the writer promises one. Both backends
// consume these builders, so this is the byte-identity contract the PR-6
// shadow comparison rests on.
func TestDocBuildersInvariants(t *testing.T) {
	assertDoc := func(t *testing.T, d Doc, wantTag string, wantType MemoryType) {
		t.Helper()
		if d.ContainerTag != wantTag {
			t.Errorf("container = %q, want %q", d.ContainerTag, wantTag)
		}
		if d.Type != string(wantType) {
			t.Errorf("type = %q, want %q", d.Type, wantType)
		}
		if d.Metadata["type"] != d.Type {
			t.Errorf("metadata type %q contradicts Doc.Type %q", d.Metadata["type"], d.Type)
		}
		if d.CustomID == "" {
			t.Error("customId empty")
		}
	}

	t.Run("review", func(t *testing.T) {
		docs, skipped := buildReviewDocs("acme", "api", []ReviewMemory{
			{ReviewID: "r1", FilePath: "a.go", Body: "nil deref", Severity: "warning", Category: "bug_risk", PRNumber: 7},
		}, slog.New(slog.DiscardHandler))
		if skipped != 0 || len(docs) != 1 {
			t.Fatalf("docs=%d skipped=%d", len(docs), skipped)
		}
		assertDoc(t, docs[0], RepoTagNew("api"), TypeReview)
		if docs[0].CustomID != FindingFingerprint("acme", "api", "a.go", "bug_risk", "nil deref") {
			t.Error("review customId != FindingFingerprint")
		}
	})

	t.Run("rule", func(t *testing.T) {
		d, err := buildRuleDoc(RuleMemory{RuleID: 42, Category: "style", Content: "no dynamic imports"})
		if err != nil {
			t.Fatal(err)
		}
		assertDoc(t, d, SharedTag, TypeRule)
		if d.CustomID != RuleCustomID(42) {
			t.Error("rule customId != RuleCustomID")
		}
	})

	t.Run("pattern retypes by source and mirrors custom_id", func(t *testing.T) {
		d, err := buildPatternDoc("api", PatternMemory{Content: "C", Source: "synthesis", FilePath: "a.go"})
		if err != nil {
			t.Fatal(err)
		}
		assertDoc(t, d, RepoTagNew("api"), TypeSynthesis)
		if d.Metadata["custom_id"] != d.CustomID {
			t.Error("pattern custom_id mirror missing")
		}
		again, _ := buildPatternDoc("api", PatternMemory{Content: "C", Source: "synthesis", FilePath: "a.go"})
		if again.CustomID != d.CustomID {
			t.Error("pattern customId not deterministic")
		}
	})

	t.Run("shared pattern pins confidence without mutating caller", func(t *testing.T) {
		p := PatternMemory{Content: "C", Source: "pattern", Extra: map[string]string{"repo": "api"}}
		d, err := buildSharedPatternDoc(p)
		if err != nil {
			t.Fatal(err)
		}
		assertDoc(t, d, SharedTag, TypePattern)
		if d.Metadata["confidence"] != "1.00" {
			t.Error("confidence pin missing")
		}
		if _, leaked := p.Extra["confidence"]; leaked {
			t.Error("caller Extra mutated")
		}
		if d.Metadata["custom_id"] != d.CustomID {
			t.Error("shared pattern custom_id mirror missing")
		}
	})

	t.Run("feedback dismissal keying", func(t *testing.T) {
		d, err := buildFeedbackDoc("acme", "api", FeedbackMemory{
			FilePath: "a.go", Category: "bug_risk", OriginalBody: "claim",
			Action: "dismissed", Reason: strings.Repeat("r", 400), ChangeKind: "feature",
		})
		if err != nil {
			t.Fatal(err)
		}
		assertDoc(t, d, RepoTagNew("api"), TypeFeedback)
		if d.CustomID != dismissalCustomID("api", "bug_risk", "claim") {
			t.Error("dismissal not keyed by dismissalCustomID")
		}
		if len(d.Metadata["reason"]) > 303 { // 300 + ellipsis slack
			t.Errorf("reason not truncated: %d", len(d.Metadata["reason"]))
		}
		if _, err := buildFeedbackDoc("acme", "api", FeedbackMemory{Action: "bogus"}); err == nil {
			t.Error("unsupported action must error")
		}
	})

	t.Run("scenario", func(t *testing.T) {
		d, err := buildScenarioDoc("api", 9, "desc", "high", []string{"a.go", "b.go"})
		if err != nil {
			t.Fatal(err)
		}
		assertDoc(t, d, RepoTagNew("api"), TypeScenario)
		if d.CustomID != ScenarioCustomID("api", 9) {
			t.Error("scenario customId != ScenarioCustomID")
		}
		if !strings.Contains(d.Content, "Related files: a.go, b.go") {
			t.Error("related-files suffix missing")
		}
	})
}
