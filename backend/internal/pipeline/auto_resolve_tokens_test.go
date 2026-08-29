// Package pipeline — auto_resolve_tokens_test.go pins the cost half of
// auto-resolve (#72): the AddressedJudge calls a push makes are real LLM spend,
// and they must reach reviews.token_usage under the "auto_resolve" bucket. The
// defect these tests guard is silent: resolution keeps working while the money
// disappears from the /stats Cost-by-Stage chart.
package pipeline

import (
	"context"
	"encoding/json"
	"testing"

	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/internal/store/db"
	"github.com/google/uuid"
)

// perCallSpend is what the fake judge reports for one candidate.
var perCallSpend = StageTokens{
	PromptTokens:     800,
	CompletionTokens: 50,
	TotalTokens:      850,
	Cost:             0.0003,
	Model:            "judge-model",
	Provider:         "openai",
}

// TestResolveCandidates_SumsJudgeSpend proves one push's judge calls arrive at
// the caller as a single summed entry. A per-candidate write or a lost sum both
// misreport what the push cost.
func TestResolveCandidates_SumsJudgeSpend(t *testing.T) {
	const candidates = 3
	judge := &fakeAddressedJudge{addressed: true, tokens: perCallSpend}
	o, _ := newResolveHarness(judge)
	threads, patch := candidateThreads(candidates)

	stats, _ := o.resolveCandidates(context.Background(),
		ghpkg.PREvent{InstallationID: 9, PRNumber: 1}, "o", "r", threads, nil, patch, 1, 2)

	if judge.calls != candidates {
		t.Fatalf("judge calls = %d, want %d", judge.calls, candidates)
	}
	want := perCallSpend.TotalTokens * candidates
	if stats.judgeTokens.TotalTokens != want {
		t.Errorf("judgeTokens.TotalTokens = %d, want %d — the push's judge spend is under-reported",
			stats.judgeTokens.TotalTokens, want)
	}
	if got, wantCost := stats.judgeTokens.Cost, perCallSpend.Cost*candidates; got < wantCost-1e-9 || got > wantCost+1e-9 {
		t.Errorf("judgeTokens.Cost = %f, want %f", got, wantCost)
	}
	if stats.judgeTokens.Model != perCallSpend.Model || stats.judgeTokens.Provider != perCallSpend.Provider {
		t.Errorf("model/provider not stamped on the summed entry: %+v — the dashboard row would render an empty model",
			stats.judgeTokens)
	}
}

// TestResolveCandidates_BillsKeptOpenPush proves the expensive-but-quiet push
// still reports spend: the judge answered "not addressed" for every candidate,
// so NOTHING resolved, yet every call was billed. Attributing spend only to
// resolves would hide the majority of auto-resolve cost.
func TestResolveCandidates_BillsKeptOpenPush(t *testing.T) {
	judge := &fakeAddressedJudge{addressed: false, tokens: perCallSpend}
	o, gh := newResolveHarness(judge)
	threads, patch := candidateThreads(2)

	stats, _ := o.resolveCandidates(context.Background(),
		ghpkg.PREvent{InstallationID: 9, PRNumber: 1}, "o", "r", threads, nil, patch, 1, 2)

	if stats.resolved != 0 || len(gh.resolved) != 0 {
		t.Fatalf("keep-open push resolved something: resolved=%d gh=%v", stats.resolved, gh.resolved)
	}
	if stats.judgeTokens.TotalTokens != perCallSpend.TotalTokens*2 {
		t.Errorf("judgeTokens.TotalTokens = %d, want %d — a push that resolved nothing still spent money",
			stats.judgeTokens.TotalTokens, perCallSpend.TotalTokens*2)
	}
}

// TestPersistAsyncStageTokens_AutoResolveWithoutRun covers the nil-run path.
// Auto-resolve fires on a later push with no live PipelineRun, so the DB merge
// is the only thing that can carry the spend; a persist that assumed a run
// would panic in a detached goroutine and lose the row entirely.
func TestPersistAsyncStageTokens_AutoResolveWithoutRun(t *testing.T) {
	fake := &fakeCrossPRStore{}
	o := &Orchestrator{
		logger:       lifecycleTestLogger(),
		crossPRHooks: &crossPRHooks{Store: fake},
	}
	reviewID := uuid.New()

	o.persistAsyncStageTokens(context.Background(), reviewID, stageKeyAutoResolve, perCallSpend, nil)

	if len(fake.tokenWrites) != 1 {
		t.Fatalf("token writes = %d, want 1 — auto-resolve spend never reached the review row", len(fake.tokenWrites))
	}
	tw := fake.tokenWrites[0]
	if tw.StageKey != stageKeyAutoResolve {
		t.Errorf("stage_key = %q, want %q — spend landed in the wrong Cost-by-Stage row", tw.StageKey, stageKeyAutoResolve)
	}
	if tw.ReviewID != reviewID {
		t.Errorf("review_id = %v, want %v", tw.ReviewID, reviewID)
	}
	var entry StageTokens
	if err := json.Unmarshal(tw.Entry, &entry); err != nil {
		t.Fatalf("unmarshal token entry: %v", err)
	}
	if entry.TotalTokens != perCallSpend.TotalTokens || entry.Cost != perCallSpend.Cost {
		t.Errorf("entry = %+v, want %+v", entry, perCallSpend)
	}
}

// TestAutoResolveBucketSurvivesJSONRoundTrip proves the "auto_resolve" key the
// SQL merge writes decodes back into RunTokenUsage.AutoResolve. The stats
// aggregator decodes token_usage into this struct, so a key/field mismatch
// would silently drop every auto-resolve row from the chart.
func TestAutoResolveBucketSurvivesJSONRoundTrip(t *testing.T) {
	var r RunTokenUsage
	r.addAutoResolve(perCallSpend)

	raw, err := json.Marshal(&r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var keyed map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keyed); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}
	if _, ok := keyed[stageKeyAutoResolve]; !ok {
		t.Fatalf("RunTokenUsage does not serialize a %q key — the SQL merge writes that bucket and it would never be read back",
			stageKeyAutoResolve)
	}

	var back RunTokenUsage
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if back.AutoResolve.TotalTokens != perCallSpend.TotalTokens || back.AutoResolve.Cost != perCallSpend.Cost {
		t.Errorf("AutoResolve after round trip = %+v, want %+v", back.AutoResolve, perCallSpend)
	}
	if back.Total.TotalTokens != perCallSpend.TotalTokens {
		t.Errorf("Total after round trip = %d, want %d — the bucket must roll into the run total",
			back.Total.TotalTokens, perCallSpend.TotalTokens)
	}
}

// compile-time guard: the persist path speaks the sqlc params type, so a rename
// on the query side breaks here rather than at runtime in a detached goroutine.
var _ = db.MergeStageTokenEntryParams{StageKey: stageKeyAutoResolve}
