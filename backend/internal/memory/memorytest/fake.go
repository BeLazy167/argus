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
	// Read stubs. Nil → the no-op default: (nil, nil) / ("", nil). The reader
	// seam collapsed to two deep methods (Search + Briefing), so these two stubs
	// drive every value-level read the pipeline performs; both carry an error
	// return so a test can inject a failed search (propagate vs degrade coverage)
	// and switch behavior on the query (q.Type / q.Scope) per call site.
	SearchFn   func(q memory.MemoryQuery) ([]memory.PatternMatch, error)
	BriefingFn func(q memory.BriefingQuery) (string, error)

	mu          sync.Mutex
	Feedback    []memory.FeedbackMemory // IndexFeedbackSignal
	Patterns    []memory.PatternMemory  // IndexPattern
	SharedPats  []memory.PatternMemory  // IndexSharedPattern
	Rules       []memory.RuleMemory     // IndexRule
	ReviewBatch [][]memory.ReviewMemory // IndexReviewCommentsBatch
	Scenarios   []FakeScenario          // IndexScenario
	Deleted     []string                // DeleteDocument
}

// FakeScenario records one IndexScenario call.
type FakeScenario struct {
	Owner, Repo, Description, Severity string
	ScenarioID                         int64
	Files                              []string
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

func (f *Fake) Search(_ context.Context, q memory.MemoryQuery) ([]memory.PatternMatch, error) {
	if f.SearchFn != nil {
		return f.SearchFn(q)
	}
	return nil, nil
}

func (f *Fake) Briefing(_ context.Context, q memory.BriefingQuery) (string, error) {
	if f.BriefingFn != nil {
		return f.BriefingFn(q)
	}
	return "", nil
}

func (f *Fake) DeleteDocument(_ context.Context, documentID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Deleted = append(f.Deleted, documentID)
	return nil
}
