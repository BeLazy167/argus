package memory

import (
	"context"
	"strings"
	"testing"
)

func TestBriefingRoutesFeedbackByActionNotPolarity(t *testing.T) {
	block := MemoryBlock{Repo: []PatternMatch{
		{Content: "confirmed bug class", Metadata: map[string]string{"type": "feedback", "action": "confirmed", "polarity": "negative"}},
		{Content: "dismissed false alarm", Metadata: map[string]string{"type": "feedback", "action": "dismissed", "polarity": "positive"}},
		{Content: "clarification pending", Metadata: map[string]string{"type": "feedback", "action": "ignored", "polarity": "negative"}},
		{Content: "legacy polarity-only feedback", Metadata: map[string]string{"type": "feedback", "polarity": "positive"}},
	}}

	briefing := briefingSections(block)
	if len(briefing.Reinforced) != 1 || briefing.Reinforced[0] != "confirmed bug class" {
		t.Fatalf("reinforced = %#v", briefing.Reinforced)
	}
	if len(briefing.FalsePositives) != 1 || briefing.FalsePositives[0] != "dismissed false alarm" {
		t.Fatalf("false positives = %#v", briefing.FalsePositives)
	}
	if len(briefing.Patterns) != 0 {
		t.Fatalf("neutral or legacy feedback leaked into patterns: %#v", briefing.Patterns)
	}

	got := briefing.renderReview(3200)
	if !strings.Contains(got, "Confirmed Findings (flag recurrences)") || strings.Contains(got, "Approved Patterns") {
		t.Fatalf("feedback semantics are not explicit in rendered prompt: %q", got)
	}
	if strings.Contains(got, "clarification pending") || strings.Contains(got, "legacy polarity-only feedback") {
		t.Fatalf("neutral/unaudited feedback reached prompt: %q", got)
	}
}

func TestSearchQuarantinesLegacyReplyFeedbackNonDestructively(t *testing.T) {
	run := func(context.Context, SearchRequest) ([]PatternMatch, error) {
		return []PatternMatch{
			{Content: "legacy unverified", Metadata: map[string]string{"source": "reply_feedback"}},
			{Content: "authorized", Metadata: map[string]string{"source": "trusted_reply_feedback"}},
			{Content: "ordinary", Metadata: map[string]string{"source": "auto_learn"}},
		}, nil
	}
	got, err := searchWith(context.Background(), run, MemoryQuery{Query: "q", Repo: "repo", Scope: ScopeRepo, Type: TypePattern})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Content != "authorized" || got[1].Content != "ordinary" {
		t.Fatalf("legacy reply_feedback was not quarantined: %#v", got)
	}
}

func TestFeedbackReconciliationPlanReversesDismissal(t *testing.T) {
	base := FeedbackMemory{FilePath: "a.go", Category: "bug", OriginalBody: "race", Repo: "api", Source: SourceReactionFeedback}
	dismissed := base
	dismissed.Action = "dismissed"
	confirmed := base
	confirmed.Action = "confirmed"

	dismissPlan, err := feedbackReconciliationPlan("acme", "api", dismissed)
	if err != nil {
		t.Fatal(err)
	}
	confirmPlan, err := feedbackReconciliationPlan("acme", "api", confirmed)
	if err != nil {
		t.Fatal(err)
	}
	neutralPlan, err := feedbackReconciliationPlan("acme", "api", base)
	if err != nil {
		t.Fatal(err)
	}

	dismissalID := dismissalCustomIDForSource("api", "bug", "race", SourceReactionFeedback)
	if dismissPlan.Upsert == nil || dismissPlan.Upsert.CustomID != dismissalID {
		t.Fatalf("dismiss plan = %+v", dismissPlan)
	}
	if confirmPlan.Upsert == nil || len(confirmPlan.DeleteFirst) < 1 || confirmPlan.DeleteFirst[0] != dismissalID {
		t.Fatalf("confirmed reversal plan = %+v", confirmPlan)
	}
	if neutralPlan.Upsert != nil || len(neutralPlan.DeleteFirst) != 3 {
		t.Fatalf("neutral reversal plan = %+v", neutralPlan)
	}
	trustedDismissalID := dismissalCustomIDForSource("api", "bug", "race", SourceTrustedReplyFeedback)
	for _, id := range append(append([]string{}, neutralPlan.DeleteFirst...), neutralPlan.DeleteAfter...) {
		if id == trustedDismissalID {
			t.Fatalf("reaction plan deletes trusted-reply identity: %+v", neutralPlan)
		}
	}
	base.Source = SourceTrustedReplyFeedback
	if _, err := feedbackReconciliationPlan("acme", "api", base); err == nil {
		t.Fatal("trusted reply was accepted by reaction-only reconciler")
	}
}
