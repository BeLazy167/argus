package graph

import (
	"sort"
	"testing"
)

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
	nameToID := map[string]int64{
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
				{Name: "GetOrder", ReturnType: "(*Order, error)"},
			},
			want: []Edge{
				{SourceName: "GetOrder", TargetName: "Order", Kind: "uses_type"},
			},
		},
		{
			name: "params create edges",
			symbols: []Symbol{
				{Name: "ListItems", Params: "(ctx Context, items []Item)"},
			},
			want: []Edge{
				{SourceName: "ListItems", TargetName: "Context", Kind: "uses_type"},
				{SourceName: "ListItems", TargetName: "Item", Kind: "uses_type"},
			},
		},
		{
			name: "both return and params",
			symbols: []Symbol{
				{Name: "NewService", ReturnType: "*UserService", Params: "(cfg Config)"},
			},
			want: []Edge{
				{SourceName: "NewService", TargetName: "UserService", Kind: "uses_type"},
				{SourceName: "NewService", TargetName: "Config", Kind: "uses_type"},
			},
		},
		{
			name: "skip unknown types",
			symbols: []Symbol{
				{Name: "GetOrder", ReturnType: "*Unknown"},
			},
			want: nil,
		},
		{
			name: "skip self reference",
			symbols: []Symbol{
				{Name: "Order", ReturnType: "*Order"},
			},
			want: nil,
		},
		{
			name: "dedup across return and params",
			symbols: []Symbol{
				{Name: "GetOrder", ReturnType: "*Order", Params: "(o *Order)"},
			},
			want: []Edge{
				{SourceName: "GetOrder", TargetName: "Order", Kind: "uses_type"},
			},
		},
		{
			name: "symbol not in nameToID skipped",
			symbols: []Symbol{
				{Name: "Missing", ReturnType: "*Order"},
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
			got := resolveTypeEdges(tt.symbols, nameToID)

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
