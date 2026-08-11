package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"

	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/internal/store"
)

// DefaultFullIndexFileCap bounds one full-repo index.
//
// Each window fetches, parses, and stages at most this many files. Parsed facts
// are persisted between continuations, so the live graph remains unchanged
// until the final atomic publish and file bodies never accumulate in memory.
//
// The cap also bounds GitHub cost: each source file costs one call and can fall
// back to a second blob call, so a window can consume at most 40 file-content calls (GetFileContent can fall back to a
// second blob call). Fleet cadence is separately persisted by the scheduler.
const DefaultFullIndexFileCap = 20

func boundedFullIndexFileCap(requested int) int {
	if requested <= 0 || requested > DefaultFullIndexFileCap {
		return DefaultFullIndexFileCap
	}
	return requested
}

// filterSourceFiles keeps only the entries a parser can do something with.
//
// A real repository's tree is mostly not code. Every non-source entry that got
// through would cost a GitHub fetch and parse to nothing.
func filterSourceFiles(entries []string) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if sourceExts[strings.ToLower(filepath.Ext(e))] {
			out = append(out, e)
		}
	}
	return out
}

// selectPendingFiles returns the deterministic next window of source files that
// do not yet have a ready staged snapshot. Transient failures remain pending,
// so a continuation retries them.
func selectPendingFiles(files []string, ready map[string]struct{}, cap int) (selected []string, remaining int) {
	pending := make([]string, 0, len(files)-len(ready))
	for _, filePath := range files {
		if _, ok := ready[filePath]; !ok {
			pending = append(pending, filePath)
		}
	}
	sort.Strings(pending)
	if cap <= 0 || len(pending) <= cap {
		return pending, 0
	}
	return pending[:cap], len(pending) - cap
}

// ErrTruncatedTree means GitHub explicitly reported that its recursive tree is
// incomplete. The generation is recorded as failed and never published.
var ErrTruncatedTree = errors.New("github repository tree was truncated")

// FullIndexResult reports one bounded staging window and whether it published.
type FullIndexResult struct {
	Snapshot  store.GraphSnapshot
	Staged    int
	Remaining int
	Published bool
	Unchanged bool
}

type fullIndexGitHub interface {
	ResolveDefaultBranchCommit(context.Context, int64, string, string, string) (string, error)
	GetRepoTree(context.Context, int64, string, string, string) (ghpkg.RepoTree, error)
	GetFileContent(context.Context, int64, string, string, string, string) (string, error)
}

// IndexRepoBounded stages one deterministic window at an immutable commit and
// atomically publishes only after every source file has a ready snapshot.
func IndexRepoBounded(
	ctx context.Context,
	st *store.Store,
	ghClient fullIndexGitHub,
	installationID int64,
	owner, repo, defaultBranch string,
	repoDBID int64,
	fileCap int,
	_ int,
) (FullIndexResult, error) {
	fileCap = boundedFullIndexFileCap(fileCap)
	// A bounded continuation must finish the snapshot it started. Resolving the
	// mutable branch again here would restart a large repository every time a
	// commit landed between windows, so an otherwise healthy busy repo might
	// never publish a complete graph.
	snapshot, err := st.GetGraphSnapshot(ctx, repoDBID)
	if err != nil {
		return FullIndexResult{}, err
	}
	commitSHA := snapshot.CommitSHA
	refreshVersion := snapshot.GenerationRefreshVersion
	if snapshot.Status != "building" || commitSHA == "" {
		refreshVersion = snapshot.RefreshVersion
		commitSHA, err = ghClient.ResolveDefaultBranchCommit(ctx, installationID, owner, repo, defaultBranch)
		if err != nil {
			return FullIndexResult{}, fmt.Errorf("resolve default branch: %w", err)
		}
		alreadyCurrent, _, err := st.ConfirmGraphDefaultHead(ctx, repoDBID, refreshVersion, commitSHA)
		if err != nil {
			return FullIndexResult{}, err
		}
		if alreadyCurrent {
			snapshot, err = st.GetGraphSnapshot(ctx, repoDBID)
			return FullIndexResult{Snapshot: snapshot, Unchanged: true}, err
		}
		// Persist the immutable head before fetching its tree. A permanent tree
		// failure can then terminate this generation and participate in the
		// head-scoped retry backoff; a transient failure leaves it resumable.
		snapshot, err = st.BeginGraphGeneration(ctx, repoDBID, commitSHA, 0, 0, false, refreshVersion)
		if err != nil {
			return FullIndexResult{}, err
		}
	}
	tree, err := ghClient.GetRepoTree(ctx, installationID, owner, repo, commitSHA)
	if err != nil {
		if snapshot.Status == "building" && ghpkg.IsPermanentGitObjectError(err) {
			if failErr := st.FailGraphGeneration(ctx, repoDBID, snapshot.GenerationID, err); failErr != nil {
				return FullIndexResult{Snapshot: snapshot}, errors.Join(err, failErr)
			}
			snapshot, failErr := st.GetGraphSnapshot(ctx, repoDBID)
			if failErr != nil {
				return FullIndexResult{}, errors.Join(err, failErr)
			}
			return FullIndexResult{Snapshot: snapshot}, err
		}
		return FullIndexResult{Snapshot: snapshot}, err
	}
	sourceFiles := filterSourceFiles(tree.Paths)
	sort.Strings(sourceFiles)
	snapshot, err = st.BeginGraphGeneration(ctx, repoDBID, commitSHA, len(sourceFiles), len(tree.Paths)-len(sourceFiles), tree.Truncated, refreshVersion)
	if err != nil {
		return FullIndexResult{}, err
	}
	result := FullIndexResult{Snapshot: snapshot}
	if tree.Truncated {
		return result, ErrTruncatedTree
	}
	if limitErr := defaultGraphGenerationPublishLimits.validate(graphGenerationStats{Files: int64(len(sourceFiles))}); limitErr != nil {
		if failErr := st.FailGraphGeneration(ctx, repoDBID, snapshot.GenerationID, limitErr); failErr != nil {
			return result, errors.Join(limitErr, failErr)
		}
		result.Snapshot, err = st.GetGraphSnapshot(ctx, repoDBID)
		if err != nil {
			return result, errors.Join(limitErr, err)
		}
		return result, limitErr
	}

	ready, err := st.ListReadyGraphGenerationPaths(ctx, snapshot.GenerationID)
	if err != nil {
		return result, err
	}
	pending, _ := selectPendingFiles(sourceFiles, ready, fileCap)

	for _, filePath := range pending {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		content, fetchErr := ghClient.GetFileContent(ctx, installationID, owner, repo, filePath, commitSHA)
		if fetchErr != nil {
			permanent := ghpkg.IsPermanentGitObjectError(fetchErr)
			snapshot, err = st.StageGraphGenerationFile(ctx, repoDBID, snapshot.GenerationID, filePath, nil, nil, nil, fetchErr, permanent)
			if err != nil {
				return result, err
			}
			result.Snapshot = snapshot
			result.Staged++
			if permanent {
				// A published generation must contain a ready snapshot for every
				// source file. Fail this immutable generation immediately and leave
				// the previous published projection untouched.
				result.Remaining = snapshot.ExpectedFiles - (snapshot.VisitedFiles - snapshot.FailedFiles)
				return result, nil
			}
			continue
		}
		symbols, edges := ParseFileSymbols(filePath, content)
		// Endpoint ownership is grounded in declarations, not the broad file
		// span. Add the file identity only after anchoring so a declaration-free
		// route stays explicitly unanchored rather than gaining a false handler.
		endpoints := anchorEndpoints(ExtractAPIEndpoints(filePath, content), symbols)
		// Every source file gets a deterministic identity/LOC node, including
		// files with no declarations. Metrics and import edges must not depend on
		// whichever declaration happened to be parsed first.
		symbols = append(symbols, fileSymbol(filePath, content))
		symbolJSON, err := json.Marshal(symbols)
		if err != nil {
			return result, fmt.Errorf("marshal symbols for %s: %w", filePath, err)
		}
		edgeJSON, err := json.Marshal(edges)
		if err != nil {
			return result, fmt.Errorf("marshal edges for %s: %w", filePath, err)
		}
		endpointJSON, err := json.Marshal(endpoints)
		if err != nil {
			return result, fmt.Errorf("marshal endpoints for %s: %w", filePath, err)
		}
		snapshot, err = st.StageGraphGenerationFile(ctx, repoDBID, snapshot.GenerationID, filePath, symbolJSON, edgeJSON, endpointJSON, nil, false)
		if err != nil {
			return result, err
		}
		result.Snapshot = snapshot
		result.Staged++
	}

	result.Remaining = snapshot.ExpectedFiles - (snapshot.VisitedFiles - snapshot.FailedFiles)
	if result.Remaining < 0 {
		result.Remaining = 0
	}
	if snapshot.VisitedFiles != snapshot.ExpectedFiles || snapshot.FailedFiles != 0 {
		return result, nil
	}
	if err := publishGraphGeneration(ctx, st, repoDBID, snapshot.GenerationID); err != nil {
		if errors.Is(err, ErrGraphGenerationResourceLimit) {
			if failed, snapshotErr := st.GetGraphSnapshot(ctx, repoDBID); snapshotErr == nil {
				result.Snapshot = failed
			} else {
				return result, errors.Join(err, snapshotErr)
			}
		}
		return result, err
	}
	result.Snapshot, err = st.GetGraphSnapshot(ctx, repoDBID)
	if err != nil {
		return result, err
	}
	result.Published = true
	result.Remaining = 0
	return result, nil
}

// IndexRepo performs one safely capped authoritative staging window.
func IndexRepo(ctx context.Context, st *store.Store, ghClient *ghpkg.Client, installationID int64, owner, repo, defaultBranch string, repoDBID int64) error {
	_, err := IndexRepoBounded(ctx, st, ghClient, installationID, owner, repo, defaultBranch, repoDBID, 0, 0)
	return err
}

// Publication limits are deliberately below the 1 GiB app memory and the
// 25-minute worker transaction budget. JSONBytes measures expanded JSON text,
// not TOAST-compressed storage. The global maps retain only symbol identities;
// staged facts are decoded one file at a time.
var defaultGraphGenerationPublishLimits = graphGenerationPublishLimits{
	JSONBytes:       32 << 20,
	Files:           25_000,
	Symbols:         100_000,
	Edges:           150_000,
	Endpoints:       50_000,
	ResolutionNodes: 250_000,
	PublishedEdges:  500_000,
}

// ErrGraphGenerationResourceLimit marks an immutable generation as too large
// to publish safely. It is terminal for that commit and participates in the
// same head-scoped backoff as other permanent generation failures.
var ErrGraphGenerationResourceLimit = errors.New("graph generation exceeds publication resource limits")

type graphGenerationStats struct {
	JSONBytes int64
	Files     int64
	Symbols   int64
	Edges     int64
	Endpoints int64
}

type graphGenerationPublishLimits struct {
	JSONBytes       int64
	Files           int64
	Symbols         int64
	Edges           int64
	Endpoints       int64
	ResolutionNodes int64
	PublishedEdges  int64
}

func (l graphGenerationPublishLimits) validate(s graphGenerationStats) error {
	var dimension string
	var got, limit int64
	switch {
	case s.JSONBytes > l.JSONBytes:
		dimension, got, limit = "expanded JSON bytes", s.JSONBytes, l.JSONBytes
	case s.Files > l.Files:
		dimension, got, limit = "files", s.Files, l.Files
	case s.Symbols > l.Symbols:
		dimension, got, limit = "symbols", s.Symbols, l.Symbols
	case s.Edges > l.Edges:
		dimension, got, limit = "edges", s.Edges, l.Edges
	case s.Endpoints > l.Endpoints:
		dimension, got, limit = "endpoints", s.Endpoints, l.Endpoints
	default:
		return nil
	}
	return fmt.Errorf("%w: %s=%d limit=%d", ErrGraphGenerationResourceLimit, dimension, got, limit)
}

type stagedGraphFile struct {
	FilePath  string
	Symbols   json.RawMessage
	Edges     json.RawMessage
	Endpoints json.RawMessage
}

// forEachStagedGraphFile uses a server-side cursor. FETCH returns exactly one
// staged row, and fn must finish with it before the next fetch, so arbitrary
// generation JSON is never materialized in the app.
func forEachStagedGraphFile(ctx context.Context, tx pgx.Tx, generationID int64, cursor string, fn func(stagedGraphFile) error) error {
	cursorIdentifier := pgx.Identifier{cursor}.Sanitize()
	if _, err := tx.Exec(ctx, fmt.Sprintf(`DECLARE %s NO SCROLL CURSOR FOR
		SELECT file_path, symbols, edges, endpoints
		FROM graph_index_generation_files
		WHERE generation_id = %d AND status = 'ready' ORDER BY file_path`, cursorIdentifier, generationID)); err != nil {
		return fmt.Errorf("declare staged graph cursor: %w", err)
	}
	defer func() { _, _ = tx.Exec(ctx, "CLOSE "+cursorIdentifier) }()
	for {
		var file stagedGraphFile
		err := tx.QueryRow(ctx, "FETCH NEXT FROM "+cursorIdentifier).Scan(&file.FilePath, &file.Symbols, &file.Edges, &file.Endpoints)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("fetch staged graph file: %w", err)
		}
		if err := fn(file); err != nil {
			return err
		}
	}
}

func commitGraphPublishPreflightFailure(ctx context.Context, tx pgx.Tx, repoID, generationID int64, limitErr error) error {
	tag, err := tx.Exec(ctx, `UPDATE graph_index_generations
		SET status = 'failed', error = $3, updated_at = NOW()
		WHERE id = $1 AND repo_id = $2 AND status = 'building'`, generationID, repoID, limitErr.Error())
	if err != nil {
		return errors.Join(limitErr, fmt.Errorf("fail oversized graph generation: %w", err))
	}
	if tag.RowsAffected() != 1 {
		return errors.Join(limitErr, fmt.Errorf("fail oversized graph generation: generation is no longer building"))
	}
	if _, err := tx.Exec(ctx, `DELETE FROM graph_index_generation_files WHERE generation_id = $1`, generationID); err != nil {
		return errors.Join(limitErr, fmt.Errorf("clean oversized graph generation payloads: %w", err))
	}
	if err := tx.Commit(ctx); err != nil {
		return errors.Join(limitErr, fmt.Errorf("commit oversized graph failure: %w", err))
	}
	return limitErr
}

func failGraphPublishLimit(ctx context.Context, st *store.Store, tx pgx.Tx, repoID, generationID int64, limitErr error) error {
	if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
		return errors.Join(limitErr, fmt.Errorf("rollback oversized graph generation: %w", rollbackErr))
	}
	if failErr := st.FailGraphGeneration(ctx, repoID, generationID, limitErr); failErr != nil {
		return errors.Join(limitErr, failErr)
	}
	return limitErr
}

func publishGraphGeneration(ctx context.Context, st *store.Store, repoID, generationID int64) error {
	tx, err := st.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("publish graph generation: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var expected, visited, failed, unavailable int
	var ready int64
	var status string
	var arraysValid bool
	var stats graphGenerationStats
	if err := tx.QueryRow(ctx, `
		SELECT g.status, g.expected_files, g.visited_files, g.failed_files,
		       COALESCE(f.ready, 0), COALESCE(f.unavailable, 0),
		       COALESCE(f.arrays_valid, true), COALESCE(f.json_bytes, 0),
		       COALESCE(f.symbols, 0), COALESCE(f.edges, 0), COALESCE(f.endpoints, 0)
		FROM graph_index_generations g
		LEFT JOIN LATERAL (
		  SELECT count(*) FILTER (WHERE status = 'ready')::bigint AS ready,
		         count(*) FILTER (WHERE status = 'unavailable')::int AS unavailable,
		         bool_and(jsonb_typeof(symbols) IN ('array', 'null') AND jsonb_typeof(edges) IN ('array', 'null')
		           AND jsonb_typeof(endpoints) IN ('array', 'null')) FILTER (WHERE status = 'ready') AS arrays_valid,
		         sum(octet_length(file_path)::bigint + octet_length(symbols::text)::bigint +
		           octet_length(edges::text)::bigint + octet_length(endpoints::text)::bigint) FILTER (WHERE status = 'ready')::bigint AS json_bytes,
		         sum(CASE WHEN jsonb_typeof(symbols) = 'array' THEN jsonb_array_length(symbols) ELSE 0 END)
		           FILTER (WHERE status = 'ready')::bigint AS symbols,
		         sum(CASE WHEN jsonb_typeof(edges) = 'array' THEN jsonb_array_length(edges) ELSE 0 END)
		           FILTER (WHERE status = 'ready')::bigint AS edges,
		         sum(CASE WHEN jsonb_typeof(endpoints) = 'array' THEN jsonb_array_length(endpoints) ELSE 0 END)
		           FILTER (WHERE status = 'ready')::bigint AS endpoints
		  FROM graph_index_generation_files WHERE generation_id = g.id
		) f ON true
		WHERE g.id = $1 AND g.repo_id = $2 FOR UPDATE OF g`, generationID, repoID).Scan(
		&status, &expected, &visited, &failed, &ready, &unavailable, &arraysValid,
		&stats.JSONBytes, &stats.Symbols, &stats.Edges, &stats.Endpoints); err != nil {
		return fmt.Errorf("publish graph generation: validate: %w", err)
	}
	stats.Files = ready
	if status != "building" || visited != expected || failed != 0 || unavailable != 0 || ready != int64(expected) {
		return fmt.Errorf("publish graph generation: incomplete generation status=%s expected=%d visited=%d failed=%d ready=%d unavailable=%d", status, expected, visited, failed, ready, unavailable)
	}
	if !arraysValid {
		limitErr := fmt.Errorf("%w: staged facts must be JSON arrays or null", ErrGraphGenerationResourceLimit)
		return commitGraphPublishPreflightFailure(ctx, tx, repoID, generationID, limitErr)
	}
	if limitErr := defaultGraphGenerationPublishLimits.validate(stats); limitErr != nil {
		return commitGraphPublishPreflightFailure(ctx, tx, repoID, generationID, limitErr)
	}

	// This transaction is the visibility boundary. Any later limit or decoding
	// error rolls the deletes and inserts back before the generation is failed.
	if _, err := tx.Exec(ctx, `DELETE FROM code_nodes WHERE repo_id = $1`, repoID); err != nil {
		return fmt.Errorf("publish graph generation: clear old graph: %w", err)
	}

	keyToID := make(map[string]int64, stats.Symbols)
	nameToIDs := make(map[string][]int64, stats.Symbols)
	rememberedIDs := make(map[int64]struct{}, stats.Symbols)
	rememberNode := func(filePath, name string, id int64) {
		key := nodeKey(filePath, name)
		if keyToID[key] == 0 {
			keyToID[key] = id
		}
		if _, exists := rememberedIDs[id]; exists {
			return
		}
		rememberedIDs[id] = struct{}{}
		nameToIDs[name] = append(nameToIDs[name], id)
	}
	if err := forEachStagedGraphFile(ctx, tx, generationID, "graph_symbols", func(file stagedGraphFile) error {
		var symbols []Symbol
		if err := json.Unmarshal(file.Symbols, &symbols); err != nil {
			return fmt.Errorf("publish graph generation: decode %s symbols: %w", file.FilePath, err)
		}
		const nodeBatchSize = 256
		for start := 0; start < len(symbols); start += nodeBatchSize {
			end := min(start+nodeBatchSize, len(symbols))
			batch := &pgx.Batch{}
			for _, sym := range symbols[start:end] {
				// The storage key predates receiver metadata, so two legal Go
				// methods can intentionally collapse to the first parser-order row.
				// The no-op update is required for PostgreSQL to return that row's ID.
				batch.Queue(`INSERT INTO code_nodes (repo_id, installation_id, kind, name, file_path, line_start, line_end, language,
				  return_type, params, visibility, is_async, receiver_type, scope, content_hash, updated_at)
				VALUES ($1, (SELECT installation_id FROM repos WHERE id = $1), $2, $3, $4, $5, $6, $7,
				  $8, $9, $10, $11, $12, $13, $14, NOW())
				ON CONFLICT (repo_id, file_path, kind, name) DO UPDATE
				SET updated_at = code_nodes.updated_at
				RETURNING id`, repoID, sym.Kind, sym.Name,
					sym.FilePath, sym.LineStart, sym.LineEnd, langForFile(sym.FilePath), sym.ReturnType,
					sym.Params, sym.Visibility, sym.IsAsync, sym.Receiver, sym.Scope, computeSymbolHash(sym))
			}
			results := tx.SendBatch(ctx, batch)
			for _, sym := range symbols[start:end] {
				var id int64
				if err := results.QueryRow().Scan(&id); err != nil {
					_ = results.Close()
					return fmt.Errorf("publish graph generation: insert node %s: %w", sym.Name, err)
				}
				rememberNode(sym.FilePath, sym.Name, id)
			}
			if err := results.Close(); err != nil {
				return fmt.Errorf("publish graph generation: close node batch: %w", err)
			}
		}
		return nil
	}); err != nil {
		return err
	}

	ensureSynthetic := func(filePath, kind, name string) (int64, error) {
		if id := keyToID[nodeKey(filePath, name)]; id != 0 {
			return id, nil
		}
		if int64(len(keyToID)) >= defaultGraphGenerationPublishLimits.ResolutionNodes {
			return 0, fmt.Errorf("%w: resolution nodes exceed limit=%d", ErrGraphGenerationResourceLimit, defaultGraphGenerationPublishLimits.ResolutionNodes)
		}
		var id int64
		if err := tx.QueryRow(ctx, `INSERT INTO code_nodes
			(repo_id, installation_id, kind, name, file_path, line_start, line_end, language, updated_at)
			VALUES ($1, (SELECT installation_id FROM repos WHERE id = $1), $2, $3, $4, 0, 0, '', NOW())
			RETURNING id`, repoID, kind, name, filePath).Scan(&id); err != nil {
			return 0, err
		}
		rememberNode(filePath, name, id)
		return id, nil
	}
	resolveOrPlaceholder := func(filePath, targetName string) (int64, error) {
		id, resolution := describeNodeResolution(filePath, targetName, keyToID, nameToIDs)
		if resolution == resolutionResolved {
			return id, nil
		}
		return ensureSynthetic(filePath, "module", string(resolution)+":"+targetName)
	}

	var edgeRows []store.CodeEdgeRow
	var publishedEdges int64
	flushEdges := func() error {
		if len(edgeRows) == 0 {
			return nil
		}
		batch := &pgx.Batch{}
		for _, edge := range edgeRows {
			batch.Queue(`INSERT INTO code_edges (repo_id, source_id, target_id, kind, inferred, updated_at)
				VALUES ($1, $2, $3, $4, false, NOW()) ON CONFLICT DO NOTHING`,
				repoID, edge.SourceID, edge.TargetID, edge.Kind)
		}
		results := tx.SendBatch(ctx, batch)
		for range edgeRows {
			if _, err := results.Exec(); err != nil {
				_ = results.Close()
				return fmt.Errorf("publish graph generation: insert edge: %w", err)
			}
		}
		if err := results.Close(); err != nil {
			return fmt.Errorf("publish graph generation: close edge batch: %w", err)
		}
		edgeRows = edgeRows[:0]
		return nil
	}
	appendEdge := func(sourceID, targetID int64, kind string) error {
		if sourceID == 0 || targetID == 0 || kind == "" {
			return nil
		}
		publishedEdges++
		if publishedEdges > defaultGraphGenerationPublishLimits.PublishedEdges {
			return fmt.Errorf("%w: published edges exceed limit=%d", ErrGraphGenerationResourceLimit, defaultGraphGenerationPublishLimits.PublishedEdges)
		}
		edgeRows = append(edgeRows, store.CodeEdgeRow{SourceID: sourceID, TargetID: targetID, Kind: kind})
		if len(edgeRows) == 256 {
			return flushEdges()
		}
		return nil
	}

	if err := forEachStagedGraphFile(ctx, tx, generationID, "graph_edges", func(file stagedGraphFile) error {
		var edges []Edge
		if err := json.Unmarshal(file.Edges, &edges); err != nil {
			return fmt.Errorf("publish graph generation: decode %s edges: %w", file.FilePath, err)
		}
		for _, edge := range edges {
			if edge.Kind == "imports" {
				sourceID := keyToID[nodeKey(file.FilePath, file.FilePath)]
				if sourceID == 0 || edge.TargetName == "" {
					continue
				}
				targetID, err := ensureSynthetic(file.FilePath, "module", "module:"+edge.TargetName)
				if err != nil {
					return fmt.Errorf("publish graph generation: insert module %s: %w", edge.TargetName, err)
				}
				if err := appendEdge(sourceID, targetID, edge.Kind); err != nil {
					return err
				}
				continue
			}
			sourceID := keyToID[nodeKey(file.FilePath, edge.SourceName)]
			if sourceID == 0 || edge.TargetName == "" {
				continue
			}
			targetID, err := resolveOrPlaceholder(file.FilePath, edge.TargetName)
			if err != nil {
				return fmt.Errorf("publish graph generation: resolve edge target: %w", err)
			}
			if err := appendEdge(sourceID, targetID, edge.Kind); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		if errors.Is(err, ErrGraphGenerationResourceLimit) {
			return failGraphPublishLimit(ctx, st, tx, repoID, generationID, err)
		}
		return err
	}

	if err := forEachStagedGraphFile(ctx, tx, generationID, "graph_types", func(file stagedGraphFile) error {
		var symbols []Symbol
		if err := json.Unmarshal(file.Symbols, &symbols); err != nil {
			return fmt.Errorf("publish graph generation: decode %s symbols for types: %w", file.FilePath, err)
		}
		for _, sym := range symbols {
			sourceID := keyToID[nodeKey(file.FilePath, sym.Name)]
			if sourceID == 0 {
				continue
			}
			for _, expression := range []string{sym.ReturnType, sym.Params} {
				for _, typeName := range extractTypeNames(expression) {
					if typeName == sym.Name {
						continue
					}
					targetID, err := resolveOrPlaceholder(file.FilePath, typeName)
					if err != nil {
						return fmt.Errorf("publish graph generation: resolve type target: %w", err)
					}
					if err := appendEdge(sourceID, targetID, "uses_type"); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}); err != nil {
		if errors.Is(err, ErrGraphGenerationResourceLimit) {
			return failGraphPublishLimit(ctx, st, tx, repoID, generationID, err)
		}
		return err
	}
	if err := flushEdges(); err != nil {
		return err
	}

	if err := forEachStagedGraphFile(ctx, tx, generationID, "graph_endpoints", func(file stagedGraphFile) error {
		var endpoints []APIEndpoint
		if err := json.Unmarshal(file.Endpoints, &endpoints); err != nil {
			return fmt.Errorf("publish graph generation: decode %s endpoints: %w", file.FilePath, err)
		}
		const endpointBatchSize = 256
		for start := 0; start < len(endpoints); start += endpointBatchSize {
			end := min(start+endpointBatchSize, len(endpoints))
			batch := &pgx.Batch{}
			for _, endpoint := range endpoints[start:end] {
				nodeID, ok := resolveEndpointNode(file.FilePath, endpoint, keyToID, nameToIDs, nil)
				if !ok {
					continue
				}
				batch.Queue(`INSERT INTO api_endpoints
					(repo_id, node_id, role, method, path_pattern, raw_path, file_path, line, updated_at)
					VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW()) ON CONFLICT DO NOTHING`,
					repoID, nodeID, endpoint.Role, endpoint.Method, endpoint.Path, endpoint.RawPath, file.FilePath, endpoint.Line)
			}
			results := tx.SendBatch(ctx, batch)
			for i := 0; i < batch.Len(); i++ {
				if _, err := results.Exec(); err != nil {
					_ = results.Close()
					return fmt.Errorf("publish graph generation: insert endpoint: %w", err)
				}
			}
			if err := results.Close(); err != nil {
				return fmt.Errorf("publish graph generation: close endpoint batch: %w", err)
			}
		}
		return nil
	}); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `UPDATE graph_index_generations
		SET status = 'published', published_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND repo_id = $2`, generationID, repoID); err != nil {
		return fmt.Errorf("publish graph generation: mark generation: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE repos r SET graph_published_generation_id = $2, graph_indexed_at = NOW(), graph_index_cursor = 0,
		  graph_index_commit_sha = g.commit_sha, graph_index_expected_files = g.expected_files,
		  graph_index_visited_files = g.visited_files, graph_index_failed_files = g.failed_files,
		  graph_index_skipped_files = g.skipped_files, graph_index_tree_truncated = g.tree_truncated,
		  graph_default_head_sha = CASE WHEN r.graph_refresh_version = g.refresh_version THEN g.commit_sha ELSE r.graph_default_head_sha END,
		  graph_default_head_observed_at = CASE WHEN r.graph_refresh_version = g.refresh_version THEN NOW() ELSE r.graph_default_head_observed_at END,
		  graph_refresh_requested_at = CASE
		    WHEN r.graph_refresh_version = g.refresh_version AND r.graph_refresh_commit_sha = g.commit_sha THEN NULL
		    ELSE r.graph_refresh_requested_at END,
		  graph_refresh_commit_sha = CASE
		    WHEN r.graph_refresh_version = g.refresh_version AND r.graph_refresh_commit_sha = g.commit_sha THEN NULL
		    ELSE r.graph_refresh_commit_sha END
		FROM graph_index_generations g WHERE r.id = $1 AND g.id = $2`, repoID, generationID); err != nil {
		return fmt.Errorf("publish graph generation: mark repo: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM graph_index_generation_files WHERE generation_id = $1`, generationID); err != nil {
		return fmt.Errorf("publish graph generation: clean staging payloads: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("publish graph generation: commit: %w", err)
	}

	if _, err := LinkAPIEndpoints(ctx, st, repoID); err != nil {
		slog.Warn("graph: cross-repo API linking after generation publish failed", "repo_id", repoID, "error", err)
	}
	return nil
}
