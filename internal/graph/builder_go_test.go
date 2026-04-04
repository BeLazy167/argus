package graph

import (
	"sort"
	"testing"
)

func TestParseGoAST(t *testing.T) {
	tests := []struct {
		name       string
		src        string
		wantSyms   []Symbol
		wantEdges  []Edge
	}{
		{
			name: "simple function",
			src: `package main

func Hello(name string) string {
	return "hello " + name
}
`,
			wantSyms: []Symbol{
				{Kind: "function", Name: "Hello", FilePath: "test.go", LineStart: 3, LineEnd: 5, Params: "(name string)", ReturnType: "string", Visibility: "exported", Scope: "package"},
			},
		},
		{
			name: "method with receiver",
			src: `package main

type Server struct{}

func (s *Server) Start(addr string) error {
	return nil
}
`,
			wantSyms: []Symbol{
				{Kind: "type", Name: "Server", FilePath: "test.go", LineStart: 3, LineEnd: 3, Visibility: "exported", Scope: "package"},
				{Kind: "method", Name: "Start", FilePath: "test.go", LineStart: 5, LineEnd: 7, Params: "(addr string)", ReturnType: "error", Visibility: "exported", Receiver: "*Server", Scope: "method"},
			},
		},
		{
			name: "struct",
			src: `package main

type Config struct {
	Host string
	Port int
}
`,
			wantSyms: []Symbol{
				{Kind: "type", Name: "Config", FilePath: "test.go", LineStart: 3, LineEnd: 6, Visibility: "exported", Scope: "package"},
			},
		},
		{
			name: "interface",
			src: `package main

type Reader interface {
	Read(p []byte) (int, error)
}
`,
			wantSyms: []Symbol{
				{Kind: "interface", Name: "Reader", FilePath: "test.go", LineStart: 3, LineEnd: 5, Visibility: "exported", Scope: "package"},
			},
		},
		{
			name: "multiple return values",
			src: `package main

func Parse(input string) (int, error) {
	return 0, nil
}
`,
			wantSyms: []Symbol{
				{Kind: "function", Name: "Parse", FilePath: "test.go", LineStart: 3, LineEnd: 5, Params: "(input string)", ReturnType: "(int, error)", Visibility: "exported", Scope: "package"},
			},
		},
		{
			name: "unexported symbol",
			src: `package main

func helper() {
}
`,
			wantSyms: []Symbol{
				{Kind: "function", Name: "helper", FilePath: "test.go", LineStart: 3, LineEnd: 4, Params: "()", Visibility: "unexported", Scope: "package"},
			},
		},
		{
			name: "call edges",
			src: `package main

import "fmt"

func greet(name string) {
	fmt.Println(name)
	helper()
}

func helper() {}
`,
			wantEdges: []Edge{
				{SourceName: "test.go", TargetName: "fmt", Kind: "imports"},
				{SourceName: "greet", TargetName: "fmt.Println", Kind: "calls"},
				{SourceName: "greet", TargetName: "helper", Kind: "calls"},
			},
		},
		{
			name: "import edges",
			src: `package main

import (
	"context"
	"fmt"
)

func main() {}
`,
			wantEdges: []Edge{
				{SourceName: "test.go", TargetName: "context", Kind: "imports"},
				{SourceName: "test.go", TargetName: "fmt", Kind: "imports"},
			},
		},
		{
			name: "no params no return",
			src: `package main

func Run() {
}
`,
			wantSyms: []Symbol{
				{Kind: "function", Name: "Run", FilePath: "test.go", LineStart: 3, LineEnd: 4, Params: "()", Visibility: "exported", Scope: "package"},
			},
		},
		{
			name: "value receiver",
			src: `package main

type Foo struct{}

func (f Foo) Bar() int {
	return 0
}
`,
			wantSyms: []Symbol{
				{Kind: "type", Name: "Foo", FilePath: "test.go", LineStart: 3, LineEnd: 3, Visibility: "exported", Scope: "package"},
				{Kind: "method", Name: "Bar", FilePath: "test.go", LineStart: 5, LineEnd: 7, Params: "()", ReturnType: "int", Visibility: "exported", Receiver: "Foo", Scope: "method"},
			},
		},
		{
			name: "fallback on invalid source",
			src:  `this is not valid go!!!`,
			// should return nil, nil (parser fails => fallback)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			syms, edges := parseGoAST("test.go", tt.src)

			if tt.wantSyms == nil && tt.wantEdges == nil {
				if syms != nil {
					t.Fatalf("expected nil syms for invalid source, got %d", len(syms))
				}
				return
			}

			if tt.wantSyms != nil {
				if len(syms) != len(tt.wantSyms) {
					t.Fatalf("symbols: got %d, want %d\ngot: %+v", len(syms), len(tt.wantSyms), syms)
				}
				for i, want := range tt.wantSyms {
					got := syms[i]
					if got.Kind != want.Kind {
						t.Errorf("sym[%d].Kind = %q, want %q", i, got.Kind, want.Kind)
					}
					if got.Name != want.Name {
						t.Errorf("sym[%d].Name = %q, want %q", i, got.Name, want.Name)
					}
					if got.FilePath != want.FilePath {
						t.Errorf("sym[%d].FilePath = %q, want %q", i, got.FilePath, want.FilePath)
					}
					if got.LineStart != want.LineStart {
						t.Errorf("sym[%d].LineStart = %d, want %d", i, got.LineStart, want.LineStart)
					}
					if got.LineEnd != want.LineEnd {
						t.Errorf("sym[%d].LineEnd = %d, want %d", i, got.LineEnd, want.LineEnd)
					}
					if got.ReturnType != want.ReturnType {
						t.Errorf("sym[%d].ReturnType = %q, want %q", i, got.ReturnType, want.ReturnType)
					}
					if got.Params != want.Params {
						t.Errorf("sym[%d].Params = %q, want %q", i, got.Params, want.Params)
					}
					if got.Visibility != want.Visibility {
						t.Errorf("sym[%d].Visibility = %q, want %q", i, got.Visibility, want.Visibility)
					}
					if got.Receiver != want.Receiver {
						t.Errorf("sym[%d].Receiver = %q, want %q", i, got.Receiver, want.Receiver)
					}
					if got.Scope != want.Scope {
						t.Errorf("sym[%d].Scope = %q, want %q", i, got.Scope, want.Scope)
					}
					if got.IsAsync != want.IsAsync {
						t.Errorf("sym[%d].IsAsync = %v, want %v", i, got.IsAsync, want.IsAsync)
					}
				}
			}

			if tt.wantEdges != nil {
				// Sort both for stable comparison
				sortEdges(edges)
				sortEdges(tt.wantEdges)

				if len(edges) != len(tt.wantEdges) {
					t.Fatalf("edges: got %d, want %d\ngot: %+v", len(edges), len(tt.wantEdges), edges)
				}
				for i, want := range tt.wantEdges {
					got := edges[i]
					if got.SourceName != want.SourceName || got.TargetName != want.TargetName || got.Kind != want.Kind {
						t.Errorf("edge[%d] = %+v, want %+v", i, got, want)
					}
				}
			}
		})
	}
}

func sortEdges(edges []Edge) {
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].Kind != edges[j].Kind {
			return edges[i].Kind < edges[j].Kind
		}
		if edges[i].SourceName != edges[j].SourceName {
			return edges[i].SourceName < edges[j].SourceName
		}
		return edges[i].TargetName < edges[j].TargetName
	})
}

func TestParseGoASTIntegration(t *testing.T) {
	src := `package service

import (
	"context"
	"fmt"
)

type UserService struct {
	db Database
}

type Database interface {
	Query(ctx context.Context, q string) error
}

func NewUserService(db Database) *UserService {
	return &UserService{db: db}
}

func (s *UserService) GetUser(ctx context.Context, id int64) (string, error) {
	err := s.db.Query(ctx, fmt.Sprintf("SELECT * FROM users WHERE id = %d", id))
	if err != nil {
		return "", err
	}
	return "user", nil
}

func helper() {
	fmt.Println("internal")
}
`

	syms, edges := parseGoAST("service.go", src)
	if syms == nil {
		t.Fatal("expected symbols, got nil")
	}

	symMap := make(map[string]Symbol)
	for _, s := range syms {
		symMap[s.Name] = s
	}

	// Verify UserService struct
	us, ok := symMap["UserService"]
	if !ok {
		t.Fatal("missing UserService symbol")
	}
	if us.Kind != "type" || us.Visibility != "exported" {
		t.Errorf("UserService: kind=%q vis=%q", us.Kind, us.Visibility)
	}

	// Verify Database interface
	db, ok := symMap["Database"]
	if !ok {
		t.Fatal("missing Database symbol")
	}
	if db.Kind != "interface" {
		t.Errorf("Database: kind=%q, want interface", db.Kind)
	}

	// Verify NewUserService function
	nus, ok := symMap["NewUserService"]
	if !ok {
		t.Fatal("missing NewUserService symbol")
	}
	if nus.Kind != "function" || nus.Params != "(db Database)" || nus.ReturnType != "*UserService" {
		t.Errorf("NewUserService: kind=%q params=%q ret=%q", nus.Kind, nus.Params, nus.ReturnType)
	}

	// Verify GetUser method
	gu, ok := symMap["GetUser"]
	if !ok {
		t.Fatal("missing GetUser symbol")
	}
	if gu.Kind != "method" || gu.Receiver != "*UserService" || gu.ReturnType != "(string, error)" {
		t.Errorf("GetUser: kind=%q recv=%q ret=%q", gu.Kind, gu.Receiver, gu.ReturnType)
	}

	// Verify helper is unexported
	h, ok := symMap["helper"]
	if !ok {
		t.Fatal("missing helper symbol")
	}
	if h.Visibility != "unexported" {
		t.Errorf("helper.Visibility = %q, want unexported", h.Visibility)
	}

	// Verify import edges
	importCount := 0
	for _, e := range edges {
		if e.Kind == "imports" {
			importCount++
		}
	}
	if importCount != 2 {
		t.Errorf("import edges: got %d, want 2", importCount)
	}

	// Verify call edges exist for GetUser
	callMap := make(map[string]bool)
	for _, e := range edges {
		if e.Kind == "calls" && e.SourceName == "GetUser" {
			callMap[e.TargetName] = true
		}
	}
	if !callMap["Query"] {
		t.Error("missing call edge GetUser -> Query")
	}
	if !callMap["fmt.Sprintf"] {
		t.Error("missing call edge GetUser -> fmt.Sprintf")
	}
}
