package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
)

// PGIndexer is the Postgres-native Indexer implementation (program PR 3/4).
// Writers land documents in the memories table via deterministic
// upsert-REPLACE; readers (Search/Briefing) arrive in PR 4. It reuses the
// exact content builders, customId derivations, and Metadata validation every
// other writer uses, so document shape is decided in one place.
//
// StorageDimensions is the fixed width of the memories.embedding column
// (migration 059). It, not EMBEDDINGS_DIMENSIONS, is the authority on
// dimensionality: pgvector rejects any other width at INSERT, and pgx runs a
// whole batch in one implicit transaction, so one mismatched vector rolls back
// every row in it. Memory indexing failures are non-fatal and log at Warn, so
// the visible symptom is that writes simply stop. Reads fail the same way — a
// query vector of the wrong width errors on every `<=>` comparison.
const StorageDimensions = 1024

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
	// reviewID attributes every write this indexer makes to one review run.
	// The row keeps it as current provenance and the attribution ledger appends
	// it as history. nil is correct for rules, reaction/reply feedback, and
	// backfills — none of those belong to a run.
	reviewID *uuid.UUID
	// disableSharedDecay makes _shared confidence use its stored pinned value.
	// It is resolved from installations.default_settings for each indexer.
	disableSharedDecay bool
	// resolveWriteEmbedder bypasses process caches through the connection that
	// holds this tenant's shared embedding-space lock. Registry-created
	// indexers set it; direct indexers retain their explicitly supplied embedder.
	resolveWriteEmbedder func(context.Context, *pgxpool.Conn, int64) (Embedder, error)
}

// NewPGIndexer builds the Postgres-backed Indexer for one installation.
// The pool must have pgvector-go types registered (store.New does this).
// dims is the storage dimensionality; vectors of any other length are
// rejected fail-open at write time (migration 059's contract).
type PGIndexerOption func(*PGIndexer)

// WithSharedDecayDisabled opts this installation out of query-time shared
// confidence decay. False/default preserves the normal retirement policy.
func WithSharedDecayDisabled(disabled bool) PGIndexerOption {
	return func(idx *PGIndexer) { idx.disableSharedDecay = disabled }
}

// withWriteEmbedderResolver equips ordinary Registry writers with a durable,
// connection-bound current-space resolver. It is internal because callers
// constructing a fixed PGIndexer for repair, backfill, or tests intentionally
// own that fixed embedder.
func withWriteEmbedderResolver(resolve func(context.Context, *pgxpool.Conn, int64) (Embedder, error)) PGIndexerOption {
	return func(idx *PGIndexer) { idx.resolveWriteEmbedder = resolve }
}

func NewPGIndexer(pool *pgxpool.Pool, embedder Embedder, installationID int64, dims int, logger *slog.Logger, opts ...PGIndexerOption) *PGIndexer {
	idx := &PGIndexer{pool: pool, embedder: embedder, installationID: installationID, dims: dims, logger: logger}
	for _, opt := range opts {
		opt(idx)
	}
	return idx
}

var _ Indexer = (*PGIndexer)(nil)

// ForReview returns a copy of this indexer that stamps current provenance and
// appends review attribution on every row it writes, so a completed review can
// answer "what did I learn" even after another review re-upserts the row.
//
// A copy, not a mutation: Registry.GetIndexer hands out a fresh PGIndexer per
// call, but the pool and embedder inside it are shared, and mutating the
// receiver would make attribution depend on which goroutine wrote last.
func (idx *PGIndexer) ForReview(reviewID uuid.UUID) Indexer {
	// The zero UUID is not an id — buildRun's callers pass one for a run with
	// no persisted review row (tests, dry runs). Stamping it would create a
	// bucket of rows attributed to a review that does not exist, and the FK
	// would reject the write outright, taking the real memory content with it.
	if reviewID == uuid.Nil {
		return idx
	}
	cp := *idx
	cp.reviewID = &reviewID
	return &cp
}

// vectorTypeOnce guards a single probe of memories.embedding's actual type.
// Both are supported deliberately: pgContext cannot be installed on ANY managed
// Postgres (RDS, Supabase, Cloud SQL, Neon), so a self-hosted install will
// always be on pgvector, while our production image carries pgContext. Assuming
// either one breaks the other half of the fleet.
var (
	vectorTypeOnce sync.Once
	vectorIsPGCtx  bool
)

// usesPGContextVector reports whether memories.embedding is a pgcontext.vector.
//
// Probed once per process from the catalog rather than inferred from
// "is the extension installed": the extension can be present while the
// ownership conversion has not run, and the operator that resolves depends on
// the COLUMN's type, not the extension's presence. Defaults to pgvector on any
// probe error — that is the portable path, and a wrong guess there produces a
// loud operator error rather than silent empty results.
func usesPGContextVector(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) bool {
	vectorTypeOnce.Do(func() {
		var typeName string
		err := pool.QueryRow(ctx, `
			SELECT atttypid::regtype::text
			FROM pg_attribute
			WHERE attrelid = 'public.memories'::regclass AND attname = 'embedding'`).Scan(&typeName)
		if err != nil {
			logger.Warn("could not probe memories.embedding type; assuming pgvector", "error", err)
			return
		}
		vectorIsPGCtx = strings.HasPrefix(typeName, "pgcontext.")
		logger.Info("memory vector column type probed", "type", typeName, "pgcontext", vectorIsPGCtx)
	})
	return vectorIsPGCtx
}

// vectorParam renders the placeholder for a bound query vector. pgvector-go
// encodes as public.vector; when the column has been converted, the parameter
// needs an explicit cast because pgcontext.vector is a DIFFERENT oid with no
// cross-type operator. The dense layouts are byte-identical, so the cast is
// free.
func vectorParam(placeholder string, pgctx bool) string {
	return placeholder + vecCast(pgctx)
}

// vecCast is the cast suffix a bound query vector needs, or "" on pgvector.
func vecCast(pgctx bool) string {
	if pgctx {
		return "::pgcontext.vector"
	}
	return ""
}

// cosineOp renders the cosine-distance operator. pgContext ships its own <=>
// in the pgcontext schema, which is NOT on the default search_path, so it must
// be schema-qualified at the call site.
func cosineOp(pgctx bool) string {
	if pgctx {
		return "OPERATOR(pgcontext.<=>)"
	}
	return "<=>"
}

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

// embeddingSpaceID returns the durable identity of the active coordinate
// system. HTTP embedders include their endpoint; small test/custom embedders
// that only implement Embedder retain a conservative model+dimensions identity.
func (idx *PGIndexer) embeddingSpaceID() string {
	if idx.embedder == nil {
		return ""
	}
	if identified, ok := idx.embedder.(interface{ SpaceID() string }); ok {
		return identified.SpaceID()
	}
	return fmt.Sprintf("legacy:%s:%d", idx.embedder.Model(), idx.dims)
}

// ImportDocs writes pre-built documents through the SAME path live writes use.
//
// Exported solely for the phase-0 archive backfill (cmd/backfill-memory). It
// deliberately adds no logic of its own: the imported corpus has to be
// indistinguishable from what the pipeline would have written, which means
// inheriting upsertDocs' dedupe, NUL stripping, embedding, dimensionality and
// zero-vector guards rather than reimplementing a second, subtly different
// writer. A divergence here would surface as rows that read back fine but
// never clear a similarity floor.
func (idx *PGIndexer) ImportDocs(ctx context.Context, docs []Doc) error {
	return idx.upsertDocs(ctx, docs)
}

// CountReembedPending reports live rows that cannot participate in the active
// dense space. It shares ReembedMissing's predicate so fleet planning cannot
// report healthy while foreign-space vectors remain excluded from search.
func (idx *PGIndexer) CountReembedPending(ctx context.Context) (int64, error) {
	if idx.embedder == nil {
		return 0, fmt.Errorf("count reembed pending: no embedder configured for installation %d", idx.installationID)
	}
	var count int64
	err := idx.pool.QueryRow(ctx, `
		SELECT count(*) FROM live_memories
		WHERE installation_id = $1
		  AND (embedding IS NULL OR embedding_space IS DISTINCT FROM $2)`,
		idx.installationID, idx.embeddingSpaceID()).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count reembed pending: %w", err)
	}
	return count, nil
}

// ReembedMissing embeds rows with no vector or a vector from a different
// embedding space, returning how many it repaired. Scoped to this indexer's installation.
//
// The write path fails OPEN: when the embedder is absent or breaks its
// contract, embedForDocs returns nil and the row is written with a NULL
// embedding, reachable through the full-text leg only. Nothing used to repair
// those rows once the nightly drift sweep was removed, so a transient
// embeddings outage silently and permanently halved retrieval for whatever was
// written during it. This is that repair.
//
// Content is deliberately NOT rewritten — only embedding and its model/space stamps.
// A re-embed must not resurrect the content of a row that has since been
// edited, and it must not touch updated_at, which query-time decay reads as
// the liveness signal for `_shared` patterns.
//
// Progress is guaranteed by the predicate itself: every repaired row stops
// matching the NULL-or-foreign-space condition. A batch that repairs nothing therefore means
// embedding is failing, and returning an error there is what stops this from
// spinning forever on the same page.
func (idx *PGIndexer) ReembedMissing(ctx context.Context, batchSize int) (int, error) {
	if idx.embedder == nil {
		return 0, fmt.Errorf("reembed: no embedder configured for installation %d", idx.installationID)
	}
	if batchSize <= 0 {
		batchSize = 100
	}
	cast := vectorParam("$1", usesPGContextVector(ctx, idx.pool, idx.logger))
	spaceID := idx.embeddingSpaceID()
	// The content predicate closes a race, and is not redundant with
	// the NULL-or-foreign-space predicate. A live upsert can rewrite this row's content
	// between the SELECT and the UPDATE and leave the embedding NULL again
	// (the same embedder is still failing), and identity alone would then
	// stamp a vector derived from the OLD text onto the NEW content --
	// producing a row that retrieves for something it no longer says, which
	// is worse than the NULL it replaced. A raced row simply does not match,
	// stays NULL, and is repaired on a later pass.
	update := fmt.Sprintf(
		`UPDATE live_memories SET embedding = %s, embedding_model = $2, embedding_space = $3
		 WHERE installation_id = $4 AND custom_id = $5
		   AND (embedding IS NULL OR embedding_space IS DISTINCT FROM $3)
		   AND content = $6`, cast)

	// A page can make zero progress without being done: if every row on it was
	// rewritten between the SELECT and the UPDATE, the content predicate
	// (correctly) refuses all of them and they are still NULL. Retrying is
	// right — contention is transient, and rows that were concurrently
	// embedded or deleted simply drop out of the next SELECT — but it must be
	// bounded, or a row rewritten in a tight loop spins forever.
	const maxZeroProgressRounds = 3
	zeroRounds := 0

	total := 0
	for {
		rows, err := idx.pool.Query(ctx, `
			SELECT custom_id, content FROM live_memories
			WHERE installation_id = $1
			  AND (embedding IS NULL OR embedding_space IS DISTINCT FROM $2)
			ORDER BY id
			LIMIT $3`, idx.installationID, spaceID, batchSize)
		if err != nil {
			return total, fmt.Errorf("reembed: selecting unembedded rows: %w", err)
		}
		var docs []Doc
		for rows.Next() {
			var d Doc
			if err := rows.Scan(&d.CustomID, &d.Content); err != nil {
				rows.Close()
				return total, fmt.Errorf("reembed: scanning row: %w", err)
			}
			docs = append(docs, d)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return total, fmt.Errorf("reembed: reading rows: %w", err)
		}
		if len(docs) == 0 {
			return total, nil
		}

		vecs, model := idx.embedForDocs(ctx, docs)
		if vecs == nil {
			// embedForDocs already logged the specific contract violation.
			return total, fmt.Errorf("reembed: embedding failed for a batch of %d; %d rows repaired before this point", len(docs), total)
		}

		batch := &pgx.Batch{}
		for i, d := range docs {
			v := pgvector.NewVector(vecs[i])
			batch.Queue(update, v, model, spaceID, idx.installationID, d.CustomID, d.Content)
		}
		br := idx.pool.SendBatch(ctx, batch)
		repaired := 0
		var execErr error
		for range docs {
			tag, err := br.Exec()
			if err != nil {
				execErr = fmt.Errorf("reembed: updating batch: %w", err)
				break
			}
			repaired += int(tag.RowsAffected())
		}
		closeErr := br.Close()
		if execErr != nil {
			return total, execErr
		}
		if closeErr != nil {
			return total, fmt.Errorf("reembed: closing batch: %w", closeErr)
		}
		total += repaired
		idx.logger.Info("reembedded memory rows", "installation_id", idx.installationID, "repaired", repaired, "total", total)

		if repaired == 0 {
			zeroRounds++
			if zeroRounds < maxZeroProgressRounds {
				continue
			}
			// Do NOT return nil here. These rows still have a NULL embedding,
			// so they are reachable through the full-text leg only, and the
			// fleet sweep would read a nil error as "this installation is
			// repaired" and move on -- reporting success over exactly the
			// state this command exists to eliminate.
			return total, fmt.Errorf(
				"reembed: %d row(s) were rewritten concurrently and remain unembedded after %d attempts; rerun to repair them",
				len(docs), maxZeroProgressRounds)
		}
		zeroRounds = 0
	}
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
	return idx.writeDocs(ctx, docs, false)
}

// mirrorDoc repairs a missing or previously deleted memory row from the
// relational outbox. An already-live row is authoritative: it may have been
// written by a review-bound pipeline indexer with provenance and metadata the
// relational projection cannot represent. The conflict predicate makes that
// preservation atomic with a concurrent pipeline write.
func (idx *PGIndexer) mirrorDoc(ctx context.Context, doc Doc) error {
	var exists bool
	if err := idx.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM memories
			WHERE installation_id = $1 AND custom_id = $2 AND deleted_at IS NULL
		)`, idx.installationID, doc.CustomID).Scan(&exists); err != nil {
		return fmt.Errorf("check mirrored memory %s: %w", doc.CustomID, err)
	}
	if exists {
		return nil
	}
	return idx.writeDocs(ctx, []Doc{doc}, true)
}

type pgBatchSender interface {
	SendBatch(context.Context, *pgx.Batch) pgx.BatchResults
}

func (idx *PGIndexer) writeDocs(ctx context.Context, docs []Doc, preserveExisting bool) error {
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

	// Probe before reserving a connection for the tenant lock. The catalog
	// probe is process-wide and unrelated to embedding-space authority.
	pgctx := usesPGContextVector(ctx, idx.pool, idx.logger)
	if idx.resolveWriteEmbedder == nil {
		return idx.writeKeptDocs(ctx, kept, preserveExisting, idx, idx.pool, pgctx)
	}

	lockKey := memoryEmbeddingLockKey(idx.installationID)
	conn, err := acquireEmbeddingWriteLock(ctx, idx.pool, lockKey)
	if err != nil {
		return fmt.Errorf("upsert memories: acquire embedding-space lock: %w", err)
	}
	defer releaseEmbeddingLock(ctx, conn, lockKey, true, idx.logger, idx.installationID)

	// Resolve only after the shared lock is held and through that same
	// connection. A rotation may commit while this provider call is in flight,
	// but its exclusive repair cannot finish until this write commits; if repair
	// already finished, this read necessarily observes the repaired space.
	embedder, resolveErr := idx.resolveWriteEmbedder(ctx, conn, idx.installationID)
	if resolveErr != nil {
		// Never substitute the platform embedder here: that would stamp a
		// confidently wrong space for a BYOK tenant. Preserve the established
		// fail-open write path by landing NULL for a later repair instead.
		idx.logger.Warn("resolve current memory embedder; row lands unembedded for backfill",
			"installation_id", idx.installationID, "error", resolveErr)
		embedder = nil
	}
	active := *idx
	active.embedder = embedder
	return idx.writeKeptDocs(ctx, kept, preserveExisting, &active, conn, pgctx)
}

func (idx *PGIndexer) writeKeptDocs(ctx context.Context, kept []Doc, preserveExisting bool, active *PGIndexer, sender pgBatchSender, pgctx bool) error {
	vecs, model := active.embedForDocs(ctx, kept)

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
	// $7 is cast only where the column has been converted: the driver binds a
	// pgvector.Vector (public.vector) and pgcontext.vector is a different oid
	// with no implicit coercion, so an unqualified bind fails at write time
	// there. On an unconverted database the cast would fail instead, which is
	// why the placeholder is probed rather than hard-coded.
	//
	// review_id follows the rewrite like content/metadata and remains useful
	// current provenance. The CTE also appends the review-to-memory link in the
	// same statement; deterministic re-upserts therefore cannot transfer
	// historical attribution away from an earlier review. A writer with no
	// review clears only current provenance and creates no history row.
	conflictPredicate := ""
	if preserveExisting {
		conflictPredicate = " WHERE memories.deleted_at IS NOT NULL"
	}
	q := fmt.Sprintf(`
		WITH upserted AS (
			INSERT INTO memories (installation_id, container_tag, custom_id, type, content, metadata, embedding, embedding_model, review_id, embedding_space)
			VALUES ($1, $2, $3, $4, $5, $6, %s, $8, $9, $10)
			ON CONFLICT (installation_id, custom_id) DO UPDATE
			SET type = EXCLUDED.type, content = EXCLUDED.content,
			    metadata = EXCLUDED.metadata,
			    embedding = EXCLUDED.embedding, embedding_model = EXCLUDED.embedding_model,
			    embedding_space = EXCLUDED.embedding_space,
			    container_tag = EXCLUDED.container_tag, updated_at = now(),
			    deleted_at = NULL, review_id = EXCLUDED.review_id%s
			RETURNING id
		)
		INSERT INTO memory_review_attributions (memory_id, review_id)
		SELECT id, $9 FROM upserted WHERE $9::uuid IS NOT NULL
		ON CONFLICT (memory_id, review_id) DO NOTHING`,
		vectorParam("$7", pgctx), conflictPredicate)
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
		// Postgres TEXT rejects NUL (22021), and
		// one poisoned doc would abort the whole implicit transaction — strip
		// rather than lose the batch.
		content := strings.ReplaceAll(d.Content, "\x00", "")
		var embeddingSpace *string
		if vecs != nil {
			space := active.embeddingSpaceID()
			embeddingSpace = &space
		}
		batch.Queue(q,
			idx.installationID, d.ContainerTag, d.CustomID, d.Type,
			content, metaJSON, embedding, embeddingModel, idx.reviewID, embeddingSpace)
	}
	br := sender.SendBatch(ctx, batch)
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

// IndexReviewCommentsBatch writes one row per comment: same
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

// IndexRule writes the rule to the installation-wide container (owner accepted for
// back-compat, `_shared` container, type=rule).
func (idx *PGIndexer) IndexRule(ctx context.Context, owner string, rule RuleMemory) error {
	_ = owner
	doc, err := buildRuleDoc(rule)
	if err != nil {
		return err
	}
	return idx.upsertOne(ctx, "indexing rule", doc)
}

// MirrorPattern converges a relational pattern into memory without reducing a
// live pipeline-written row. Missing rows are inserted and soft-deleted rows
// are recreated; live rows retain their current metadata and review provenance.
func (idx *PGIndexer) MirrorPattern(ctx context.Context, repo string, shared bool, p PatternMemory) error {
	var (
		doc Doc
		err error
	)
	if shared {
		doc, err = buildSharedPatternDoc(p)
	} else {
		doc, err = buildPatternDoc(repo, p)
	}
	if err != nil {
		return err
	}
	if err := idx.mirrorDoc(ctx, doc); err != nil {
		return fmt.Errorf("mirroring pattern: %w", err)
	}
	return nil
}

// IndexPattern writes a repo-scoped pattern. The returned IndexResult.ID is
// the deterministic customId: doc id == customId in this store, which collapses
// the chunk-id/custom-id resolution dance.
func (idx *PGIndexer) IndexPattern(ctx context.Context, repo string, p PatternMemory) (*IndexResult, error) {
	doc, err := buildPatternDoc(repo, p)
	if err != nil {
		return nil, err
	}
	if err := idx.upsertOne(ctx, "indexing repo pattern", doc); err != nil {
		return nil, err
	}
	idx.logger.Info("indexed repo pattern", "repo", repo, "source", p.Source)
	return &IndexResult{ID: doc.CustomID}, nil
}

// IndexSharedPattern writes an installation-wide pattern, including the
// confidence=1.00 pin (re-learning is the liveness signal for shared decay).
func (idx *PGIndexer) IndexSharedPattern(ctx context.Context, p PatternMemory) (*IndexResult, error) {
	doc, err := buildSharedPatternDoc(p)
	if err != nil {
		return nil, err
	}
	if err := idx.upsertOne(ctx, "indexing shared pattern", doc); err != nil {
		return nil, err
	}
	idx.logger.Info("indexed shared pattern", "source", p.Source)
	return &IndexResult{ID: doc.CustomID}, nil
}

// IndexFeedbackSignal records an accept/dismiss signal: same shape
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

// ReconcileFeedbackSignal replaces reaction-owned state for one finding. The
// deletes are soft and idempotent; source-specific IDs preserve feedback from
// trusted replies and automatic praise. The legacy cleanup recognizes old
// reaction dismissals by the absence of source and developer-explanation text.
func (idx *PGIndexer) ReconcileFeedbackSignal(ctx context.Context, owner, repo string, fb FeedbackMemory) error {
	plan, err := feedbackReconciliationPlan(owner, repo, fb)
	if err != nil {
		return err
	}
	if err := idx.deleteFeedbackDocuments(ctx, plan.DeleteFirst, plan.LegacyDismissalBefore); err != nil {
		return err
	}
	if plan.Upsert != nil {
		if err := idx.upsertOne(ctx, "reconciling feedback signal", *plan.Upsert); err != nil {
			return err
		}
	}
	if err := idx.deleteFeedbackDocuments(ctx, plan.DeleteAfter, plan.LegacyDismissalAfter); err != nil {
		return err
	}
	idx.logger.Info("reconciled feedback signal", "action", fb.Action, "repo", repo, "file", fb.FilePath)
	return nil
}

func (idx *PGIndexer) deleteFeedbackDocuments(ctx context.Context, documentIDs []string, legacyReactionDismissalID string) error {
	if len(documentIDs) == 0 && legacyReactionDismissalID == "" {
		return nil
	}
	_, err := idx.pool.Exec(ctx, `
		UPDATE memories SET deleted_at = now(), updated_at = now()
		WHERE installation_id = $1 AND deleted_at IS NULL AND (
			custom_id = ANY($2::text[])
			OR (
				custom_id = NULLIF($3, '')
				AND metadata->>'action' = 'dismissed'
				AND COALESCE(metadata->>'source', '') = ''
				AND content NOT LIKE '%' || E'\n\nDeveloper explanation:' || '%'
			)
		)
	`, idx.installationID, documentIDs, legacyReactionDismissalID)
	if err != nil {
		return fmt.Errorf("retracting stale feedback memory: %w", err)
	}
	return nil
}

// IndexScenario writes a scenario seed to the repo container.
func (idx *PGIndexer) IndexScenario(ctx context.Context, owner, repo string, scenarioID int64, description, severity string, files []string) error {
	_ = owner
	doc, err := buildScenarioDoc(repo, scenarioID, description, severity, files)
	if err != nil {
		return err
	}
	return idx.upsertOne(ctx, "indexing scenario", doc)
}

// InvalidateDocument records that a memory is no longer valid without deleting
// its content or attribution history. Repeating the same transition is a
// successful no-op; a deleted or unknown document returns an explicit error.
func (idx *PGIndexer) InvalidateDocument(ctx context.Context, documentID string) error {
	tag, err := idx.pool.Exec(ctx, `
		UPDATE memories
		SET invalidated_at = COALESCE(invalidated_at, now()),
		    updated_at = CASE WHEN invalidated_at IS NULL THEN now() ELSE updated_at END
		WHERE installation_id = $1 AND custom_id = $2 AND deleted_at IS NULL`,
		idx.installationID, documentID)
	if err != nil {
		return fmt.Errorf("invalidating memory %s: %w", documentID, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("invalidating memory %s: document not found", documentID)
	}
	return nil
}

// SupersedeDocument records that replacementID is the current knowledge that
// replaces documentID. The replacement must be a distinct live row in the
// same installation. Repeating the same transition is idempotent; replacing a
// source with a different target requires a separate policy decision instead
// of silently rewriting history.
func (idx *PGIndexer) SupersedeDocument(ctx context.Context, documentID, replacementID string) error {
	if documentID == replacementID {
		return fmt.Errorf("superseding memory %s: replacement must be a different document", documentID)
	}
	tag, err := idx.pool.Exec(ctx, `
		UPDATE memories AS source
		SET invalidated_at = COALESCE(source.invalidated_at, now()),
		    superseded_by = replacement.id,
		    updated_at = CASE
		      WHEN source.invalidated_at IS NULL OR source.superseded_by IS DISTINCT FROM replacement.id THEN now()
		      ELSE source.updated_at
		    END
		FROM live_memories AS replacement
		WHERE source.installation_id = $1
		  AND source.custom_id = $2
		  AND source.deleted_at IS NULL
		  AND (source.superseded_by IS NULL OR source.superseded_by = replacement.id)
		  AND replacement.installation_id = source.installation_id
		  AND replacement.custom_id = $3`,
		idx.installationID, documentID, replacementID)
	if err != nil {
		return fmt.Errorf("superseding memory %s with %s: %w", documentID, replacementID, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("superseding memory %s with %s: source or live replacement not found", documentID, replacementID)
	}
	return nil
}

// DeleteDocument soft-deletes by customId. In the PG store doc id == customId
// by construction (IndexPattern returns it as IndexResult.ID), which unifies
// doc id and customId into one value. A no-match delete is logged, not silent:
// a row whose mirror column still holds a non-derived id can never match a
// custom_id, and the log is how it surfaces (backfill-memory --repoint fixes
// them).
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
