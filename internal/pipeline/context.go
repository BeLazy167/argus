package pipeline

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	ghpkg "github.com/BeLazy167/argus/internal/github"
	"github.com/BeLazy167/argus/internal/store"
	"github.com/BeLazy167/argus/pkg/diff"
)

// RelatedFile represents a file related to the current diff with a relevance reason.
type RelatedFile struct {
	Path    string
	Reason  string // e.g., "calls calculateProration", "imports billing/types"
	Content string // truncated file content
	Score   int    // relevance score for sorting
}

// maxRelatedFiles is the maximum number of related files to include in context.
const maxRelatedFiles = 5

// maxRelatedContentLines caps how many lines of each related file are included.
const maxRelatedContentLines = 150

// GatherCrossFileContext analyzes a diff file to find related files in the repository
// that callers, importers, or co-dependents. Returns up to maxRelatedFiles results.
func GatherCrossFileContext(
	ctx context.Context,
	ghClient *ghpkg.Client,
	installationID int64,
	owner, repo, ref string,
	file diff.FileDiff,
	allDiffFiles []diff.FileDiff,
) []RelatedFile {
	symbols := extractDefinedSymbols(file)
	imports := extractImportPaths(file)

	var candidates []RelatedFile

	// 1. Find callers of symbols defined in this diff
	for _, sym := range symbols {
		files, err := ghClient.SearchCode(ctx, installationID, owner, repo, sym)
		if err != nil {
			continue
		}
		for _, f := range files {
			if f == file.NewName {
				continue // skip self
			}
			if isDiffFile(f, allDiffFiles) {
				continue // skip files already in the diff (reviewed separately)
			}
			candidates = append(candidates, RelatedFile{
				Path:   f,
				Reason: fmt.Sprintf("references `%s`", sym),
				Score:  10,
			})
		}
	}

	// 2. Find test files for the changed file
	testPath := guessTestFile(file.NewName)
	if testPath != "" && testPath != file.NewName {
		candidates = append(candidates, RelatedFile{
			Path:   testPath,
			Reason: "test file for this module",
			Score:  8,
		})
	}

	// 3. Find files imported by the changed file
	for _, imp := range imports {
		candidates = append(candidates, RelatedFile{
			Path:   imp,
			Reason: "imported by changed file",
			Score:  5,
		})
	}

	// Deduplicate by path, keeping highest score
	seen := make(map[string]int)
	var deduped []RelatedFile
	for _, c := range candidates {
		if idx, ok := seen[c.Path]; ok {
			if c.Score > deduped[idx].Score {
				deduped[idx] = c
			}
			continue
		}
		seen[c.Path] = len(deduped)
		deduped = append(deduped, c)
	}

	// Sort by relevance score descending
	sort.Slice(deduped, func(i, j int) bool { return deduped[i].Score > deduped[j].Score })

	// Cap results
	if len(deduped) > maxRelatedFiles {
		deduped = deduped[:maxRelatedFiles]
	}

	// Fetch file contents
	var results []RelatedFile
	for _, c := range deduped {
		content, err := ghClient.GetFileContent(ctx, installationID, owner, repo, c.Path, ref)
		if err != nil {
			continue // skip files we can't fetch (deleted, binary, etc)
		}
		c.Content = truncateLines(content, maxRelatedContentLines)
		results = append(results, c)
	}

	return results
}

// FormatRelatedContext formats related files as a context block for the review prompt.
func FormatRelatedContext(related []RelatedFile) string {
	if len(related) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("\n<related_context>\n")
	sb.WriteString("The following files are related to the changes in this PR:\n\n")
	for _, r := range related {
		sb.WriteString(fmt.Sprintf("File: %s (%s)\n```\n%s\n```\n\n", r.Path, r.Reason, r.Content))
	}
	sb.WriteString("</related_context>\n")
	return sb.String()
}

// FormatBlastRadius formats code graph blast radius results as a context block for the review prompt.
// If fileContents is provided, includes source code of key dependent files (depth 1 only, capped).
func FormatBlastRadius(nodes []store.CodeNode, fileContents map[string]string) string {
	if len(nodes) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("\n<blast_radius>\n")
	sb.WriteString("These code symbols depend on the changed files. Check if your changes break them:\n\n")
	for _, n := range nodes {
		sb.WriteString(fmt.Sprintf("- [depth %d] %s `%s` in %s\n", n.Depth, n.Kind, n.Name, n.FilePath))
	}

	// Include source of depth-1 dependents so the LLM can trace call chains
	if len(fileContents) > 0 {
		sb.WriteString("\n### Dependent file contents (outside the diff — for reference only, do NOT comment on these files)\n\n")
		for path, content := range fileContents {
			sb.WriteString(fmt.Sprintf("**%s**\n```\n%s\n```\n\n", path, content))
		}
	}

	sb.WriteString("Key questions: Do callers assume a specific return type/value that changed? Do dependents handle the error cases you introduced? Could a new null/undefined return break a dependent that doesn't check for it?\n")
	sb.WriteString("</blast_radius>\n")
	return sb.String()
}

// --- Symbol extraction (regex-based MVP) ---

// funcDefRe matches function/method definitions across common languages.
var funcDefRe = regexp.MustCompile(`(?m)^[\+].*(?:func |function |def |fn |public |private |protected )?(\w{3,})\s*[\(\[<]`)

// extractDefinedSymbols extracts function/method names from added lines in the diff.
func extractDefinedSymbols(file diff.FileDiff) []string {
	var symbols []string
	seen := make(map[string]bool)
	for _, match := range funcDefRe.FindAllStringSubmatch(file.RawDiff, -1) {
		name := match[1]
		// Skip common keywords and short names
		if isKeyword(name) || len(name) < 3 || seen[name] {
			continue
		}
		seen[name] = true
		symbols = append(symbols, name)
	}
	// Cap to avoid too many searches
	if len(symbols) > 8 {
		symbols = symbols[:8]
	}
	return symbols
}

// importRe matches import/require/from statements.
var importRe = regexp.MustCompile(`(?m)(?:import\s+.*from\s+["']([^"']+)["']|require\s*\(\s*["']([^"']+)["']\)|"([^"]+/[^"]+)")`)

// extractImportPaths extracts imported file paths from the diff.
func extractImportPaths(file diff.FileDiff) []string {
	var paths []string
	seen := make(map[string]bool)
	for _, match := range importRe.FindAllStringSubmatch(file.RawDiff, -1) {
		for _, p := range match[1:] {
			if p == "" || seen[p] {
				continue
			}
			// Convert relative imports to file paths
			resolved := resolveImportPath(file.NewName, p)
			if resolved != "" && !seen[resolved] {
				seen[resolved] = true
				paths = append(paths, resolved)
			}
		}
	}
	return paths
}

func resolveImportPath(currentFile, importPath string) string {
	// Skip external packages
	if strings.Contains(importPath, "github.com/") || strings.HasPrefix(importPath, "@") {
		return ""
	}
	// Relative imports
	if strings.HasPrefix(importPath, "./") || strings.HasPrefix(importPath, "../") {
		dir := filepath.Dir(currentFile)
		return filepath.Clean(filepath.Join(dir, importPath))
	}
	// Go imports within the same module
	if !strings.Contains(importPath, ".") && strings.Contains(importPath, "/") {
		return importPath
	}
	return ""
}

// guessTestFile attempts to find the corresponding test file for a source file.
func guessTestFile(path string) string {
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)

	switch ext {
	case ".go":
		return base + "_test.go"
	case ".ts", ".tsx":
		return base + ".test" + ext
	case ".js", ".jsx":
		return base + ".test" + ext
	case ".py":
		dir := filepath.Dir(path)
		name := filepath.Base(base)
		return filepath.Join(dir, "test_"+name+ext)
	default:
		return ""
	}
}

func isDiffFile(path string, diffFiles []diff.FileDiff) bool {
	for _, f := range diffFiles {
		if f.NewName == path {
			return true
		}
	}
	return false
}

var keywords = map[string]bool{
	"func": true, "function": true, "return": true, "const": true, "var": true, "let": true,
	"class": true, "interface": true, "struct": true, "type": true, "import": true, "export": true,
	"if": true, "else": true, "for": true, "range": true, "switch": true, "case": true,
	"default": true, "package": true, "public": true, "private": true, "protected": true,
	"async": true, "await": true, "new": true, "nil": true, "null": true, "undefined": true,
	"true": true, "false": true, "string": true, "int": true, "bool": true, "error": true,
}

func isKeyword(s string) bool {
	return keywords[strings.ToLower(s)]
}
