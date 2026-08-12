package pipeline

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/BeLazy167/argus/backend/internal/memory"
	"github.com/BeLazy167/argus/backend/internal/memory/memorytest"
	"github.com/google/uuid"
)

// indexerAssignment matches every place a PipelineRun receives its memory
// indexer — the struct literal field and any later assignment.
var indexerAssignment = regexp.MustCompile(`(?m)^\s*(?:\w+\.)?Indexer(?::| =)\s*(.+)$`)

// TestEveryRunIndexerIsAttributed is the grep-of-record for the sibling-site
// trap: a run's memory writes are attributed by the WRITER, so every path that
// puts an Indexer on a PipelineRun must route it through indexerForReview.
//
// There are three such paths and they are far apart — the fresh run
// (orchestrator.buildRun), the terminal-run retry (orchestrator), and resume
// hydration (resume_context.go). Fixing one and missing another is invisible:
// the missed path still reviews, still writes memory, and still posts. It just
// writes rows no review claims, so that run's dashboard panel says "learned
// nothing" and its PR comment drops the line — the exact silence this feature
// exists to end.
func TestEveryRunIndexerIsAttributed(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	found := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, m := range indexerAssignment.FindAllStringSubmatch(string(data), -1) {
			rhs := strings.TrimSpace(m[1])
			// The interface field declaration on PipelineRun itself, not an
			// assignment.
			if strings.HasPrefix(rhs, "memory.Indexer") {
				continue
			}
			found++
			if !strings.Contains(rhs, "indexerForReview(") {
				t.Errorf("%s assigns a run Indexer as %q without indexerForReview — "+
					"memory written on this path would belong to no review", name, strings.TrimSuffix(rhs, ","))
			}
		}
	}
	if found < 3 {
		t.Errorf("found only %d run-Indexer assignments; the regex has drifted from the code it guards", found)
	}
}

// TestLearnedLineIsCountedAfterTheMemorySinks pins the ORDER the footnote
// depends on, which no unit test of appendLearnedLine can see.
//
// The count is read back from the memories table, so it must happen after the
// pre-post memory sinks have written and before the review is posted. Moved
// above the sinks it silently reports zero on every review — a line that says
// "learned nothing" is worse than no line, because it accuses a healthy memory
// path of being broken. Moved below PostReview it renders into a body GitHub
// already has.
func TestLearnedLineIsCountedAfterTheMemorySinks(t *testing.T) {
	data, err := os.ReadFile(filepath.Clean("orchestrator.go"))
	if err != nil {
		t.Fatalf("read orchestrator.go: %v", err)
	}
	src := string(data)

	sinks := strings.Index(src, `"pre_post"`)
	appendCall := strings.Index(src, "appendLearnedLine(prePostCtx")
	post := strings.Index(src, "o.ghClient.PostReview(")

	if sinks < 0 || post < 0 {
		t.Fatalf("landmarks moved (pre_post=%d, PostReview=%d); this guard has drifted from the code", sinks, post)
	}
	if appendCall < 0 {
		t.Fatal("post() no longer appends the learned-memory footnote; the PR comment silently stops reporting what the review learned")
	}
	if appendCall < sinks {
		t.Error("the footnote is counted BEFORE the memory sinks run, so it will always report zero")
	}
	if appendCall > post {
		t.Error("the footnote is appended AFTER PostReview, so the posted comment never carries it")
	}
}

// TestRecordedIDRecoverySkipsPrePostSinksAndKeepsCompletionElection pins the
// crash-recovery ordering around the non-idempotent post. A durable id must be
// read before pre-post LLM/memory work, and recovery must continue to the normal
// completion CAS rather than return early and omit winner follow-ups.
func TestRecordedIDRecoverySkipsPrePostSinksAndKeepsCompletionElection(t *testing.T) {
	data, err := os.ReadFile(filepath.Clean("orchestrator.go"))
	if err != nil {
		t.Fatalf("read orchestrator.go: %v", err)
	}
	src := string(data)
	postStart := strings.Index(src, "func (o *Orchestrator) post(")
	if postStart < 0 {
		t.Fatal("post stage not found")
	}
	src = src[postStart:]
	precheck := strings.Index(src, "GetRecordedReviewID(")
	gate := strings.Index(src, "if !postAlreadyRecorded {")
	sinks := strings.Index(src, `"pre_post"`)
	completion := strings.Index(src, "CompletePostedReview(")
	if precheck < 0 || gate < 0 || sinks < 0 || completion < 0 {
		t.Fatalf("recovery landmarks moved: precheck=%d gate=%d sinks=%d completion=%d", precheck, gate, sinks, completion)
	}
	if !(precheck < gate && gate < sinks && sinks < completion) {
		t.Fatalf("unsafe recorded-id ordering: precheck=%d gate=%d sinks=%d completion=%d", precheck, gate, sinks, completion)
	}
	if strings.Contains(src[:completion], "if postAlreadyRecorded {\n\t\treturn") {
		t.Fatal("recorded-id recovery returns before the completion winner election")
	}
}

// TestIndexerForReviewAttributesWrites covers the helper's two contracts: it
// passes the review through, and it tolerates the nil indexer that means
// "memory is unconfigured for this org" instead of panicking mid-review.
func TestIndexerForReviewAttributesWrites(t *testing.T) {
	reviewID := uuid.New()

	tests := []struct {
		name string
		in   memory.Indexer
		want uuid.UUID
	}{
		{name: "a live indexer is bound to the review", in: &memorytest.Fake{}, want: reviewID},
		{name: "memory unconfigured stays nil", in: nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := indexerForReview(tc.in, reviewID)
			if tc.in == nil {
				if got != nil {
					t.Fatalf("got %T, want nil for an unconfigured org", got)
				}
				return
			}
			fake, ok := got.(*memorytest.Fake)
			if !ok {
				t.Fatalf("got %T, want the same *memorytest.Fake so recorded writes stay on one object", got)
			}
			if fake.ReviewID != tc.want {
				t.Errorf("ReviewID = %s, want %s", fake.ReviewID, tc.want)
			}
		})
	}
}
