package graph

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BeLazy167/argus/backend/internal/obs"

	"github.com/BeLazy167/argus/backend/internal/store"
)

// symbolDiffKey is the identity used to match a parsed Symbol against a DB
// code_nodes row. It mirrors the row's unique-index subset that we diff on
// (kind, name). File path is implicit in the per-file scope of the diff
// loop. Kept separate from nodeKey (which joins file_path+name for edge
// resolution) so future migrations that change either mapping don't have
// to untangle the two purposes.
func symbolDiffKey(kind, name string) string { return kind + "\x1f" + name }

// indexerStore is the narrow persistence surface retained symbol-diff and
// endpoint helpers require. Declaring it here (instead of taking
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

// fileResult bundles parser output for the symbol-diff helpers.
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

// fileSymbol records deterministic physical LOC and file identity in the existing
// code_nodes schema. A trailing newline terminates the last content line; it
// does not create an additional blank line.
func fileSymbol(filePath, content string) Symbol {
	loc := strings.Count(content, "\n")
	if content != "" && !strings.HasSuffix(content, "\n") {
		loc++
	}
	lineStart := 0
	if loc > 0 {
		lineStart = 1
	}
	return Symbol{Kind: "file", Name: filePath, FilePath: filePath, LineStart: lineStart, LineEnd: loc}
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
	operationID := obs.NewLogID()
	started := time.Now()
	endpointCount := 0
	for _, endpoints := range endpointsByFile {
		endpointCount += len(endpoints)
	}
	slog.InfoContext(ctx, "graph API endpoint persistence started", "operation_id", operationID,
		"repo_id", repoDBID, "file_count", len(endpointsByFile), "endpoint_count", endpointCount)
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
		slog.WarnContext(ctx, "graph API endpoint persistence failed", "operation_id", operationID,
			"repo_id", repoDBID, "file_count", len(filePaths), "extracted_count", endpointCount,
			"resolved_count", len(rows), "dropped_unresolved_count", endpointCount-len(rows),
			"duration_ms", time.Since(started).Milliseconds(), "error", err)
		return false
	}
	slog.InfoContext(ctx, "graph API endpoint persistence completed", "operation_id", operationID,
		"repo_id", repoDBID, "file_count", len(filePaths), "extracted_count", endpointCount,
		"resolved_count", len(rows), "dropped_unresolved_count", endpointCount-len(rows),
		"changed", changed, "duration_ms", time.Since(started).Milliseconds())
	return changed
}

// lookupUnresolvedHandlers batches the DB fallback for every endpoint name this
// run's own parse cannot resolve. One query per run, not per endpoint; a lookup
// failure is logged and degrades to the in-run maps rather than dropping rows.
func lookupUnresolvedHandlers(ctx context.Context, st indexerStore, repoDBID int64, endpointsByFile map[string][]APIEndpoint, keyToID map[string]int64, nameToIDs map[string][]int64) map[string]int64 {
	started := time.Now()
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
		slog.DebugContext(ctx, "graph endpoint handler lookup skipped", "repo_id", repoDBID,
			"reason", "all_handlers_resolved_in_run", "duration_ms", time.Since(started).Milliseconds())
		return nil
	}
	names := make([]string, 0, len(missing))
	for name := range missing {
		names = append(names, name)
	}
	sort.Strings(names)
	ids, err := st.LookupCodeNodeIDsByName(ctx, repoDBID, names)
	if err != nil {
		slog.WarnContext(ctx, "graph endpoint handler lookup failed", "repo_id", repoDBID,
			"lookup_count", len(names), "duration_ms", time.Since(started).Milliseconds(), "error", err)
		return nil
	}
	slog.DebugContext(ctx, "graph endpoint handler lookup completed", "repo_id", repoDBID,
		"lookup_count", len(names), "resolved_count", len(ids), "duration_ms", time.Since(started).Milliseconds())
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
		if id, ok := dbIDs[name]; ok && id != 0 {
			return id, true
		}
	}
	return 0, false
}

// indexParsedSymbols is the test seam for the retained hash-gated symbol diff
// and edge-resolution algorithm. It accepts already-parsed results so tests can
// exercise persistence without a GitHub client.
func indexParsedSymbols(ctx context.Context, st indexerStore, repoDBID int64, results map[string]fileResult) (err error) {
	operationID := obs.NewLogID()
	started := time.Now()
	symbolCount, edgeCount := 0, 0
	for _, result := range results {
		symbolCount += len(result.symbols)
		edgeCount += len(result.edges)
	}
	slog.InfoContext(ctx, "graph parsed symbol indexing started", "operation_id", operationID,
		"repo_id", repoDBID, "file_count", len(results), "symbol_count", symbolCount, "edge_count", edgeCount)
	defer func() {
		attrs := []any{"operation_id", operationID, "repo_id", repoDBID, "file_count", len(results),
			"symbol_count", symbolCount, "edge_count", edgeCount, "duration_ms", time.Since(started).Milliseconds(), "error", err}
		if err != nil {
			slog.WarnContext(ctx, "graph parsed symbol indexing failed", attrs...)
			return
		}
		slog.InfoContext(ctx, "graph parsed symbol indexing completed", attrs...)
	}()
	// Fan results out before resolving cross-file edges.
	keyToID := make(map[string]int64)
	nameToIDs := make(map[string][]int64)
	edgesByFile := make(map[string][]Edge, len(results))
	symbolsByFile := make(map[string][]Symbol, len(results))
	for filePath, res := range results {
		upsertFileSymbols(ctx, st, repoDBID, filePath, res.symbols, keyToID, nameToIDs)
		edgesByFile[filePath] = res.edges
		symbolsByFile[filePath] = res.symbols
	}
	err = resolveAndUpsertEdges(ctx, st, repoDBID, edgesByFile, symbolsByFile, keyToID, nameToIDs)
	return err
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
	started := time.Now()
	slog.DebugContext(ctx, "graph file symbol diff started", "repo_id", repoDBID, "file", filePath, "parsed_count", len(symbols))
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
	failedUpserts := 0

	// Phase 1: reuse IDs of unchanged rows for edge resolution.
	for _, u := range plan.Unchanged {
		rememberSymbolResolution(u.Symbol, u.NodeID, keyToID, nameToIDs)
	}
	// Phase 2: upsert the subset that actually changed (or is new).
	for _, sym := range plan.Changed {
		id, err := st.UpsertCodeNodeFullWithHash(ctx, repoDBID, sym.Kind, sym.Name, sym.FilePath, sym.LineStart, sym.LineEnd, lang, 0, sym.ReturnType, sym.Params, sym.Visibility, sym.IsAsync, sym.Receiver, sym.Scope, computeSymbolHash(sym))
		if err != nil {
			failedUpserts++
			slog.WarnContext(ctx, "graph node upsert failed", "repo_id", repoDBID, "name", sym.Name,
				"kind", sym.Kind, "file", sym.FilePath, "error", err)
			continue
		}
		rememberSymbolResolution(sym, id, keyToID, nameToIDs)
	}
	// Phase 3: batch-delete orphans (no-ops on len == 0).
	orphanErr := st.DeleteNodesByIDs(ctx, repoDBID, plan.Orphans)
	if orphanErr != nil {
		slog.WarnContext(ctx, "graph orphan node sweep failed", "repo_id", repoDBID, "file", filePath,
			"orphan_count", len(plan.Orphans), "error", orphanErr)
	}
	slog.DebugContext(ctx, "graph file symbol diff completed", "repo_id", repoDBID, "file", filePath,
		"existing_count", len(existing), "parsed_count", len(symbols), "unchanged_count", len(plan.Unchanged),
		"changed_count", len(plan.Changed), "failed_upsert_count", failedUpserts, "orphan_count", len(plan.Orphans),
		"orphan_sweep_error", orphanErr, "duration_ms", time.Since(started).Milliseconds())
}

// rememberSymbolResolution registers both the persisted qualified identity and
// its unqualified lookup alias. Aliases are intentionally name-only: same-file
// unqualified duplicates must remain ambiguous rather than winning by map order.
func rememberSymbolResolution(sym Symbol, id int64, keyToID map[string]int64, nameToIDs map[string][]int64) {
	keyToID[nodeKey(sym.FilePath, sym.Name)] = id
	nameToIDs[sym.Name] = appendUniqueID(nameToIDs[sym.Name], id)
	if (sym.Kind == KindMethod || sym.Kind == KindClass) && simpleSymbolName(sym.Name) != sym.Name {
		alias := simpleSymbolName(sym.Name)
		nameToIDs[alias] = appendUniqueID(nameToIDs[alias], id)
	}
}

func appendUniqueID(ids []int64, id int64) []int64 {
	for _, existing := range ids {
		if existing == id {
			return ids
		}
	}
	return append(ids, id)
}

// resolveNodeName finds an unambiguous node ID for a symbol reference.
// Same-file identity wins. A repository-wide name resolves only when unique;
// collisions remain explicit instead of depending on tree or database order.
//
// ONE definition, used by both edge resolution and endpoint anchoring.
type nodeResolution string

const (
	resolutionResolved   nodeResolution = "resolved"
	resolutionAmbiguous  nodeResolution = "ambiguous"
	resolutionUnresolved nodeResolution = "unresolved"
)

// describeNodeResolution never chooses an arbitrary repository-wide duplicate.
// Same-file identity wins; otherwise a name must be globally unique. Both the
// incremental writer and atomic generation publisher use this policy.
func describeNodeResolution(sourceFile, name string, keyToID map[string]int64, nameToIDs map[string][]int64) (int64, nodeResolution) {
	if id := keyToID[nodeKey(sourceFile, name)]; id != 0 {
		return id, resolutionResolved
	}
	// Qualified references are authoritative: if Type.Handle is absent, a
	// unique unrelated Handle must not silently become its target. Unqualified
	// references already have their method aliases registered under name.
	ids := nameToIDs[name]
	switch len(ids) {
	case 0:
		return 0, resolutionUnresolved
	case 1:
		return ids[0], resolutionResolved
	default:
		return 0, resolutionAmbiguous
	}
}

func resolveNodeName(sourceFile, name string, keyToID map[string]int64, nameToIDs map[string][]int64) (int64, bool) {
	id, status := describeNodeResolution(sourceFile, name, keyToID, nameToIDs)
	return id, status == resolutionResolved
}

func resolutionPlaceholder(status, name string) string {
	return status + ":" + name
}

func ensureResolutionPlaceholder(ctx context.Context, st indexerStore, repoID int64, sourceFile, status, name string, keyToID map[string]int64, nameToIDs map[string][]int64) (int64, bool) {
	placeholder := resolutionPlaceholder(status, name)
	if id, ok := keyToID[nodeKey(sourceFile, placeholder)]; ok {
		return id, true
	}
	id, err := st.UpsertCodeNode(ctx, repoID, "module", placeholder, sourceFile, 0, 0, "", 0)
	if err != nil {
		slog.Warn("graph: record unresolved edge failed", "status", status, "target", name, "file", sourceFile, "error", err)
		return 0, false
	}
	keyToID[nodeKey(sourceFile, placeholder)] = id
	nameToIDs[placeholder] = append(nameToIDs[placeholder], id)
	return id, true
}

func resolveOrRecordTarget(ctx context.Context, st indexerStore, repoID int64, sourceFile, name string, keyToID map[string]int64, nameToIDs map[string][]int64) (int64, bool) {
	id, status := describeNodeResolution(sourceFile, name, keyToID, nameToIDs)
	if status == resolutionResolved {
		return id, true
	}
	return ensureResolutionPlaceholder(ctx, st, repoID, sourceFile, string(status), name, keyToID, nameToIDs)
}

// resolveAndUpsertEdges runs the edge-resolution + upsert pass after every
// file's nodes have been committed and keyToID/nameToIDs are fully populated.
// Edges and symbol slices are kept separate from the fileResult map so the
// caller can free file bodies eagerly during the fetch/parse phase.
func resolveAndUpsertEdges(ctx context.Context, st indexerStore, repoDBID int64, edgesByFile map[string][]Edge, symbolsByFile map[string][]Symbol, keyToID map[string]int64, nameToIDs map[string][]int64) (err error) {
	operationID := obs.NewLogID()
	started := time.Now()
	parsedEdgeCount := 0
	for _, edges := range edgesByFile {
		parsedEdgeCount += len(edges)
	}
	slog.InfoContext(ctx, "graph edge resolution started", "operation_id", operationID, "repo_id", repoDBID,
		"file_count", len(edgesByFile), "parsed_edge_count", parsedEdgeCount, "known_symbol_count", len(keyToID))
	defer func() {
		attrs := []any{"operation_id", operationID, "repo_id", repoDBID, "file_count", len(edgesByFile),
			"parsed_edge_count", parsedEdgeCount, "duration_ms", time.Since(started).Milliseconds(), "error", err}
		if err != nil {
			slog.WarnContext(ctx, "graph edge resolution failed", attrs...)
			return
		}
		slog.InfoContext(ctx, "graph edge resolution completed", attrs...)
	}()
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
				// The store returns zero when qualified aliases exist but the
				// unqualified request matches more than one. Two sentinels preserve
				// that ambiguity without inventing a concrete target.
				nameToIDs[name] = []int64{-1, -2}
				continue
			}
			nameToIDs[name] = appendUniqueID(nameToIDs[name], id)
		}
	}

	resolveEdgeTarget := func(sourceFile, targetName string) (int64, bool) {
		return resolveOrRecordTarget(ctx, st, repoDBID, sourceFile, targetName, keyToID, nameToIDs)
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
				// Import relationships belong to the deterministic file identity,
				// never an arbitrary first symbol in that file.
				sourceID, found := keyToID[nodeKey(filePath, filePath)]
				if !found {
					continue
				}
				moduleName := "module:" + edge.TargetName
				targetID, ok := resolveNodeName(filePath, moduleName, keyToID, nameToIDs)
				if !ok {
					var err error
					targetID, err = st.UpsertCodeNode(ctx, repoDBID, "module", moduleName, filePath, 0, 0, "", 0)
					if err != nil {
						slog.Warn("graph: upsert import node failed", "target", edge.TargetName, "error", err)
						continue
					}
					keyToID[nodeKey(filePath, moduleName)] = targetID
					nameToIDs[moduleName] = append(nameToIDs[moduleName], targetID)
				}
				appendEdge(sourceID, targetID, edge.Kind)
				continue
			}

			sourceID, ok := resolveNodeName(filePath, edge.SourceName, keyToID, nameToIDs)
			if !ok {
				continue
			}
			targetID, ok := resolveEdgeTarget(filePath, edge.TargetName)
			if !ok {
				continue
			}
			appendEdge(sourceID, targetID, edge.Kind)
		}
	}

	// Resolve type references through the same exact/alias/placeholder policy as
	// call edges. This keeps incremental replacement in parity with atomic full
	// publication, including explicit ambiguity for nested same-name classes.
	for filePath, symbols := range symbolsByFile {
		for _, sym := range symbols {
			sourceID := keyToID[nodeKey(filePath, sym.Name)]
			if sourceID == 0 {
				continue
			}
			for _, expression := range []string{sym.ReturnType, sym.Params} {
				for _, typeName := range extractTypeNames(expression) {
					if typeName == sym.Name {
						continue
					}
					targetID, ok := resolveEdgeTarget(filePath, typeName)
					if ok {
						appendEdge(sourceID, targetID, EdgeUsesType)
					}
				}
			}
		}
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

	// Build a name-only lookup for type resolution (type names don't carry file paths).
	// Repository-wide duplicate type names remain ambiguous.
	nameToKey := make(map[string]string)
	ambiguous := make(map[string]bool)
	for key := range keyToID {
		// key format is "filePath\x00name"
		idx := strings.Index(key, "\x00")
		if idx < 0 {
			continue
		}
		name := key[idx+1:]
		if _, exists := nameToKey[name]; exists {
			ambiguous[name] = true
			continue
		}
		nameToKey[name] = key
	}
	for name := range ambiguous {
		delete(nameToKey, name)
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
