package graph

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

// ctagsBinary returns the path to universal-ctags if available, or empty string.
// Rejects exuberant-ctags since it lacks JSON output support.
func ctagsBinary() string {
	path, err := exec.LookPath("ctags")
	if err != nil {
		return ""
	}
	// Verify it's universal-ctags by checking --version output
	out, err := exec.Command(path, "--version").CombinedOutput()
	if err != nil {
		return ""
	}
	if strings.Contains(string(out), "Universal Ctags") {
		return path
	}
	return ""
}

// ctagsTag represents a single JSON tag emitted by universal-ctags --output-format=json.
type ctagsTag struct {
	Type      string `json:"_type"`
	Name      string `json:"name"`
	Path      string `json:"path"`
	Line      int    `json:"line"`
	End       int    `json:"end"`
	Kind      string `json:"kind"`
	Scope     string `json:"scope"`
	ScopeKind string `json:"scopeKind"`
	Signature string `json:"signature"`
	Language  string `json:"language"`
	Access    string `json:"access"`
}

// parseCTags runs universal-ctags on the given content and extracts symbols.
// Falls back gracefully if ctags is not installed or not universal-ctags (returns nil, nil).
func parseCTags(filePath, content string) ([]Symbol, []Edge) {
	ctagsPath := ctagsBinary()
	if ctagsPath == "" {
		return nil, nil
	}

	ext := filepath.Ext(filePath)
	tmpFile, err := os.CreateTemp("", "argus-ctags-*"+ext)
	if err != nil {
		return nil, nil
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(content); err != nil {
		tmpFile.Close()
		return nil, nil
	}
	tmpFile.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, ctagsPath,
		"--output-format=json",
		"--fields=+KSznep",
		"--kinds-all=*",
		"-f", "-",
		tmpFile.Name(),
	)

	out, err := cmd.Output()
	if err != nil {
		return nil, nil
	}

	var syms []Symbol
	var edges []Edge

	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var tag ctagsTag
		if err := json.Unmarshal([]byte(line), &tag); err != nil {
			continue
		}
		if tag.Type != "tag" {
			continue
		}

		sym, edge, ok := mapCTagToSymbol(tag, filePath)
		if ok && sym != nil {
			syms = append(syms, *sym)
		}
		if edge != nil {
			edges = append(edges, *edge)
		}
	}

	return syms, edges
}

// mapCTagToSymbol converts a ctags JSON tag into a Symbol or Edge.
// Returns (symbol, edge, handled). Skips noisy kinds like variable/field.
func mapCTagToSymbol(tag ctagsTag, filePath string) (*Symbol, *Edge, bool) {
	kind := mapCTagKind(tag.Kind)

	// Import/include tags produce edges, not symbols
	if tag.Kind == "import" || tag.Kind == "include" || tag.Kind == "using" {
		return nil, &Edge{
			SourceName: filePath,
			TargetName: tag.Name,
			Kind:       "imports",
		}, true
	}

	// Skip noisy kinds
	if kind == "" {
		return nil, nil, false
	}

	vis := ctagsVisibility(tag)
	params := ""
	if tag.Signature != "" {
		params = tag.Signature
	}

	scope := "package"
	if tag.ScopeKind == "class" || tag.ScopeKind == "struct" || tag.ScopeKind == "interface" {
		scope = "method"
	}

	receiver := ""
	if (kind == "method" || tag.ScopeKind == "class" || tag.ScopeKind == "struct") && tag.Scope != "" {
		receiver = tag.Scope
	}

	sym := &Symbol{
		Kind:       kind,
		Name:       tag.Name,
		FilePath:   filePath,
		LineStart:  tag.Line,
		LineEnd:    tag.End,
		Params:     params,
		Visibility: vis,
		Receiver:   receiver,
		Scope:      scope,
	}

	if sym.LineEnd == 0 {
		sym.LineEnd = sym.LineStart
	}

	return sym, nil, true
}

// mapCTagKind maps ctags kind strings to our Symbol.Kind values.
func mapCTagKind(ctagKind string) string {
	switch strings.ToLower(ctagKind) {
	case "function", "func", "subroutine", "procedure":
		return "function"
	case "method":
		return "method"
	case "class":
		return "class"
	case "struct", "type":
		return "type"
	case "interface", "trait", "protocol":
		return "interface"
	// Skip noisy kinds
	case "variable", "constant", "const", "member", "field", "property",
		"enumerator", "enum", "package", "namespace", "module",
		"import", "include", "using", "label", "parameter",
		"typealias", "typedef":
		return ""
	default:
		return ""
	}
}

// ctagsVisibility determines symbol visibility from ctags access field or name casing.
func ctagsVisibility(tag ctagsTag) string {
	switch tag.Access {
	case "public":
		return "exported"
	case "private", "protected":
		return "unexported"
	}
	// Fallback: Go-style uppercase = exported
	if len(tag.Name) > 0 && unicode.IsUpper([]rune(tag.Name)[0]) {
		return "exported"
	}
	return "unexported"
}
