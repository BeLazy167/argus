package graph

import (
	"strings"
	"testing"
)

func TestParseJava(t *testing.T) {
	src := `package com.example;

import java.util.List;
import java.util.Map;

public class UserService {
    private final Database db;

    public String getUser(int id) {
        return db.find(id);
    }

    private void helper() {
    }
}

public interface Database {
    String find(int id);
}
`
	syms, edges := parseJava("UserService.java", src, splitLines(src))

	symMap := make(map[string]Symbol)
	for _, s := range syms {
		symMap[s.Name] = s
	}

	if s, ok := symMap["UserService"]; !ok || s.Kind != "class" {
		t.Errorf("UserService: got %+v", symMap["UserService"])
	}
	if s, ok := symMap["Database"]; !ok || s.Kind != "interface" {
		t.Errorf("Database: got %+v", symMap["Database"])
	}
	if s, ok := symMap["getUser"]; !ok || s.Kind != "method" {
		t.Errorf("getUser: got %+v", symMap["getUser"])
	}
	if s, ok := symMap["helper"]; !ok || s.Kind != "method" {
		t.Errorf("helper: got %+v", symMap["helper"])
	}

	importCount := 0
	for _, e := range edges {
		if e.Kind == "imports" {
			importCount++
		}
	}
	if importCount != 2 {
		t.Errorf("import edges: got %d, want 2", importCount)
	}
}

func TestParseRust(t *testing.T) {
	src := `use std::io;
use std::fmt;

pub struct Config {
    pub host: String,
    pub port: u16,
}

pub trait Service {
    fn start(&self) -> io::Result<()>;
}

pub fn create_config(host: &str, port: u16) -> Config {
    Config { host: host.to_string(), port }
}

fn internal_helper() {
}
`
	syms, edges := parseRust("lib.rs", src, splitLines(src))

	symMap := make(map[string]Symbol)
	for _, s := range syms {
		symMap[s.Name] = s
	}

	if s, ok := symMap["Config"]; !ok || s.Kind != "type" || s.Visibility != "exported" {
		t.Errorf("Config: got %+v", symMap["Config"])
	}
	if s, ok := symMap["Service"]; !ok || s.Kind != "interface" || s.Visibility != "exported" {
		t.Errorf("Service: got %+v", symMap["Service"])
	}
	if s, ok := symMap["create_config"]; !ok || s.Kind != "function" || s.Visibility != "exported" {
		t.Errorf("create_config: got %+v", symMap["create_config"])
	}
	if s, ok := symMap["internal_helper"]; !ok || s.Kind != "function" || s.Visibility != "unexported" {
		t.Errorf("internal_helper: got %+v", symMap["internal_helper"])
	}

	importCount := 0
	for _, e := range edges {
		if e.Kind == "imports" {
			importCount++
		}
	}
	if importCount != 2 {
		t.Errorf("import edges: got %d, want 2", importCount)
	}
}

func TestParseCSharp(t *testing.T) {
	src := `using System;
using System.Collections.Generic;

public class UserController {
    private readonly IUserService _service;

    public string GetUser(int id) {
        return _service.Find(id);
    }

    private void Log(string msg) {
    }
}

public interface IUserService {
    string Find(int id);
}
`
	syms, edges := parseCSharp("UserController.cs", src, splitLines(src))

	symMap := make(map[string]Symbol)
	for _, s := range syms {
		symMap[s.Name] = s
	}

	if s, ok := symMap["UserController"]; !ok || s.Kind != "class" {
		t.Errorf("UserController: got %+v", symMap["UserController"])
	}
	if s, ok := symMap["IUserService"]; !ok || s.Kind != "interface" {
		t.Errorf("IUserService: got %+v", symMap["IUserService"])
	}
	if s, ok := symMap["GetUser"]; !ok || s.Kind != "method" {
		t.Errorf("GetUser: got %+v", symMap["GetUser"])
	}
	if s, ok := symMap["Log"]; !ok || s.Kind != "method" {
		t.Errorf("Log: got %+v", symMap["Log"])
	}

	importCount := 0
	for _, e := range edges {
		if e.Kind == "imports" {
			importCount++
		}
	}
	if importCount != 2 {
		t.Errorf("import edges: got %d, want 2", importCount)
	}
}

func TestParseFileSymbols_NewLanguages(t *testing.T) {
	tests := []struct {
		name     string
		filePath string
		content  string
		wantSyms bool
	}{
		{
			name:     "java file",
			filePath: "Main.java",
			content:  "public class Main {\n    public void run() {}\n}\n",
			wantSyms: true,
		},
		{
			name:     "rust file",
			filePath: "main.rs",
			content:  "pub fn main() {\n    println!(\"hello\");\n}\n",
			wantSyms: true,
		},
		{
			name:     "csharp file",
			filePath: "Program.cs",
			content:  "public class Program {\n    public void Main() {}\n}\n",
			wantSyms: true,
		},
		{
			name:     "unknown extension",
			filePath: "file.xyz",
			content:  "something",
			wantSyms: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			syms, _ := ParseFileSymbols(tt.filePath, tt.content)
			if tt.wantSyms && len(syms) == 0 {
				t.Error("expected symbols, got none")
			}
			if !tt.wantSyms && len(syms) > 0 {
				t.Errorf("expected no symbols, got %d", len(syms))
			}
		})
	}
}

func TestLangForFile_Extended(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"main.go", "go"},
		{"app.ts", "typescript"},
		{"app.tsx", "typescript"},
		{"app.js", "javascript"},
		{"app.jsx", "javascript"},
		{"app.mjs", "javascript"},
		{"app.py", "python"},
		{"Main.java", "java"},
		{"lib.rs", "rust"},
		{"Program.cs", "csharp"},
		{"app.rb", "ruby"},
		{"App.kt", "kotlin"},
		{"App.kts", "kotlin"},
		{"App.swift", "swift"},
		{"main.c", "c"},
		{"main.h", "c"},
		{"main.cpp", "cpp"},
		{"main.cc", "cpp"},
		{"main.cxx", "cpp"},
		{"main.hpp", "cpp"},
		{"index.php", "php"},
		{"App.scala", "scala"},
		{"main.dart", "dart"},
		{"file.xyz", ""},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := langForFile(tt.path)
			if got != tt.want {
				t.Errorf("langForFile(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func splitLines(s string) []string {
	return strings.Split(s, "\n")
}
