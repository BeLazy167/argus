package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
)

// PGIndexer is the Postgres-native Indexer implementation (program PR 3/4).
// Writers land documents in the memories table via deterministic
// upsert-REPLACE; readers (Search/Briefing) arrive in PR 4. It reuses the
// exact content builders, customId derivations, and Metadata validation the
// Supermemory implementation uses — the two backends must write
// byte-identical documents so the shadow comparison (PR 6) measures retrieval,
// not formatting drift.
//
// Embedding is synchronous and fail-open: on embedder absence, failure, or a
// contract violation (wrong count or wrong dimensionality — a misconfigured
// custom endpoint) rows land with a NULL embedding (immediately FTS/metadata-
// searchable) and the backfill sweep retries later. Mirrors "indexing
// failures are non-fatal".
type PGIndexer struct {
	pool           *pgxpool.Pool
	embedder       Embedder // nil = embeddings off; rows land unembedded
	installationID int64
	dims           int // storage dimensionality (memories.embedding)
	logger         *slog.Logger
}

// NewPGIndexer builds the Postgres-backed Indexer for one installation.
// The pool must have pgvector-go types registered (store.New does this).
// dims is the storage dimensionality; vectors of any other length are
// rejected fail-open at write time (migration 059's contract).
func NewPGIndexer(pool *pgxpool.Pool, embedder Embedder, installationID int64, dims int, logger *slog.Logger) *PGIndexer {
	return &PGIndexer{pool: pool, embedder: embedder, installationID: installationID, dims: dims, logger: logger}
}

var _ Indexer = (*PGIndexer)(nil)

// DisableLLMFilter is a Supermemory account-level concern; Postgres has no
// equivalent. No-op by design.
func (idx *PGIndexer) DisableLLMFilter(context.Context) error { return nil }

// embedForDocs returns one vector per doc plus the model id, or (nil, "") on
// the fail-open path: embedder absent, embed error, or embedder output
// violating the contract (count mismatch or non-storage dimensionality). A
// wrong-dims vector MUST NOT reach the INSERT — pgvector rejects it with a
// hard error that would fail the whole batch closed, the opposite of the
// documented degrade-to-NULL behavior.
func (idx *PGIndexer) embedForDocs(ctx context.Context, docs []Doc) ([][]float32, string) {
	if idx.embedder == nil {
		return nil, ""
	}
	texts := make([]string, len(docs))
	for i, d := range docs {
		texts[i] = d.Content
	}
	vecs, err := idx.embedder.Embed(ctx, texts)
	if err != nil {
		idx.logger.Warn("memory embed failed; rows land unembedded for backfill",
			"error", err, "docs", len(docs))
		return nil, ""
	}
	if len(vecs) != len(docs) {
		idx.logger.Warn("memory embed returned wrong vector count; rows land unembedded for backfill",
			"want", len(docs), "got", len(vecs))
		return nil, ""
	}
	for i, v := range vecs {
		if len(v) != idx.dims {
			idx.logger.Warn("memory embed returned wrong dimensionality; rows land unembedded for backfill",
				"want_dims", idx.dims, "got_dims", len(v), "doc", docs[i].CustomID)
			return nil, ""
		}
		// An all-zero vector has no direction, so cosine distance against it
		// is NaN — and NaN outranks every real score in Postgres, so such a
		// row would clear every similarity floor and poison briefings. A
		// stubbed or degenerate custom embedding endpoint can return one
		// (migration 059 explicitly supports self-hosted endpoints), and
		// dimensionality alone does not catch it. Treat it as a contract
		// violation and fail open to NULL, like the other two checks.
		if allZero(v) {
			idx.logger.Warn("memory embed returned a zero vector; rows land unembedded for backfill",
				"doc", docs[i].CustomID)
			return nil, ""
		}
	}
	return vecs, idx.embedder.Model()
}

// allZero reports whether a vector has no direction (cosine-undefined).
func allZero(v []float32) bool {
	for _, f := range v {
		if f != 0 {
			return false
		}
	}
	return true
}

// upsertDocs embeds and writes a batch of documents.
//
// Dedupe (last-write-wins on customId) keeps the batch deterministic: each
// doc is its own queued statement today, so in-transaction ON CONFLICT
// already yields LWW — but a future refactor to one multi-row INSERT would
// raise a cardinality violation on duplicate arbiter keys, and explicit
// dedupe keeps that door safely closed. Docs whose customId derivation
// produced "" are skipped (a shared "" key would LWW-collapse unrelated docs
// across the installation); the queue is sorted by customId so two
// concurrent batches over overlapping keys lock rows in the same order —
// cross-order acquisition deadlocks (40P01) were reproduced without it.
//
// pgx runs the whole SendBatch in one implicit transaction: a failed
// statement rolls back every row, and the deterministic upserts make a
// retry re-converge.
func (idx *PGIndexer) upsertDocs(ctx context.Context, docs []Doc) error {
	kept := make([]Doc, 0, len(docs))
	seen := make(map[string]int, len(docs))
	skippedEmpty := 0
	for _, d := range docs {
		if d.CustomID == "" {
			skippedEmpty++
			continue
		}
		if i, ok := seen[d.CustomID]; ok {
			kept[i] = d
			continue
		}
		seen[d.CustomID] = len(kept)
		kept = append(kept, d)
	}
	if skippedEmpty > 0 {
		idx.logger.Warn("skipping memory docs with empty customId", "count", skippedEmpty)
	}
	if len(kept) == 0 {
		if skippedEmpty > 0 {
			return fmt.Errorf("upsert memories: all %d docs dropped (empty customId)", skippedEmpty)
		}
		return nil
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].CustomID < kept[j].CustomID })

	vecs, model := idx.embedForDocs(ctx, kept)

	// ON CONFLICT notes: deleted_at resets — a re-index of the same customId
	// is a deliberate recreate. invalidated_at/superseded_by are PRESERVED —
	// invalidation is a policy judgment ("this knowledge is wrong") that a
	// mechanical re-write of the same content must not silently overturn;
	// un-invalidation is an explicit operation. type follows the rewrite like
	// content/metadata — search filters on the type column, so it must never
	// contradict metadata->>'type'. embedding overwrites even to NULL on
	// embed failure: content may have changed, and a stale vector for new
	// content is worse than a backfill-recoverable NULL.
	batch := &pgx.Batch{}
	const q = `
		INSERT INTO memories (installation_id, container_tag, custom_id, type, content, metadata, embedding, embedding_model)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (installation_id, custom_id) DO UPDATE
		SET type = EXCLUDED.type, content = EXCLUDED.content,
		    metadata = EXCLUDED.metadata,
		    embedding = EXCLUDED.embedding, embedding_model = EXCLUDED.embedding_model,
		    container_tag = EXCLUDED.container_tag, updated_at = now(),
		    deleted_at = NULL`
	for i, d := range kept {
		metaJSON, err := json.Marshal(d.Metadata)
		if err != nil {
			return fmt.Errorf("marshal metadata for %s: %w", d.CustomID, err)
		}
		var embedding *pgvector.Vector
		var embeddingModel *string
		if vecs != nil {
			v := pgvector.NewVector(vecs[i])
			embedding = &v
			embeddingModel = &model
		}
		// Postgres TEXT rejects NUL (22021) where Supermemory accepted it, and
		// one poisoned doc would abort the whole implicit transaction — strip
		// rather than lose the batch.
		content := strings.ReplaceAll(d.Content, "\x00", "")
		batch.Queue(q,
			idx.installationID, d.ContainerTag, d.CustomID, d.Type,
			content, metaJSON, embedding, embeddingModel)
	}
	br := idx.pool.SendBatch(ctx, batch)
	var execErr error
	for range kept {
		if _, err := br.Exec(); err != nil {
			execErr = fmt.Errorf("upsert memories batch: %w", err)
			break
		}
	}
	closeErr := br.Close()
	if execErr != nil {
		return execErr
	}
	if closeErr != nil {
		return fmt.Errorf("close memories batch: %w", closeErr)
	}
	return nil
}

// upsertOne writes a single document, wrapping any error with op.
func (idx *PGIndexer) upsertOne(ctx context.Context, op string, d Doc) error {
	if err := idx.upsertDocs(ctx, []Doc{d}); err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	return nil
}

// IndexReviewCommentsBatch mirrors the Supermemory implementation: same
// metadata validation, same skip semantics, same all-dropped error.
func (idx *PGIndexer) IndexReviewCommentsBatch(ctx context.Context, owner, repo string, comments []ReviewMemory) error {
	if len(comments) == 0 {
		return nil
	}
	docs, skipped := buildReviewDocs(owner, repo, comments, idx.logger)
	if len(docs) == 0 {
		if skipped > 0 {
			return fmt.Errorf("batch indexing review comments: all %d docs dropped (invalid metadata)", skipped)
		}
		return nil
	}
	if err := idx.upsertDocs(ctx, docs); err != nil {
		return fmt.Errorf("batch indexing review comments: %w", err)
	}
	if skipped > 0 {
		idx.logger.Error("batch indexed review comments with drops", "repo", repo, "indexed", len(docs), "skipped", skipped)
	} else {
		idx.logger.Info("batch indexed review comments", "repo", repo, "count", len(docs))
	}
	return nil
}

// IndexRule mirrors the Supermemory implementation (owner accepted for
// back-compat, `_shared` container, type=rule).
func (idx *PGIndexer) IndexRule(ctx context.Context, owner string, rule RuleMemory) error {
	_ = owner
	doc, err := buildRuleDoc(rule)
	if err != nil {
		return err
	}
	return idx.upsertOne(ctx, "indexing rule", doc)
}

// IndexPattern mirrors the Supermemory implementation; the returned
// AddResponse.ID is the deterministic customId (doc id == customId in the PG
// store — this collapses the chunk-id/custom-id resolution dance).
func (idx *PGIndexer) IndexPattern(ctx context.Context, repo string, p PatternMemory) (*AddResponse, error) {
	doc, err := buildPatternDoc(repo, p)
	if err != nil {
		return nil, err
	}
	if err := idx.upsertOne(ctx, "indexing repo pattern", doc); err != nil {
		return nil, err
	}
	idx.logger.Info("indexed repo pattern", "repo", repo, "source", p.Source)
	return &AddResponse{ID: doc.CustomID}, nil
}

// IndexSharedPattern mirrors the Supermemory implementation, including the
// confidence=1.00 pin (re-learning is the liveness signal for shared decay).
func (idx *PGIndexer) IndexSharedPattern(ctx context.Context, p PatternMemory) (*AddResponse, error) {
	doc, err := buildSharedPatternDoc(p)
	if err != nil {
		return nil, err
	}
	if err := idx.upsertOne(ctx, "indexing shared pattern", doc); err != nil {
		return nil, err
	}
	idx.logger.Info("indexed shared pattern", "source", p.Source)
	return &AddResponse{ID: doc.CustomID}, nil
}

// IndexFeedbackSignal mirrors the Supermemory implementation: same shape
// derivation, same dismissal keying and provenance extras, same
// unsupported-action error.
func (idx *PGIndexer) IndexFeedbackSignal(ctx context.Context, owner, repo string, fb FeedbackMemory) error {
	doc, err := buildFeedbackDoc(owner, repo, fb)
	if err != nil {
		return err
	}
	if err := idx.upsertOne(ctx, "indexing feedback signal", doc); err != nil {
		return err
	}
	idx.logger.Info("indexed feedback signal", "action", fb.Action, "repo", repo, "file", fb.FilePath)
	return nil
}

// IndexScenario mirrors the Supermemory implementation.
func (idx *PGIndexer) IndexScenario(ctx context.Context, owner, repo string, scenarioID int64, description, severity string, files []string) error {
	_ = owner
	doc, err := buildScenarioDoc(repo, scenarioID, description, severity, files)
	if err != nil {
		return err
	}
	return idx.upsertOne(ctx, "indexing scenario", doc)
}

// DeleteDocument soft-deletes by customId. In the PG store doc id == customId
// by construction (IndexPattern returns it as AddResponse.ID), which unifies
// the two id kinds in circulation under the Supermemory backend. A no-match
// delete is logged, not silent: rows written under the Supermemory backend
// carry SM server doc-ids in their mirror columns, and those can never match
// a PG custom_id — the log is how such transition gaps surface (PR 5/6
// backfill repoints them).
func (idx *PGIndexer) DeleteDocument(ctx context.Context, documentID string) error {
	tag, err := idx.pool.Exec(ctx,
		"UPDATE memories SET deleted_at = now(), updated_at = now() WHERE installation_id = $1 AND custom_id = $2 AND deleted_at IS NULL",
		idx.installationID, documentID)
	if err != nil {
		return fmt.Errorf("deleting memory %s: %w", documentID, err)
	}
	if tag.RowsAffected() == 0 {
		idx.logger.Warn("memory delete matched no live row (SM-era id or already deleted)", "document_id", documentID)
	}
	return nil
}
