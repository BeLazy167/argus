package pipeline

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/BeLazy167/argus/backend/internal/memory"
	"github.com/BeLazy167/argus/backend/internal/memory/memorytest"
)

// recordedEvent is one EventBus publish captured by eventRecorder.
type recordedEvent struct {
	evt  EventType
	data map[string]any
}

// eventRecorder is a concurrency-safe publish sink for the Enricher's injected
// publish func, so a test can assert EventMemoryMatched fired without an
// EventBus (and stay race-clean under the per-finding fan-out).
type eventRecorder struct {
	mu     sync.Mutex
	events []recordedEvent
}

func (r *eventRecorder) publish(evt EventType, data map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, recordedEvent{evt, data})
}

func (r *eventRecorder) count(evt EventType) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, e := range r.events {
		if e.evt == evt {
			n++
		}
	}
	return n
}

// feedbackLeg answers only the dismissal leg (Type=feedback) with (matches, err)
// and returns a successful empty result for the pattern + rule legs — so a
// suppression test drives the dismissal path with the pattern/rule paths inert.
func feedbackLeg(matches []memory.PatternMatch, err error) func(memory.MemoryQuery) ([]memory.PatternMatch, error) {
	return func(q memory.MemoryQuery) ([]memory.PatternMatch, error) {
		if q.Type == memory.TypeFeedback {
			return matches, err
		}
		return nil, nil
	}
}

// enrichComments runs the Enricher over the given comments (one file) and
// returns the enriched comments plus the aggregate result.
func enrichComments(e *Enricher, comments []FileComment) ([]FileComment, EnrichResult) {
	reviews := []FileReview{{Path: "handler.go", Comments: comments}}
	res := e.Run(context.Background(), reviews)
	return reviews[0].Comments, res
}

// A pattern match ABOVE the attribution gate links + increments the
// pattern, publishes EventMemoryMatched, stamps provenance, and is NOT novel —
// the positive path end-to-end through the Enricher.
// Scores expressed against the tuned gates rather than as literals, so these
// cases keep testing the band their names describe after a recalibration.
var (
	aboveAttribution = memory.DefaultThresholdAttribution + (1.0-memory.DefaultThresholdAttribution)/2
	atOrAboveDrop    = memory.DefaultThresholdSuppressionDrop
	midDowngradeBand = (memory.DefaultThresholdSuppressionDowngrade + memory.DefaultThresholdSuppressionDrop) / 2
)

func TestEnricher_PatternMatchAboveGate(t *testing.T) {
	fake := &memorytest.Fake{
		SearchFn: patternLeg([]memory.PatternMatch{{
			Score: aboveAttribution, ID: "doc1",
			Metadata: map[string]string{"source": "auto_learn", "pr_number": "77", "pr_author": "alice"},
		}}, nil),
	}
	store := &fakeEnrichStore{byMemoryDocID: map[string]int64{"doc1": 99}}
	rec := &eventRecorder{}
	e := newTestEnricher(fake, store)
	e.publish = rec.publish

	got, res := enrichComments(e, []FileComment{
		{Severity: SeverityWarning, Category: CategoryBug, Line: 10, Body: "possible nil deref"},
	})
	c := got[0]

	if c.IsNewFinding {
		t.Error("an above-gate pattern match must not be marked novel")
	}
	if c.MatchedPatternScore != aboveAttribution || c.MatchedPatternID != 99 {
		t.Errorf("link wrong: score=%v id=%d, want %v/99", c.MatchedPatternScore, c.MatchedPatternID, aboveAttribution)
	}
	if c.MatchedPatternKind != "pattern" || c.MatchedPatternPR != 77 || c.MatchedPatternAuthor != "alice" {
		t.Errorf("provenance wrong: kind=%q pr=%d author=%q", c.MatchedPatternKind, c.MatchedPatternPR, c.MatchedPatternAuthor)
	}
	if len(store.incremented) != 1 || store.incremented[0] != 99 {
		t.Errorf("IncrementPatternMatch = %v, want [99]", store.incremented)
	}
	if rec.count(EventMemoryMatched) != 1 {
		t.Errorf("EventMemoryMatched publishes = %d, want 1", rec.count(EventMemoryMatched))
	}
	if res.Matched != 1 || res.Novel != 0 {
		t.Errorf("result = %+v, want Matched=1 Novel=0", res)
	}
}

// An identical type=pattern hit is a real learned-pattern match. Review
// comments live under type=review and cannot enter this leg, so overlap must not
// sever retrieval from relational attribution and pattern_stats.
func TestEnricher_ExactPatternMatchIncrementsStats(t *testing.T) {
	body := "nil pointer dereference crashes handler"
	fake := &memorytest.Fake{
		// Same text is the strongest possible pattern match.
		SearchFn: patternLeg([]memory.PatternMatch{{Score: 0.95, ID: "doc1", Content: body}}, nil),
	}
	store := &fakeEnrichStore{byMemoryDocID: map[string]int64{"doc1": 99}}
	got, res := enrichComments(newTestEnricher(fake, store), []FileComment{
		{Severity: SeverityWarning, Category: CategoryBug, Line: 10, Body: body},
	})
	c := got[0]

	if c.MatchedPatternScore != 0.95 || c.MatchedPatternID != 99 {
		t.Errorf("exact pattern did not link: score=%v id=%d", c.MatchedPatternScore, c.MatchedPatternID)
	}
	if len(store.incremented) != 1 || store.incremented[0] != 99 {
		t.Errorf("exact pattern must increment stats, got %v", store.incremented)
	}
	if c.IsNewFinding {
		t.Error("an exact learned pattern was marked novel")
	}
	if res.Matched != 1 || res.Novel != 0 {
		t.Errorf("result = %+v, want Matched=1 Novel=0", res)
	}
}

// A single dismissed-feedback match at/above the drop threshold drops a
// non-exempt finding: Suppressed + reason set, and a suppression key recorded.
func TestEnricher_DismissalDrops(t *testing.T) {
	fake := &memorytest.Fake{
		SearchFn: feedbackLeg([]memory.PatternMatch{{Score: atOrAboveDrop, ID: "fb1"}}, nil),
	}
	store := &fakeEnrichStore{}
	got, res := enrichComments(newTestEnricher(fake, store), []FileComment{
		{Severity: SeverityWarning, Category: CategoryBug, Line: 10, Body: "dead code branch"},
	})
	c := got[0]

	if !c.Suppressed || c.SuppressedReason == "" {
		t.Errorf("a dismissal at/above the drop floor must drop the finding: Suppressed=%v reason=%q", c.Suppressed, c.SuppressedReason)
	}
	if res.Suppressed != 1 || len(res.SuppressedKeys) != 1 {
		t.Errorf("result = %+v, want Suppressed=1 with one key", res)
	}
}

// A mid-band dismissal (>= downgrade floor, < drop floor) downgrades severity one level and flags
// the comment without suppressing it.
func TestEnricher_DismissalDowngrades(t *testing.T) {
	fake := &memorytest.Fake{
		SearchFn: feedbackLeg([]memory.PatternMatch{{Score: midDowngradeBand, ID: "fb1", Metadata: map[string]string{"pr_number": "12"}}}, nil),
	}
	store := &fakeEnrichStore{}
	got, res := enrichComments(newTestEnricher(fake, store), []FileComment{
		{Severity: SeverityCritical, Category: CategoryBug, Line: 10, Body: "questionable cast"},
	})
	c := got[0]

	if c.Suppressed {
		t.Error("a mid-band dismissal must downgrade, not drop")
	}
	if !c.DismissedDowngrade || c.Severity != SeverityWarning {
		t.Errorf("downgrade wrong: flag=%v severity=%q, want true/warning", c.DismissedDowngrade, c.Severity)
	}
	if res.Downgraded != 1 {
		t.Errorf("result = %+v, want Downgraded=1", res)
	}
}

// A security finding is exempt from suppression: even a drop-level dismissal only
// DOWNGRADES it — memory may lower the volume but never silence a security check.
func TestEnricher_SecurityExemptionDowngradesInsteadOfDrops(t *testing.T) {
	fake := &memorytest.Fake{
		SearchFn: feedbackLeg([]memory.PatternMatch{{Score: atOrAboveDrop, ID: "fb1"}}, nil),
	}
	store := &fakeEnrichStore{}
	got, _ := enrichComments(newTestEnricher(fake, store), []FileComment{
		{Severity: SeverityCritical, Category: CategorySecurity, Line: 10, Body: "sql injection risk"},
	})
	c := got[0]

	if c.Suppressed {
		t.Error("a security finding must never be dropped by memory")
	}
	if !c.DismissedDowngrade || c.Severity != SeverityWarning {
		t.Errorf("exempt drop must downgrade: flag=%v severity=%q", c.DismissedDowngrade, c.Severity)
	}
}

// A team-feedback streak drops through the Enricher: three mid-band dismissals
// (each >= SuppressionDowngrade but below the SuppressionDrop floor) suppress a
// finding even though NO single match is exact — the SuppressSimilarCount path.
// Wires dismissalSearch -> evaluateDismissals -> similarCount end-to-end via Run.
func TestEnricher_TeamFeedbackStreakDrops(t *testing.T) {
	fake := &memorytest.Fake{
		SearchFn: feedbackLeg([]memory.PatternMatch{
			{Score: midDowngradeBand, ID: "fb1"}, {Score: midDowngradeBand + 0.005, ID: "fb2"}, {Score: midDowngradeBand + 0.01, ID: "fb3"},
		}, nil),
	}
	store := &fakeEnrichStore{}
	got, res := enrichComments(newTestEnricher(fake, store), []FileComment{
		{Severity: SeverityWarning, Category: CategoryBug, Line: 10, Body: "repeatedly rejected finding"},
	})
	c := got[0]

	if !c.Suppressed || c.SuppressedReason != "team_feedback:3" {
		t.Errorf("a 3-dismissal streak must drop: Suppressed=%v reason=%q, want true/team_feedback:3", c.Suppressed, c.SuppressedReason)
	}
	if res.Suppressed != 1 || len(res.SuppressedKeys) != 1 {
		t.Errorf("result = %+v, want Suppressed=1 with one key", res)
	}
}

// A category the repo auto-suppressed (consecutive-ignore streak) drops a
// finding through the Enricher with NO dismissal match at all — wiring the
// GetAutoSuppressedCategories lookup (repoID != 0) into the drop decision.
func TestEnricher_AutoSuppressedCategoryDrops(t *testing.T) {
	fake := &memorytest.Fake{
		SearchFn: feedbackLeg(nil, nil), // no dismissal matches
	}
	store := &fakeEnrichStore{autoSuppressed: map[string]bool{string(CategoryStyle): true}}
	e := newTestEnricher(fake, store)
	e.repoID = 7 // non-zero => GetAutoSuppressedCategories runs

	got, res := enrichComments(e, []FileComment{
		{Severity: SeverityWarning, Category: CategoryStyle, Line: 10, Body: "nit the team keeps ignoring"},
	})
	c := got[0]

	if !c.Suppressed || c.SuppressedReason != "category_auto_suppressed" {
		t.Errorf("an auto-suppressed category must drop: Suppressed=%v reason=%q", c.Suppressed, c.SuppressedReason)
	}
	if res.Suppressed != 1 {
		t.Errorf("result = %+v, want Suppressed=1", res)
	}
}

// Concurrency safety: enriching many findings fans out under the bound; a
// resolvable pattern hit increments + publishes per finding. Run under -race to
// prove the shared linker + publish sink + per-comment mutation are race-clean.
func TestEnricher_ConcurrencySafe(t *testing.T) {
	const files, perFile = 6, 8
	total := files * perFile

	fake := &memorytest.Fake{
		SearchFn: patternLeg([]memory.PatternMatch{{Score: aboveAttribution, ID: "doc1"}}, nil),
	}
	store := &fakeEnrichStore{byMemoryDocID: map[string]int64{"doc1": 1}}
	rec := &eventRecorder{}
	e := newTestEnricher(fake, store)
	e.publish = rec.publish

	reviews := make([]FileReview, files)
	for f := 0; f < files; f++ {
		comments := make([]FileComment, perFile)
		for i := 0; i < perFile; i++ {
			comments[i] = FileComment{
				Severity: SeverityWarning, Category: CategoryBug, Line: i + 1,
				Body: fmt.Sprintf("finding %d-%d distinct body", f, i),
			}
		}
		reviews[f] = FileReview{Path: fmt.Sprintf("f%d.go", f), Comments: comments}
	}

	res := e.Run(context.Background(), reviews)

	if res.Matched != total {
		t.Errorf("Matched = %d, want %d", res.Matched, total)
	}
	if len(store.incremented) != total {
		t.Errorf("increments = %d, want %d", len(store.incremented), total)
	}
	if rec.count(EventMemoryMatched) != total {
		t.Errorf("EventMemoryMatched = %d, want %d", rec.count(EventMemoryMatched), total)
	}
}
