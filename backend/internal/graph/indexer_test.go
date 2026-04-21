package graph

import (
	"sort"
	"testing"

	"github.com/BeLazy167/argus/backend/internal/store"
)

// BenchmarkComputeSymbolHash guards the zero-allocation contract of the
// hot-path hash function. A regression in allocations (e.g. someone
// replacing the append buffer with a strings.Builder or per-field
// []byte(s) conversions) will surface as >1 alloc/op. Run with:
//
//	go test -bench=BenchmarkComputeSymbolHash -benchmem ./internal/graph/
func BenchmarkComputeSymbolHash(b *testing.B) {
	sym := Symbol{
		Kind: KindMethod, Name: "HandleUpdate", FilePath: "internal/api/server.go",
		LineStart: 128, LineEnd: 176,
		ReturnType: "(*Response, error)", Params: "(ctx context.Context, req *UpdateRequest)",
		Visibility: "exported", IsAsync: false, Receiver: "*Server", Scope: "method",
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = computeSymbolHash(sym)
	}
}

// TestPlanSymbolDiff exercises the pure decision core of the hash-gated
// diff. Five scenarios cover the 4 meaningful state transitions plus the
// pre-migration (empty hash) case that must NEVER short-circuit to
// "unchanged" — an empty stored hash is a legacy row, not proof the
// content matches.
func TestPlanSymbolDiff(t *testing.T) {
	// Stable pair of symbols across subtests. Using distinct (kind, name)
	// keeps diff keys unambiguous; each subtest mutates inputs locally.
	mkSym := func(name string, line int) Symbol {
		return Symbol{
			Kind: KindFunction, Name: name, FilePath: "a.go",
			LineStart: line, LineEnd: line + 10,
			ReturnType: "error", Visibility: "exported", Scope: "package",
		}
	}
	hashOf := func(s Symbol) string { return computeSymbolHash(s) }

	foo := mkSym("Foo", 10)
	bar := mkSym("Bar", 30)
	fooMoved := foo
	fooMoved.LineStart = 11
	fooMoved.LineEnd = 21

	tests := []struct {
		name            string
		parsed          []Symbol
		existing        []store.NodeHashRow
		wantUnchanged   int
		wantChangedNames []string
		wantOrphanIDs   []int64
	}{
		{
			name:            "empty DB → everything changed, no orphans",
			parsed:          []Symbol{foo, bar},
			existing:        nil,
			wantUnchanged:   0,
			wantChangedNames: []string{"Foo", "Bar"},
			wantOrphanIDs:   nil,
		},
		{
			name:   "all hashes match → all unchanged, zero writes",
			parsed: []Symbol{foo, bar},
			existing: []store.NodeHashRow{
				{ID: 1, Kind: KindFunction, Name: "Foo", ContentHash: hashOf(foo)},
				{ID: 2, Kind: KindFunction, Name: "Bar", ContentHash: hashOf(bar)},
			},
			wantUnchanged:   2,
			wantChangedNames: []string{},
			wantOrphanIDs:   nil,
		},
		{
			name:   "one hash drifted → that symbol upserts, other stays",
			parsed: []Symbol{fooMoved, bar},
			existing: []store.NodeHashRow{
				{ID: 1, Kind: KindFunction, Name: "Foo", ContentHash: hashOf(foo)},
				{ID: 2, Kind: KindFunction, Name: "Bar", ContentHash: hashOf(bar)},
			},
			wantUnchanged:   1,
			wantChangedNames: []string{"Foo"},
			wantOrphanIDs:   nil,
		},
		{
			name:   "symbol removed from parse → listed as orphan",
			parsed: []Symbol{foo},
			existing: []store.NodeHashRow{
				{ID: 1, Kind: KindFunction, Name: "Foo", ContentHash: hashOf(foo)},
				{ID: 2, Kind: KindFunction, Name: "Bar", ContentHash: hashOf(bar)},
			},
			wantUnchanged:   1,
			wantChangedNames: []string{},
			wantOrphanIDs:   []int64{2},
		},
		{
			name:   "empty stored hash forces re-upsert (pre-migration row)",
			parsed: []Symbol{foo},
			existing: []store.NodeHashRow{
				{ID: 1, Kind: KindFunction, Name: "Foo", ContentHash: ""},
			},
			wantUnchanged:   0,
			wantChangedNames: []string{"Foo"},
			wantOrphanIDs:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := planSymbolDiff(tt.parsed, tt.existing)
			if len(plan.Unchanged) != tt.wantUnchanged {
				t.Errorf("Unchanged: got %d, want %d", len(plan.Unchanged), tt.wantUnchanged)
			}
			gotNames := make([]string, len(plan.Changed))
			for i, s := range plan.Changed {
				gotNames[i] = s.Name
			}
			sort.Strings(gotNames)
			wantNames := append([]string(nil), tt.wantChangedNames...)
			sort.Strings(wantNames)
			if !stringSliceEqual(gotNames, wantNames) {
				t.Errorf("Changed names: got %v, want %v", gotNames, wantNames)
			}
			gotOrphans := append([]int64(nil), plan.Orphans...)
			sort.Slice(gotOrphans, func(i, j int) bool { return gotOrphans[i] < gotOrphans[j] })
			wantOrphans := append([]int64(nil), tt.wantOrphanIDs...)
			sort.Slice(wantOrphans, func(i, j int) bool { return wantOrphans[i] < wantOrphans[j] })
			if !int64SliceEqual(gotOrphans, wantOrphans) {
				t.Errorf("Orphans: got %v, want %v", gotOrphans, wantOrphans)
			}
		})
	}
}

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func int64SliceEqual(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestComputeSymbolHashStable pins the contract that a given Symbol value
// always hashes to the same string. A regression here (e.g. the function
// accidentally depending on map iteration or a clock) would defeat the
// whole no-op-upsert optimization silently — every run would look like a
// "changed" symbol.
func TestComputeSymbolHashStable(t *testing.T) {
	sym := Symbol{
		Kind: KindFunction, Name: "HandleRequest", FilePath: "api/handler.go",
		LineStart: 42, LineEnd: 77,
		ReturnType: "(error)", Params: "(ctx context.Context, r *Request)",
		Visibility: "exported", IsAsync: false, Receiver: "", Scope: "package",
	}
	want := computeSymbolHash(sym)
	for i := 0; i < 5; i++ {
		got := computeSymbolHash(sym)
		if got != want {
			t.Fatalf("iteration %d: hash drifted\n  want %s\n  got  %s", i, want, got)
		}
	}
}

// TestComputeSymbolHashFieldChangesFlipHash asserts that every column the
// indexer persists on code_nodes is mixed into the hash. If an attribute
// changes in the DB but not in the hash input, the diff pass would read
// "unchanged" and never upsert — leaving stale data around. The table
// drives one mutation per hashed field plus a cross-check that the Symbol
// columns NOT persisted (FilePath — implicit in the per-file SELECT;
// ParamCount — derived, not stored) deliberately do NOT affect the hash.
func TestComputeSymbolHashFieldChangesFlipHash(t *testing.T) {
	base := Symbol{
		Kind: KindMethod, Name: "Save", FilePath: "store/user.go",
		LineStart: 10, LineEnd: 20,
		ReturnType: "error", Params: "(ctx context.Context)",
		Visibility: "exported", IsAsync: false, Receiver: "*User", Scope: "method",
	}
	baseHash := computeSymbolHash(base)

	mutations := []struct {
		name    string
		mutate  func(*Symbol)
		mustFlip bool
	}{
		{"kind", func(s *Symbol) { s.Kind = KindFunction }, true},
		{"name", func(s *Symbol) { s.Name = "Save2" }, true},
		{"line_start", func(s *Symbol) { s.LineStart = 11 }, true},
		{"line_end", func(s *Symbol) { s.LineEnd = 21 }, true},
		{"return_type", func(s *Symbol) { s.ReturnType = "(int, error)" }, true},
		{"params", func(s *Symbol) { s.Params = "(ctx context.Context, id int64)" }, true},
		{"visibility", func(s *Symbol) { s.Visibility = "unexported" }, true},
		{"is_async", func(s *Symbol) { s.IsAsync = true }, true},
		{"receiver", func(s *Symbol) { s.Receiver = "User" }, true},
		{"scope", func(s *Symbol) { s.Scope = "nested" }, true},

		// FilePath is intentionally NOT part of the hash — the per-file
		// diff loop already scopes everything to a single file via the
		// SELECT. Including it would just make hashes gratuitously fatter.
		{"file_path (must NOT flip)", func(s *Symbol) { s.FilePath = "store/other.go" }, false},
		// ParamCount is derived from Params at parse time, not stored,
		// so it must not influence the hash or renaming a formal param
		// would spuriously mark the symbol changed.
		{"param_count (must NOT flip)", func(s *Symbol) { s.ParamCount = 99 }, false},
	}

	for _, m := range mutations {
		t.Run(m.name, func(t *testing.T) {
			mutated := base
			m.mutate(&mutated)
			got := computeSymbolHash(mutated)
			if m.mustFlip && got == baseHash {
				t.Fatalf("mutating %q did not change the hash — persisted column missed in computeSymbolHash", m.name)
			}
			if !m.mustFlip && got != baseHash {
				t.Fatalf("mutating %q flipped the hash but that field isn't persisted — remove from computeSymbolHash", m.name)
			}
		})
	}
}

func TestExtractTypeNames(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{name: "empty", input: "", want: nil},
		{name: "simple type", input: "Order", want: []string{"Order"}},
		{name: "pointer type", input: "*Order", want: []string{"Order"}},
		{name: "slice type", input: "[]Item", want: []string{"Item"}},
		{name: "tuple return", input: "(*Order, error)", want: []string{"Order"}},
		{name: "map type", input: "map[string]Config", want: []string{"Config"}},
		{name: "multiple types", input: "(ctx context.Context, items []Item)", want: []string{"ctx", "Context", "items", "Item"}},
		{name: "builtin only lowercase", input: "error", want: nil},
		{name: "int types", input: "(int, int64)", want: nil},
		{name: "mixed", input: "(*UserService, error)", want: []string{"UserService"}},
		{name: "chan type", input: "chan *Event", want: []string{"Event"}},
		{name: "dedup", input: "(Order, Order)", want: []string{"Order"}},
		{name: "unexported type", input: "myHandler", want: []string{"myHandler"}},
		{name: "python style", input: "user_service", want: []string{"user_service"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractTypeNames(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("extractTypeNames(%q) = %v, want %v", tt.input, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("extractTypeNames(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestResolveTypeEdges(t *testing.T) {
	// Build composite-key map from plain names, using a default file path
	const testFile = "test.go"
	makeKeyToID := func(names map[string]int64) map[string]int64 {
		m := make(map[string]int64, len(names))
		for name, id := range names {
			m[nodeKey(testFile, name)] = id
		}
		return m
	}

	plainNameToID := map[string]int64{
		"Order":       1,
		"Item":        2,
		"GetOrder":    3,
		"ListItems":   4,
		"Config":      5,
		"NewService":  6,
		"Context":     7,
		"UserService": 8,
	}

	tests := []struct {
		name    string
		symbols []Symbol
		want    []Edge
	}{
		{
			name: "return type creates edge",
			symbols: []Symbol{
				{Name: "GetOrder", FilePath: testFile, ReturnType: "(*Order, error)"},
			},
			want: []Edge{
				{SourceName: nodeKey(testFile, "GetOrder"), TargetName: nodeKey(testFile, "Order"), Kind: "uses_type"},
			},
		},
		{
			name: "params create edges",
			symbols: []Symbol{
				{Name: "ListItems", FilePath: testFile, Params: "(ctx Context, items []Item)"},
			},
			want: []Edge{
				{SourceName: nodeKey(testFile, "ListItems"), TargetName: nodeKey(testFile, "Context"), Kind: "uses_type"},
				{SourceName: nodeKey(testFile, "ListItems"), TargetName: nodeKey(testFile, "Item"), Kind: "uses_type"},
			},
		},
		{
			name: "both return and params",
			symbols: []Symbol{
				{Name: "NewService", FilePath: testFile, ReturnType: "*UserService", Params: "(cfg Config)"},
			},
			want: []Edge{
				{SourceName: nodeKey(testFile, "NewService"), TargetName: nodeKey(testFile, "UserService"), Kind: "uses_type"},
				{SourceName: nodeKey(testFile, "NewService"), TargetName: nodeKey(testFile, "Config"), Kind: "uses_type"},
			},
		},
		{
			name: "skip unknown types",
			symbols: []Symbol{
				{Name: "GetOrder", FilePath: testFile, ReturnType: "*Unknown"},
			},
			want: nil,
		},
		{
			name: "skip self reference",
			symbols: []Symbol{
				{Name: "Order", FilePath: testFile, ReturnType: "*Order"},
			},
			want: nil,
		},
		{
			name: "dedup across return and params",
			symbols: []Symbol{
				{Name: "GetOrder", FilePath: testFile, ReturnType: "*Order", Params: "(o *Order)"},
			},
			want: []Edge{
				{SourceName: nodeKey(testFile, "GetOrder"), TargetName: nodeKey(testFile, "Order"), Kind: "uses_type"},
			},
		},
		{
			name: "symbol not in keyToID skipped",
			symbols: []Symbol{
				{Name: "Missing", FilePath: testFile, ReturnType: "*Order"},
			},
			want: nil,
		},
		{
			name:    "empty symbols",
			symbols: nil,
			want:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keyToID := makeKeyToID(plainNameToID)
			got := resolveTypeEdges(tt.symbols, keyToID)

			if len(got) == 0 {
				got = nil
			}
			if len(tt.want) == 0 {
				tt.want = nil
			}

			// Sort for stable comparison
			sortEdgeSlice(got)
			sortEdgeSlice(tt.want)

			if len(got) != len(tt.want) {
				t.Fatalf("resolveTypeEdges() = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("edge[%d] = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func sortEdgeSlice(edges []Edge) {
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].SourceName != edges[j].SourceName {
			return edges[i].SourceName < edges[j].SourceName
		}
		return edges[i].TargetName < edges[j].TargetName
	})
}
