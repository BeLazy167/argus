package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/BeLazy167/argus/backend/internal/store/db"
	"github.com/jackc/pgx/v5"
)

type Pattern struct {
	ID             int64   `json:"id"`
	InstallationID int64   `json:"installation_id"`
	RepoID         *int64  `json:"repo_id,omitempty"`
	Content        string  `json:"content"`
	MemoryDocID    *string `json:"memory_doc_id,omitempty"`
	// MemoryCustomID is the deterministic memory identity for rows written with
	// one (every path since the custom-id migration). Legacy rows carry only
	// MemoryDocID. Populated by GetPattern.
	MemoryCustomID *string   `json:"memory_custom_id,omitempty"`
	CreatedBy      *string   `json:"created_by,omitempty"`
	Source         string    `json:"source"`
	Category       *string   `json:"category,omitempty"`
	PRNumber       *int      `json:"pr_number,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	Status         string    `json:"status"`
	EvidenceCount  int       `json:"evidence_count"`
}

type PatternStat struct {
	Week   time.Time `json:"week"`
	Source string    `json:"source"`
	Count  int       `json:"count"`
}

func (s *Store) ListPatterns(ctx context.Context, installationIDs []int64) (storeResult0 []Pattern, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "ListPatterns",

			"installation_ids_count",

			len(installationIDs))
	defer func() {
		if recovered := recover(); recovered !=
			nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr,
			storeResult0)
	}()

	rows, err := s.q.ListPatterns(ctx, installationIDs)
	if err != nil {
		return nil, err
	}
	patterns := make([]Pattern, 0, len(rows))
	for _, row := range rows {
		pattern, err := patternFromSQLC(row.ID, row.InstallationID, row.RepoID, row.Content, row.MemoryDocID, row.CreatedBy, row.Source, row.Category, row.PRNumber, row.CreatedAt, row.UpdatedAt)
		if err != nil {
			return nil, err
		}
		pattern.Status, pattern.EvidenceCount = row.Status, row.EvidenceCount
		patterns = append(patterns, pattern)
	}
	return patterns, nil
}

// ListPatternsForRepo returns org-wide patterns (repo_id IS NULL) plus patterns scoped to the given repo.
func (s *Store) ListPatternsForRepo(ctx context.Context, installationIDs []int64, repoID int64) (storeResult0 []Pattern, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "ListPatternsForRepo",

			"installation_ids_count",

			len(installationIDs), "repo_id", storeLogValue(repoID))
	defer func() {
		if recovered :=
			recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

	rows, err := s.q.ListPatternsForRepo(ctx, db.ListPatternsForRepoParams{Column1: installationIDs, RepoID: &repoID})
	if err != nil {
		return nil, err
	}
	patterns := make([]Pattern, 0, len(rows))
	for _, row := range rows {
		pattern, err := patternFromSQLC(row.ID, row.InstallationID, row.RepoID, row.Content, row.MemoryDocID, row.CreatedBy, row.Source, row.Category, row.PRNumber, row.CreatedAt, row.UpdatedAt)
		if err != nil {
			return nil, err
		}
		pattern.Status, pattern.EvidenceCount = row.Status, row.EvidenceCount
		patterns = append(patterns, pattern)
	}
	return patterns, nil
}

// CreatePattern inserts a pattern and its memory-mirror event atomically.
// Callers must supply a deterministic memoryCustomID (or a legacy memoryDocID)
// so a committed relational row always has a retryable mirror identity.
// mirrorExtra carries provenance that the patterns table does not model but a
// repaired memory document still needs (for example a shared pattern's full
// owner/repo origin).
func (s *Store) CreatePattern(ctx context.Context, installationID int64, repoID *int64, content string, memoryDocID *string, createdBy *string, source *string, category *string, prNumber *int, memoryCustomID *string, mirrorExtra map[string]string) (storeResult0 *Pattern, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "CreatePattern",

			"installation_id",

			storeLogValue(installationID), "repo_id", storeLogValue(repoID), "memory_doc_id",

			storeLogValue(memoryDocID), "source", storeLogValue(source), "category", storeLogValue(category), "pr_number",
			storeLogValue(prNumber), "memory_custom_id", storeLogValue(memoryCustomID), "mirror_extra_count",
			len(mirrorExtra))
	defer func() {
		if recovered :=
			recover(); recovered != nil {
			storeFinishPanic(storeFinish,
				recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

	customID := firstNonEmpty(memoryCustomID, memoryDocID)
	if customID == "" {
		return nil, fmt.Errorf("creating pattern requires a deterministic memory identity")
	}

	var pattern Pattern
	err := s.WithMemoryMirrorTx(ctx, func(tx pgx.Tx) (MemoryMirrorEvent, error) {
		// Every creation path takes the per-identity lock so CreateOrGetPattern's
		// recheck is meaningful against concurrent pipeline/dashboard writers.
		// Same key the mirror worker uses; cheap; released at commit.
		if err := lockMemoryMirrorCustomID(ctx, tx, installationID, customID); err != nil {
			return MemoryMirrorEvent{}, err
		}
		created, event, err := createPatternTx(ctx, tx, installationID, repoID, content, memoryDocID, createdBy, source, category, prNumber, memoryCustomID, mirrorExtra)
		if err != nil {
			return MemoryMirrorEvent{}, err
		}
		pattern = created
		return event, nil
	})
	if err != nil {
		return nil, err
	}
	return &pattern, nil
}

// createPatternTx is CreatePattern's insert + outbox payload inside a caller's
// transaction. It keeps the existing ON CONFLICT upsert semantics — pipeline
// writers rely on them — and is shared with CreateOrGetPattern, which decides
// BEFORE calling it whether an insert should happen at all.
func createPatternTx(ctx context.Context, tx pgx.Tx, installationID int64, repoID *int64, content string, memoryDocID *string, createdBy *string, source *string, category *string, prNumber *int, memoryCustomID *string, mirrorExtra map[string]string) (Pattern, MemoryMirrorEvent, error) {
	customID := firstNonEmpty(memoryCustomID, memoryDocID)
	q := db.New(tx)
	repo := ""
	if repoID != nil {
		repoRow, err := q.GetRepoScoped(ctx, db.GetRepoScopedParams{ID: *repoID, Column2: []int64{installationID}})
		if err != nil {
			return Pattern{}, MemoryMirrorEvent{}, fmt.Errorf("resolve pattern repo: %w", err)
		}
		_, repo, _ = strings.Cut(repoRow.FullName, "/")
		if repo == "" {
			return Pattern{}, MemoryMirrorEvent{}, fmt.Errorf("repo %d has invalid full name", *repoID)
		}
	}
	row, err := q.CreatePattern(ctx, db.CreatePatternParams{
		InstallationID: installationID, RepoID: repoID, Content: content,
		MemoryDocID: memoryDocID, CreatedBy: createdBy, Source: source,
		Category: category, PRNumber: prNumber, MemoryCustomID: memoryCustomID,
	})
	if err != nil {
		return Pattern{}, MemoryMirrorEvent{}, err
	}
	pattern, err := patternFromSQLC(row.ID, row.InstallationID, row.RepoID, row.Content, row.MemoryDocID, row.CreatedBy, row.Source, row.Category, row.PRNumber, row.CreatedAt, row.UpdatedAt)
	if err != nil {
		return Pattern{}, MemoryMirrorEvent{}, err
	}
	pattern.MemoryCustomID = memoryCustomID
	payload, err := newPatternOutboxPayload(pattern, customID, repo, mirrorExtra)
	if err != nil {
		return Pattern{}, MemoryMirrorEvent{}, err
	}
	return pattern, MemoryMirrorEvent{
		InstallationID: installationID, AggregateType: MemoryMirrorPattern, AggregateID: pattern.ID,
		Operation: MemoryMirrorUpsert, Payload: payload,
	}, nil
}

// identityQuerier is satisfied by *pgxpool.Pool and pgx.Tx.
type identityQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// findPatternIDByIdentity returns the LOWEST pattern id carrying the identity —
// deterministic when legacy siblings exist — or pgx.ErrNoRows.
func findPatternIDByIdentity(ctx context.Context, q identityQuerier, installationID int64, identity string) (int64, error) {
	var id *int64
	if err := q.QueryRow(ctx, `SELECT min(id) FROM patterns WHERE installation_id = $1 AND `+patternIdentityExpr+` = $2`, installationID, identity).Scan(&id); err != nil {
		return 0, err
	}
	if id == nil {
		return 0, pgx.ErrNoRows
	}
	return *id, nil
}

// FindPatternIDByIdentity is the read-only exact-duplicate check callers run
// before any similarity work. CreateOrGetPattern repeats it under the lock.
func (s *Store) FindPatternIDByIdentity(ctx context.Context, installationID int64, identity string) (storeResult0 int64, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "FindPatternIDByIdentity",

			"installation_id",

			storeLogValue(installationID), "identity", storeLogValue(identity))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr,
			storeResult0)
	}()

	return findPatternIDByIdentity(ctx, s.Pool, installationID, identity)
}

// CreateOrGetPattern is the manual-creation path (MCP and dashboard). Under
// the per-identity advisory lock it rechecks for an existing row and returns
// it untouched — no upsert, no second outbox event, no author or content
// rewrite, nothing that could revive a retired memory — or inserts a new row
// and its mirror event. Two callers racing on an absent identity produce one
// row and one event. No network work happens while the lock is held.
func (s *Store) CreateOrGetPattern(ctx context.Context, installationID int64, repoID *int64, content string, createdBy *string, source *string, category *string, memoryCustomID string, mirrorExtra map[string]string) (storeResult0 *Pattern, storeResult1 bool, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "CreateOrGetPattern",

			"installation_id",

			storeLogValue(installationID), "repo_id", storeLogValue(repoID), "source", storeLogValue(source),
			"memory_custom_id", storeLogValue(memoryCustomID))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered, storeResult0, storeResult1)
			panic(recovered)
		}
		storeFinish(storeErr,
			storeResult0, storeResult1)
	}()

	if memoryCustomID == "" {
		return nil, false, fmt.Errorf("creating pattern requires a deterministic memory identity")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("begin create-or-get transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := lockMemoryMirrorCustomID(ctx, tx, installationID, memoryCustomID); err != nil {
		return nil, false, err
	}
	existingID, err := findPatternIDByIdentity(ctx, tx, installationID, memoryCustomID)
	if err == nil {
		// Read the row INSIDE the transaction that found it. Reading after
		// the commit reopens the window this function exists to close: a
		// concurrent DeletePattern between commit and read turns a
		// successful get-or-create into an internal pgx.ErrNoRows the caller
		// has no way to interpret. The identity lock still held here makes
		// the id and the row one consistent observation.
		row, err := db.New(tx).GetPattern(ctx, existingID)
		if err != nil {
			return nil, false, err
		}
		existing, err := patternFromSQLC(row.ID, row.InstallationID, row.RepoID, row.Content, row.MemoryDocID, row.CreatedBy, row.Source, row.Category, row.PRNumber, row.CreatedAt, row.UpdatedAt)
		if err != nil {
			return nil, false, err
		}
		existing.MemoryCustomID = row.MemoryCustomID
		if err := tx.Commit(ctx); err != nil { // nothing written; releases the lock
			return nil, false, fmt.Errorf("commit create-or-get transaction: %w", err)
		}
		return &existing, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, fmt.Errorf("checking pattern identity: %w", err)
	}

	pattern, event, err := createPatternTx(ctx, tx, installationID, repoID, content, nil, createdBy, source, category, nil, &memoryCustomID, mirrorExtra)
	if err != nil {
		return nil, false, err
	}
	if err := enqueueMemoryMirrorEvent(ctx, tx, event); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, fmt.Errorf("commit create-or-get transaction: %w", err)
	}
	return &pattern, true, nil
}

func (s *Store) DeletePattern(ctx context.Context, id int64, installationIDs []int64) (storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "DeletePattern",

			"id",
			storeLogValue(id), "installation_ids_count", len(installationIDs))
	defer func() {
		if recovered := recover(); recovered !=
			nil {
			storeFinishPanic(storeFinish,
				recovered)
			panic(recovered)
		}
		storeFinish(storeErr)
	}()

	return s.WithMemoryMirrorTx(ctx, func(tx pgx.Tx) (MemoryMirrorEvent, error) {
		if err := lockPatternIdentityForWrite(ctx, tx, id, installationIDs); err != nil {
			return MemoryMirrorEvent{}, err
		}
		_, event, err := deletePatternTx(ctx, tx, id, installationIDs)
		return event, err
	})
}

// lockPatternIdentityForWrite takes the per-identity producer key for one
// pattern row BEFORE anything locks or writes that row — the invariant on
// lockMemoryMirrorCustomID. It reads the identity with a plain SELECT, which
// takes no tuple lock, so it cannot invert the order it exists to preserve. The
// row's identity is immutable (CreatePattern's upsert conflicts on
// memory_custom_id and never rewrites it), so the value read here is the one a
// tombstone will carry. Legacy rows with neither identity column need no key.
func lockPatternIdentityForWrite(ctx context.Context, tx pgx.Tx, id int64, installationIDs []int64) error {
	var installationID int64
	var identity *string
	// patterns.id is int4. The ::bigint cast is what makes an out-of-range id a
	// plain no-match instead of a pgx encode failure — callers take ids from
	// untrusted input and must get "not found", not an internal error.
	err := tx.QueryRow(ctx,
		`SELECT installation_id, `+patternIdentityExpr+` FROM patterns WHERE id = $1::bigint AND installation_id = ANY($2::bigint[])`,
		id, installationIDs).Scan(&installationID, &identity)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrPatternNotFound
	}
	if err != nil {
		return fmt.Errorf("read pattern identity: %w", err)
	}
	if identity == nil || *identity == "" {
		return nil
	}
	return lockMemoryMirrorCustomID(ctx, tx, installationID, *identity)
}

// IsHumanAuthoredSource reports whether a pattern source was written by a
// person (dashboard or /argus remember) rather than learned by the pipeline.
// Guarded delete and retire refuse other sources without acknowledgment.
func IsHumanAuthoredSource(source string) bool {
	return source == "manual" || source == "remember_command"
}

var (
	ErrPatternNotFound        = errors.New("pattern not found")
	ErrPipelineLearnedPattern = errors.New("pattern was learned by the review pipeline")
)

// PatternDeletion is what DeletePatternGuarded observed inside its
// transaction. SiblingsAtDelete counts the OTHER rows still carrying the same
// identity after the delete; it is a snapshot, not the mirror worker's later
// decision — a concurrent writer can change ownership before the worker runs.
type PatternDeletion struct {
	CustomID         string
	Source           string
	SiblingsAtDelete int
}

// deletePatternTx is DeletePattern's delete + tombstone event inside a
// caller's transaction; shared with DeletePatternGuarded. Callers must already
// hold the row's identity key (lockPatternIdentityForWrite).
func deletePatternTx(ctx context.Context, tx pgx.Tx, id int64, installationIDs []int64) (db.DeletePatternRow, MemoryMirrorEvent, error) {
	q := db.New(tx)
	row, err := q.DeletePattern(ctx, db.DeletePatternParams{ID: id, InstallationIds: installationIDs})
	if errors.Is(err, pgx.ErrNoRows) {
		return row, MemoryMirrorEvent{}, ErrPatternNotFound
	}
	if err != nil {
		return row, MemoryMirrorEvent{}, err
	}

	repo := ""
	if row.RepoID != nil {
		repoRow, err := q.GetRepoScoped(ctx, db.GetRepoScopedParams{ID: *row.RepoID, Column2: []int64{row.InstallationID}})
		if err != nil {
			return row, MemoryMirrorEvent{}, fmt.Errorf("resolve deleted pattern repo: %w", err)
		}
		_, repo, _ = strings.Cut(repoRow.FullName, "/")
		if repo == "" {
			return row, MemoryMirrorEvent{}, fmt.Errorf("repo %d has invalid full name", *row.RepoID)
		}
	}

	// Legacy rows can predate both identity columns. Keep the full pattern
	// projection in the tombstone so the memory worker can reconstruct the
	// deterministic ID after the relational row is gone.
	customID := firstNonEmpty(row.MemoryCustomID, row.MemoryDocID)
	pattern := Pattern{
		ID: id, InstallationID: row.InstallationID, RepoID: row.RepoID,
		Content: row.Content, Source: row.Source, Category: row.Category,
		PRNumber: row.PRNumber,
	}
	payload, err := newPatternOutboxPayload(pattern, customID, repo, nil)
	if err != nil {
		return row, MemoryMirrorEvent{}, err
	}
	return row, MemoryMirrorEvent{
		InstallationID: row.InstallationID,
		AggregateType:  MemoryMirrorPattern,
		AggregateID:    id,
		Operation:      MemoryMirrorDelete,
		Payload:        payload,
	}, nil
}

// DeletePatternGuarded deletes one pattern row after checking, inside the same
// transaction and against the locked row, that its source is human-authored or
// the caller acknowledged deleting pipeline-learned knowledge. A refused delete
// rolls back. The result carries the identity and the sibling snapshot so the
// caller can say whether the memory is expected to survive this delete.
func (s *Store) DeletePatternGuarded(ctx context.Context, id int64, installationIDs []int64, allowPipelineLearned bool) (storeResult0 PatternDeletion, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "DeletePatternGuarded",

			"id",
			storeLogValue(id), "installation_ids_count", len(installationIDs), "allow_pipeline_learned", allowPipelineLearned)
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

	var result PatternDeletion
	err := s.WithMemoryMirrorTx(ctx, func(tx pgx.Tx) (MemoryMirrorEvent, error) {
		// Identity key first, then the row lock below, then the delete. Locking
		// the row before the key would invert the order every other producer
		// uses and deadlock against them (see lockMemoryMirrorCustomID).
		if err := lockPatternIdentityForWrite(ctx, tx, id, installationIDs); err != nil {
			return MemoryMirrorEvent{}, err
		}
		// The guard runs on THIS read, under a tuple lock. source is mutable —
		// CreatePattern's upsert rewrites it — so an earlier unlocked read, in
		// this transaction or in the handler, is not what is about to be
		// deleted. The row can also vanish between the two reads.
		var source string
		err := tx.QueryRow(ctx,
			`SELECT COALESCE(source, 'manual') FROM patterns WHERE id = $1::bigint AND installation_id = ANY($2::bigint[]) FOR UPDATE`,
			id, installationIDs).Scan(&source)
		if errors.Is(err, pgx.ErrNoRows) {
			return MemoryMirrorEvent{}, ErrPatternNotFound
		}
		if err != nil {
			return MemoryMirrorEvent{}, fmt.Errorf("locking pattern for delete: %w", err)
		}
		if !IsHumanAuthoredSource(source) && !allowPipelineLearned {
			return MemoryMirrorEvent{}, ErrPipelineLearnedPattern
		}

		row, event, err := deletePatternTx(ctx, tx, id, installationIDs)
		if err != nil {
			return MemoryMirrorEvent{}, err
		}

		// Siblings are counted after the delete and inside the same
		// transaction, so this row is already excluded and the identity key
		// still keeps concurrent producers out.
		identity := firstNonEmpty(row.MemoryCustomID, row.MemoryDocID)
		var siblings int
		if identity != "" {
			if err := tx.QueryRow(ctx,
				`SELECT count(*) FROM patterns WHERE installation_id = $1 AND `+patternIdentityExpr+` = $2`,
				row.InstallationID, identity).Scan(&siblings); err != nil {
				return MemoryMirrorEvent{}, fmt.Errorf("counting sibling patterns: %w", err)
			}
		}
		result = PatternDeletion{CustomID: identity, Source: row.Source, SiblingsAtDelete: siblings}
		return event, nil
	})
	if err != nil {
		return PatternDeletion{}, err
	}
	return result, nil
}

func firstNonEmpty(values ...*string) string {
	for _, value := range values {
		if value != nil && *value != "" {
			return *value
		}
	}
	return ""
}

func newPatternOutboxPayload(pattern Pattern, customID, repo string, extra map[string]string) (json.RawMessage, error) {
	payload := struct {
		CustomID string `json:"custom_id"`
		Repo     string `json:"repo,omitempty"`
		Shared   bool   `json:"shared,omitempty"`
		Pattern  struct {
			Content  string
			CustomID string
			Source   string
			Category string
			PRNumber int
			Extra    map[string]string
		} `json:"pattern"`
	}{CustomID: customID, Repo: repo, Shared: pattern.RepoID == nil}
	payload.Pattern.Content = pattern.Content
	payload.Pattern.CustomID = customID
	payload.Pattern.Source = pattern.Source
	payload.Pattern.Extra = extra
	if pattern.Category != nil {
		payload.Pattern.Category = *pattern.Category
	}
	if pattern.PRNumber != nil {
		payload.Pattern.PRNumber = *pattern.PRNumber
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal pattern mirror payload: %w", err)
	}
	return raw, nil
}

func (s *Store) GetPattern(ctx context.Context, id int64) (storeResult0 *Pattern, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "GetPattern",

			"id",
			storeLogValue(id))
	defer func() {
		if recovered := recover(); recovered !=
			nil {
			storeFinishPanic(storeFinish, recovered,
				storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

	row, err := s.q.GetPattern(ctx, id)
	if err != nil {
		return nil, err
	}
	pattern, err := patternFromSQLC(row.ID, row.InstallationID, row.RepoID, row.Content, row.MemoryDocID, row.CreatedBy, row.Source, row.Category, row.PRNumber, row.CreatedAt, row.UpdatedAt)
	if err != nil {
		return nil, err
	}
	pattern.MemoryCustomID = row.MemoryCustomID
	return &pattern, nil
}

// GetPatternIDByMemoryDocID maps a memory pattern doc id back to its
// patterns-table row id. SearchPatternMatch returns memory docs; callers
// use this to persist review_comments.matched_pattern_id and to bump
// pattern_stats. Returns (0, pgx.ErrNoRows) when no patterns row carries that
// memory_doc_id (e.g. a synthesis/convention doc that was never mirrored to
// the patterns table) — a miss, not a failure.
// Scoped by installation: the id is a deterministic customId, not a globally
// unique server id, so two installations that learned the same pattern in
// same-named repos hold
// the SAME string here, and an unscoped LIMIT 1 with no ORDER BY can resolve
// one tenant's hit to another tenant's row — persisting a foreign
// matched_pattern_id and bumping its stats.
func (s *Store) GetPatternIDByMemoryDocID(ctx context.Context, installationID int64, memoryDocID string) (storeResult0 int64, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "GetPatternIDByMemoryDocID",

			"installation_id", storeLogValue(installationID), "memory_doc_id",
			storeLogValue(
				memoryDocID,
			))
	defer func() {
		if recovered := recover(); recovered !=
			nil {
			storeFinishPanic(storeFinish, recovered,
				storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr, storeResult0)
	}()

	var id int64
	err := s.Pool.QueryRow(ctx,
		`SELECT id FROM patterns WHERE installation_id = $1 AND memory_doc_id = $2 LIMIT 1`,
		installationID, memoryDocID).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}

// GetPatternIDByCustomID maps a pattern doc's deterministic customId
// back to its patterns-table row id. The per-finding enrich read prefers this
// over GetPatternIDByMemoryDocID because a hybrid-search hit's own ID may be a
// chunk id that never matches the stored memory_doc_id, whereas the customId is
// mirrored into result metadata at write time. Returns (0, pgx.ErrNoRows) when
// no row carries that customId (legacy rows written before the mirror column, or
// docs never mirrored to the patterns table) — a miss, not a failure.
// Scoped by installation for the same reason as the sibling above: customIds
// are deterministic per repo+content, so they were never globally unique.
func (s *Store) GetPatternIDByCustomID(ctx context.Context, installationID int64, customID string) (storeResult0 int64, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "GetPatternIDByCustomID",

			"installation_id",

			storeLogValue(installationID), "custom_id",
			storeLogValue(customID),
		)
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr,
			storeResult0)
	}()

	var id int64
	err := s.Pool.QueryRow(ctx,
		`SELECT id FROM patterns WHERE installation_id = $1 AND memory_custom_id = $2 LIMIT 1`,
		installationID, customID).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}

func (s *Store) GetPatternStats(ctx context.Context, installationIDs []int64) (storeResult0 []PatternStat, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "GetPatternStats",

			"installation_ids_count",

			len(installationIDs))
	defer func() {
		if recovered := recover(); recovered !=
			nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr,
			storeResult0)
	}()

	rows, err := s.q.GetPatternStats(ctx, installationIDs)
	if err != nil {
		return nil, err
	}
	stats := make([]PatternStat, 0, len(rows))
	for _, row := range rows {
		stats = append(stats, PatternStat{Week: row.Week, Source: row.Source, Count: row.Count})
	}
	return stats, nil
}

// patternIdentityExpr is the effective memory identity of a patterns row:
// the deterministic custom id for every row written since the custom-id
// migration, the legacy memory doc id otherwise. It matches the precedence
// firstNonEmpty(row.MemoryCustomID, row.MemoryDocID) uses in DeletePattern's
// tombstone, so every sibling query in the store agrees on identity.
const patternIdentityExpr = "COALESCE(NULLIF(memory_custom_id, ''), memory_doc_id)"

// ListPatternIDsByIdentity maps each memory identity to EVERY patterns row in
// the installation that carries it, ordered by id. Search returns identities
// (custom ids); delete_memory takes pattern ids; this is the bridge. Multiple
// siblings can share one identity, so GetPatternIDByCustomID's LIMIT 1 is the
// wrong tool here. Identities with no row are absent from the map.
func (s *Store) ListPatternIDsByIdentity(ctx context.Context, installationID int64, identities []string) (storeResult0 map[string][]int64, storeErr error) {
	storeFinish :=
		beginStoreOperation(ctx, "ListPatternIDsByIdentity",

			"installation_id",

			storeLogValue(installationID), "identity_count", len(identities))
	defer func() {
		if recovered := recover(); recovered != nil {
			storeFinishPanic(storeFinish, recovered, storeResult0)
			panic(recovered)
		}
		storeFinish(storeErr,
			storeResult0)
	}()

	out := map[string][]int64{}
	if len(identities) == 0 {
		return out, nil
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT `+patternIdentityExpr+` AS identity, id
		FROM patterns
		WHERE installation_id = $1 AND `+patternIdentityExpr+` = ANY($2::text[])
		ORDER BY id`, installationID, identities)
	if err != nil {
		return nil, fmt.Errorf("listing pattern ids by identity: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var identity string
		var id int64
		if err := rows.Scan(&identity, &id); err != nil {
			return nil, fmt.Errorf("scanning pattern identity: %w", err)
		}
		out[identity] = append(out[identity], id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing pattern ids by identity: %w", err)
	}
	return out, nil
}
