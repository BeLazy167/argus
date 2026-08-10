package graph

import (
	"context"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"

	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/internal/store"
)

// DefaultFullIndexFileCap bounds one full-repo index.
//
// indexFileSet streams file CONTENT — each body goes out of scope at the end of
// its iteration — but it must retain the parsed symbols and edges of every file
// until the cross-file edge-resolution pass runs. That retained set is what
// scales with repo size, and it is why an earlier full-index caller OOM'd a
// 512 MB VM on an 890-file repo and was deleted rather than bounded.
//
// The machines now run at 1024 MB, so 1500 files leaves real headroom over the
// size that previously failed. It also bounds the OTHER cost: indexFileSet
// fetches one file per GitHub API call, so this is 1500 calls against an
// installation's 5000/hour budget — which is why the scheduler runs one repo
// per tick rather than a whole installation at once.
const DefaultFullIndexFileCap = 1500

// filterSourceFiles keeps only the entries a parser can do something with.
//
// A real repository's tree is mostly not code. Every non-source entry that got
// through would cost a GitHub fetch and parse to nothing.
func filterSourceFiles(entries []string) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if sourceExts[strings.ToLower(filepath.Ext(e))] {
			out = append(out, e)
		}
	}
	return out
}

// selectIndexableFiles applies the cap, starting from offset, and reports what
// it left out plus where the next run should resume.
//
// Sorted, so a given offset always names the same window. The window ROTATES
// because a cap takes a prefix, and a fixed prefix would exclude the same
// alphabetical tail on every run forever — on a monorepo where web/ sorts after
// backend/, an entire top-level tree would never enter the graph while the repo
// reported as fully indexed.
//
// Rotating is safe because the orphan sweep is per-FILE: upsertFileSymbols
// loads hashes for one path and can only orphan ids belonging to that same
// path, so files a run does not visit keep their existing nodes. Successive
// windows accumulate coverage rather than deleting each other's work.
//
// Returns the dropped count rather than logging, so the caller can report it. A
// cap that truncates silently reads, to everything above it, as "this repo is
// fully indexed" — the same false confidence the fragmented graph produces.
func selectIndexableFiles(files []string, cap, offset int) (selected []string, dropped, nextOffset int) {
	sorted := make([]string, len(files))
	copy(sorted, files)
	sort.Strings(sorted)

	if cap <= 0 || len(sorted) <= cap {
		return sorted, 0, 0
	}
	if offset < 0 || offset >= len(sorted) {
		offset = 0
	}

	// Wraps, so a window near the end of the list is still a full window rather
	// than a short tail — otherwise the last run of each cycle would index fewer
	// files than the cap allows for no reason.
	selected = make([]string, 0, cap)
	for i := range cap {
		selected = append(selected, sorted[(offset+i)%len(sorted)])
	}
	return selected, len(sorted) - cap, (offset + cap) % len(sorted)
}

// IndexRepoBounded walks a repository's whole tree into the code graph.
//
// This is the recall fix. Blast radius was limited by EXTRACTION, not
// traversal: IndexFiles only ever parses one pull request's changed files, so
// the graph accumulated as disconnected PR-shaped islands and no traversal
// engine could join them. Walking the tree once is what turns those islands
// into a dependency graph.
//
// Bounded, unlike the caller that was deleted for OOMing. Returns how many
// files were indexed and how many the cap left out, so the scheduler can say so
// rather than record a partial index as a complete one.
func IndexRepoBounded(
	ctx context.Context,
	st *store.Store,
	ghClient *ghpkg.Client,
	installationID int64,
	owner, repo, ref string,
	repoDBID int64,
	fileCap int,
	cursor int,
) (indexed int, dropped int, nextCursor int, err error) {
	tree, err := ghClient.GetRepoTree(ctx, installationID, owner, repo, ref)
	if err != nil {
		return 0, 0, cursor, err
	}

	files, dropped, nextCursor := selectIndexableFiles(filterSourceFiles(tree), fileCap, cursor)
	if len(files) == 0 {
		// Not an error. A docs-only or empty repository has nothing to index,
		// and treating that as a failure would retry it on every tick forever.
		slog.Info("graph: full index found no source files", "repo", owner+"/"+repo)
		return 0, 0, 0, nil
	}

	slog.Info("graph: full index starting",
		"repo", owner+"/"+repo, "ref", ref, "files", len(files),
		"over_cap", dropped, "from_offset", cursor)

	if err := indexFileSet(ctx, st, ghClient, installationID, owner, repo, ref, repoDBID, files); err != nil {
		return 0, dropped, cursor, err
	}
	return len(files), dropped, nextCursor, nil
}
