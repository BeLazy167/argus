package graph

import "testing"

func hasCTags() bool {
	return ctagsBinary() != ""
}

func TestParseCTags_GracefulDegradation(t *testing.T) {
	// parseCTags should return nil, nil when ctags is not installed.
	// We can't truly mock exec.LookPath without build tags, so we just verify
	// that the function doesn't panic on valid input regardless.
	syms, edges := parseCTags("test.go", "package main\nfunc Hello() {}\n")
	if !hasCTags() {
		if syms != nil || edges != nil {
			t.Fatal("expected nil when ctags not installed")
		}
	}
	// If ctags IS installed, we just ensure no panic — integration tests below cover correctness.
	_ = syms
	_ = edges
}

func TestParseCTags_Go(t *testing.T) {
	if !hasCTags() {
		t.Skip("ctags not in PATH")
	}

	src := `package main

import "fmt"

type Server struct {
	addr string
}

func NewServer(addr string) *Server {
	return &Server{addr: addr}
}

func (s *Server) Start() error {
	fmt.Println("starting", s.addr)
	return nil
}
`
	syms, _ := parseCTags("main.go", src)
	if len(syms) == 0 {
		t.Fatal("expected symbols from Go source")
	}

	found := make(map[string]string)
	for _, s := range syms {
		found[s.Name] = s.Kind
	}

	if found["Server"] == "" {
		t.Error("missing Server symbol")
	}
	if found["NewServer"] != "function" {
		t.Errorf("NewServer kind=%q, want function", found["NewServer"])
	}
	if found["Start"] != "method" && found["Start"] != "function" {
		t.Errorf("Start kind=%q, want method or function", found["Start"])
	}
}

func TestParseCTags_Python(t *testing.T) {
	if !hasCTags() {
		t.Skip("ctags not in PATH")
	}

	src := `import os

class UserService:
    def __init__(self, db):
        self.db = db

    def get_user(self, user_id):
        return self.db.find(user_id)

def helper():
    pass
`
	syms, edges := parseCTags("service.py", src)
	if len(syms) == 0 {
		t.Fatal("expected symbols from Python source")
	}

	found := make(map[string]string)
	for _, s := range syms {
		found[s.Name] = s.Kind
	}

	if found["UserService"] != "class" {
		t.Errorf("UserService kind=%q, want class", found["UserService"])
	}
	if found["helper"] != "function" {
		t.Errorf("helper kind=%q, want function", found["helper"])
	}

	hasImport := false
	for _, e := range edges {
		if e.Kind == "imports" {
			hasImport = true
		}
	}
	if !hasImport {
		t.Log("no import edges found (ctags may not emit import tags for Python)")
	}
}

func TestParseCTags_Java(t *testing.T) {
	if !hasCTags() {
		t.Skip("ctags not in PATH")
	}

	src := `package com.example;

import java.util.List;

public class UserService {
    private final Database db;

    public UserService(Database db) {
        this.db = db;
    }

    public String getUser(int id) {
        return db.find(id);
    }
}

public interface Database {
    String find(int id);
}
`
	syms, _ := parseCTags("UserService.java", src)
	if len(syms) == 0 {
		t.Fatal("expected symbols from Java source")
	}

	found := make(map[string]string)
	for _, s := range syms {
		found[s.Name] = s.Kind
	}

	if found["UserService"] != "class" {
		t.Errorf("UserService kind=%q, want class", found["UserService"])
	}
	if found["Database"] != "interface" {
		t.Errorf("Database kind=%q, want interface", found["Database"])
	}
}

func TestParseCTags_Rust(t *testing.T) {
	if !hasCTags() {
		t.Skip("ctags not in PATH")
	}

	src := `use std::io;

pub struct Config {
    pub host: String,
    pub port: u16,
}

pub trait Service {
    fn start(&self) -> io::Result<()>;
}

impl Service for Config {
    fn start(&self) -> io::Result<()> {
        Ok(())
    }
}

pub fn create_config(host: &str, port: u16) -> Config {
    Config { host: host.to_string(), port }
}
`
	syms, _ := parseCTags("lib.rs", src)
	if len(syms) == 0 {
		t.Fatal("expected symbols from Rust source")
	}

	found := make(map[string]string)
	for _, s := range syms {
		found[s.Name] = s.Kind
	}

	if found["Config"] == "" {
		t.Error("missing Config symbol")
	}
	if found["Service"] == "" {
		t.Error("missing Service symbol")
	}
	if found["create_config"] != "function" {
		t.Errorf("create_config kind=%q, want function", found["create_config"])
	}
}

func TestMapCTagKind(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"function", "function"},
		{"func", "function"},
		{"method", "method"},
		{"class", "class"},
		{"struct", "type"},
		{"interface", "interface"},
		{"trait", "interface"},
		{"variable", ""},
		{"constant", ""},
		{"field", ""},
		{"member", ""},
		{"unknown_kind", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := mapCTagKind(tt.input)
			if got != tt.want {
				t.Errorf("mapCTagKind(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestCTagsVisibility(t *testing.T) {
	tests := []struct {
		name   string
		tag    ctagsTag
		want   string
	}{
		{"public access", ctagsTag{Name: "foo", Access: "public"}, "exported"},
		{"private access", ctagsTag{Name: "Foo", Access: "private"}, "unexported"},
		{"uppercase no access", ctagsTag{Name: "Foo"}, "exported"},
		{"lowercase no access", ctagsTag{Name: "foo"}, "unexported"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ctagsVisibility(tt.tag)
			if got != tt.want {
				t.Errorf("ctagsVisibility(%+v) = %q, want %q", tt.tag, got, tt.want)
			}
		})
	}
}

func TestMapCTagToSymbol_Import(t *testing.T) {
	tag := ctagsTag{Type: "tag", Name: "fmt", Kind: "import", Line: 3}
	sym, edge, ok := mapCTagToSymbol(tag, "main.go")
	if !ok {
		t.Fatal("expected ok=true for import tag")
	}
	if sym != nil {
		t.Error("expected nil symbol for import tag")
	}
	if edge == nil {
		t.Fatal("expected non-nil edge for import tag")
	}
	if edge.Kind != "imports" || edge.TargetName != "fmt" {
		t.Errorf("edge = %+v, want imports/fmt", edge)
	}
}

func TestMapCTagToSymbol_Function(t *testing.T) {
	tag := ctagsTag{
		Type:      "tag",
		Name:      "NewServer",
		Kind:      "function",
		Line:      10,
		End:       15,
		Signature: "(addr string)",
		Access:    "public",
	}
	sym, edge, ok := mapCTagToSymbol(tag, "server.go")
	if !ok {
		t.Fatal("expected ok=true for function tag")
	}
	if edge != nil {
		t.Error("expected nil edge for function tag")
	}
	if sym == nil {
		t.Fatal("expected non-nil symbol")
	}
	if sym.Kind != "function" {
		t.Errorf("kind=%q, want function", sym.Kind)
	}
	if sym.Name != "NewServer" {
		t.Errorf("name=%q, want NewServer", sym.Name)
	}
	if sym.Visibility != "exported" {
		t.Errorf("vis=%q, want exported", sym.Visibility)
	}
	if sym.Params != "(addr string)" {
		t.Errorf("params=%q, want (addr string)", sym.Params)
	}
	if sym.LineStart != 10 || sym.LineEnd != 15 {
		t.Errorf("lines=%d-%d, want 10-15", sym.LineStart, sym.LineEnd)
	}
}

func TestMapCTagToSymbol_Skip(t *testing.T) {
	tag := ctagsTag{Type: "tag", Name: "x", Kind: "variable", Line: 1}
	sym, edge, ok := mapCTagToSymbol(tag, "main.go")
	if ok {
		t.Error("expected ok=false for variable tag")
	}
	if sym != nil || edge != nil {
		t.Error("expected nil sym and edge for variable tag")
	}
}
