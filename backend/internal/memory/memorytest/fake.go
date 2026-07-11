// Package memorytest provides an in-memory memory.Indexer for pipeline tests.
// It is the second adapter behind the memory.Indexer interface (the first being
// the Supermemory-backed indexerImpl): read methods return configurable stub
// values and write methods record their arguments, so a test can assert what
// the pipeline persisted instead of driving a live Supermemory client.
package memorytest

import (
	"context"
	"sync"

	"github.com/BeLazy167/argus/backend/internal/memory"
)

// Fake is a concurrency-safe, no-op-by-default memory.Indexer. With no stubs
// set it behaves exactly like memory.NewIndexer(nil, …) — every read returns the
// zero value and every write is a no-op — except it also RECORDS writes. Set a
// *Fn field to stub the corresponding read.
type Fake struct {
	// Read stubs. Nil → zero-value return (the no-op default).
	BriefingFn    func(owner, repo, filePath, query string, opts memory.BriefingOptions) string
	HintsFn       func(query, containerTag string, limit int, typ memory.MemoryType) []string
	RuleContentFn func(query string) string
	ScoredFn      func(query, containerTag string, typ memory.MemoryType, limit int) ([]memory.PatternMatch, error)
	PatternFn     func(owner, repo, query string) memory.PatternMatch
	DismissedFn   func(owner, repo, query string, limit int) []memory.PatternMatch
	ScenariosFn   func(owner, repo, query, severity string, limit int) []memory.ScenarioSearchResult

	mu          sync.Mutex
	Feedback    []memory.FeedbackMemory  // IndexFeedbackSignal
	Patterns    []memory.PatternMemory   // IndexPattern
	SharedPats  []memory.PatternMemory   // IndexSharedPattern
	Rules       []memory.RuleMemory      // IndexRule
	ReviewBatch [][]memory.ReviewMemory  // IndexReviewCommentsBatch
	Scenarios   []FakeScenario           // IndexScenario
	Deleted     []string                 // DeleteDocument
}

// FakeScenario records one IndexScenario call.
type FakeScenario struct {
	Owner, Repo, Description, Severity string
	ScenarioID                        int64
	Files                             []string
}

// Ensure Fake satisfies the interface at compile time.
var _ memory.Indexer = (*Fake)(nil)

func (f *Fake) DisableLLMFilter(context.Context) error { return nil }

func (f *Fake) IndexReviewCommentsBatch(_ context.Context, _, _ string, comments []memory.ReviewMemory) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ReviewBatch = append(f.ReviewBatch, comments)
	return nil
}

func (f *Fake) IndexRule(_ context.Context, _ string, rule memory.RuleMemory) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Rules = append(f.Rules, rule)
	return nil
}

func (f *Fake) IndexPattern(_ context.Context, _ string, pattern memory.PatternMemory) (*memory.AddResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Patterns = append(f.Patterns, pattern)
	return nil, nil
}

func (f *Fake) IndexSharedPattern(_ context.Context, pattern memory.PatternMemory) (*memory.AddResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.SharedPats = append(f.SharedPats, pattern)
	return nil, nil
}

func (f *Fake) IndexFeedbackSignal(_ context.Context, _, _ string, fb memory.FeedbackMemory) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Feedback = append(f.Feedback, fb)
	return nil
}

func (f *Fake) IndexScenario(_ context.Context, owner, repo string, scenarioID int64, description, severity string, files []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Scenarios = append(f.Scenarios, FakeScenario{Owner: owner, Repo: repo, ScenarioID: scenarioID, Description: description, Severity: severity, Files: files})
	return nil
}

func (f *Fake) SearchPatternMatch(_ context.Context, owner, repo, query string, _ memory.Thresholds) memory.PatternMatch {
	if f.PatternFn != nil {
		return f.PatternFn(owner, repo, query)
	}
	return memory.PatternMatch{}
}

func (f *Fake) SearchDismissedMatches(_ context.Context, owner, repo, query string, _ memory.Thresholds, limit int) []memory.PatternMatch {
	if f.DismissedFn != nil {
		return f.DismissedFn(owner, repo, query, limit)
	}
	return nil
}

func (f *Fake) SearchScenariosWithIDs(_ context.Context, owner, repo, query, severity string, limit int) []memory.ScenarioSearchResult {
	if f.ScenariosFn != nil {
		return f.ScenariosFn(owner, repo, query, severity, limit)
	}
	return nil
}

func (f *Fake) Briefing(_ context.Context, owner, repo, filePath, query string, opts memory.BriefingOptions) string {
	if f.BriefingFn != nil {
		return f.BriefingFn(owner, repo, filePath, query, opts)
	}
	return ""
}

func (f *Fake) SearchHints(_ context.Context, query, containerTag string, limit int, typ memory.MemoryType) []string {
	if f.HintsFn != nil {
		return f.HintsFn(query, containerTag, limit, typ)
	}
	return nil
}

func (f *Fake) SearchRuleContent(_ context.Context, query string) string {
	if f.RuleContentFn != nil {
		return f.RuleContentFn(query)
	}
	return ""
}

func (f *Fake) SearchScored(_ context.Context, query, containerTag string, typ memory.MemoryType, limit int) ([]memory.PatternMatch, error) {
	if f.ScoredFn != nil {
		return f.ScoredFn(query, containerTag, typ, limit)
	}
	return nil, nil
}

func (f *Fake) DeleteDocument(_ context.Context, documentID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Deleted = append(f.Deleted, documentID)
	return nil
}
