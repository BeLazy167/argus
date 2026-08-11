package memory

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/BeLazy167/argus/backend/internal/store"
)

// A pipeline writes memory first with a review-bound indexer, then persists the
// patterns row whose outbox event repairs a failed first write. Replaying that
// repair over an already-live row must not replace the pipeline's richer shape
// with the reduced relational projection or clear current review provenance.
func TestMirrorWorkerPreservesPipelinePatternProvenanceAndMetadata(t *testing.T) {
	pool, install := pgTestPool(t)
	ctx := context.Background()
	reviewID, _ := seedAttributionReviews(t, pool, install)
	embedder := &stubEmbedder{}
	base := NewPGIndexer(pool, embedder, install, pgTestDims, slog.New(slog.DiscardHandler))

	const (
		originRepo = "acme/attribution-test"
		content    = "generic guard writes before publishing"
		category   = "correctness"
	)
	customID := PatternCustomID("", "", "org_learned", content)
	pipelinePattern := PatternMemory{
		Content:  content,
		CustomID: customID,
		Source:   "auto_learn",
		Category: category,
		PRNumber: 41,
		Score:    97,
		Extra: map[string]string{
			"repo":         originRepo,
			"origin_stage": "scoring",
		},
	}
	if _, err := base.ForReview(reviewID).IndexSharedPattern(ctx, pipelinePattern); err != nil {
		t.Fatalf("pipeline shared pattern write: %v", err)
	}
	before := readRow(t, pool, install, customID)
	beforeReview := readReviewID(t, pool, install, customID)
	beforeEmbedCalls := embedder.calls

	// This is the intentionally smaller projection available from patterns.
	mirrorPayload, err := NewPatternMirrorPayload(customID, "", true, PatternMemory{
		Content: content, Source: "auto_learn", Category: category, PRNumber: 41,
		Extra: map[string]string{"repo": originRepo},
	})
	if err != nil {
		t.Fatal(err)
	}
	outbox := &fakeMirrorOutbox{events: []store.MemoryMirrorOutboxEvent{{
		ID: 1, InstallationID: install, AggregateType: store.MemoryMirrorPattern,
		AggregateID: 101, Operation: store.MemoryMirrorUpsert, Payload: mirrorPayload,
	}}}
	worker := NewMirrorWorker(outbox, func(context.Context, int64) MirrorIndexer { return base }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	processed, err := worker.RunOnce(ctx, 1)
	if err != nil {
		t.Fatalf("mirror replay: %v", err)
	}
	if processed != 1 {
		t.Fatalf("processed=%d, want 1", processed)
	}

	after := readRow(t, pool, install, customID)
	afterReview := readReviewID(t, pool, install, customID)
	if beforeReview == nil || afterReview == nil || *afterReview != *beforeReview {
		t.Fatalf("review provenance changed: before=%v after=%v", beforeReview, afterReview)
	}
	if after.metadata["score"] != "97" || after.metadata["origin_stage"] != "scoring" || after.metadata["repo"] != originRepo {
		t.Fatalf("rich pipeline metadata was reduced: before=%v after=%v", before.metadata, after.metadata)
	}
	if after.content != before.content || after.containerTag != before.containerTag || after.docType != before.docType {
		t.Fatalf("live projection changed: before=%+v after=%+v", before, after)
	}
	if embedder.calls != beforeEmbedCalls {
		t.Fatalf("mirror embedded an already-live document: calls before=%d after=%d", beforeEmbedCalls, embedder.calls)
	}

	// Exercise the atomic conflict guard directly: this is the state reached if
	// the pipeline inserts after mirrorDoc's preflight read but before its write.
	reducedDoc, err := buildSharedPatternDoc(PatternMemory{
		Content: content, CustomID: customID, Source: "auto_learn", Category: category, PRNumber: 41,
		Extra: map[string]string{"repo": originRepo},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := base.writeDocs(ctx, []Doc{reducedDoc}, true); err != nil {
		t.Fatalf("raced mirror write: %v", err)
	}
	afterRace := readRow(t, pool, install, customID)
	afterRaceReview := readReviewID(t, pool, install, customID)
	if afterRaceReview == nil || *afterRaceReview != reviewID || afterRace.metadata["score"] != "97" ||
		afterRace.metadata["origin_stage"] != "scoring" || afterRace.metadata["repo"] != originRepo {
		t.Fatalf("atomic mirror conflict reduced live row: review=%v metadata=%v", afterRaceReview, afterRace.metadata)
	}
}
