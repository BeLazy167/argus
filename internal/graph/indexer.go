package graph

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"

	ghpkg "github.com/BeLazy167/argus/internal/github"
	"github.com/BeLazy167/argus/internal/store"
)

// sourceExts lists file extensions we parse for the code graph.
var sourceExts = map[string]bool{
	".go": true, ".ts": true, ".tsx": true, ".js": true, ".jsx": true,
	".mjs": true, ".cjs": true, ".py": true,
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

	// Upsert all nodes, building a name->ID lookup
	nameToID := make(map[string]int64)
	lang := func(path string) string { return langForFile(path) }

	for _, res := range results {
		for _, sym := range res.symbols {
			id, err := st.UpsertCodeNodeFull(ctx, repoDBID, sym.Kind, sym.Name, sym.FilePath, sym.LineStart, sym.LineEnd, lang(sym.FilePath), 0, sym.ReturnType, sym.Params, sym.Visibility, sym.IsAsync, sym.Receiver, sym.Scope)
			if err != nil {
				slog.Warn("graph: upsert node failed", "name", sym.Name, "error", err)
				continue
			}
			nameToID[sym.Name] = id
		}
	}

	// Upsert edges where both source and target exist in the graph
	for _, res := range results {
		for _, edge := range res.edges {
			sourceID, sok := nameToID[edge.SourceName]
			targetID, tok := nameToID[edge.TargetName]
			if !sok || !tok {
				continue
			}
			if err := st.UpsertCodeEdge(ctx, repoDBID, sourceID, targetID, edge.Kind); err != nil {
				slog.Warn("graph: upsert edge failed", "source", edge.SourceName, "target", edge.TargetName, "error", err)
			}
		}
	}

	return nil
}
