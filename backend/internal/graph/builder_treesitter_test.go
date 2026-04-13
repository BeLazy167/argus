package graph

import (
	"testing"
)

func TestTreeSitter_TypeScript(t *testing.T) {
	src := `import { useState } from 'react';
import type { FC } from 'react';

export class App extends Component implements Renderable {
  private name: string;

  public greet(): string {
    console.log("hello");
    return this.name;
  }
}

export function add(a: number, b: number): number {
  return a + b;
}
`
	syms, edges := parseTreeSitter("app.tsx", src)

	symMap := make(map[string]Symbol)
	for _, s := range syms {
		symMap[s.Name] = s
	}

	// Check class
	if s, ok := symMap["App"]; !ok || s.Kind != "class" {
		t.Errorf("App: got %+v", symMap["App"])
	}

	// Check method
	if s, ok := symMap["greet"]; !ok || s.Kind != "method" {
		t.Errorf("greet: got %+v", symMap["greet"])
	}

	// Check function
	if s, ok := symMap["add"]; !ok || s.Kind != "function" {
		t.Errorf("add: got %+v", symMap["add"])
	}

	// Check import edges
	importCount := 0
	for _, e := range edges {
		if e.Kind == "imports" {
			importCount++
		}
	}
	if importCount == 0 {
		t.Error("expected import edges, got none")
	}

	// Check inherits edge
	hasInherits := false
	for _, e := range edges {
		if e.Kind == "inherits" && e.SourceName == "App" && e.TargetName == "Component" {
			hasInherits = true
		}
	}
	if !hasInherits {
		t.Error("expected App inherits Component edge")
	}

	// Check implements edge
	hasImplements := false
	for _, e := range edges {
		if e.Kind == "implements" && e.SourceName == "App" && e.TargetName == "Renderable" {
			hasImplements = true
		}
	}
	if !hasImplements {
		t.Error("expected App implements Renderable edge")
	}
}

func TestTreeSitter_Python(t *testing.T) {
	src := `from typing import List
import os

class Animal:
    def speak(self) -> str:
        return "unknown"

class Dog(Animal):
    def speak(self) -> str:
        return "bark"
`
	syms, edges := parseTreeSitter("animal.py", src)

	// Count-based iteration avoids symMap overwrite collisions when
	// multiple symbols share a name (e.g. speak on Animal and Dog).
	classCount := make(map[string]int)
	methodCount := make(map[string]int)
	for _, s := range syms {
		switch s.Kind {
		case "class":
			classCount[s.Name]++
		case "method":
			methodCount[s.Name]++
		}
	}

	if classCount["Animal"] != 1 {
		t.Errorf("expected 1 Animal class, got %d", classCount["Animal"])
	}
	if classCount["Dog"] != 1 {
		t.Errorf("expected 1 Dog class, got %d", classCount["Dog"])
	}
	if methodCount["speak"] != 2 {
		t.Errorf("expected 2 speak methods, got %d", methodCount["speak"])
	}

	// Check inheritance
	hasInherits := false
	for _, e := range edges {
		if e.Kind == "inherits" && e.SourceName == "Dog" && e.TargetName == "Animal" {
			hasInherits = true
		}
	}
	if !hasInherits {
		t.Error("expected Dog inherits Animal edge")
	}

	// Check import edges
	importCount := 0
	for _, e := range edges {
		if e.Kind == "imports" {
			importCount++
		}
	}
	if importCount == 0 {
		t.Error("expected import edges, got none")
	}
}

func TestTreeSitter_Rust(t *testing.T) {
	src := `use std::io;
use std::fmt;

pub struct Config {
    pub host: String,
    pub port: u16,
}

pub trait Service {
    fn start(&self) -> io::Result<()>;
}

impl Service for Config {
    pub fn start(&self) -> io::Result<()> {
        Ok(())
    }
}

pub fn create_config(host: &str, port: u16) -> Config {
    Config { host: host.to_string(), port }
}
`
	syms, edges := parseTreeSitter("lib.rs", src)

	symMap := make(map[string]Symbol)
	for _, s := range syms {
		symMap[s.Name] = s
	}

	if s, ok := symMap["Config"]; !ok || s.Kind != "type" {
		t.Errorf("Config: got %+v", symMap["Config"])
	}
	if s, ok := symMap["Service"]; !ok || s.Kind != "interface" {
		t.Errorf("Service: got %+v", symMap["Service"])
	}
	if s, ok := symMap["create_config"]; !ok || s.Kind != "function" {
		t.Errorf("create_config: got %+v", symMap["create_config"])
	}

	// Check implements edge from impl block
	hasImplements := false
	for _, e := range edges {
		if e.Kind == "implements" && e.SourceName == "Config" && e.TargetName == "Service" {
			hasImplements = true
		}
	}
	if !hasImplements {
		t.Error("expected Config implements Service edge")
	}

	// Check use/import edges
	importCount := 0
	for _, e := range edges {
		if e.Kind == "imports" {
			importCount++
		}
	}
	if importCount == 0 {
		t.Error("expected import edges from use declarations, got none")
	}
}

func TestTreeSitter_Java(t *testing.T) {
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
	syms, edges := parseTreeSitter("UserService.java", src)

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

	// Check import edges
	importCount := 0
	for _, e := range edges {
		if e.Kind == "imports" {
			importCount++
		}
	}
	if importCount == 0 {
		t.Error("expected import edges, got none")
	}
}

func TestTreeSitter_CSharp(t *testing.T) {
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
	syms, edges := parseTreeSitter("UserController.cs", src)

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

	// Check using/import edges
	importCount := 0
	for _, e := range edges {
		if e.Kind == "imports" {
			importCount++
		}
	}
	if importCount == 0 {
		t.Error("expected import edges from using directives, got none")
	}
}

func TestTreeSitter_UnsupportedFile(t *testing.T) {
	syms, edges := parseTreeSitter("file.xyz", "some content")
	if syms != nil || edges != nil {
		t.Error("expected nil for unsupported file extension")
	}
}

func TestTreeSitter_DispatchInParseFileSymbols(t *testing.T) {
	// Verify ParseFileSymbols dispatch works across languages.
	// Python/Rust go through parseTreeSitter, Go goes through parseGoAST.
	cases := []struct {
		name, path, src string
	}{
		{
			name: "python",
			path: "test.py",
			src: `def hello():
    print("world")
`,
		},
		{
			name: "go",
			path: "srv.go",
			src: `package foo
func Hello() string { return "world" }
`,
		},
		{
			name: "rust",
			path: "lib.rs",
			src: `pub fn hello() -> String {
    String::from("world")
}
`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			syms, _ := ParseFileSymbols(c.path, c.src)
			if len(syms) == 0 {
				t.Errorf("%s: expected symbols, got none", c.name)
			}
		})
	}
}

func TestTreeSitter_Go(t *testing.T) {
	// parseTreeSitter with Go source — note ParseFileSymbols dispatches
	// Go through parseGoAST, NOT tree-sitter. We call parseTreeSitter
	// directly here to verify it either extracts func/method symbols
	// via the Go grammar or returns nil cleanly without crashing.
	src := `package foo

type Server struct {
	addr string
}

func NewServer(a string) *Server {
	return &Server{addr: a}
}

func (s *Server) Start() error {
	return nil
}
`
	syms, _ := parseTreeSitter("srv.go", src)
	if syms == nil {
		// Grammar may not be registered or may not match walker's node
		// kinds — acceptable as long as it returns cleanly. Go is routed
		// through parseGoAST in ParseFileSymbols regardless.
		t.Skip("tree-sitter Go grammar returned no symbols; parseGoAST handles Go via ParseFileSymbols")
		return
	}

	// Tree-sitter Go grammar uses function_declaration/method_declaration
	// which the walker recognizes. Top-level type_declaration with a
	// struct_type child is NOT currently handled for Go, so we assert
	// on funcs + methods only.
	names := make(map[string]bool)
	for _, s := range syms {
		names[s.Name] = true
	}

	if !names["NewServer"] {
		t.Errorf("expected NewServer function symbol, got syms=%+v", syms)
	}
	if !names["Start"] {
		t.Errorf("expected Start method symbol, got syms=%+v", syms)
	}
}

func TestTreeSitter_DoesNotCrashOnGarbage(t *testing.T) {
	// Garbage, empty, and malformed input must not panic. The function
	// has a recover() that returns nil on panic, so we assert it returns
	// cleanly (nil or a partial result) rather than crashing the process.
	cases := []struct {
		name, path, src string
	}{
		{"empty py", "test.py", ""},
		{"garbage py", "test.py", "!!!~~~###$$$@@@"},
		{"malformed ts", "test.ts", "class { function (((("},
		{"only whitespace", "test.py", "   \n  \t\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			syms, edges := parseTreeSitter(c.path, c.src)
			// Returning nil is fine; returning a partial result is also fine.
			// The only failure mode is a panic, which recover() would trap.
			_ = syms
			_ = edges
		})
	}

	// ParseFileSymbols (which dispatches through tree-sitter first,
	// then regex fallback) should also survive empty content.
	syms, _ := ParseFileSymbols("test.py", "")
	_ = syms
}

func TestTreeSitter_ArrowFunction(t *testing.T) {
	src := `const add = (a: number, b: number): number => a + b;
export const greet = (name: string): string => "hello " + name;
const notAFunction = 42;
`
	syms, _ := parseTreeSitter("arrow.ts", src)

	symMap := make(map[string]Symbol)
	for _, s := range syms {
		symMap[s.Name] = s
	}

	if s, ok := symMap["add"]; !ok || s.Kind != "function" {
		t.Errorf("add: got %+v", symMap["add"])
	}
	if s, ok := symMap["greet"]; !ok || s.Kind != "function" {
		t.Errorf("greet: got %+v", symMap["greet"])
	}
	if _, ok := symMap["notAFunction"]; ok {
		t.Error("notAFunction should not be extracted as a function")
	}
}

func TestTreeSitter_RustImplNoDuplicate(t *testing.T) {
	src := `pub struct Config {}

impl Config {
    pub fn new() -> Self {
        Config {}
    }
}
`
	syms, _ := parseTreeSitter("config.rs", src)

	// Count how many times "new" appears
	count := 0
	for _, s := range syms {
		if s.Name == "new" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 'new' symbol, got %d", count)
	}
}
