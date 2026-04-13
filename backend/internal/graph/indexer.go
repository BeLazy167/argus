package graph

import (
	"context"
	"log/slog"
	"path/filepath"
	"regexp"
	"strings"

	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/internal/store"
)

// sourceExts lists file extensions we parse for the code graph.
var sourceExts = map[string]bool{
	".go": true, ".ts": true, ".tsx": true, ".js": true, ".jsx": true,
	".mjs": true, ".cjs": true, ".py": true,
	".java": true, ".rs": true, ".cs": true, ".rb": true,
	".kt": true, ".kts": true, ".swift": true,
	".c": true, ".h": true, ".cpp": true, ".cc": true, ".cxx": true, ".hpp": true,
	".php": true, ".scala": true, ".dart": true,
}

// IndexRepo performs a full code graph index for a repository.
// Fetches the repo tree via GitHub API, parses each source file, and upserts nodes+edges.
func IndexRepo(ctx context.Context, st *store.Store, ghClient *ghpkg.Client, installationID int64, owner, repo, ref string, repoDBID int64) error {
	tree, err := ghClient.GetRepoTree(ctx, installationID, owner, repo, ref)
	if err != nil {
		return err
	}

	var files []string
	for _, entry := range tree {
		if sourceExts[strings.ToLower(filepath.Ext(entry))] {
			files = append(files, entry)
		}
	}

	slog.Info("graph: full index", "repo", owner+"/"+repo, "source_files", len(files))
	return indexFileSet(ctx, st, ghClient, installationID, owner, repo, ref, repoDBID, files)
}

// IndexFiles performs incremental code graph indexing for specific files.
// Deletes old nodes for these files, re-parses, and upserts.
func IndexFiles(ctx context.Context, st *store.Store, ghClient *ghpkg.Client, installationID int64, owner, repo, ref string, repoDBID int64, files []string) error {
	var sourceFiles []string
	for _, f := range files {
		if sourceExts[strings.ToLower(filepath.Ext(f))] {
			sourceFiles = append(sourceFiles, f)
		}
	}
	if len(sourceFiles) == 0 {
		return nil
	}

	// Delete old nodes for these files (edges cascade)
	for _, f := range sourceFiles {
		if err := st.DeleteNodesByFile(ctx, repoDBID, f); err != nil {
			slog.Warn("graph: delete nodes failed", "file", f, "error", err)
		}
	}

	slog.Info("graph: incremental index", "repo", owner+"/"+repo, "files", len(sourceFiles))
	return indexFileSet(ctx, st, ghClient, installationID, owner, repo, ref, repoDBID, sourceFiles)
}

// indexFileSet fetches content for each file, parses symbols/edges, and upserts them.
func indexFileSet(ctx context.Context, st *store.Store, ghClient *ghpkg.Client, installationID int64, owner, repo, ref string, repoDBID int64, files []string) error {
	// Collect all symbols and edges across files, then resolve edges by name
	type fileResult struct {
		symbols []Symbol
		edges   []Edge
	}
	results := make(map[string]fileResult, len(files))

	for _, f := range files {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		content, err := ghClient.GetFileContent(ctx, installationID, owner, repo, f, ref)
		if err != nil {
			slog.Warn("graph: fetch file failed", "file", f, "error", err)
			continue
		}
		syms, edges := ParseFileSymbols(f, content)
		results[f] = fileResult{symbols: syms, edges: edges}
	}

	// Upsert all nodes, building composite-key -> ID and name -> []ID lookups.
	// The composite key (filePath+name) avoids collisions when multiple files
	// define symbols with the same name. The name-only index supports
	// resolving edges that reference symbols by name alone.
	keyToID := make(map[string]int64)     // composite key -> node ID
	nameToIDs := make(map[string][]int64) // symbol name -> all node IDs with that name
	lang := func(path string) string { return langForFile(path) }

	for _, res := range results {
		for _, sym := range res.symbols {
			id, err := st.UpsertCodeNodeFull(ctx, repoDBID, sym.Kind, sym.Name, sym.FilePath, sym.LineStart, sym.LineEnd, lang(sym.FilePath), 0, sym.ReturnType, sym.Params, sym.Visibility, sym.IsAsync, sym.Receiver, sym.Scope)
			if err != nil {
				slog.Warn("graph: upsert node failed", "name", sym.Name, "file", sym.FilePath, "error", err)
				continue
			}
			key := nodeKey(sym.FilePath, sym.Name)
			keyToID[key] = id
			nameToIDs[sym.Name] = append(nameToIDs[sym.Name], id)
		}
	}

	// resolveEdgeTarget finds the best node ID for an edge target name.
	// For same-file references, prefer the node in the source file.
	// Otherwise, pick the first (most common) match.
	resolveEdgeTarget := func(sourceFile, targetName string) (int64, bool) {
		// Try same-file first (most precise)
		if id, ok := keyToID[nodeKey(sourceFile, targetName)]; ok {
			return id, true
		}
		// Fall back to any file
		ids := nameToIDs[targetName]
		if len(ids) > 0 {
			return ids[0], true
		}
		return 0, false
	}

	// Upsert edges where both source and target exist in the graph.
	// Iterate with filePath so we resolve edge source in its own file (composite key),
	// avoiding cross-file name collisions (e.g. multiple `init`, `New`, `Handle`).
	for filePath, res := range results {
		for _, edge := range res.edges {
			// Import edges: SourceName is a file path, not a symbol name.
			// These represent file-level dependencies and are resolved differently.
			if edge.Kind == "imports" {
				// Use any symbol defined in filePath as the edge source.
				// The import edge semantically means "this file depends on that module".
				var sourceID int64
				var found bool
				for _, sym := range res.symbols {
					if sym.FilePath != filePath {
						continue
					}
					if id, ok := keyToID[nodeKey(filePath, sym.Name)]; ok {
						sourceID = id
						found = true
						break
					}
				}
				if !found {
					continue
				}
				// Import targets are external packages — they won't be in nameToIDs.
				// Create a synthetic "module" node so the edge is preserved.
				// The code_nodes_kind_check constraint only allows
				// function|method|class|type|interface|file|module, so we
				// use "module" for external package references.
				targetID, tok := resolveEdgeTarget(filePath, edge.TargetName)
				if !tok {
					// Create a placeholder node for the external import target
					var err error
					targetID, err = st.UpsertCodeNode(ctx, repoDBID, "module", edge.TargetName, filePath, 0, 0, "", 0)
					if err != nil {
						slog.Warn("graph: upsert import node failed", "target", edge.TargetName, "error", err)
						continue
					}
					// Cache for future lookups
					keyToID[nodeKey(filePath, edge.TargetName)] = targetID
					nameToIDs[edge.TargetName] = append(nameToIDs[edge.TargetName], targetID)
				}
				if err := st.UpsertCodeEdge(ctx, repoDBID, sourceID, targetID, edge.Kind); err != nil {
					slog.Warn("graph: upsert edge failed", "source", filePath, "target", edge.TargetName, "error", err)
				}
				continue
			}

			// Non-import edges: resolve source in the file that produced the edge
			// (composite key) so two files defining a symbol with the same name
			// (e.g. `init`, `New`) don't have their call edges collapsed.
			sourceID, ok := keyToID[nodeKey(filePath, edge.SourceName)]
			if !ok {
				// Fallback: edge synthesized across files — pick first match.
				sourceIDs := nameToIDs[edge.SourceName]
				if len(sourceIDs) == 0 {
					continue
				}
				sourceID = sourceIDs[0]
			}

			targetID, tok := resolveEdgeTarget(filePath, edge.TargetName)
			if !tok {
				targetIDs := nameToIDs[edge.TargetName]
				if len(targetIDs) == 0 {
					continue
				}
				targetID = targetIDs[0]
			}
			if err := st.UpsertCodeEdge(ctx, repoDBID, sourceID, targetID, edge.Kind); err != nil {
				slog.Warn("graph: upsert edge failed", "source", edge.SourceName, "target", edge.TargetName, "error", err)
			}
		}
	}

	// Second pass: resolve uses_type edges from return types and parameter types
	var allSyms []Symbol
	for _, res := range results {
		allSyms = append(allSyms, res.symbols...)
	}
	for _, edge := range resolveTypeEdges(allSyms, keyToID) {
		sourceID := keyToID[edge.SourceName]
		targetID := keyToID[edge.TargetName]
		if err := st.UpsertCodeEdge(ctx, repoDBID, sourceID, targetID, edge.Kind); err != nil {
			slog.Warn("graph: upsert type edge failed", "source", edge.SourceName, "target", edge.TargetName, "error", err)
		}
	}

	return nil
}

// typeNameRe extracts identifiers from type expressions, stripping pointers, slices, maps, etc.
var typeNameRe = regexp.MustCompile(`\b([A-Za-z]\w+)`)

// typeKeywords are common language keywords/builtins that should not be treated as type names.
var typeKeywords = map[string]bool{
	"func": true, "return": true, "if": true, "else": true, "for": true,
	"var": true, "const": true, "let": true, "string": true, "int": true,
	"bool": true, "float": true, "void": true, "nil": true, "null": true,
	"true": true, "false": true, "error": true, "context": true, "map": true,
	"chan": true, "byte": true, "rune": true, "int64": true, "float64": true,
	"uint": true, "uint64": true, "int32": true, "uint32": true,
}

// extractTypeNames parses a type expression string and returns unique type names found.
// Strips *, [], map, chan prefixes and returns identifiers that aren't common keywords.
func extractTypeNames(typeExpr string) []string {
	if typeExpr == "" {
		return nil
	}
	seen := make(map[string]bool)
	var names []string
	for _, match := range typeNameRe.FindAllStringSubmatch(typeExpr, -1) {
		m := match[1]
		if !seen[m] && !typeKeywords[m] {
			seen[m] = true
			names = append(names, m)
		}
	}
	return names
}

// resolveTypeEdges creates uses_type edges by inspecting each symbol's ReturnType
// and Params for type names that exist in keyToID.
// SourceName and TargetName in returned edges use composite keys (filePath\x00name).
func resolveTypeEdges(symbols []Symbol, keyToID map[string]int64) []Edge {
	var edges []Edge
	seen := make(map[[2]string]bool)

	// Build a name-only lookup for type resolution (type names don't carry file paths)
	nameToKey := make(map[string]string) // symbol name -> first composite key found
	for key := range keyToID {
		// key format is "filePath\x00name"
		idx := strings.Index(key, "\x00")
		if idx < 0 {
			continue
		}
		name := key[idx+1:]
		if _, exists := nameToKey[name]; !exists {
			nameToKey[name] = key
		}
	}

	for _, sym := range symbols {
		srcKey := nodeKey(sym.FilePath, sym.Name)
		if _, ok := keyToID[srcKey]; !ok {
			continue
		}
		// Check return type
		for _, typeName := range extractTypeNames(sym.ReturnType) {
			if typeName == sym.Name {
				continue
			}
			tgtKey, ok := nameToKey[typeName]
			if !ok {
				continue
			}
			edgeKey := [2]string{srcKey, tgtKey}
			if seen[edgeKey] {
				continue
			}
			seen[edgeKey] = true
			edges = append(edges, Edge{SourceName: srcKey, TargetName: tgtKey, Kind: "uses_type"})
		}
		// Check params
		for _, typeName := range extractTypeNames(sym.Params) {
			if typeName == sym.Name {
				continue
			}
			tgtKey, ok := nameToKey[typeName]
			if !ok {
				continue
			}
			edgeKey := [2]string{srcKey, tgtKey}
			if seen[edgeKey] {
				continue
			}
			seen[edgeKey] = true
			edges = append(edges, Edge{SourceName: srcKey, TargetName: tgtKey, Kind: "uses_type"})
		}
	}
	return edges
}
