package memory

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func seedMemoryRow(t *testing.T, ctx context.Context, pool *pgxpool.Pool, install int64, customID, source string) {
	t.Helper()
	metadata := `{}`
	if source != "" {
		metadata = `{"source":"` + source + `"}`
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO memories (installation_id, container_tag, custom_id, type, content, metadata)
		VALUES ($1, 'x', $2, 'pattern', $2, $3::jsonb)`, install, customID, metadata); err != nil {
		t.Fatalf("seed memory %s: %v", customID, err)
	}
}

func isLive(t *testing.T, ctx context.Context, pool *pgxpool.Pool, install int64, customID string) bool {
	t.Helper()
	var live bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM live_memories WHERE installation_id=$1 AND custom_id=$2)`, install, customID).Scan(&live); err != nil {
		t.Fatal(err)
	}
	return live
}

// supersededByCustomID returns the custom_id of the memory that superseded
// customID, or "" when superseded_by is NULL. Asserting the mode a supersede
// reports is not the same as asserting the row it wrote: the mode comes from
// the request shape, the link comes from the UPDATE.
func supersededByCustomID(t *testing.T, ctx context.Context, pool *pgxpool.Pool, install int64, customID string) string {
	t.Helper()
	var replacement *string
	if err := pool.QueryRow(ctx, `
		SELECT r.custom_id
		FROM memories s LEFT JOIN memories r ON r.id = s.superseded_by
		WHERE s.installation_id = $1 AND s.custom_id = $2`, install, customID).Scan(&replacement); err != nil {
		t.Fatal(err)
	}
	if replacement == nil {
		return ""
	}
	return *replacement
}

func TestRetireDocument(t *testing.T) {
	pool, install := pgTestPool(t)
	ctx := context.Background()
	idx := NewPGIndexer(pool, nil, install, StorageDimensions, discardLogger())
	seedMemoryRow(t, ctx, pool, install, "human", "manual")
	seedMemoryRow(t, ctx, pool, install, "learned", "auto_learn")
	// The pipeline's convention writer stamps "convention", not the
	// "convention_extraction" the memory document carries; a row it wrote must
	// be retirable, not refused as unclassifiable provenance.
	seedMemoryRow(t, ctx, pool, install, "conventional", "convention")
	seedMemoryRow(t, ctx, pool, install, "orphan", "")
	seedMemoryRow(t, ctx, pool, install, "weird", "not_a_known_source")
	seedMemoryRow(t, ctx, pool, install, "replacement", "manual")
	seedMemoryRow(t, ctx, pool, install, "retired-replacement", "manual")
	if _, err := pool.Exec(ctx, `UPDATE memories SET invalidated_at = now() WHERE installation_id=$1 AND custom_id='retired-replacement'`, install); err != nil {
		t.Fatal(err)
	}

	// Human-authored: invalidated; repeat is idempotent.
	res, err := idx.RetireDocument(ctx, RetireRequest{CustomID: "human"})
	if err != nil || res.Mode != RetireModeInvalidated || res.Source != "manual" {
		t.Fatalf("human: res=%+v err=%v", res, err)
	}
	if isLive(t, ctx, pool, install, "human") {
		t.Fatal("retired memory still live")
	}
	if _, err := idx.RetireDocument(ctx, RetireRequest{CustomID: "human"}); err != nil {
		t.Fatalf("repeat must be idempotent: %v", err)
	}

	// Pipeline-learned: refused, still live; allowed with acknowledgment.
	if _, err := idx.RetireDocument(ctx, RetireRequest{CustomID: "learned"}); !errors.Is(err, ErrPipelineLearnedMemory) {
		t.Fatalf("learned: %v", err)
	}
	if !isLive(t, ctx, pool, install, "learned") {
		t.Fatal("refused retirement must leave the row live")
	}
	if res, err := idx.RetireDocument(ctx, RetireRequest{CustomID: "learned", AllowPipelineLearned: true}); err != nil || res.Source != "auto_learn" {
		t.Fatalf("learned acknowledged: res=%+v err=%v", res, err)
	}
	if isLive(t, ctx, pool, install, "learned") {
		t.Fatal("acknowledged retirement must take the row out of live_memories")
	}

	// Convention-sourced rows follow the same rule: pipeline-learned, so
	// refused without the flag and retired with it — never ErrUnknownProvenance.
	if _, err := idx.RetireDocument(ctx, RetireRequest{CustomID: "conventional"}); !errors.Is(err, ErrPipelineLearnedMemory) {
		t.Fatalf("conventional: err = %v, want ErrPipelineLearnedMemory", err)
	}
	if !isLive(t, ctx, pool, install, "conventional") {
		t.Fatal("refused retirement must leave the convention row live")
	}
	if res, err := idx.RetireDocument(ctx, RetireRequest{CustomID: "conventional", AllowPipelineLearned: true}); err != nil || res.Source != "convention" {
		t.Fatalf("conventional acknowledged: res=%+v err=%v", res, err)
	}
	if isLive(t, ctx, pool, install, "conventional") {
		t.Fatal("acknowledged retirement must take the convention row out of live_memories")
	}

	// Unknown provenance is an error even with the flag.
	for _, id := range []string{"orphan", "weird"} {
		if _, err := idx.RetireDocument(ctx, RetireRequest{CustomID: id, AllowPipelineLearned: true}); !errors.Is(err, ErrUnknownProvenance) {
			t.Fatalf("%s: %v", id, err)
		}
		if !isLive(t, ctx, pool, install, id) {
			t.Fatalf("%s must stay live", id)
		}
	}

	// Supersede: replacement must be distinct and live in this installation.
	seedMemoryRow(t, ctx, pool, install, "old", "manual")
	for _, bad := range []string{"old", "missing", "retired-replacement"} {
		if _, err := idx.RetireDocument(ctx, RetireRequest{CustomID: "old", ReplacementCustomID: bad}); !errors.Is(err, ErrReplacementNotLive) {
			t.Fatalf("replacement %q: %v", bad, err)
		}
		if !isLive(t, ctx, pool, install, "old") {
			t.Fatalf("failed supersede (%q) must leave the source live", bad)
		}
	}
	res, err = idx.RetireDocument(ctx, RetireRequest{CustomID: "old", ReplacementCustomID: "replacement"})
	if err != nil || res.Mode != RetireModeSuperseded {
		t.Fatalf("supersede: res=%+v err=%v", res, err)
	}
	if got := supersededByCustomID(t, ctx, pool, install, "old"); got != "replacement" {
		t.Fatalf("old superseded_by = %q, want replacement", got)
	}
	if _, err := idx.RetireDocument(ctx, RetireRequest{CustomID: "old", ReplacementCustomID: "replacement"}); err != nil {
		t.Fatalf("repeat supersede must be idempotent: %v", err)
	}
	if got := supersededByCustomID(t, ctx, pool, install, "old"); got != "replacement" {
		t.Fatalf("after repeat, old superseded_by = %q, want replacement", got)
	}

	// Missing memory.
	if _, err := idx.RetireDocument(ctx, RetireRequest{CustomID: "nope", AllowPipelineLearned: true}); !errors.Is(err, ErrDocumentNotFound) {
		t.Fatalf("missing: %v", err)
	}
	// The pre-existing single-purpose methods now wrap the same sentinel.
	if err := idx.InvalidateDocument(ctx, "nope"); !errors.Is(err, ErrDocumentNotFound) {
		t.Fatalf("InvalidateDocument missing: %v", err)
	}
}

// openConventionConflict puts both named memories on an open conflict edge, the
// state live_memories excludes but the memories table knows nothing about.
func openConventionConflict(t *testing.T, ctx context.Context, pool *pgxpool.Pool, install int64, leftCustomID, rightCustomID string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO convention_conflicts (installation_id, category, memory_low_id, memory_high_id, introducing_memory_id, introducing_pr)
		SELECT $1, 'testing', LEAST(a.id, b.id), GREATEST(a.id, b.id), b.id, 1
		FROM memories a, memories b
		WHERE a.installation_id = $1 AND a.custom_id = $2
		  AND b.installation_id = $1 AND b.custom_id = $3`, install, leftCustomID, rightCustomID); err != nil {
		t.Fatalf("open conflict %s/%s: %v", leftCustomID, rightCustomID, err)
	}
	// Runs before pgTestPool's memories cleanup: convention_conflicts
	// references memories(id) without ON DELETE CASCADE.
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM convention_conflicts WHERE installation_id = $1`, install); err != nil {
			t.Errorf("cleanup conflicts: %v", err)
		}
	})
}

// A replacement on an open convention conflict is not live: live_memories
// excludes both ends of the edge, and supersedeMemorySQL joins that view. The
// probe has to read the same relation. Reading the memories table instead let a
// conflicted replacement through, the UPDATE then matched nothing, and the
// caller was told the SOURCE was already superseded by another memory — a false
// statement about a row that had not been touched.
func TestRetireDocumentConflictedReplacementIsNotLive(t *testing.T) {
	pool, install := pgTestPool(t)
	ctx := context.Background()
	idx := NewPGIndexer(pool, nil, install, StorageDimensions, discardLogger())
	seedMemoryRow(t, ctx, pool, install, "conflict-source", "manual")
	seedMemoryRow(t, ctx, pool, install, "conflict-replacement", "manual")
	seedMemoryRow(t, ctx, pool, install, "conflict-partner", "manual")
	openConventionConflict(t, ctx, pool, install, "conflict-replacement", "conflict-partner")

	if isLive(t, ctx, pool, install, "conflict-replacement") {
		t.Fatal("fixture is wrong: a memory on an open conflict must not be live")
	}
	_, err := idx.RetireDocument(ctx, RetireRequest{CustomID: "conflict-source", ReplacementCustomID: "conflict-replacement"})
	if !errors.Is(err, ErrReplacementNotLive) {
		t.Fatalf("conflicted replacement: %v", err)
	}
	if strings.Contains(err.Error(), "already superseded") {
		t.Fatalf("diagnosis blames the source for the replacement's state: %v", err)
	}
	if !isLive(t, ctx, pool, install, "conflict-source") {
		t.Fatal("a refused supersede must leave the source live")
	}
	var supersededBy *int64
	if err := pool.QueryRow(ctx, `SELECT superseded_by FROM memories WHERE installation_id=$1 AND custom_id='conflict-source'`, install).Scan(&supersededBy); err != nil {
		t.Fatal(err)
	}
	if supersededBy != nil {
		t.Fatalf("source superseded_by = %v, want NULL", *supersededBy)
	}

	// Resolving the conflict makes the same replacement usable, so the refusal
	// is about conflict state and not about the row being unreachable.
	if _, err := pool.Exec(ctx, `UPDATE convention_conflicts SET state='resolved_new' WHERE installation_id=$1`, install); err != nil {
		t.Fatal(err)
	}
	res, err := idx.RetireDocument(ctx, RetireRequest{CustomID: "conflict-source", ReplacementCustomID: "conflict-replacement"})
	if err != nil || res.Mode != RetireModeSuperseded {
		t.Fatalf("after resolution: res=%+v err=%v", res, err)
	}
	if got := supersededByCustomID(t, ctx, pool, install, "conflict-source"); got != "conflict-replacement" {
		t.Fatalf("conflict-source superseded_by = %q, want conflict-replacement", got)
	}
}
