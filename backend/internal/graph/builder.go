package graph

import (
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/BeLazy167/argus/backend/internal/obs"
)

// maxContentBytes limits source file size to prevent OOM on minified/binary files.
const maxContentBytes = 2 << 20 // 2 MB

// Symbol represents a code symbol extracted from a source file.
type Symbol struct {
	Kind       string // function, method, class, type, interface
	Name       string
	FilePath   string
	LineStart  int
	LineEnd    int
	ReturnType string // "(int, error)", "error", etc.
	Params     string // "(ctx context.Context, id int64)" — display only
	ParamCount int    // derived arity, or 0 if unknown
	Visibility string // "exported" | "unexported"
	IsAsync    bool   // Go: always false; TS: true if async; Python: true if async def
	Receiver   string // Go method receiver type
	Scope      string // "package" | "method" | "nested"
}

// Edge represents a relationship between two symbols.
type Edge struct {
	SourceName string
	TargetName string
	Kind       string // calls, imports, inherits, implements, uses_type
}

// SymbolKind enumerates valid Symbol.Kind values. Kept as string alias
// for backwards compat with existing callers that use raw strings.
type SymbolKind = string

const (
	KindFunction  SymbolKind = "function"
	KindMethod    SymbolKind = "method"
	KindClass     SymbolKind = "class"
	KindType      SymbolKind = "type"
	KindInterface SymbolKind = "interface"
)

// EdgeKind enumerates valid Edge.Kind values.
type EdgeKind = string

const (
	EdgeCalls      EdgeKind = "calls"
	EdgeImports    EdgeKind = "imports"
	EdgeInherits   EdgeKind = "inherits"
	EdgeImplements EdgeKind = "implements"
	EdgeUsesType   EdgeKind = "uses_type"
)

// nodeKey returns a unique map key for a symbol within a repo.
// Uses filePath+name to avoid collisions when multiple files define
// symbols with the same name (e.g. Config in different packages).
func nodeKey(filePath, name string) string {
	return filePath + "\x00" + name
}

// qualifySymbolName is the persisted identity for receiver/scoped symbols.
// Keeping it in name makes the existing unique index sufficient: no schema
// migration is needed, and API/UI consumers receive a useful display label.
func qualifySymbolName(owner, name string) string {
	owner = receiverIdentity(owner)
	if owner == "" || strings.HasPrefix(name, owner+".") {
		return name
	}
	return owner + "." + name
}

// receiverIdentity removes syntax that is part of a receiver declaration but
// not its type identity. Pointer and parenthesized receivers share one method
// set for graph lookup (for example *Alpha and Alpha both map to Alpha).
func receiverIdentity(receiver string) string {
	receiver = strings.TrimSpace(receiver)
	receiver = strings.Trim(receiver, "()")
	fields := strings.Fields(receiver)
	if len(fields) > 1 {
		receiver = fields[len(fields)-1]
	}
	receiver = strings.TrimLeft(receiver, "*&")
	if i := strings.Index(receiver, "["); i >= 0 {
		receiver = receiver[:i]
	}
	return strings.TrimSpace(receiver)
}

func simpleSymbolName(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i+1:]
	}
	if i := strings.LastIndex(name, "::"); i >= 0 {
		return name[i+2:]
	}
	return name
}

// qualifyScopedCall maps language self-references onto the same identity used
// for method nodes. Explicit type selectors (Alpha.Handle) are already stable.
func qualifyScopedCall(target, owner string) string {
	owner = receiverIdentity(owner)
	if owner != "" {
		for _, prefix := range []string{"self.", "this.", "cls.", "Self::"} {
			if strings.HasPrefix(target, prefix) {
				return qualifySymbolName(owner, strings.TrimPrefix(target, prefix))
			}
		}
	}
	// Rust and C++ spell explicit type/namespace selectors with ::, while
	// persisted receiver identities use dots in every language.
	return strings.ReplaceAll(target, "::", ".")
}

// --- Go patterns ---

var goFuncRe = regexp.MustCompile(`(?m)^func\s+(\w+)\s*\(`)
var goMethodRe = regexp.MustCompile(`(?m)^func\s+\(([^)]+)\)\s+(\w+)\s*\(`)
var goStructRe = regexp.MustCompile(`(?m)^type\s+(\w+)\s+struct\b`)
var goInterfaceRe = regexp.MustCompile(`(?m)^type\s+(\w+)\s+interface\b`)
var goImportRe = regexp.MustCompile(`(?m)"([^"]+)"`)
var goCallRe = regexp.MustCompile(`([A-Za-z_]\w*(?:(?:\.|::)[A-Za-z_]\w*)*)\s*\(`)

// --- TS/JS patterns ---

var tsFuncRe = regexp.MustCompile(`(?m)^(?:export\s+)?function\s+(\w+)`)
var tsClassRe = regexp.MustCompile(`(?m)^(?:export\s+)?class\s+(\w+)`)
var tsExportConstRe = regexp.MustCompile(`(?m)^(?:export\s+)?const\s+(\w+)\s*=\s*.*=>`)
var tsMethodRe = regexp.MustCompile(`(?m)^\s+(?:(?:public|private|protected|static|async|readonly)\s+)*([A-Za-z_]\w*)\s*\([^;\n]*\)\s*(?::[^\{\n]+)?\s*\{`)
var tsImportRe = regexp.MustCompile(`(?m)import\s+.*from\s+["']([^"']+)["']`)
var tsExtendsRe = regexp.MustCompile(`(?m)class\s+\w+\s+extends\s+(\w+)`)
var tsImplementsRe = regexp.MustCompile(`(?m)class\s+\w+[^{]*implements\s+([\w,\s]+)`)

// --- Python patterns ---

var pyFuncRe = regexp.MustCompile(`(?m)^([ \t]*)(?:async\s+)?def\s+(\w+)\s*\(`)
var pyClassRe = regexp.MustCompile(`(?m)^([ \t]*)class\s+(\w+)`)
var pyImportRe = regexp.MustCompile(`(?m)^(?:from\s+(\S+)\s+import|import\s+(\S+))`)
var pyInheritsRe = regexp.MustCompile(`(?m)^class\s+\w+\(([^)]+)\)`)

// --- Java patterns ---

var javaClassRe = regexp.MustCompile(`(?m)^(?:public\s+)?(?:abstract\s+)?class\s+(\w+)`)
var javaMethodRe = regexp.MustCompile(`(?m)^\s+(?:public|private|protected)\s+\w+\s+(\w+)\s*\(`)
var javaInterfaceRe = regexp.MustCompile(`(?m)^(?:public\s+)?interface\s+(\w+)`)
var javaImportRe = regexp.MustCompile(`(?m)^import\s+(.+);`)

// --- Rust patterns ---

var rustFnRe = regexp.MustCompile(`(?m)^(?:pub\s+)?(?:async\s+)?fn\s+(\w+)`)
var rustStructRe = regexp.MustCompile(`(?m)^(?:pub\s+)?struct\s+(\w+)`)
var rustTraitRe = regexp.MustCompile(`(?m)^(?:pub\s+)?trait\s+(\w+)`)
var rustImplRe = regexp.MustCompile(`(?m)^impl(?:<[^>]+>)?\s+(?:\w+\s+for\s+)?(\w+)`)
var rustImplMethodRe = regexp.MustCompile(`(?m)^\s+(?:pub\s+)?(?:async\s+)?fn\s+(\w+)`)
var rustUseRe = regexp.MustCompile(`(?m)^use\s+(.+);`)

// --- C# patterns ---

var csClassRe = regexp.MustCompile(`(?m)^(?:public|internal|private)?\s*(?:abstract|sealed|static)?\s*class\s+(\w+)`)
var csMethodRe = regexp.MustCompile(`(?m)^\s+(?:public|private|protected|internal)\s+\w+\s+(\w+)\s*\(`)
var csInterfaceRe = regexp.MustCompile(`(?m)^(?:public|internal)?\s*interface\s+(\w+)`)
var csUsingRe = regexp.MustCompile(`(?m)^using\s+(.+);`)

// langForFile returns a language string for the file extension, or empty.
func langForFile(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return "go"
	case ".ts", ".tsx":
		return "typescript"
	case ".js", ".jsx", ".mjs", ".cjs":
		return "javascript"
	case ".py":
		return "python"
	case ".java":
		return "java"
	case ".rs":
		return "rust"
	case ".cs":
		return "csharp"
	case ".rb":
		return "ruby"
	case ".kt", ".kts":
		return "kotlin"
	case ".swift":
		return "swift"
	case ".c", ".h":
		return "c"
	case ".cpp", ".cc", ".cxx", ".hpp":
		return "cpp"
	case ".php":
		return "php"
	case ".scala":
		return "scala"
	case ".dart":
		return "dart"
	default:
		return ""
	}
}

// ParseFileSymbols extracts symbols and edges from a source file.
// Dispatch order: Go AST > tree-sitter > regex fallback > empty.
func ParseFileSymbols(filePath, content string) (syms []Symbol, edges []Edge) {
	ctx := context.Background()
	operationID := obs.NewLogID()
	started := time.Now()
	lang := langForFile(filePath)
	parserName := "unsupported"
	slog.Info("source graph parse started",
		"operation_id", operationID, "file", filePath, "language", lang, "source_bytes", len(content))
	obs.LogPayload(ctx, slog.Default(), "source graph parse input", operationID, "source", "text/plain", []byte(content))
	defer func() {
		if payload, err := json.Marshal(map[string]any{"symbols": syms, "edges": edges}); err == nil {
			obs.LogPayload(ctx, slog.Default(), "source graph parse output", operationID, "result", "application/json", payload)
		}
		slog.Info("source graph parse completed",
			"operation_id", operationID, "file", filePath, "language", lang,
			"parser", parserName, "symbol_count", len(syms), "edge_count", len(edges),
			"duration_ms", time.Since(started).Milliseconds())
	}()
	if lang == "" {
		return nil, nil
	}
	if len(content) > maxContentBytes {
		parserName = "skipped_too_large"
		slog.Warn("graph: file too large, skipping", "file", filePath, "bytes", len(content))
		return nil, nil
	}
	lines := strings.Split(content, "\n")

	// Go: prefer AST parser, then regex.
	if lang == "go" {
		parserName = "go_ast"
		syms, edges = parseGoAST(filePath, content)
		if syms != nil || edges != nil {
			return syms, edges
		}
		parserName = "go_regex_fallback"
		return parseGo(filePath, content, lines)
	}

	// Non-Go: try tree-sitter first.
	parserName = "tree_sitter"
	syms, edges = parseTreeSitter(filePath, content)
	if syms != nil || edges != nil {
		return syms, edges
	}

	parserName = "regex_fallback"
	switch lang {
	case "typescript", "javascript":
		return parseTS(filePath, content, lines)
	case "python":
		return parsePython(filePath, content, lines)
	case "java":
		return parseJava(filePath, content, lines)
	case "rust":
		return parseRust(filePath, content, lines)
	case "csharp":
		return parseCSharp(filePath, content, lines)
	default:
		parserName = "unsupported_language"
		return nil, nil
	}
}

func parseGo(filePath, content string, lines []string) ([]Symbol, []Edge) {
	var syms []Symbol
	var edges []Edge

	// Functions
	for _, m := range goFuncRe.FindAllStringSubmatchIndex(content, -1) {
		name := content[m[2]:m[3]]
		line := lineAt(content, m[0])
		end := findBlockEnd(lines, line)
		syms = append(syms, Symbol{Kind: "function", Name: name, FilePath: filePath, LineStart: line, LineEnd: end})
		edges = append(edges, extractCalls(name, lines, line, end)...)
	}

	// Methods
	for _, m := range goMethodRe.FindAllStringSubmatchIndex(content, -1) {
		receiver := receiverIdentity(content[m[2]:m[3]])
		name := qualifySymbolName(receiver, content[m[4]:m[5]])
		line := lineAt(content, m[0])
		end := findBlockEnd(lines, line)
		syms = append(syms, Symbol{Kind: "method", Name: name, FilePath: filePath, LineStart: line, LineEnd: end, Receiver: receiver, Scope: "method"})
		edges = append(edges, extractCalls(name, lines, line, end)...)
	}

	// Structs
	for _, m := range goStructRe.FindAllStringSubmatchIndex(content, -1) {
		name := content[m[2]:m[3]]
		line := lineAt(content, m[0])
		syms = append(syms, Symbol{Kind: "type", Name: name, FilePath: filePath, LineStart: line, LineEnd: findBlockEnd(lines, line)})
	}

	// Interfaces
	for _, m := range goInterfaceRe.FindAllStringSubmatchIndex(content, -1) {
		name := content[m[2]:m[3]]
		line := lineAt(content, m[0])
		syms = append(syms, Symbol{Kind: "interface", Name: name, FilePath: filePath, LineStart: line, LineEnd: findBlockEnd(lines, line)})
	}

	// Imports as edges
	importBlock := extractImportBlock(content)
	for _, m := range goImportRe.FindAllStringSubmatch(importBlock, -1) {
		edges = append(edges, Edge{SourceName: filePath, TargetName: m[1], Kind: "imports"})
	}

	return syms, edges
}

func parseTS(filePath, content string, lines []string) ([]Symbol, []Edge) {
	var syms []Symbol
	var edges []Edge

	// Functions
	for _, m := range tsFuncRe.FindAllStringSubmatchIndex(content, -1) {
		name := content[m[2]:m[3]]
		line := lineAt(content, m[0])
		end := findBlockEnd(lines, line)
		syms = append(syms, Symbol{Kind: "function", Name: name, FilePath: filePath, LineStart: line, LineEnd: end})
		edges = append(edges, extractCalls(name, lines, line, end)...)
	}

	// Classes
	for _, m := range tsClassRe.FindAllStringSubmatchIndex(content, -1) {
		name := content[m[2]:m[3]]
		line := lineAt(content, m[0])
		end := findBlockEnd(lines, line)
		syms = append(syms, Symbol{Kind: "class", Name: name, FilePath: filePath, LineStart: line, LineEnd: end})
	}

	// Class methods. This is the degraded parser path; containment keeps its
	// identities aligned with tree-sitter without trying to parse arbitrary JS.
	for _, m := range tsMethodRe.FindAllStringSubmatchIndex(content, -1) {
		methodName := content[m[2]:m[3]]
		line := lineAt(content, m[0])
		receiver := enclosingSymbolScope(syms, line)
		if receiver == "" || isBuiltin(methodName) {
			continue
		}
		name := qualifySymbolName(receiver, methodName)
		end := findBlockEnd(lines, line)
		syms = append(syms, Symbol{Kind: KindMethod, Name: name, FilePath: filePath, LineStart: line, LineEnd: end, Receiver: receiver, Scope: "method"})
		edges = append(edges, extractCalls(name, lines, line, end)...)
	}

	// Arrow function exports
	for _, m := range tsExportConstRe.FindAllStringSubmatchIndex(content, -1) {
		name := content[m[2]:m[3]]
		line := lineAt(content, m[0])
		end := findBlockEnd(lines, line)
		syms = append(syms, Symbol{Kind: "function", Name: name, FilePath: filePath, LineStart: line, LineEnd: end})
		edges = append(edges, extractCalls(name, lines, line, end)...)
	}

	// Imports
	for _, m := range tsImportRe.FindAllStringSubmatch(content, -1) {
		edges = append(edges, Edge{SourceName: filePath, TargetName: m[1], Kind: "imports"})
	}

	// Extends
	for _, m := range tsExtendsRe.FindAllStringSubmatch(content, -1) {
		edges = append(edges, Edge{SourceName: filePath, TargetName: m[1], Kind: "inherits"})
	}

	// Implements
	for _, m := range tsImplementsRe.FindAllStringSubmatch(content, -1) {
		for _, iface := range strings.Split(m[1], ",") {
			iface = strings.TrimSpace(iface)
			if iface != "" {
				edges = append(edges, Edge{SourceName: filePath, TargetName: iface, Kind: "implements"})
			}
		}
	}

	return syms, edges
}

func parsePython(filePath, content string, lines []string) ([]Symbol, []Edge) {
	var syms []Symbol
	var edges []Edge

	// Classes are collected before methods so lexical containment can qualify
	// same-named methods even when tree-sitter is unavailable.
	for _, m := range pyClassRe.FindAllStringSubmatchIndex(content, -1) {
		name := content[m[4]:m[5]]
		line := lineAt(content, m[0])
		owner := enclosingSymbolScope(syms, line)
		name = qualifySymbolName(owner, name)
		scope := "package"
		if owner != "" {
			scope = owner
		}
		syms = append(syms, Symbol{Kind: "class", Name: name, FilePath: filePath, LineStart: line, LineEnd: findPythonBlockEnd(lines, line), Scope: scope})
	}

	// Functions and methods (indented def = method).
	for _, m := range pyFuncRe.FindAllStringSubmatchIndex(content, -1) {
		name := content[m[4]:m[5]]
		line := lineAt(content, m[0])
		kind := "function"
		scope := "package"
		receiver := ""
		if indent := content[m[2]:m[3]]; indent != "" {
			receiver = enclosingSymbolScope(syms, line)
			if receiver != "" {
				kind = "method"
				scope = "method"
				name = qualifySymbolName(receiver, name)
			}
		}
		end := findPythonBlockEnd(lines, line)
		syms = append(syms, Symbol{Kind: kind, Name: name, FilePath: filePath, LineStart: line, LineEnd: end, Receiver: receiver, Scope: scope})
		edges = append(edges, extractCalls(name, lines, line, end)...)
	}

	// Imports
	for _, m := range pyImportRe.FindAllStringSubmatch(content, -1) {
		target := m[1]
		if target == "" {
			target = m[2]
		}
		edges = append(edges, Edge{SourceName: filePath, TargetName: target, Kind: "imports"})
	}

	// Inheritance
	for _, m := range pyInheritsRe.FindAllStringSubmatch(content, -1) {
		for _, parent := range strings.Split(m[1], ",") {
			parent = strings.TrimSpace(parent)
			if parent != "" && parent != "object" {
				edges = append(edges, Edge{SourceName: filePath, TargetName: parent, Kind: "inherits"})
			}
		}
	}

	return syms, edges
}

// lineAt returns the 1-based line number for a byte offset in content.
func lineAt(content string, offset int) int {
	return strings.Count(content[:offset], "\n") + 1
}

// findBlockEnd finds the closing brace for a block starting at line (1-based).
// Returns the line number of the closing brace, or the start line if not found.
func findBlockEnd(lines []string, startLine int) int {
	depth := 0
	for i := startLine - 1; i < len(lines); i++ {
		depth += strings.Count(lines[i], "{") - strings.Count(lines[i], "}")
		if depth <= 0 && i > startLine-1 {
			return i + 1
		}
	}
	return startLine
}

// findPythonBlockEnd estimates the end of a Python block by indentation.
func findPythonBlockEnd(lines []string, startLine int) int {
	if startLine-1 >= len(lines) {
		return startLine
	}
	baseIndent := indentLevel(lines[startLine-1])
	for i := startLine; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			continue
		}
		if indentLevel(lines[i]) <= baseIndent {
			return i // line before this one ends the block
		}
	}
	return len(lines)
}

func indentLevel(line string) int {
	return len(line) - len(strings.TrimLeft(line, " \t"))
}

// enclosingSymbolScope returns the narrowest type/class/interface containing a line.
func enclosingSymbolScope(symbols []Symbol, line int) string {
	owner := ""
	span := int(^uint(0) >> 1)
	for _, sym := range symbols {
		if sym.Kind != KindClass && sym.Kind != KindType && sym.Kind != KindInterface {
			continue
		}
		if sym.LineStart < line && line <= sym.LineEnd && sym.LineEnd-sym.LineStart < span {
			owner = sym.Name
			span = sym.LineEnd - sym.LineStart
		}
	}
	return owner
}

// extractCalls finds function call patterns within a block of code.
func extractCalls(sourceName string, lines []string, startLine, endLine int) []Edge {
	var edges []Edge
	seen := make(map[string]bool)
	for i := startLine; i <= endLine && i <= len(lines); i++ {
		for _, m := range goCallRe.FindAllStringSubmatch(lines[i-1], -1) {
			target := m[1]
			owner := strings.TrimSuffix(sourceName, "."+simpleSymbolName(sourceName))
			target = qualifyScopedCall(target, owner)
			if target == sourceName || target == simpleSymbolName(sourceName) || isBuiltin(target) || seen[target] {
				continue
			}
			seen[target] = true
			edges = append(edges, Edge{SourceName: sourceName, TargetName: target, Kind: "calls"})
		}
	}
	return edges
}

// extractImportBlock returns the import(...) block from Go source.
func extractImportBlock(content string) string {
	start := strings.Index(content, "import (")
	if start < 0 {
		return content // fallback: scan whole file
	}
	end := strings.Index(content[start:], ")")
	if end < 0 {
		return content[start:]
	}
	return content[start : start+end+1]
}

var builtins = map[string]bool{
	"len": true, "cap": true, "make": true, "new": true, "append": true, "copy": true,
	"delete": true, "close": true, "panic": true, "recover": true, "print": true, "println": true,
	"string": true, "int": true, "int64": true, "float64": true, "bool": true, "byte": true,
	"error": true, "nil": true, "true": true, "false": true, "iota": true,
	"if": true, "else": true, "for": true, "range": true, "switch": true, "case": true,
	"return": true, "break": true, "continue": true, "defer": true, "go": true,
	"var": true, "const": true, "type": true, "func": true, "map": true,
	// JS/TS builtins
	"console": true, "require": true, "import": true, "export": true, "from": true,
	"this": true, "super": true, "class": true, "extends": true, "implements": true,
	"constructor": true, "typeof": true, "instanceof": true,
	// Python builtins
	"self": true, "def": true, "lambda": true,
	"str": true, "dict": true, "list": true, "tuple": true, "set": true,
}

func isBuiltin(name string) bool {
	return builtins[name] || len(name) < 2
}

func parseJava(filePath, content string, lines []string) ([]Symbol, []Edge) {
	var syms []Symbol
	var edges []Edge

	for _, m := range javaClassRe.FindAllStringSubmatchIndex(content, -1) {
		name := content[m[2]:m[3]]
		line := lineAt(content, m[0])
		snippet := content[m[0]:m[1]]
		vis := javaVisibility(snippet)
		syms = append(syms, Symbol{Kind: "class", Name: name, FilePath: filePath, LineStart: line, LineEnd: findBlockEnd(lines, line), Visibility: vis})
	}

	for _, m := range javaInterfaceRe.FindAllStringSubmatchIndex(content, -1) {
		name := content[m[2]:m[3]]
		line := lineAt(content, m[0])
		snippet := content[m[0]:m[1]]
		vis := javaVisibility(snippet)
		syms = append(syms, Symbol{Kind: "interface", Name: name, FilePath: filePath, LineStart: line, LineEnd: findBlockEnd(lines, line), Visibility: vis})
	}

	for _, m := range javaMethodRe.FindAllStringSubmatchIndex(content, -1) {
		name := content[m[2]:m[3]]
		line := lineAt(content, m[0])
		end := findBlockEnd(lines, line)
		snippet := content[m[0]:m[1]]
		vis := javaVisibility(snippet)
		receiver := enclosingSymbolScope(syms, line)
		name = qualifySymbolName(receiver, name)
		syms = append(syms, Symbol{Kind: "method", Name: name, FilePath: filePath, LineStart: line, LineEnd: end, Scope: "method", Receiver: receiver, Visibility: vis})
		edges = append(edges, extractCalls(name, lines, line, end)...)
	}

	for _, m := range javaImportRe.FindAllStringSubmatch(content, -1) {
		edges = append(edges, Edge{SourceName: filePath, TargetName: m[1], Kind: "imports"})
	}

	return syms, edges
}

func javaVisibility(snippet string) string {
	if strings.Contains(snippet, "private") {
		return "unexported"
	}
	if strings.Contains(snippet, "protected") {
		return "unexported"
	}
	if strings.Contains(snippet, "public") {
		return "exported"
	}
	return "unexported" // Java default (package-private) = unexported
}

func parseRust(filePath, content string, lines []string) ([]Symbol, []Edge) {
	var syms []Symbol
	var edges []Edge

	for _, m := range rustFnRe.FindAllStringSubmatchIndex(content, -1) {
		name := content[m[2]:m[3]]
		line := lineAt(content, m[0])
		end := findBlockEnd(lines, line)
		vis := "unexported"
		snippet := content[m[0]:m[1]]
		if strings.HasPrefix(snippet, "pub") {
			vis = "exported"
		}
		syms = append(syms, Symbol{Kind: "function", Name: name, FilePath: filePath, LineStart: line, LineEnd: end, Visibility: vis})
		edges = append(edges, extractCalls(name, lines, line, end)...)
	}

	for _, m := range rustStructRe.FindAllStringSubmatchIndex(content, -1) {
		name := content[m[2]:m[3]]
		line := lineAt(content, m[0])
		vis := "unexported"
		snippet := content[m[0]:m[1]]
		if strings.HasPrefix(snippet, "pub") {
			vis = "exported"
		}
		syms = append(syms, Symbol{Kind: "type", Name: name, FilePath: filePath, LineStart: line, LineEnd: findBlockEnd(lines, line), Visibility: vis})
	}

	for _, m := range rustTraitRe.FindAllStringSubmatchIndex(content, -1) {
		name := content[m[2]:m[3]]
		line := lineAt(content, m[0])
		vis := "unexported"
		snippet := content[m[0]:m[1]]
		if strings.HasPrefix(snippet, "pub") {
			vis = "exported"
		}
		syms = append(syms, Symbol{Kind: "interface", Name: name, FilePath: filePath, LineStart: line, LineEnd: findBlockEnd(lines, line), Visibility: vis})
	}

	for _, m := range rustImplRe.FindAllStringSubmatchIndex(content, -1) {
		name := content[m[2]:m[3]]
		line := lineAt(content, m[0])
		syms = append(syms, Symbol{Kind: "type", Name: name + "_impl", FilePath: filePath, LineStart: line, LineEnd: findBlockEnd(lines, line)})
	}
	for _, m := range rustImplMethodRe.FindAllStringSubmatchIndex(content, -1) {
		line := lineAt(content, m[0])
		implScope := enclosingSymbolScope(syms, line)
		if !strings.HasSuffix(implScope, "_impl") {
			continue
		}
		receiver := strings.TrimSuffix(implScope, "_impl")
		name := qualifySymbolName(receiver, content[m[2]:m[3]])
		end := findBlockEnd(lines, line)
		syms = append(syms, Symbol{Kind: KindMethod, Name: name, FilePath: filePath, LineStart: line, LineEnd: end, Receiver: receiver, Scope: "method"})
		edges = append(edges, extractCalls(name, lines, line, end)...)
	}

	for _, m := range rustUseRe.FindAllStringSubmatch(content, -1) {
		edges = append(edges, Edge{SourceName: filePath, TargetName: m[1], Kind: "imports"})
	}

	return syms, edges
}

func parseCSharp(filePath, content string, lines []string) ([]Symbol, []Edge) {
	var syms []Symbol
	var edges []Edge

	for _, m := range csClassRe.FindAllStringSubmatchIndex(content, -1) {
		name := content[m[2]:m[3]]
		line := lineAt(content, m[0])
		syms = append(syms, Symbol{Kind: "class", Name: name, FilePath: filePath, LineStart: line, LineEnd: findBlockEnd(lines, line), Visibility: "exported"})
	}

	for _, m := range csInterfaceRe.FindAllStringSubmatchIndex(content, -1) {
		name := content[m[2]:m[3]]
		line := lineAt(content, m[0])
		syms = append(syms, Symbol{Kind: "interface", Name: name, FilePath: filePath, LineStart: line, LineEnd: findBlockEnd(lines, line), Visibility: "exported"})
	}

	for _, m := range csMethodRe.FindAllStringSubmatchIndex(content, -1) {
		name := content[m[2]:m[3]]
		line := lineAt(content, m[0])
		end := findBlockEnd(lines, line)
		receiver := enclosingSymbolScope(syms, line)
		name = qualifySymbolName(receiver, name)
		syms = append(syms, Symbol{Kind: "method", Name: name, FilePath: filePath, LineStart: line, LineEnd: end, Scope: "method", Receiver: receiver})
		edges = append(edges, extractCalls(name, lines, line, end)...)
	}

	for _, m := range csUsingRe.FindAllStringSubmatch(content, -1) {
		edges = append(edges, Edge{SourceName: filePath, TargetName: m[1], Kind: "imports"})
	}

	return syms, edges
}
