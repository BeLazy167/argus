package graph

import (
	"log/slog"
	"strings"
	"sync"

	"github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

// tsEngine holds the singleton tree-sitter parser state.
// Initialized lazily via sync.Once on first use.
var tsEngine struct {
	sync.Once
	ready bool
}

// parseTreeSitter extracts symbols and edges from source code using the
// gotreesitter pure-Go tree-sitter runtime. Returns nil, nil on failure
// so the caller can fall back to regex parsing.
func parseTreeSitter(filePath, content string) (syms []Symbol, edges []Edge) {
	// Recover from any panic in the tree-sitter parser so the caller can
	// fall back to regex parsing instead of crashing.
	defer func() {
		if r := recover(); r != nil {
			slog.Warn("graph: tree-sitter panic, falling back", "file", filePath, "panic", r)
			syms = nil
			edges = nil
		}
	}()

	tsEngine.Do(func() {
		tsEngine.ready = true
		slog.Info("graph: tree-sitter engine initialized")
	})
	if !tsEngine.ready {
		return nil, nil
	}

	// Detect language from file path. Missing entry is expected for
	// unsupported extensions, so we return silently without logging.
	entry := grammars.DetectLanguage(filePath)
	if entry == nil || entry.Language == nil {
		return nil, nil
	}

	lang := entry.Language()
	if lang == nil {
		return nil, nil
	}

	// Create parser and parse
	var tree *gotreesitter.Tree
	var err error

	p := gotreesitter.NewParser(lang)

	if entry.TokenSourceFactory != nil {
		ts := entry.TokenSourceFactory([]byte(content), lang)
		tree, err = p.ParseWithTokenSource([]byte(content), ts)
	} else {
		tree, err = p.Parse([]byte(content))
	}
	if err != nil {
		slog.Debug("graph: tree-sitter parse error", "file", filePath, "error", err)
		return nil, nil
	}
	if tree == nil {
		slog.Debug("graph: tree-sitter nil tree", "file", filePath)
		return nil, nil
	}
	defer tree.Release()

	root := tree.RootNode()
	if root == nil || root.IsError() {
		slog.Debug("graph: tree-sitter AST error", "file", filePath)
		return nil, nil
	}

	source := []byte(content)

	// Walk the AST and extract symbols + edges
	walkNode(root, lang, source, filePath, &syms, &edges)

	return syms, edges
}

// maxWalkDepth prevents stack overflow on deeply nested or malformed ASTs.
const maxWalkDepth = 500

// walkNode recursively walks the tree-sitter AST, extracting symbols and edges.
func walkNode(n *gotreesitter.Node, lang *gotreesitter.Language, source []byte, filePath string, syms *[]Symbol, edges *[]Edge) {
	walkNodeDepth(n, lang, source, filePath, syms, edges, 0)
}

func walkNodeDepth(n *gotreesitter.Node, lang *gotreesitter.Language, source []byte, filePath string, syms *[]Symbol, edges *[]Edge, depth int) {
	if n == nil || depth > maxWalkDepth {
		return
	}

	kind := n.Type(lang)

	switch kind {
	// --- Functions (all languages) ---
	case "function_declaration", "generator_function_declaration", "function",
		"function_definition", "function_item":
		// Python: function_definition with self/cls first param is a method
		if kind == "function_definition" && isPythonMethod(n, lang, source) {
			extractMethodSymbol(n, lang, source, filePath, syms, edges)
		} else {
			extractFuncSymbol(n, lang, source, filePath, "function", syms, edges)
		}

	// --- Methods (all languages) ---
	case "method_definition", "method_declaration", "method", "singleton_method":
		extractMethodSymbol(n, lang, source, filePath, syms, edges)

	// --- Classes (all languages) ---
	case "class_declaration", "class", "class_specifier",
		"struct_declaration", "record_declaration":
		extractClassSymbol(n, lang, source, filePath, syms, edges)

	// --- Python class_definition ---
	case "class_definition":
		extractClassSymbol(n, lang, source, filePath, syms, edges)

	// --- Structs/Types (all languages) ---
	case "struct_item", "struct_specifier":
		extractStructSymbol(n, lang, source, filePath, syms, edges)

	// --- Interfaces/Traits (all languages) ---
	case "interface_declaration", "trait_item", "protocol_declaration":
		extractInterfaceSymbol(n, lang, source, filePath, syms, edges)

	// --- Impl blocks (Rust) ---
	case "impl_item":
		extractImplSymbol(n, lang, source, filePath, syms, edges)
		return // extractImplSymbol already walks children; don't recurse

	// --- Modules (Ruby, etc.) ---
	case "module":
		extractModuleSymbol(n, lang, source, filePath, syms, edges)

	// --- Imports (all languages) ---
	case "import_statement", "import_declaration", "import_from_statement",
		"import_header":
		extractImportEdges(n, lang, source, filePath, edges)
	case "use_declaration":
		extractUseEdges(n, lang, source, filePath, edges)
	case "using_directive":
		extractUsingEdges(n, lang, source, filePath, edges)
	case "preproc_include":
		extractIncludeEdges(n, lang, source, filePath, edges)

	// --- Variable declarators with arrow/function values (TS/JS) ---
	case "variable_declarator":
		extractVariableDeclaratorSymbol(n, lang, source, filePath, syms, edges)

	// --- Arrow functions (TS/JS — typically assigned to variables) ---
	case "arrow_function":
		// handled by parent variable_declarator
	}

	// Recurse into children
	for _, child := range n.Children() {
		walkNodeDepth(child, lang, source, filePath, syms, edges, depth+1)
	}
}

// extractFuncSymbol extracts a function symbol and its call edges.
func extractFuncSymbol(n *gotreesitter.Node, lang *gotreesitter.Language, source []byte, filePath string, kind string, syms *[]Symbol, edges *[]Edge) {
	name := nodeName(n, lang, source)
	if name == "" {
		return
	}

	params := extractFieldText(n, lang, source, "parameters")
	sym := Symbol{
		Kind:       kind,
		Name:       name,
		FilePath:   filePath,
		LineStart:  int(n.StartPoint().Row) + 1,
		LineEnd:    int(n.EndPoint().Row) + 1,
		Params:     params,
		ParamCount: countParams(params),
		ReturnType: returnTypeOf(n, lang, source),
		Visibility: visibilityFromModifiers(n, lang, source),
		IsAsync:    hasChildKind(n, lang, "async"),
		Scope:      "package",
	}
	*syms = append(*syms, sym)

	extractBodyCallEdges(name, n, lang, source, edges)
}

// extractMethodSymbol extracts a method symbol.
func extractMethodSymbol(n *gotreesitter.Node, lang *gotreesitter.Language, source []byte, filePath string, syms *[]Symbol, edges *[]Edge) {
	name := nodeName(n, lang, source)
	if name == "" {
		return
	}

	params := extractFieldText(n, lang, source, "parameters")
	sym := Symbol{
		Kind:       "method",
		Name:       name,
		FilePath:   filePath,
		LineStart:  int(n.StartPoint().Row) + 1,
		LineEnd:    int(n.EndPoint().Row) + 1,
		Params:     params,
		ParamCount: countParams(params),
		ReturnType: returnTypeOf(n, lang, source),
		Visibility: visibilityFromModifiers(n, lang, source),
		Receiver:   extractReceiver(n, lang, source),
		Scope:      "method",
	}
	*syms = append(*syms, sym)

	extractBodyCallEdges(name, n, lang, source, edges)
}

// extractClassSymbol extracts a class symbol and inheritance/implements edges.
func extractClassSymbol(n *gotreesitter.Node, lang *gotreesitter.Language, source []byte, filePath string, syms *[]Symbol, edges *[]Edge) {
	nameNode := n.ChildByFieldName("name", lang)
	if nameNode == nil {
		return
	}
	name := nameNode.Text(source)
	if name == "" {
		return
	}

	vis := visibilityFromModifiers(n, lang, source)

	sym := Symbol{
		Kind:       "class",
		Name:       name,
		FilePath:   filePath,
		LineStart:  int(n.StartPoint().Row) + 1,
		LineEnd:    int(n.EndPoint().Row) + 1,
		Visibility: vis,
		Scope:      "package",
	}
	*syms = append(*syms, sym)

	// Inheritance edge — try field-based lookup first
	parentNode := n.ChildByFieldName("superclass", lang)
	if parentNode == nil {
		parentNode = n.ChildByFieldName("parent", lang)
	}
	if parentNode == nil {
		// Python: superclasses field contains argument_list
		parentNode = n.ChildByFieldName("superclasses", lang)
	}
	if parentNode != nil {
		// If it's an argument_list (Python), extract each named child
		if parentNode.Type(lang) == "argument_list" {
			for _, child := range parentNode.Children() {
				if child.IsNamed() {
					parentName := child.Text(source)
					if parentName != "" && !isBuiltin(parentName) {
						*edges = append(*edges, Edge{SourceName: name, TargetName: parentName, Kind: "inherits"})
					}
				}
			}
		} else {
			parentName := parentNode.Text(source)
			if parentName != "" && !isBuiltin(parentName) {
				*edges = append(*edges, Edge{SourceName: name, TargetName: parentName, Kind: "inherits"})
			}
		}
	}

	// TypeScript/JS: class_heritage contains extends_clause + implements_clause
	for _, child := range n.Children() {
		if child.Type(lang) == "class_heritage" {
			for _, hc := range child.Children() {
				hk := hc.Type(lang)
				if hk == "extends_clause" {
					for _, gc := range hc.Children() {
						if gc.IsNamed() {
							parentName := gc.Text(source)
							if parentName != "" && !isBuiltin(parentName) {
								*edges = append(*edges, Edge{SourceName: name, TargetName: parentName, Kind: "inherits"})
							}
						}
					}
				} else if hk == "implements_clause" {
					for _, gc := range hc.Children() {
						if gc.IsNamed() {
							ifaceName := gc.Text(source)
							if ifaceName != "" && !isBuiltin(ifaceName) {
								*edges = append(*edges, Edge{SourceName: name, TargetName: ifaceName, Kind: "implements"})
							}
						}
					}
				}
			}
		}
	}

	// Implements edge — try field-based lookup
	ifacesNode := n.ChildByFieldName("interfaces", lang)
	if ifacesNode == nil {
		ifacesNode = n.ChildByFieldName("superinterfaces", lang)
	}
	if ifacesNode != nil {
		for _, child := range ifacesNode.Children() {
			if child.IsNamed() {
				ifaceName := child.Text(source)
				if ifaceName != "" && !isBuiltin(ifaceName) {
					*edges = append(*edges, Edge{SourceName: name, TargetName: ifaceName, Kind: "implements"})
				}
			}
		}
	}
}

// extractStructSymbol extracts a struct/type symbol.
func extractStructSymbol(n *gotreesitter.Node, lang *gotreesitter.Language, source []byte, filePath string, syms *[]Symbol, edges *[]Edge) {
	extractNamedSymbol(n, lang, source, filePath, "type", "package", syms)
}

// extractInterfaceSymbol extracts an interface symbol.
func extractInterfaceSymbol(n *gotreesitter.Node, lang *gotreesitter.Language, source []byte, filePath string, syms *[]Symbol, edges *[]Edge) {
	extractNamedSymbol(n, lang, source, filePath, "interface", "package", syms)
}

// extractNamedSymbol is the shared implementation for struct/interface/module symbols
// that only have a name, visibility, and position — no params, return type, or body.
func extractNamedSymbol(n *gotreesitter.Node, lang *gotreesitter.Language, source []byte, filePath, kind, scope string, syms *[]Symbol) {
	name := nodeName(n, lang, source)
	if name == "" {
		return
	}
	*syms = append(*syms, Symbol{
		Kind:       kind,
		Name:       name,
		FilePath:   filePath,
		LineStart:  int(n.StartPoint().Row) + 1,
		LineEnd:    int(n.EndPoint().Row) + 1,
		Visibility: visibilityFromModifiers(n, lang, source),
		Scope:      scope,
	})
}

// extractImplSymbol extracts an impl block (Rust) — records the type being implemented.
func extractImplSymbol(n *gotreesitter.Node, lang *gotreesitter.Language, source []byte, filePath string, syms *[]Symbol, edges *[]Edge) {
	typeNode := n.ChildByFieldName("type", lang)
	if typeNode == nil {
		return
	}
	typeName := typeNode.Text(source)
	if typeName == "" {
		return
	}

	// Check if this is a trait impl (impl Trait for Type)
	traitNode := n.ChildByFieldName("trait", lang)
	if traitNode != nil {
		traitName := traitNode.Text(source)
		if traitName != "" && !isBuiltin(traitName) {
			*edges = append(*edges, Edge{SourceName: typeName, TargetName: traitName, Kind: "implements"})
		}
	}

	// Walk children for method definitions.
	// Rust impl_item wraps methods inside a declaration_list child,
	// so we must descend into it rather than iterating direct children.
	var methodNodes []*gotreesitter.Node
	for _, child := range n.Children() {
		if !child.IsNamed() {
			continue
		}
		if child.Type(lang) == "function_item" {
			methodNodes = append(methodNodes, child)
		} else if child.Type(lang) == "declaration_list" {
			for _, dc := range child.Children() {
				if dc.IsNamed() && dc.Type(lang) == "function_item" {
					methodNodes = append(methodNodes, dc)
				}
			}
		}
	}

	for _, child := range methodNodes {
		nameNode := child.ChildByFieldName("name", lang)
		if nameNode == nil {
			continue
		}
		name := nameNode.Text(source)
		params := extractFieldText(child, lang, source, "parameters")
		returnType := extractFieldText(child, lang, source, "return_type")

		sym := Symbol{
			Kind:       "method",
			Name:       name,
			FilePath:   filePath,
			LineStart:  int(child.StartPoint().Row) + 1,
			LineEnd:    int(child.EndPoint().Row) + 1,
			Params:     params,
			ParamCount: countParams(params),
			ReturnType: returnType,
			Visibility: "exported",
			Receiver:   typeName,
			Scope:      "method",
		}
		if !hasChildKind(child, lang, "visibility_modifier") {
			sym.Visibility = "unexported"
		}
		*syms = append(*syms, sym)

		// Rust function_item uses "block" field for the body
		bodyNode := child.ChildByFieldName("body", lang)
		if bodyNode == nil {
			bodyNode = child.ChildByFieldName("block", lang)
		}
		if bodyNode != nil {
			extractCallEdges(name, bodyNode, lang, source, edges)
		}
	}
}

// extractModuleSymbol extracts a module symbol (Ruby, etc.).
func extractModuleSymbol(n *gotreesitter.Node, lang *gotreesitter.Language, source []byte, filePath string, syms *[]Symbol, edges *[]Edge) {
	name := nodeName(n, lang, source)
	if name == "" {
		return
	}
	*syms = append(*syms, Symbol{
		Kind:       "type",
		Name:       name,
		FilePath:   filePath,
		LineStart:  int(n.StartPoint().Row) + 1,
		LineEnd:    int(n.EndPoint().Row) + 1,
		Visibility: "exported",
		Scope:      "package",
	})
}

// extractImportEdges extracts import edges from import statements.
func extractImportEdges(n *gotreesitter.Node, lang *gotreesitter.Language, source []byte, filePath string, edges *[]Edge) {
	// Walk children looking for import sources (string literals, module names)
	for _, child := range n.Children() {
		kind := child.Type(lang)
		text := child.Text(source)

		switch kind {
		case "string", "string_literal", "identifier", "module_name",
			"from_clause", "import_clause", "named_imports",
			"namespace_import", "dotted_name", "aliased_import",
			"package_or_class_name_to_use":
			if text != "" && !isBuiltin(text) {
				// Clean up string quotes
				cleaned := strings.Trim(text, "\"'`")
				if cleaned != "" {
					*edges = append(*edges, Edge{SourceName: filePath, TargetName: cleaned, Kind: "imports"})
				}
			}
		}

		// Recurse into children for nested import specifiers
		if child.IsNamed() && child.NamedChildCount() > 0 {
			for _, grandchild := range child.Children() {
				if grandchild.IsNamed() {
					gk := grandchild.Type(lang)
					if gk == "import_specifier" || gk == "identifier" || gk == "module_name" {
						gt := grandchild.Text(source)
						if gt != "" && !isBuiltin(gt) {
							cleaned := strings.Trim(gt, "\"'`")
							if cleaned != "" {
								*edges = append(*edges, Edge{SourceName: filePath, TargetName: cleaned, Kind: "imports"})
							}
						}
					}
				}
			}
		}
	}
}

// extractUseEdges extracts import edges from Rust use declarations.
func extractUseEdges(n *gotreesitter.Node, lang *gotreesitter.Language, source []byte, filePath string, edges *[]Edge) {
	extractNamedChildImportEdge(n, lang, source, filePath, "argument", edges)
}

// extractUsingEdges extracts import edges from C# using directives.
func extractUsingEdges(n *gotreesitter.Node, lang *gotreesitter.Language, source []byte, filePath string, edges *[]Edge) {
	extractNamedChildImportEdge(n, lang, source, filePath, "name", edges)
}

// extractNamedChildImportEdge finds a named child by field name (or first named child
// as fallback) and emits an import edge from its text content.
func extractNamedChildImportEdge(n *gotreesitter.Node, lang *gotreesitter.Language, source []byte, filePath, fieldName string, edges *[]Edge) {
	child := n.ChildByFieldName(fieldName, lang)
	if child == nil {
		for _, c := range n.Children() {
			if c.IsNamed() {
				child = c
				break
			}
		}
	}
	if child != nil {
		if text := child.Text(source); text != "" {
			*edges = append(*edges, Edge{SourceName: filePath, TargetName: text, Kind: "imports"})
		}
	}
}

// extractIncludeEdges extracts import edges from C/C++ #include directives.
func extractIncludeEdges(n *gotreesitter.Node, lang *gotreesitter.Language, source []byte, filePath string, edges *[]Edge) {
	for _, child := range n.Children() {
		if child.IsNamed() {
			text := child.Text(source)
			cleaned := strings.Trim(text, "\"<>")
			if cleaned != "" {
				*edges = append(*edges, Edge{SourceName: filePath, TargetName: cleaned, Kind: "imports"})
			}
		}
	}
}

// extractCallEdges walks a function body and extracts call edges.
func extractCallEdges(sourceName string, body *gotreesitter.Node, lang *gotreesitter.Language, source []byte, edges *[]Edge) {
	seen := make(map[string]bool)
	extractCallsRecursive(sourceName, body, lang, source, edges, seen)
}

func extractCallsRecursive(sourceName string, n *gotreesitter.Node, lang *gotreesitter.Language, source []byte, edges *[]Edge, seen map[string]bool) {
	if n == nil {
		return
	}

	kind := n.Type(lang)

	// Detect call expressions across languages
	switch kind {
	case "call_expression", "call", "method_call", "function_call",
		"macro_invocation", "invocation_expression":
		// Get the function being called
		funcNode := n.ChildByFieldName("function", lang)
		if funcNode == nil {
			funcNode = n.ChildByFieldName("name", lang)
		}
		if funcNode == nil {
			// First named child is often the callee
			for _, child := range n.Children() {
				if child.IsNamed() {
					funcNode = child
					break
				}
			}
		}
		if funcNode != nil {
			target := resolveCallTarget(funcNode, lang, source)
			if target != "" && target != sourceName && !isBuiltin(target) && !seen[target] {
				seen[target] = true
				*edges = append(*edges, Edge{SourceName: sourceName, TargetName: target, Kind: "calls"})
			}
		}
	}

	// Recurse into children
	for _, child := range n.Children() {
		if child.IsNamed() {
			extractCallsRecursive(sourceName, child, lang, source, edges, seen)
		}
	}
}

// resolveCallTarget resolves the target name from a call expression's function node.
func resolveCallTarget(n *gotreesitter.Node, lang *gotreesitter.Language, source []byte) string {
	if n.Type(lang) == "parenthesized_expression" {
		for _, child := range n.Children() {
			if child.IsNamed() {
				return resolveCallTarget(child, lang, source)
			}
		}
	}
	return n.Text(source)
}

// extractFieldText returns the text content of a named field child.
func extractFieldText(n *gotreesitter.Node, lang *gotreesitter.Language, source []byte, fieldName string) string {
	child := n.ChildByFieldName(fieldName, lang)
	if child == nil {
		return ""
	}
	return child.Text(source)
}

// nodeName returns the "name" field text of a node, or empty string.
func nodeName(n *gotreesitter.Node, lang *gotreesitter.Language, source []byte) string {
	return extractFieldText(n, lang, source, "name")
}

// returnTypeOf returns the return type text, trying "return_type" then "type" fields.
func returnTypeOf(n *gotreesitter.Node, lang *gotreesitter.Language, source []byte) string {
	if rt := extractFieldText(n, lang, source, "return_type"); rt != "" {
		return rt
	}
	return extractFieldText(n, lang, source, "type")
}

// extractBodyCallEdges extracts call edges from a node's "body" field.
func extractBodyCallEdges(name string, n *gotreesitter.Node, lang *gotreesitter.Language, source []byte, edges *[]Edge) {
	bodyNode := n.ChildByFieldName("body", lang)
	if bodyNode != nil {
		extractCallEdges(name, bodyNode, lang, source, edges)
	}
}

// extractReceiver extracts the method receiver (Go, Rust).
func extractReceiver(n *gotreesitter.Node, lang *gotreesitter.Language, source []byte) string {
	return extractFieldText(n, lang, source, "receiver")
}

// isExportedModifier returns true for visibility keywords that mean exported/public.
func isExportedModifier(text string) bool {
	switch text {
	case "pub", "public", "export", "open":
		return true
	}
	return false
}

// visibilityFromModifiers determines symbol visibility from modifier nodes.
func visibilityFromModifiers(n *gotreesitter.Node, lang *gotreesitter.Language, source []byte) string {
	if n == nil {
		return "unexported"
	}
	for _, child := range n.Children() {
		kind := child.Type(lang)
		switch kind {
		case "visibility_modifier", "accessibility_level_modifier",
			"property_modifier", "modifier", "declaration_modifier":
			if isExportedModifier(child.Text(source)) {
				return "exported"
			}
			return "unexported"
		}
	}

	// Fallback: uppercase first char = exported (Go convention)
	nameNode := n.ChildByFieldName("name", lang)
	if nameNode != nil {
		name := nameNode.Text(source)
		if len(name) > 0 && name[0] >= 'A' && name[0] <= 'Z' {
			return "exported"
		}
	}

	return "unexported"
}

// hasChildKind checks if a node has a direct child with the given kind.
func hasChildKind(n *gotreesitter.Node, lang *gotreesitter.Language, targetKind string) bool {
	if n == nil {
		return false
	}
	for _, child := range n.Children() {
		if child.Type(lang) == targetKind {
			return true
		}
	}
	return false
}

// extractVariableDeclaratorSymbol extracts a function symbol when a variable
// is assigned an arrow or function expression (e.g. const add = (a, b) => a + b).
func extractVariableDeclaratorSymbol(n *gotreesitter.Node, lang *gotreesitter.Language, source []byte, filePath string, syms *[]Symbol, edges *[]Edge) {
	nameNode := n.ChildByFieldName("name", lang)
	if nameNode == nil {
		return
	}
	name := nameNode.Text(source)
	if name == "" {
		return
	}

	// Only extract if the value is an arrow_function or function expression
	valueNode := n.ChildByFieldName("value", lang)
	if valueNode == nil {
		return
	}
	valueKind := valueNode.Type(lang)
	if valueKind != "arrow_function" && valueKind != "function" && valueKind != "generator_function" {
		return
	}

	params := extractFieldText(valueNode, lang, source, "parameters")
	returnType := extractFieldText(valueNode, lang, source, "return_type")
	if returnType == "" {
		returnType = extractFieldText(valueNode, lang, source, "type")
	}

	vis := visibilityFromModifiers(n.Parent(), lang, source)
	isAsync := hasChildKind(valueNode, lang, "async")

	sym := Symbol{
		Kind:       "function",
		Name:       name,
		FilePath:   filePath,
		LineStart:  int(n.StartPoint().Row) + 1,
		LineEnd:    int(n.EndPoint().Row) + 1,
		Params:     params,
		ParamCount: countParams(params),
		ReturnType: returnType,
		Visibility: vis,
		IsAsync:    isAsync,
		Scope:      "package",
	}
	*syms = append(*syms, sym)

	bodyNode := valueNode.ChildByFieldName("body", lang)
	if bodyNode != nil {
		extractCallEdges(name, bodyNode, lang, source, edges)
	}
}

// countParams returns the number of comma-separated parameters in a
// pre-formatted parameters text like "(ctx context.Context, id int64)".
// Naive — doesn't handle nested generics <T, U> perfectly, but good
// enough for metrics. Returns 0 for empty or unparseable input.
func countParams(paramsText string) int {
	t := strings.TrimSpace(paramsText)
	t = strings.TrimPrefix(t, "(")
	t = strings.TrimSuffix(t, ")")
	if t == "" {
		return 0
	}
	return strings.Count(t, ",") + 1
}

// isPythonMethod detects if a function_definition is a method by checking
// if its first parameter is "self" or "cls" (Python convention).
func isPythonMethod(n *gotreesitter.Node, lang *gotreesitter.Language, source []byte) bool {
	paramsNode := n.ChildByFieldName("parameters", lang)
	if paramsNode == nil {
		return false
	}
	for _, child := range paramsNode.Children() {
		if child.IsNamed() {
			text := child.Text(source)
			return text == "self" || text == "cls"
		}
	}
	return false
}
