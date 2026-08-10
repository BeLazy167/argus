package pipeline

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/BeLazy167/argus/backend/internal/memory/memorytest"
	"github.com/BeLazy167/argus/backend/internal/store"
	"github.com/google/uuid"
)

// fakeLearnedCounter is a learnedMemoryCounter that returns fixed values and
// records the tenancy arguments it was called with.
type fakeLearnedCounter struct {
	counts   []store.LearnedMemoryCount
	err      error
	gotInst  int64
	gotRevID uuid.UUID
	calls    int
}

func (f *fakeLearnedCounter) CountReviewMemoriesByType(_ context.Context, installationID int64, reviewID uuid.UUID) ([]store.LearnedMemoryCount, error) {
	f.calls++
	f.gotInst, f.gotRevID = installationID, reviewID
	return f.counts, f.err
}

// TestAppendLearnedLine pins the OTHER half of the pair: rendering the footnote
// is worthless unless it reaches the body that actually gets posted. It also
// pins the degrade — a failed count must cost the line, never the review.
func TestAppendLearnedLine(t *testing.T) {
	const body = "## 🔎 Argus · 8/10\n\nfooter"
	reviewID := uuid.New()

	tests := []struct {
		name       string
		indexer    bool // the run has a memory indexer
		counter    *fakeLearnedCounter
		wantSuffix string
		wantCalls  int
	}{
		{
			name:       "the footnote lands on the posted body",
			indexer:    true,
			counter:    &fakeLearnedCounter{counts: []store.LearnedMemoryCount{{Type: "pattern", Count: 2}}},
			wantSuffix: "<br>\n<sub>🧠 Learned: 2 patterns</sub>",
			wantCalls:  1,
		},
		{
			name:      "a failed count costs the line, not the review",
			indexer:   true,
			counter:   &fakeLearnedCounter{err: errors.New("db down")},
			wantCalls: 1,
		},
		{
			name:      "a review that wrote nothing gets no line",
			indexer:   true,
			counter:   &fakeLearnedCounter{},
			wantCalls: 1,
		},
		{
			// Memory unconfigured for the org. Not a failure, and not worth a
			// query on every post.
			name:      "no indexer means no query at all",
			indexer:   false,
			counter:   &fakeLearnedCounter{counts: []store.LearnedMemoryCount{{Type: "pattern", Count: 2}}},
			wantCalls: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			run := &PipelineRun{ReviewID: reviewID, DBInstallationID: 77}
			if tc.indexer {
				run.Indexer = &memorytest.Fake{}
			}
			sub := ComposedReview{}
			sub.GitHub.Summary = body

			appendLearnedLine(context.Background(), tc.counter, run, &sub, slog.New(slog.DiscardHandler))

			if got := sub.GitHub.Summary; got != body+tc.wantSuffix {
				t.Errorf("summary = %q, want %q", got, body+tc.wantSuffix)
			}
			if tc.counter.calls != tc.wantCalls {
				t.Errorf("counter calls = %d, want %d", tc.counter.calls, tc.wantCalls)
			}
			if tc.wantCalls > 0 {
				if tc.counter.gotInst != 77 {
					t.Errorf("counted for installation %d, want 77 — the count must be tenant-scoped", tc.counter.gotInst)
				}
				if tc.counter.gotRevID != reviewID {
					t.Errorf("counted for review %s, want %s", tc.counter.gotRevID, reviewID)
				}
			}
		})
	}
}

// TestRenderLearnedLine covers the posted footnote that tells a developer what
// the review learned. The comment is already dense — a prior fix existed purely
// to stop one row wrapping to three lines — so the assertions on length and
// bucket count are as load-bearing as the wording.
func TestRenderLearnedLine(t *testing.T) {
	count := func(typ string, n int) store.LearnedMemoryCount {
		return store.LearnedMemoryCount{Type: typ, Count: n}
	}

	tests := []struct {
		name   string
		counts []store.LearnedMemoryCount
		want   string
	}{
		{
			name:   "nothing learned renders nothing",
			counts: nil,
			want:   "",
		},
		{
			// A review whose memory writes all failed must not get a line
			// claiming a write. Failures only log at Warn, so a "0 patterns"
			// line would read as success.
			name:   "a zero bucket is not a claim",
			counts: []store.LearnedMemoryCount{count("pattern", 0)},
			want:   "",
		},
		{
			name:   "singular",
			counts: []store.LearnedMemoryCount{count("pr_summary", 1)},
			want:   "<br>\n<sub>🧠 Learned: 1 PR summary</sub>",
		},
		{
			// pluralize() would render "PR summarys" here, which is why the
			// nouns are explicit pairs.
			name:   "irregular plural",
			counts: []store.LearnedMemoryCount{count("pr_summary", 3)},
			want:   "<br>\n<sub>🧠 Learned: 3 PR summaries</sub>",
		},
		{
			name:   "file synthesis pluralizes irregularly too",
			counts: []store.LearnedMemoryCount{count("synthesis", 2)},
			want:   "<br>\n<sub>🧠 Learned: 2 file memories</sub>",
		},
		{
			name:   "several buckets in count order",
			counts: []store.LearnedMemoryCount{count("pattern", 4), count("pr_summary", 1)},
			want:   "<br>\n<sub>🧠 Learned: 4 patterns · 1 PR summary</sub>",
		},
		{
			name:   "an unmapped type is reported, not dropped",
			counts: []store.LearnedMemoryCount{count("future_type", 2)},
			want:   "<br>\n<sub>🧠 Learned: 2 future_type</sub>",
		},
		{
			name: "the bucket list is capped so the row cannot grow into a paragraph",
			counts: []store.LearnedMemoryCount{
				count("pattern", 9), count("synthesis", 8), count("scenario", 7),
				count("topology", 6), count("feedback", 5), count("rule", 4),
			},
			want: "<br>\n<sub>🧠 Learned: 9 patterns · 8 file memories · 7 scenarios · 6 architecture notes · …</sub>",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := RenderLearnedLine(tc.counts)
			if got != tc.want {
				t.Errorf("RenderLearnedLine() =\n  %q\nwant\n  %q", got, tc.want)
			}
			if strings.Count(got, "\n") > 1 {
				t.Errorf("footnote spans %d newlines; it must stay one row", strings.Count(got, "\n"))
			}
		})
	}
}
