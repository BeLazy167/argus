// Package graph — indexer_integration_test.go exercises the IO shell of
// the hash-gated per-file diff (indexParsedSymbols). The pure decision
// core (planSymbolDiff) is already covered exhaustively in
// indexer_test.go; what this file protects is the wiring between
// planSymbolDiff's output and the indexerStore methods — i.e. the shape
// "did phase 2 actually call UpsertCodeNodeFullWithHash the right number
// of times, with the right hash, and did phase 3 sweep the correct IDs?"
//
// No database is touched. fakeIndexerStore records every call into flat
// slices; tests assert on counts + arguments. Single-threaded by design
// (indexParsedSymbols is strictly sequential), so no mutex is needed.
package graph

import (
	"context"
	"slices"
	"testing"

	"github.com/BeLazy167/argus/backend/internal/store"
)

// fakeIndexerStore records every call the indexer makes. It seeds
// GetNodesHashesForFile via hashesByFile so each scenario can model
// "what's already in the DB" without a live store. newID monotonically
// allocates IDs for upserts — IDs are only meaningful relative to the
// test, never compared across tests.
//
// Single-threaded: indexParsedSymbols walks files sequentially and does
// not spawn goroutines, so no mutex is required. If that ever changes,
// the -race run in CI will catch it.
type fakeIndexerStore struct {
	hashesByFile map[string][]store.NodeHashRow

	// Recorded calls, in invocation order, per method.
	getHashesCalls []getHashesCall
	upsertFull     []upsertFullCall
	upsertPlain    []upsertPlainCall
	upsertEdges    []upsertEdgeCall
	deletes        []deleteCall

	// API-endpoint side of indexerStore. endpointRows is the fake's whole
	// api_endpoints table, keyed by file, and installEndpoints is what a
	// sibling repo in the same installation already has — the two together
	// let a test drive the cross-repo linking pass with no database.
	endpointRows     map[string][]store.APIEndpointRow
	installEndpoints []store.APIEndpointRow
	inferredEdges    []upsertEdgeCall
	// nodeIDsByName is what the DB already knows about symbols this run did
	// not parse — the fallback an incremental index depends on.
	nodeIDsByName map[string]int64
	// linkCalls counts how often the installation-wide re-derivation ran, so a
	// test can assert the change gate actually gates.
	linkCalls int

	// nextID feeds monotonically increasing IDs to upsert returns.
	// Seeded high enough that it cannot collide with seeded existing IDs.
	nextID int64
}

type getHashesCall struct {
	repoID   int64
	filePath string
}

type upsertFullCall struct {
	repoID      int64
	kind        string
	name        string
	filePath    string
	lineStart   int
	lineEnd     int
	prNumber    int
	contentHash string
}

type upsertPlainCall struct {
	id       int64
	repoID   int64
	kind     string
	name     string
	filePath string
}

type upsertEdgeCall struct {
	repoID   int64
	sourceID int64
	targetID int64
	kind     string
}

type deleteCall struct {
	repoID int64
	ids    []int64
}

func newFakeIndexerStore() *fakeIndexerStore {
	return &fakeIndexerStore{
		hashesByFile: map[string][]store.NodeHashRow{},
		endpointRows: map[string][]store.APIEndpointRow{},
		nextID:       1000,
	}
}

// ReplaceAPIEndpointsForFiles models the real store's equality gate: it reports
// changed only when the rows for the visited files actually differ.
func (f *fakeIndexerStore) ReplaceAPIEndpointsForFiles(_ context.Context, _ int64, filePaths []string, rows []store.APIEndpointRow) (bool, error) {
	next := map[string][]store.APIEndpointRow{}
	for _, p := range filePaths {
		next[p] = nil
	}
	for _, r := range rows {
		next[r.FilePath] = append(next[r.FilePath], r)
	}
	changed := false
	for _, p := range filePaths {
		if !slices.Equal(f.endpointRows[p], next[p]) {
			changed = true
		}
		f.endpointRows[p] = next[p]
	}
	return changed, nil
}

func (f *fakeIndexerStore) LookupCodeNodeIDsByName(_ context.Context, _ int64, names []string) (map[string]int64, error) {
	out := map[string]int64{}
	for _, n := range names {
		if id, ok := f.nodeIDsByName[n]; ok {
			out[n] = id
		}
	}
	return out, nil
}

// ListAPIEndpointsForInstallationOf returns what this repo just wrote plus what
// the fake was seeded with for sibling repos — the same union the real query
// produces from the installation join.
func (f *fakeIndexerStore) ListAPIEndpointsForInstallationOf(_ context.Context, _ int64) ([]store.APIEndpointRow, bool, error) {
	out := append([]store.APIEndpointRow{}, f.installEndpoints...)
	for _, rows := range f.endpointRows {
		out = append(out, rows...)
	}
	return out, false, nil
}

func (f *fakeIndexerStore) ReplaceInferredAPIEdges(_ context.Context, _ int64, edges []store.InferredEdgeRow) (int, error) {
	f.linkCalls++
	f.inferredEdges = nil
	for _, e := range edges {
		f.inferredEdges = append(f.inferredEdges, upsertEdgeCall{repoID: e.RepoID, sourceID: e.SourceID, targetID: e.TargetID, kind: e.Kind})
	}
	return len(edges), nil
}

func (f *fakeIndexerStore) GetNodesHashesForFile(_ context.Context, repoID int64, filePath string) ([]store.NodeHashRow, error) {
	f.getHashesCalls = append(f.getHashesCalls, getHashesCall{repoID: repoID, filePath: filePath})
	rows, ok := f.hashesByFile[filePath]
	if !ok {
		// Mirror the real store's contract: empty slice, no error, when
		// the file has no existing rows. Returning nil would also work
		// because planSymbolDiff tolerates it — we pick the stricter
		// shape so the fake is a faithful stand-in.
		return []store.NodeHashRow{}, nil
	}
	return rows, nil
}

func (f *fakeIndexerStore) UpsertCodeNodeFullWithHash(_ context.Context, repoID int64, kind, name, filePath string, lineStart, lineEnd int, _ string, prNumber int, _, _, _ string, _ bool, _, _, contentHash string) (int64, error) {
	f.nextID++
	f.upsertFull = append(f.upsertFull, upsertFullCall{
		repoID: repoID, kind: kind, name: name, filePath: filePath,
		lineStart: lineStart, lineEnd: lineEnd, prNumber: prNumber,
		contentHash: contentHash,
	})
	return f.nextID, nil
}

func (f *fakeIndexerStore) UpsertCodeNode(_ context.Context, repoID int64, kind, name, filePath string, _, _ int, _ string, _ int) (int64, error) {
	f.nextID++
	f.upsertPlain = append(f.upsertPlain, upsertPlainCall{
		id: f.nextID, repoID: repoID, kind: kind, name: name, filePath: filePath,
	})
	return f.nextID, nil
}

func (f *fakeIndexerStore) UpsertCodeEdge(_ context.Context, repoID, sourceID, targetID int64, kind string) error {
	f.upsertEdges = append(f.upsertEdges, upsertEdgeCall{repoID: repoID, sourceID: sourceID, targetID: targetID, kind: kind})
	return nil
}

func (f *fakeIndexerStore) ReplaceCodeEdgesForFiles(_ context.Context, repoID int64, _ []string, edges []store.CodeEdgeRow) error {
	f.upsertEdges = nil
	for _, edge := range edges {
		f.upsertEdges = append(f.upsertEdges, upsertEdgeCall{repoID: repoID, sourceID: edge.SourceID, targetID: edge.TargetID, kind: edge.Kind})
	}
	return nil
}

func (f *fakeIndexerStore) DeleteNodesByIDs(_ context.Context, repoID int64, ids []int64) error {
	// Only record non-empty sweeps. The real store no-ops on empty, and a
	// test that asserts "0 deletes" should read 0 regardless of whether
	// indexParsedSymbols calls through with an empty slice.
	if len(ids) == 0 {
		return nil
	}
	// Copy the slice so a caller that later reuses the backing array
	// cannot mutate what we recorded.
	idsCopy := make([]int64, len(ids))
	copy(idsCopy, ids)
	f.deletes = append(f.deletes, deleteCall{repoID: repoID, ids: idsCopy})
	return nil
}

// TestIndexParsedSymbols_HashGatedDiff exercises the five transitions
// that the indexer has to get right between the planSymbolDiff output
// and the store calls. Each subtest constructs a fresh fake, seeds its
// DB-side state, runs indexParsedSymbols once, and asserts on the
// upsert/delete counts — not the edges, because edge resolution is
// covered in other files and is orthogonal to the diff invariant.
func TestIndexParsedSymbols_HashGatedDiff(t *testing.T) {
	const repoID int64 = 42
	const filePath = "a.go"

	// mkSym mirrors the factory used by TestPlanSymbolDiff so the hashes
	// this file produces line up with expectations an engineer would
	// form from reading the unit test. Keep the default fields in sync
	// if Symbol gains new fields.
	mkSym := func(name string, line int) Symbol {
		return Symbol{
			Kind: KindFunction, Name: name, FilePath: filePath,
			LineStart: line, LineEnd: line + 10,
			ReturnType: "error", Visibility: "exported", Scope: "package",
		}
	}
	hashOf := func(s Symbol) string { return computeSymbolHash(s) }

	foo := mkSym("Foo", 10)
	bar := mkSym("Bar", 30)
	baz := mkSym("Baz", 50)

	fooMovedLine := foo
	fooMovedLine.LineStart = 11
	fooMovedLine.LineEnd = 21

	// existingAllMatch simulates a DB already fully in sync with the
	// parsed set: same IDs, same hashes, same (kind, name) pairs. Used
	// as the zero-write baseline in scenario #2.
	existingAllMatch := []store.NodeHashRow{
		{ID: 1, Kind: KindFunction, Name: "Foo", ContentHash: hashOf(foo)},
		{ID: 2, Kind: KindFunction, Name: "Bar", ContentHash: hashOf(bar)},
	}

	tests := []struct {
		name          string
		parsed        []Symbol
		existing      []store.NodeHashRow // nil means "no row in hashesByFile"
		wantUpserts   int
		wantDeletes   int
		wantDeletedID int64 // 0 = don't assert
	}{
		{
			name:        "cold start: N symbols, empty DB → N upserts, 0 deletes",
			parsed:      []Symbol{foo, bar, baz},
			existing:    nil,
			wantUpserts: 3,
			wantDeletes: 0,
		},
		{
			name:        "steady state: hashes match → 0 upserts, 0 deletes",
			parsed:      []Symbol{foo, bar},
			existing:    existingAllMatch,
			wantUpserts: 0,
			wantDeletes: 0,
		},
		{
			name:        "one symbol moved: line drift → 1 upsert, 0 deletes",
			parsed:      []Symbol{fooMovedLine, bar},
			existing:    existingAllMatch,
			wantUpserts: 1,
			wantDeletes: 0,
		},
		{
			name:          "one symbol removed from parse → 0 upserts, 1 delete",
			parsed:        []Symbol{foo}, // Bar gone
			existing:      existingAllMatch,
			wantUpserts:   0,
			wantDeletes:   1,
			wantDeletedID: 2, // Bar's seeded ID
		},
		{
			name:        "one new symbol appended → 1 upsert, 0 deletes",
			parsed:      []Symbol{foo, bar, baz},
			existing:    existingAllMatch,
			wantUpserts: 1,
			wantDeletes: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := newFakeIndexerStore()
			if tc.existing != nil {
				st.hashesByFile[filePath] = tc.existing
			}

			// indexParsedSymbols expects pre-parsed results keyed by
			// file. Edges are empty here — this test pins the node diff
			// behavior, not edge resolution.
			results := map[string]fileResult{
				filePath: {symbols: tc.parsed, edges: nil},
			}

			if err := indexParsedSymbols(context.Background(), st, repoID, results); err != nil {
				t.Fatalf("indexParsedSymbols: unexpected error: %v", err)
			}

			if got := len(st.upsertFull); got != tc.wantUpserts {
				t.Fatalf("upsert count: got %d, want %d (calls=%+v)", got, tc.wantUpserts, st.upsertFull)
			}
			if got := len(st.deletes); got != tc.wantDeletes {
				t.Fatalf("delete count: got %d, want %d (calls=%+v)", got, tc.wantDeletes, st.deletes)
			}
			if tc.wantDeletedID != 0 {
				if len(st.deletes) != 1 || len(st.deletes[0].ids) != 1 || st.deletes[0].ids[0] != tc.wantDeletedID {
					t.Fatalf("deleted ID: want [%d], got %+v", tc.wantDeletedID, st.deletes)
				}
			}

			// Structural check: every upsert call must pass a non-empty
			// content hash. A regression where the indexer forgets to
			// thread the hash would otherwise pass the count assertions
			// but leave the DB permanently stuck on the Changed path.
			for i, call := range st.upsertFull {
				if call.contentHash == "" {
					t.Fatalf("upsert[%d] missing content hash: %+v", i, call)
				}
				if call.repoID != repoID {
					t.Fatalf("upsert[%d] repoID: got %d, want %d", i, call.repoID, repoID)
				}
			}

			// GetNodesHashesForFile must be called exactly once per
			// file; anything else means we duplicated the SELECT, which
			// would cost real DB round-trips on hot paths.
			if got := len(st.getHashesCalls); got != 1 {
				t.Fatalf("GetNodesHashesForFile calls: got %d, want 1 (%+v)", got, st.getHashesCalls)
			}
		})
	}
}

// Static check: the real *store.Store must satisfy indexerStore. If this
// ever fails to compile, we have a method-signature drift between the
// two; having it here points at interface drift in one line instead of
// burying the error inside the symbol-diff helpers.
var _ indexerStore = (*store.Store)(nil)

func TestIndexParsedSymbolsReplacesEdgeSnapshotIncludingEmpty(t *testing.T) {
	const repoID int64 = 42
	foo := Symbol{Kind: KindFunction, Name: "Foo", FilePath: "a.go", LineStart: 1, LineEnd: 4}
	bar := Symbol{Kind: KindFunction, Name: "Bar", FilePath: "a.go", LineStart: 6, LineEnd: 9}

	tests := []struct {
		name      string
		edges     []Edge
		wantEdges int
	}{
		{name: "current call is written", edges: []Edge{{SourceName: "Foo", TargetName: "Bar", Kind: "calls"}}, wantEdges: 1},
		{name: "zero-edge snapshot clears removed call", edges: nil, wantEdges: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := newFakeIndexerStore()
			st.upsertEdges = []upsertEdgeCall{{repoID: repoID, sourceID: 1, targetID: 2, kind: "calls"}}
			err := indexParsedSymbols(context.Background(), st, repoID, map[string]fileResult{
				"a.go": {symbols: []Symbol{foo, bar}, edges: tt.edges},
			})
			if err != nil {
				t.Fatalf("indexParsedSymbols: %v", err)
			}
			if got := len(st.upsertEdges); got != tt.wantEdges {
				t.Fatalf("replacement edge count = %d, want %d (%+v)", got, tt.wantEdges, st.upsertEdges)
			}
		})
	}
}

func TestIndexParsedSymbolsResolvesEdgeTargetFromExistingRepoGraph(t *testing.T) {
	const repoID int64 = 42
	st := newFakeIndexerStore()
	st.nodeIDsByName = map[string]int64{"UntouchedTarget": 77}

	err := indexParsedSymbols(context.Background(), st, repoID, map[string]fileResult{
		"changed.go": {
			symbols: []Symbol{{Kind: KindFunction, Name: "ChangedCaller", FilePath: "changed.go", LineStart: 1, LineEnd: 4}},
			edges:   []Edge{{SourceName: "ChangedCaller", TargetName: "UntouchedTarget", Kind: "calls"}},
		},
	})
	if err != nil {
		t.Fatalf("indexParsedSymbols: %v", err)
	}
	if len(st.upsertEdges) != 1 {
		t.Fatalf("replacement edges = %+v, want one cross-file call", st.upsertEdges)
	}
	if got := st.upsertEdges[0].targetID; got != 77 {
		t.Fatalf("target id = %d, want existing repo node 77", got)
	}
}

func TestIndexParsedSymbolsResolvesTypeTargetFromExistingRepoGraph(t *testing.T) {
	const repoID int64 = 42
	st := newFakeIndexerStore()
	st.nodeIDsByName = map[string]int64{"UntouchedType": 88}

	err := indexParsedSymbols(context.Background(), st, repoID, map[string]fileResult{
		"changed.go": {
			symbols: []Symbol{{
				Kind: KindFunction, Name: "ChangedCaller", FilePath: "changed.go",
				LineStart: 1, LineEnd: 4, ReturnType: "*UntouchedType",
			}},
		},
	})
	if err != nil {
		t.Fatalf("indexParsedSymbols: %v", err)
	}
	if len(st.upsertEdges) != 1 {
		t.Fatalf("replacement edges = %+v, want one cross-file type edge", st.upsertEdges)
	}
	if got := st.upsertEdges[0]; got.targetID != 88 || got.kind != "uses_type" {
		t.Fatalf("edge = %+v, want target 88 uses_type", got)
	}
}

func TestIndexParsedSymbols_RecordsAmbiguousAndUnresolvedTargets(t *testing.T) {
	const repoID int64 = 42
	st := newFakeIndexerStore()
	results := map[string]fileResult{
		"caller.go": {
			symbols: []Symbol{{Kind: KindFunction, Name: "Caller", FilePath: "caller.go", LineStart: 1, LineEnd: 5}},
			edges: []Edge{
				{SourceName: "Caller", TargetName: "Shared", Kind: EdgeCalls},
				{SourceName: "Caller", TargetName: "Missing", Kind: EdgeCalls},
			},
		},
		"a/shared.go": {symbols: []Symbol{{Kind: KindFunction, Name: "Shared", FilePath: "a/shared.go", LineStart: 1, LineEnd: 2}}},
		"b/shared.go": {symbols: []Symbol{{Kind: KindFunction, Name: "Shared", FilePath: "b/shared.go", LineStart: 1, LineEnd: 2}}},
	}

	if err := indexParsedSymbols(context.Background(), st, repoID, results); err != nil {
		t.Fatalf("indexParsedSymbols: %v", err)
	}

	gotNames := make([]string, 0, len(st.upsertPlain))
	placeholderIDs := map[int64]bool{}
	for _, call := range st.upsertPlain {
		gotNames = append(gotNames, call.name)
		placeholderIDs[call.id] = true
		if call.kind != "module" || call.filePath != "caller.go" {
			t.Fatalf("placeholder %+v is not an explicit caller-scoped module", call)
		}
	}
	slices.Sort(gotNames)
	wantNames := []string{"ambiguous:Shared", "unresolved:Missing"}
	if !slices.Equal(gotNames, wantNames) {
		t.Fatalf("placeholder names = %v, want %v", gotNames, wantNames)
	}
	if len(st.upsertEdges) != 2 {
		t.Fatalf("edges = %+v, want two explicit placeholder edges", st.upsertEdges)
	}
	for _, edge := range st.upsertEdges {
		if !placeholderIDs[edge.targetID] {
			t.Fatalf("edge arbitrarily selected a same-name target instead of a placeholder: %+v", edge)
		}
	}
}

func TestIndexParsedSymbols_ImportUsesFileAndQualifiedModuleIdentity(t *testing.T) {
	st := newFakeIndexerStore()
	results := map[string]fileResult{
		"a.go": {
			symbols: []Symbol{
				{Kind: KindFunction, Name: "A", FilePath: "a.go", LineStart: 1, LineEnd: 3},
				{Kind: "file", Name: "a.go", FilePath: "a.go", LineStart: 1, LineEnd: 10},
			},
			edges: []Edge{{SourceName: "a.go", TargetName: "example.com/acme/lib", Kind: EdgeImports}},
		},
	}
	if err := indexParsedSymbols(context.Background(), st, 42, results); err != nil {
		t.Fatalf("indexParsedSymbols: %v", err)
	}
	if len(st.upsertPlain) != 1 || st.upsertPlain[0].name != "module:example.com/acme/lib" {
		t.Fatalf("module identity = %+v", st.upsertPlain)
	}
	if len(st.upsertEdges) != 1 || st.upsertEdges[0].sourceID != 1002 || st.upsertEdges[0].targetID != st.upsertPlain[0].id {
		t.Fatalf("import did not use file -> module identities: full=%+v plain=%+v edges=%+v", st.upsertFull, st.upsertPlain, st.upsertEdges)
	}
}
