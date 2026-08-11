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

	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/internal/store"
)

// DefaultFullIndexFileCap bounds one full-repo index.
//
// Each window fetches, parses, and stages at most this many files. Parsed facts
// are persisted between continuations, so the live graph remains unchanged
// until the final atomic publish and file bodies never accumulate in memory.
//
// The cap also bounds GitHub cost: each source file is one API call, so a window
// can consume up to 1500 calls against an
// installation's 5000/hour budget — which is why the scheduler runs one repo
// per tick rather than a whole installation at once.
const DefaultFullIndexFileCap = 1500

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

// IndexRepo performs an uncapped authoritative full index.
func IndexRepo(ctx context.Context, st *store.Store, ghClient *ghpkg.Client, installationID int64, owner, repo, defaultBranch string, repoDBID int64) error {
	_, err := IndexRepoBounded(ctx, st, ghClient, installationID, owner, repo, defaultBranch, repoDBID, 0, 0)
	return err
}

func publishGraphGeneration(ctx context.Context, st *store.Store, repoID, generationID int64) error {
	files, err := st.ListGraphGenerationFiles(ctx, generationID)
	if err != nil {
		return err
	}
	tx, err := st.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("publish graph generation: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var expected, visited, failed, unavailable int
	var status string
	if err := tx.QueryRow(ctx, `
		SELECT status, expected_files, visited_files, failed_files
		FROM graph_index_generations WHERE id = $1 AND repo_id = $2 FOR UPDATE`, generationID, repoID).
		Scan(&status, &expected, &visited, &failed); err != nil {
		return fmt.Errorf("publish graph generation: validate: %w", err)
	}
	if err := tx.QueryRow(ctx, `
		SELECT count(*)::int FROM graph_index_generation_files
		WHERE generation_id = $1 AND status = 'unavailable'`, generationID).Scan(&unavailable); err != nil {
		return fmt.Errorf("publish graph generation: count unavailable: %w", err)
	}
	if status != "building" || visited != expected || failed != 0 || unavailable != 0 || len(files) != expected {
		return fmt.Errorf("publish graph generation: incomplete generation status=%s expected=%d visited=%d failed=%d ready=%d unavailable=%d", status, expected, visited, failed, len(files), unavailable)
	}

	// This transaction is the visibility boundary. Deleted and renamed paths,
	// removed symbols, endpoints, and every old edge disappear together.
	if _, err := tx.Exec(ctx, `DELETE FROM code_nodes WHERE repo_id = $1`, repoID); err != nil {
		return fmt.Errorf("publish graph generation: clear old graph: %w", err)
	}

	keyToID := map[string]int64{}
	nameToIDs := map[string][]int64{}
	edgesByFile := map[string][]Edge{}
	symbolsByFile := map[string][]Symbol{}
	endpointsByFile := map[string][]APIEndpoint{}
	for _, file := range files {
		var symbols []Symbol
		var edges []Edge
		var endpoints []APIEndpoint
		if err := json.Unmarshal(file.Symbols, &symbols); err != nil {
			return fmt.Errorf("publish graph generation: decode %s symbols: %w", file.FilePath, err)
		}
		if err := json.Unmarshal(file.Edges, &edges); err != nil {
			return fmt.Errorf("publish graph generation: decode %s edges: %w", file.FilePath, err)
		}
		if err := json.Unmarshal(file.Endpoints, &endpoints); err != nil {
			return fmt.Errorf("publish graph generation: decode %s endpoints: %w", file.FilePath, err)
		}
		symbolsByFile[file.FilePath], edgesByFile[file.FilePath], endpointsByFile[file.FilePath] = symbols, edges, endpoints
		for _, sym := range symbols {
			var id int64
			err := tx.QueryRow(ctx, `
				INSERT INTO code_nodes (repo_id, installation_id, kind, name, file_path, line_start, line_end, language,
				  return_type, params, visibility, is_async, receiver_type, scope, content_hash, updated_at)
				VALUES ($1, (SELECT installation_id FROM repos WHERE id = $1), $2, $3, $4, $5, $6, $7,
				  $8, $9, $10, $11, $12, $13, $14, NOW()) RETURNING id`, repoID, sym.Kind, sym.Name,
				sym.FilePath, sym.LineStart, sym.LineEnd, langForFile(sym.FilePath), sym.ReturnType,
				sym.Params, sym.Visibility, sym.IsAsync, sym.Receiver, sym.Scope, computeSymbolHash(sym)).Scan(&id)
			if err != nil {
				return fmt.Errorf("publish graph generation: insert node %s: %w", sym.Name, err)
			}
			keyToID[nodeKey(sym.FilePath, sym.Name)] = id
			nameToIDs[sym.Name] = append(nameToIDs[sym.Name], id)
		}
	}

	resolved := make([]store.CodeEdgeRow, 0)
	seen := map[store.CodeEdgeRow]struct{}{}
	appendEdge := func(sourceID, targetID int64, kind string) {
		row := store.CodeEdgeRow{SourceID: sourceID, TargetID: targetID, Kind: kind}
		if sourceID == 0 || targetID == 0 || kind == "" {
			return
		}
		if _, ok := seen[row]; ok {
			return
		}
		seen[row] = struct{}{}
		resolved = append(resolved, row)
	}
	ensureSynthetic := func(filePath, kind, name string) (int64, error) {
		if id := keyToID[nodeKey(filePath, name)]; id != 0 {
			return id, nil
		}
		var id int64
		if err := tx.QueryRow(ctx, `
			INSERT INTO code_nodes (repo_id, installation_id, kind, name, file_path, line_start, line_end, language, updated_at)
			VALUES ($1, (SELECT installation_id FROM repos WHERE id = $1), $2, $3, $4, 0, 0, '', NOW())
			RETURNING id`, repoID, kind, name, filePath).Scan(&id); err != nil {
			return 0, err
		}
		keyToID[nodeKey(filePath, name)] = id
		nameToIDs[name] = append(nameToIDs[name], id)
		return id, nil
	}
	resolveOrPlaceholder := func(filePath, targetName string) (int64, error) {
		id, status := describeNodeResolution(filePath, targetName, keyToID, nameToIDs)
		if status == resolutionResolved {
			return id, nil
		}
		placeholder := string(status) + ":" + targetName
		id, err := ensureSynthetic(filePath, "module", placeholder)
		if err != nil {
			return 0, fmt.Errorf("insert %s placeholder %s: %w", status, targetName, err)
		}
		return id, nil
	}

	filePaths := make([]string, 0, len(files))
	for filePath := range symbolsByFile {
		filePaths = append(filePaths, filePath)
	}
	sort.Strings(filePaths)
	for _, filePath := range filePaths {
		for _, edge := range edgesByFile[filePath] {
			if edge.Kind == "imports" {
				// Imports are file-level facts. A qualified synthetic module keeps
				// `module:fmt` distinct from a user symbol named `fmt`.
				sourceID := keyToID[nodeKey(filePath, filePath)]
				if sourceID == 0 || edge.TargetName == "" {
					continue
				}
				moduleName := "module:" + edge.TargetName
				targetID, err := ensureSynthetic(filePath, "module", moduleName)
				if err != nil {
					return fmt.Errorf("publish graph generation: insert module %s: %w", edge.TargetName, err)
				}
				appendEdge(sourceID, targetID, edge.Kind)
				continue
			}
			sourceID := keyToID[nodeKey(filePath, edge.SourceName)]
			if sourceID == 0 || edge.TargetName == "" {
				continue
			}
			targetID, err := resolveOrPlaceholder(filePath, edge.TargetName)
			if err != nil {
				return fmt.Errorf("publish graph generation: resolve edge target: %w", err)
			}
			appendEdge(sourceID, targetID, edge.Kind)
		}
	}

	// Type references use the same explicit resolution policy as parsed call
	// edges. Missing or colliding types remain visible instead of disappearing
	// or attaching to whichever duplicate happened to be inserted first.
	for _, filePath := range filePaths {
		for _, sym := range symbolsByFile[filePath] {
			sourceID := keyToID[nodeKey(filePath, sym.Name)]
			if sourceID == 0 {
				continue
			}
			for _, expression := range []string{sym.ReturnType, sym.Params} {
				for _, typeName := range extractTypeNames(expression) {
					if typeName == sym.Name {
						continue
					}
					targetID, err := resolveOrPlaceholder(filePath, typeName)
					if err != nil {
						return fmt.Errorf("publish graph generation: resolve type target: %w", err)
					}
					appendEdge(sourceID, targetID, "uses_type")
				}
			}
		}
	}

	for _, edge := range resolved {
		if _, err := tx.Exec(ctx, `
			INSERT INTO code_edges (repo_id, source_id, target_id, kind, inferred, updated_at)
			VALUES ($1, $2, $3, $4, false, NOW()) ON CONFLICT DO NOTHING`,
			repoID, edge.SourceID, edge.TargetID, edge.Kind); err != nil {
			return fmt.Errorf("publish graph generation: insert edge: %w", err)
		}
	}

	for _, filePath := range filePaths {
		for _, endpoint := range endpointsByFile[filePath] {
			nodeID, ok := resolveEndpointNode(filePath, endpoint, keyToID, nameToIDs, nil)
			if !ok {
				continue
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO api_endpoints (repo_id, node_id, role, method, path_pattern, raw_path, file_path, line, updated_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW()) ON CONFLICT DO NOTHING`,
				repoID, nodeID, endpoint.Role, endpoint.Method, endpoint.Path, endpoint.RawPath, filePath, endpoint.Line); err != nil {
				return fmt.Errorf("publish graph generation: insert endpoint: %w", err)
			}
		}
	}

	if _, err := tx.Exec(ctx, `
		UPDATE graph_index_generations SET status = 'published', published_at = NOW(), updated_at = NOW()
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
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("publish graph generation: commit: %w", err)
	}

	// Cross-repo API inference is derived from published endpoints. It remains a
	// separate best-effort projection and is rebuilt from the full installation.
	if _, err := LinkAPIEndpoints(ctx, st, repoID); err != nil {
		slog.Warn("graph: cross-repo API linking after generation publish failed", "repo_id", repoID, "error", err)
	}
	return nil
}
