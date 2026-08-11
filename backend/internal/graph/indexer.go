package graph

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/internal/store"
)

// symbolDiffKey is the identity used to match a parsed Symbol against a DB
// code_nodes row. It mirrors the row's unique-index subset that we diff on
// (kind, name). File path is implicit in the per-file scope of the diff
// loop. Kept separate from nodeKey (which joins file_path+name for edge
// resolution) so future migrations that change either mapping don't have
// to untangle the two purposes.
func symbolDiffKey(kind, name string) string { return kind + "\x1f" + name }

// indexerStore is the narrow persistence surface indexFileSet + its
// phase-1/2/3 diff helper require. Declaring it here (instead of taking
// *store.Store concretely) lets the integration test in
// indexer_integration_test.go drop in a recording fake that asserts call
// counts and arguments — without standing up Postgres. *store.Store
// satisfies this interface implicitly; no wrapper type is needed.
//
// Method set MUST stay minimal. Adding a method here means the fake has
// to track one more call site, which dilutes the test's focus on the
// diff loop.
type indexerStore interface {
	// apiEndpointStore is embedded, not duplicated, so the cross-repo API
	// linking pass and the indexer cannot drift apart on what persistence
	// they need. See apilink.go.
	apiEndpointStore

	ReplaceAPIEndpointsForFiles(ctx context.Context, repoID int64, filePaths []string, rows []store.APIEndpointRow) (bool, error)
	LookupCodeNodeIDsByName(ctx context.Context, repoID int64, names []string) (map[string]int64, error)
	GetNodesHashesForFile(ctx context.Context, repoID int64, filePath string) ([]store.NodeHashRow, error)
	UpsertCodeNodeFullWithHash(ctx context.Context, repoID int64, kind, name, filePath string, lineStart, lineEnd int, language string, prNumber int, returnType, params, visibility string, isAsync bool, receiverType, scope, contentHash string) (int64, error)
	UpsertCodeNode(ctx context.Context, repoID int64, kind, name, filePath string, lineStart, lineEnd int, language string, prNumber int) (int64, error)
	ReplaceCodeEdgesForFiles(ctx context.Context, repoID int64, filePaths []string, edges []store.CodeEdgeRow) error
	DeleteNodesByIDs(ctx context.Context, repoID int64, ids []int64) error
}

// fileResult bundles the parser output for a single file so indexFileSet
// can hand pre-parsed data to indexParsedSymbols without the test also
// needing a fake GitHub client. Exported field names are intentional —
// the struct is package-internal but the names flow into the test helper.
type fileResult struct {
	symbols []Symbol
	edges   []Edge
}

// unchangedSymbol pairs a parsed Symbol with the existing DB row ID that
// already holds an identical content_hash. The indexer reuses the ID for
// edge resolution and skips the upsert entirely.
type unchangedSymbol struct {
	Symbol Symbol
	NodeID int64
}

// symbolDiffPlan is the minimum set of writes needed to bring the DB state
// for a file in line with the newly-parsed symbols. It is a pure decision
// over hashes — no IO — so the diff logic is testable without a store.
type symbolDiffPlan struct {
	Unchanged []unchangedSymbol
	Changed   []Symbol
	Orphans   []int64
}

// planSymbolDiff partitions the parsed set against the DB state into three
// disjoint buckets:
//
//   - Unchanged: parsed.hash matches existing.ContentHash. Skip the upsert,
//     reuse the existing node ID for edge resolution.
//   - Changed: parsed symbol is either new or has a mismatched hash. Upsert.
//   - Orphans: DB rows whose (kind, name) is absent from the parse. Sweep.
//
// An empty existing.ContentHash (pre-migration-043 row, or a synthetic
// "module" node created by edge resolution before the hash column landed)
// always forces the Changed path — never trust an empty string to mean
// "unchanged", because the current parse cannot possibly have hashed to
// the empty string.
func planSymbolDiff(parsed []Symbol, existing []store.NodeHashRow) symbolDiffPlan {
	plan := symbolDiffPlan{
		Unchanged: make([]unchangedSymbol, 0, len(parsed)),
		Changed:   make([]Symbol, 0, len(parsed)),
	}
	type existingRow struct {
		id   int64
		hash string
	}
	existingByKey := make(map[string]existingRow, len(existing))
	for _, e := range existing {
		existingByKey[symbolDiffKey(e.Kind, e.Name)] = existingRow{id: e.ID, hash: e.ContentHash}
	}
	seenKeys := make(map[string]struct{}, len(parsed))
	for _, sym := range parsed {
		diffKey := symbolDiffKey(sym.Kind, sym.Name)
		seenKeys[diffKey] = struct{}{}
		prev, ok := existingByKey[diffKey]
		if ok && prev.hash != "" && prev.hash == computeSymbolHash(sym) {
			plan.Unchanged = append(plan.Unchanged, unchangedSymbol{Symbol: sym, NodeID: prev.id})
			continue
		}
		plan.Changed = append(plan.Changed, sym)
	}
	for key, prev := range existingByKey {
		if _, ok := seenKeys[key]; !ok {
			plan.Orphans = append(plan.Orphans, prev.id)
		}
	}
	return plan
}

// symbolHashSeparator is the unit-separator byte (\x1f, ASCII US) written
// between fields in the content-hash buffer. Picked because no realistic
// symbol identifier or type fragment contains it — so shuffling adjacent
// field boundaries can't produce a collision (e.g. name="foo" + params="bar"
// must not hash the same as name="fooba" + params="r"). Separate from
// nodeKey's NUL separator, which is for in-memory maps only and never
// hashed.
const symbolHashSeparator = 0x1f

// computeSymbolHash fingerprints every attribute the indexer persists on a
// code_node row. The hash is compared against the stored content_hash to
// short-circuit the upsert when a symbol hasn't structurally changed — the
// 95% case for any given file in a PR diff.
//
// Minimal-allocation on the steady-state path: one append buffer plus the
// two allocations inside hex.EncodeToString (a 64-byte make + string cast).
// The previous implementation did 10+ per-field []byte(string) conversions;
// this is ~3 allocs / ~200 ns on an M3 Pro. BenchmarkComputeSymbolHash pins
// the number so a regression (e.g. reverting to per-field Write calls) shows
// up as a jump in allocs/op in CI benchmark runs.
//
// IMPORTANT: extending the persisted column set on code_nodes MUST also
// extend the fields mixed in here. Otherwise a column change would leave
// stale row data around because the hash wouldn't flip. Keep this list in
// lockstep with UpsertCodeNodeFullWithHash.
//
// code_nodes.installation_id is the one deliberate exception. It is not symbol
// content — it is derived in SQL from the row's own repo_id (migration 071,
// store.installationOfRepo), so it cannot drift while repo_id is unchanged, and
// repo_id is part of the unique index this hash diffs within. Mixing it in
// would force a rewrite of every row in the fleet for a value that never
// differs.
func computeSymbolHash(sym Symbol) string {
	// Rough upper bound: 10 fields + 10 separators + 2 int fields (≤10 chars).
	// Oversizing slightly avoids regrowth for typical symbols.
	buf := make([]byte, 0, len(sym.Kind)+len(sym.Name)+len(sym.ReturnType)+
		len(sym.Params)+len(sym.Visibility)+len(sym.Receiver)+len(sym.Scope)+32)
	buf = append(buf, sym.Kind...)
	buf = append(buf, symbolHashSeparator)
	buf = append(buf, sym.Name...)
	buf = append(buf, symbolHashSeparator)
	buf = strconv.AppendInt(buf, int64(sym.LineStart), 10)
	buf = append(buf, symbolHashSeparator)
	buf = strconv.AppendInt(buf, int64(sym.LineEnd), 10)
	buf = append(buf, symbolHashSeparator)
	buf = append(buf, sym.ReturnType...)
	buf = append(buf, symbolHashSeparator)
	buf = append(buf, sym.Params...)
	buf = append(buf, symbolHashSeparator)
	buf = append(buf, sym.Visibility...)
	buf = append(buf, symbolHashSeparator)
	if sym.IsAsync {
		buf = append(buf, 1)
	} else {
		buf = append(buf, 0)
	}
	buf = append(buf, symbolHashSeparator)
	buf = append(buf, sym.Receiver...)
	buf = append(buf, symbolHashSeparator)
	buf = append(buf, sym.Scope...)

	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:])
}

// sourceExts lists file extensions we parse for the code graph.
var sourceExts = map[string]bool{
	".go": true, ".ts": true, ".tsx": true, ".js": true, ".jsx": true,
	".mjs": true, ".cjs": true, ".py": true,
	".java": true, ".rs": true, ".cs": true, ".rb": true,
	".kt": true, ".kts": true, ".swift": true,
	".c": true, ".h": true, ".cpp": true, ".cc": true, ".cxx": true, ".hpp": true,
	".php": true, ".scala": true, ".dart": true,
}

// IndexFiles performs incremental code graph indexing for specific files.
// Deletes old nodes for these files, re-parses, and upserts.
func IndexFiles(ctx context.Context, st *store.Store, ghClient *ghpkg.Client, installationID int64, owner, repo, ref string, repoDBID int64, files []string) error {
	var sourceFiles []string
	for _, f := range files {
		if sourceExts[strings.ToLower(filepath.Ext(f))] {
			sourceFiles = append(sourceFiles, f)
		}
	}
	if len(sourceFiles) == 0 {
		return nil
	}

	// Per-file DELETE loop removed — indexFileSet now runs a hash-gated
	// diff that touches only changed/new/removed symbols. See
	// computeSymbolHash + the orphan sweep at the end of indexFileSet.

	slog.Info("graph: incremental index", "repo", owner+"/"+repo, "files", len(sourceFiles))
	return indexFileSet(ctx, st, ghClient, installationID, owner, repo, ref, repoDBID, sourceFiles)
}

// indexFileSet fetches content for each file, parses symbols/edges, and upserts them.
// The store dependency is the narrow indexerStore interface so the IO loop
// below can be exercised by an in-memory fake in indexer_integration_test.go.
// *store.Store implicitly satisfies indexerStore, so callers pass it through.
//
// Streams per file: fetch → parse → upsert nodes → next file. Only
// symbols + edges (small structs) accumulate for the cross-file edge-resolution
// pass. The memory win vs. the older version comes from NOT buffering a
// `map[string]fileResult{content, symbols, edges}` across files — content
// goes out of scope when each iteration ends. Symbol strings carved from
// `content` by the parser still share backing bytes with the file body,
// so peak memory scales with parsed-text-retained-per-file, not raw file
// size. An earlier version OOM'd a 512 MB VM on an 890-file full re-index.
func indexFileSet(ctx context.Context, st indexerStore, ghClient *ghpkg.Client, installationID int64, owner, repo, ref string, repoDBID int64, files []string) error {
	keyToID := make(map[string]int64)
	nameToIDs := make(map[string][]int64)
	edgesByFile := make(map[string][]Edge, len(files))
	symbolsByFile := make(map[string][]Symbol, len(files))
	endpointsByFile := make(map[string][]APIEndpoint, len(files))

	for _, f := range files {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		content, err := ghClient.GetFileContent(ctx, installationID, owner, repo, f, ref)
		if err != nil {
			slog.Warn("graph: fetch file failed", "file", f, "error", err)
			continue
		}
		syms, edges := ParseFileSymbols(f, content)
		upsertFileSymbols(ctx, st, repoDBID, f, syms, keyToID, nameToIDs)
		edgesByFile[f] = edges
		symbolsByFile[f] = syms
		// Assigned for every VISITED file, including files with no endpoints,
		// because the empty slice is what deletes a route that was removed
		// from the file. A file whose fetch failed is skipped above and keeps
		// its existing rows — we did not observe it, so we cannot claim its
		// endpoints are gone.
		//
		endpointsByFile[f] = anchorEndpoints(ExtractAPIEndpoints(f, content), syms)
	}

	if err := resolveAndUpsertEdges(ctx, st, repoDBID, edgesByFile, symbolsByFile, keyToID, nameToIDs); err != nil {
		return err
	}

	// After the nodes exist, not before: an endpoint row references the
	// code_node it belongs to, so it cannot be written until that node has an
	// id.
	changed := persistAPIEndpoints(ctx, st, repoDBID, endpointsByFile, keyToID, nameToIDs)
	if !changed {
		// Nothing this run visited altered the route table, so the derived edge
		// set it feeds cannot have changed either. Without this gate every
		// incremental index — including a PR that touches twenty files
		// declaring nothing — re-listed the whole installation, rematched it,
		// and rewrote every derived edge, inside the 10s budget the pass shares
		// with the GitHub file fetches.
		return nil
	}

	written, err := LinkAPIEndpoints(ctx, st, repoDBID)
	if err != nil {
		// Non-fatal, like every other enrichment in this loop. The parsed graph
		// is already written and correct; the derived edges are an addition.
		slog.Warn("graph: cross-repo API linking failed", "repo", owner+"/"+repo, "error", err)
		return nil
	}
	slog.Info("graph: cross-repo API edges", "repo", owner+"/"+repo, "edges", written)
	return nil
}

// persistAPIEndpoints resolves each extracted endpoint to a code_nodes id and
// rewrites the api_endpoints rows for every file the run visited.
//
// Resolution order, and the order matters:
//
//  1. the HANDLER named in the registration — chi's r.Get("/x", s.healthz)
//     names a symbol that almost always lives in another file, and that symbol
//     is what a change to the endpoint actually touches;
//  2. the symbol that ENCLOSES the site, which is the only anchor a client call
//     or an inline handler has.
//
// An endpoint that resolves to neither is dropped rather than anchored to a
// synthesised node. Attaching it to something that does not describe it is how
// an inferred edge starts pointing at the wrong code.
//
// Handlers this run did not parse are resolved from the DATABASE, in one
// batched lookup. keyToID/nameToIDs only hold the files a single run visited,
// so an incremental index of server.go could not see the handler functions its
// routes name and fell through to the enclosing router function — anchoring all
// 80+ routes to `routes`, while a full index anchored each to its handler.
// Which node a route carried then depended on which files a PR happened to
// change.
//
// Returns whether the stored route table changed, which is what gates the
// installation-wide re-derivation of calls_api edges.
func persistAPIEndpoints(ctx context.Context, st indexerStore, repoDBID int64, endpointsByFile map[string][]APIEndpoint, keyToID map[string]int64, nameToIDs map[string][]int64) bool {
	filePaths := make([]string, 0, len(endpointsByFile))
	for filePath := range endpointsByFile {
		filePaths = append(filePaths, filePath)
	}
	sort.Strings(filePaths) // deterministic row order, so the change gate is stable

	dbIDs := lookupUnresolvedHandlers(ctx, st, repoDBID, endpointsByFile, keyToID, nameToIDs)

	rows := make([]store.APIEndpointRow, 0, len(endpointsByFile))
	for _, filePath := range filePaths {
		for _, e := range endpointsByFile[filePath] {
			nodeID, ok := resolveEndpointNode(filePath, e, keyToID, nameToIDs, dbIDs)
			if !ok {
				continue
			}
			rows = append(rows, store.APIEndpointRow{
				RepoID: repoDBID, NodeID: nodeID, Role: e.Role, Method: e.Method,
				PathPattern: e.Path, RawPath: e.RawPath, FilePath: filePath, Line: e.Line,
			})
		}
	}

	changed, err := st.ReplaceAPIEndpointsForFiles(ctx, repoDBID, filePaths, rows)
	if err != nil {
		slog.Warn("graph: persist api endpoints failed", "repo_id", repoDBID, "files", len(filePaths), "error", err)
		return false
	}
	return changed
}

// lookupUnresolvedHandlers batches the DB fallback for every endpoint name this
// run's own parse cannot resolve. One query per run, not per endpoint; a lookup
// failure is logged and degrades to the in-run maps rather than dropping rows.
func lookupUnresolvedHandlers(ctx context.Context, st indexerStore, repoDBID int64, endpointsByFile map[string][]APIEndpoint, keyToID map[string]int64, nameToIDs map[string][]int64) map[string]int64 {
	missing := map[string]struct{}{}
	for filePath, eps := range endpointsByFile {
		for _, e := range eps {
			for _, name := range []string{e.Handler, e.Symbol} {
				if name == "" {
					continue
				}
				if _, ok := resolveNodeName(filePath, name, keyToID, nameToIDs); !ok {
					missing[name] = struct{}{}
				}
			}
		}
	}
	if len(missing) == 0 {
		return nil
	}
	names := make([]string, 0, len(missing))
	for name := range missing {
		names = append(names, name)
	}
	sort.Strings(names)
	ids, err := st.LookupCodeNodeIDsByName(ctx, repoDBID, names)
	if err != nil {
		slog.Warn("graph: endpoint handler lookup failed", "repo_id", repoDBID, "error", err)
		return nil
	}
	return ids
}

func resolveEndpointNode(filePath string, e APIEndpoint, keyToID map[string]int64, nameToIDs map[string][]int64, dbIDs map[string]int64) (int64, bool) {
	for _, name := range []string{e.Handler, e.Symbol} {
		if name == "" {
			continue
		}
		if id, ok := resolveNodeName(filePath, name, keyToID, nameToIDs); ok {
			return id, true
		}
		if id, ok := dbIDs[name]; ok {
			return id, true
		}
	}
	return 0, false
}

// indexParsedSymbols runs the hash-gated diff + edge upsert loop against
// an already-populated parser result map. Split out of indexFileSet so the
// integration test can drive the exact three-phase loop (plan, apply,
// sweep) and the two edge-resolution passes without standing up a GitHub
// client. indexFileSet uses this after its fetch+parse phase; the behavior
// is identical — no extra retries, no extra logging.
func indexParsedSymbols(ctx context.Context, st indexerStore, repoDBID int64, results map[string]fileResult) error {
	// Thin wrapper over the two streaming helpers. Fans results out so the
	// edge-resolution pass can see all files. indexFileSet prefers calling
	// upsertFileSymbols directly per file to bound memory; this entry point
	// exists for the integration test's pre-populated results map.
	keyToID := make(map[string]int64)
	nameToIDs := make(map[string][]int64)
	edgesByFile := make(map[string][]Edge, len(results))
	symbolsByFile := make(map[string][]Symbol, len(results))
	for filePath, res := range results {
		upsertFileSymbols(ctx, st, repoDBID, filePath, res.symbols, keyToID, nameToIDs)
		edgesByFile[filePath] = res.edges
		symbolsByFile[filePath] = res.symbols
	}
	return resolveAndUpsertEdges(ctx, st, repoDBID, edgesByFile, symbolsByFile, keyToID, nameToIDs)
}

// upsertFileSymbols runs the hash-gated plan/apply/sweep for one file and
// extends keyToID + nameToIDs in place for cross-file edge resolution. Errors
// on individual symbols are logged and skipped; the loop never returns an
// error so one bad file doesn't halt a 900-file index.
//
// Call exactly once per filePath per run. A second call for the same file
// would re-append that file's node IDs to nameToIDs[name], producing
// duplicates. Edge resolution picks the first match so behavior is benign
// in practice, but the invariant is "one entry per symbol per run."
func upsertFileSymbols(ctx context.Context, st indexerStore, repoDBID int64, filePath string, symbols []Symbol, keyToID map[string]int64, nameToIDs map[string][]int64) {
	existing, err := st.GetNodesHashesForFile(ctx, repoDBID, filePath)
	if err != nil {
		slog.Warn("graph: load existing node hashes failed", "file", filePath, "error", err)
		// Fall back equivalent to the old path: treat every parsed symbol
		// as Changed, do not sweep orphans. Losing the skip optimization
		// for this file is fine; losing correctness is not.
		existing = nil
	}
	plan := planSymbolDiff(symbols, existing)
	lang := langForFile(filePath)

	// Phase 1: reuse IDs of unchanged rows for edge resolution.
	for _, u := range plan.Unchanged {
		keyToID[nodeKey(u.Symbol.FilePath, u.Symbol.Name)] = u.NodeID
		nameToIDs[u.Symbol.Name] = append(nameToIDs[u.Symbol.Name], u.NodeID)
	}
	// Phase 2: upsert the subset that actually changed (or is new).
	for _, sym := range plan.Changed {
		id, err := st.UpsertCodeNodeFullWithHash(ctx, repoDBID, sym.Kind, sym.Name, sym.FilePath, sym.LineStart, sym.LineEnd, lang, 0, sym.ReturnType, sym.Params, sym.Visibility, sym.IsAsync, sym.Receiver, sym.Scope, computeSymbolHash(sym))
		if err != nil {
			slog.Warn("graph: upsert node failed", "name", sym.Name, "file", sym.FilePath, "error", err)
			continue
		}
		keyToID[nodeKey(sym.FilePath, sym.Name)] = id
		nameToIDs[sym.Name] = append(nameToIDs[sym.Name], id)
	}
	// Phase 3: batch-delete orphans (no-ops on len == 0).
	if err := st.DeleteNodesByIDs(ctx, repoDBID, plan.Orphans); err != nil {
		slog.Warn("graph: orphan sweep failed", "file", filePath, "error", err)
	}
}

// resolveNodeName finds the best node ID for a symbol name referenced from
// sourceFile. Same-file references prefer the node in that file; otherwise the
// first match wins.
//
// ONE definition, used by both edge resolution and endpoint anchoring. The two
// had identical copies, so a change to the collision rule applied to edges
// would have left endpoints resolving the same name to a different node — the
// two halves of the graph drifting apart on what a name means.
func resolveNodeName(sourceFile, name string, keyToID map[string]int64, nameToIDs map[string][]int64) (int64, bool) {
	if id, ok := keyToID[nodeKey(sourceFile, name)]; ok {
		return id, true
	}
	if ids := nameToIDs[name]; len(ids) > 0 {
		return ids[0], true
	}
	return 0, false
}

// resolveAndUpsertEdges runs the edge-resolution + upsert pass after every
// file's nodes have been committed and keyToID/nameToIDs are fully populated.
// Edges and symbol slices are kept separate from the fileResult map so the
// caller can free file bodies eagerly during the fetch/parse phase.
func resolveAndUpsertEdges(ctx context.Context, st indexerStore, repoDBID int64, edgesByFile map[string][]Edge, symbolsByFile map[string][]Symbol, keyToID map[string]int64, nameToIDs map[string][]int64) error {
	// An incremental run only parses changed files. Resolve targets that live in
	// untouched files from the published repo graph before replacing the changed
	// files' outgoing edge snapshots; otherwise a one-file change would silently
	// delete every call or type edge into an unchanged file.
	missing := map[string]struct{}{}
	for filePath, edges := range edgesByFile {
		for _, edge := range edges {
			if _, ok := resolveNodeName(filePath, edge.TargetName, keyToID, nameToIDs); !ok && edge.TargetName != "" {
				missing[edge.TargetName] = struct{}{}
			}
		}
	}
	for filePath, symbols := range symbolsByFile {
		for _, sym := range symbols {
			for _, expression := range []string{sym.ReturnType, sym.Params} {
				for _, typeName := range extractTypeNames(expression) {
					if _, ok := resolveNodeName(filePath, typeName, keyToID, nameToIDs); !ok {
						missing[typeName] = struct{}{}
					}
				}
			}
		}
	}
	if len(missing) > 0 {
		names := make([]string, 0, len(missing))
		for name := range missing {
			names = append(names, name)
		}
		sort.Strings(names)
		dbIDs, err := st.LookupCodeNodeIDsByName(ctx, repoDBID, names)
		if err != nil {
			return fmt.Errorf("resolve existing edge targets: %w", err)
		}
		for name, id := range dbIDs {
			if id == 0 {
				continue
			}
			nameToIDs[name] = append(nameToIDs[name], id)
			// resolveTypeEdges consumes composite keys. The empty path marks a
			// database fallback without pretending to know the target's path.
			keyToID[nodeKey("", name)] = id
		}
	}

	resolveEdgeTarget := func(sourceFile, targetName string) (int64, bool) {
		return resolveNodeName(sourceFile, targetName, keyToID, nameToIDs)
	}

	filePaths := make([]string, 0, len(edgesByFile))
	for filePath := range edgesByFile {
		filePaths = append(filePaths, filePath)
	}
	sort.Strings(filePaths)

	resolved := make([]store.CodeEdgeRow, 0)
	seen := make(map[store.CodeEdgeRow]struct{})
	appendEdge := func(sourceID, targetID int64, kind string) {
		row := store.CodeEdgeRow{SourceID: sourceID, TargetID: targetID, Kind: kind}
		if _, ok := seen[row]; ok {
			return
		}
		seen[row] = struct{}{}
		resolved = append(resolved, row)
	}

	for _, filePath := range filePaths {
		for _, edge := range edgesByFile[filePath] {
			if edge.Kind == "imports" {
				var sourceID int64
				for _, sym := range symbolsByFile[filePath] {
					if id, ok := keyToID[nodeKey(filePath, sym.Name)]; ok {
						sourceID = id
						break
					}
				}
				if sourceID == 0 {
					continue
				}
				targetID, ok := resolveEdgeTarget(filePath, edge.TargetName)
				if !ok {
					var err error
					targetID, err = st.UpsertCodeNode(ctx, repoDBID, "module", edge.TargetName, filePath, 0, 0, "", 0)
					if err != nil {
						slog.Warn("graph: upsert import node failed", "target", edge.TargetName, "error", err)
						continue
					}
					keyToID[nodeKey(filePath, edge.TargetName)] = targetID
					nameToIDs[edge.TargetName] = append(nameToIDs[edge.TargetName], targetID)
				}
				appendEdge(sourceID, targetID, edge.Kind)
				continue
			}

			sourceID, ok := keyToID[nodeKey(filePath, edge.SourceName)]
			if !ok {
				sourceIDs := nameToIDs[edge.SourceName]
				if len(sourceIDs) == 0 {
					continue
				}
				sourceID = sourceIDs[0]
			}
			targetID, ok := resolveEdgeTarget(filePath, edge.TargetName)
			if !ok {
				continue
			}
			appendEdge(sourceID, targetID, edge.Kind)
		}
	}

	var allSyms []Symbol
	for _, syms := range symbolsByFile {
		allSyms = append(allSyms, syms...)
	}
	for _, edge := range resolveTypeEdges(allSyms, keyToID) {
		appendEdge(keyToID[edge.SourceName], keyToID[edge.TargetName], edge.Kind)
	}

	if err := st.ReplaceCodeEdgesForFiles(ctx, repoDBID, filePaths, resolved); err != nil {
		return fmt.Errorf("replace code edges: %w", err)
	}
	return nil
}

// typeNameRe extracts identifiers from type expressions, stripping pointers, slices, maps, etc.
var typeNameRe = regexp.MustCompile(`\b([A-Za-z]\w+)`)

// typeKeywords are common language keywords/builtins that should not be treated as type names.
var typeKeywords = map[string]bool{
	"func": true, "return": true, "if": true, "else": true, "for": true,
	"var": true, "const": true, "let": true, "string": true, "int": true,
	"bool": true, "float": true, "void": true, "nil": true, "null": true,
	"true": true, "false": true, "error": true, "context": true, "map": true,
	"chan": true, "byte": true, "rune": true, "int64": true, "float64": true,
	"uint": true, "uint64": true, "int32": true, "uint32": true,
}

// extractTypeNames parses a type expression string and returns unique type names found.
// Strips *, [], map, chan prefixes and returns identifiers that aren't common keywords.
func extractTypeNames(typeExpr string) []string {
	if typeExpr == "" {
		return nil
	}
	seen := make(map[string]bool)
	var names []string
	for _, match := range typeNameRe.FindAllStringSubmatch(typeExpr, -1) {
		m := match[1]
		if !seen[m] && !typeKeywords[m] {
			seen[m] = true
			names = append(names, m)
		}
	}
	return names
}

// resolveTypeEdges creates uses_type edges by inspecting each symbol's ReturnType
// and Params for type names that exist in keyToID.
// SourceName and TargetName in returned edges use composite keys (filePath\x00name).
func resolveTypeEdges(symbols []Symbol, keyToID map[string]int64) []Edge {
	var edges []Edge
	seen := make(map[[2]string]bool)

	// Build a name-only lookup for type resolution (type names don't carry file paths)
	nameToKey := make(map[string]string) // symbol name -> first composite key found
	for key := range keyToID {
		// key format is "filePath\x00name"
		idx := strings.Index(key, "\x00")
		if idx < 0 {
			continue
		}
		name := key[idx+1:]
		if _, exists := nameToKey[name]; !exists {
			nameToKey[name] = key
		}
	}

	for _, sym := range symbols {
		srcKey := nodeKey(sym.FilePath, sym.Name)
		if _, ok := keyToID[srcKey]; !ok {
			continue
		}
		// Check return type
		for _, typeName := range extractTypeNames(sym.ReturnType) {
			if typeName == sym.Name {
				continue
			}
			tgtKey, ok := nameToKey[typeName]
			if !ok {
				continue
			}
			edgeKey := [2]string{srcKey, tgtKey}
			if seen[edgeKey] {
				continue
			}
			seen[edgeKey] = true
			edges = append(edges, Edge{SourceName: srcKey, TargetName: tgtKey, Kind: "uses_type"})
		}
		// Check params
		for _, typeName := range extractTypeNames(sym.Params) {
			if typeName == sym.Name {
				continue
			}
			tgtKey, ok := nameToKey[typeName]
			if !ok {
				continue
			}
			edgeKey := [2]string{srcKey, tgtKey}
			if seen[edgeKey] {
				continue
			}
			seen[edgeKey] = true
			edges = append(edges, Edge{SourceName: srcKey, TargetName: tgtKey, Kind: "uses_type"})
		}
	}
	return edges
}
