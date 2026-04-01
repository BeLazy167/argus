package diff

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// PatchSet represents the entire diff of a PR.
type PatchSet struct {
	Files []FileDiff
}

// FileDiff represents changes to a single file.
type FileDiff struct {
	OldName     string
	NewName     string
	Status      FileStatus // added, modified, deleted, renamed
	Hunks       []Hunk
	RawDiff     string // The raw unified diff for this file
	LargeFile   bool   // true when patch was too large for GitHub to return
	FullContent string // full file content fetched separately for large files
}

// Hunk represents a contiguous block of changes within a file.
type Hunk struct {
	OldStart int
	OldLines int
	NewStart int
	NewLines int
	Header   string // The @@ line
	Lines    []DiffLine
}

// DiffLine is a single line in a hunk.
type DiffLine struct {
	Type    LineType
	Content string
	OldNum  int // Line number in old file (0 if addition)
	NewNum  int // Line number in new file (0 if deletion)
}

type FileStatus string

const (
	FileAdded    FileStatus = "added"
	FileModified FileStatus = "modified"
	FileDeleted  FileStatus = "deleted"
	FileRenamed  FileStatus = "renamed"
)

type LineType string

const (
	LineContext  LineType = "context"
	LineAdded   LineType = "added"
	LineDeleted LineType = "deleted"
)

// ChangedLines returns the set of new-file line numbers that were actually
// added or modified (LineAdded) in this file diff. Unlike ValidCommentLines,
// this excludes context lines — only lines that represent real changes.
func (f *FileDiff) ChangedLines() map[int]bool {
	changed := make(map[int]bool)
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			if l.Type == LineAdded && l.NewNum > 0 {
				changed[l.NewNum] = true
			}
		}
	}
	return changed
}

// ChangedLineRanges returns a sorted list of contiguous line ranges that were
// modified in this file. Each range is [start, end] inclusive. Adjacent changed
// lines are merged into a single range with a configurable proximity gap.
func (f *FileDiff) ChangedLineRanges(proximity int) [][2]int {
	changed := f.ChangedLines()
	if len(changed) == 0 {
		return nil
	}
	// Collect and sort
	lines := make([]int, 0, len(changed))
	for l := range changed {
		lines = append(lines, l)
	}
	sort.Ints(lines)

	var ranges [][2]int
	start, end := lines[0], lines[0]
	for _, l := range lines[1:] {
		if l <= end+proximity+1 {
			end = l
		} else {
			ranges = append(ranges, [2]int{start, end})
			start, end = l, l
		}
	}
	ranges = append(ranges, [2]int{start, end})
	return ranges
}

// ValidCommentLines returns the set of new-file line numbers that GitHub accepts
// for Side:"RIGHT" review comments (additions + context lines with NewNum > 0).
func (f *FileDiff) ValidCommentLines() map[int]bool {
	if f.LargeFile {
		return make(map[int]bool)
	}
	valid := make(map[int]bool)
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			if l.NewNum > 0 {
				valid[l.NewNum] = true
			}
		}
	}
	return valid
}

// Parse parses a unified diff string into a PatchSet.
func Parse(raw string) (*PatchSet, error) {
	ps := &PatchSet{}
	fileDiffs := splitFiles(raw)

	for _, fd := range fileDiffs {
		file, err := parseFileDiff(fd)
		if err != nil {
			return nil, fmt.Errorf("parsing file diff: %w", err)
		}
		ps.Files = append(ps.Files, *file)
	}
	return ps, nil
}

// TotalLinesChanged returns the total number of added + deleted lines across all files.
func (ps *PatchSet) TotalLinesChanged() int {
	total := 0
	for _, f := range ps.Files {
		for _, h := range f.Hunks {
			for _, l := range h.Lines {
				if l.Type == LineAdded || l.Type == LineDeleted {
					total++
				}
			}
		}
	}
	return total
}

// CountLargeFiles returns the number of files that were too large for GitHub to return a patch.
func (ps *PatchSet) CountLargeFiles() int {
	n := 0
	for _, f := range ps.Files {
		if f.LargeFile {
			n++
		}
	}
	return n
}

// FileInfo holds per-file data from GitHub's List PR Files API.
type FileInfo struct {
	Name    string
	OldName string
	Status  string // "added", "modified", "removed", "renamed"
	Patch   string // may be empty for large files
}

// ParseFromFiles builds a PatchSet from GitHub's per-file API data.
// Files with a patch are parsed normally; files without are marked as LargeFile.
func ParseFromFiles(files []FileInfo) (*PatchSet, error) {
	ps := &PatchSet{}
	for _, f := range files {
		var status FileStatus
		switch f.Status {
		case "added":
			status = FileAdded
		case "removed":
			status = FileDeleted
		case "renamed":
			status = FileRenamed
		default:
			status = FileModified
		}

		oldName := f.OldName
		if oldName == "" {
			oldName = f.Name
		}

		if f.Patch != "" {
			raw := fmt.Sprintf("diff --git a/%s b/%s\n--- a/%s\n+++ b/%s\n%s\n", oldName, f.Name, oldName, f.Name, f.Patch)
			fd, err := parseFileDiff(raw)
			if err != nil {
				return nil, fmt.Errorf("parsing patch for %s: %w", f.Name, err)
			}
			fd.Status = status
			ps.Files = append(ps.Files, *fd)
		} else {
			ps.Files = append(ps.Files, FileDiff{
				OldName:   oldName,
				NewName:   f.Name,
				Status:    status,
				LargeFile: true,
			})
		}
	}
	return ps, nil
}

func splitFiles(raw string) []string {
	var files []string
	lines := strings.Split(raw, "\n")
	var current []string

	for _, line := range lines {
		if strings.HasPrefix(line, "diff --git") {
			if len(current) > 0 {
				files = append(files, strings.Join(current, "\n"))
			}
			current = []string{line}
		} else {
			current = append(current, line)
		}
	}
	if len(current) > 0 {
		files = append(files, strings.Join(current, "\n"))
	}
	return files
}

func parseFileDiff(raw string) (*FileDiff, error) {
	fd := &FileDiff{RawDiff: raw}
	lines := strings.Split(raw, "\n")

	i := 0
	// Parse header
	for i < len(lines) {
		line := lines[i]
		if strings.HasPrefix(line, "diff --git") {
			parts := strings.Fields(line)
			if len(parts) >= 4 {
				fd.OldName = strings.TrimPrefix(parts[2], "a/")
				fd.NewName = strings.TrimPrefix(parts[3], "b/")
			}
		} else if strings.HasPrefix(line, "new file") {
			fd.Status = FileAdded
		} else if strings.HasPrefix(line, "deleted file") {
			fd.Status = FileDeleted
		} else if strings.HasPrefix(line, "rename from") {
			fd.Status = FileRenamed
		} else if strings.HasPrefix(line, "@@") {
			break
		}
		i++
	}

	if fd.Status == "" {
		fd.Status = FileModified
	}

	// Parse hunks
	for i < len(lines) {
		if strings.HasPrefix(lines[i], "@@") {
			hunk, nextI, err := parseHunk(lines, i)
			if err != nil {
				return nil, err
			}
			fd.Hunks = append(fd.Hunks, *hunk)
			i = nextI
		} else {
			i++
		}
	}

	return fd, nil
}

func parseHunk(lines []string, start int) (*Hunk, int, error) {
	header := lines[start]
	hunk := &Hunk{Header: header}

	// Parse @@ -oldStart,oldLines +newStart,newLines @@
	parts := strings.SplitN(header, "@@", 3)
	if len(parts) < 2 {
		return nil, start + 1, fmt.Errorf("invalid hunk header: %s", header)
	}
	ranges := strings.TrimSpace(parts[1])
	rangeParts := strings.Fields(ranges)

	if len(rangeParts) >= 2 {
		old := strings.TrimPrefix(rangeParts[0], "-")
		new := strings.TrimPrefix(rangeParts[1], "+")

		oldParts := strings.SplitN(old, ",", 2)
		var err error
		hunk.OldStart, err = strconv.Atoi(oldParts[0])
		if err != nil {
			return nil, start + 1, fmt.Errorf("parsing old start line %q: %w", oldParts[0], err)
		}
		if len(oldParts) > 1 {
			hunk.OldLines, err = strconv.Atoi(oldParts[1])
			if err != nil {
				return nil, start + 1, fmt.Errorf("parsing old line count %q: %w", oldParts[1], err)
			}
		} else {
			hunk.OldLines = 1
		}

		newParts := strings.SplitN(new, ",", 2)
		hunk.NewStart, err = strconv.Atoi(newParts[0])
		if err != nil {
			return nil, start + 1, fmt.Errorf("parsing new start line %q: %w", newParts[0], err)
		}
		if len(newParts) > 1 {
			hunk.NewLines, err = strconv.Atoi(newParts[1])
			if err != nil {
				return nil, start + 1, fmt.Errorf("parsing new line count %q: %w", newParts[1], err)
			}
		} else {
			hunk.NewLines = 1
		}
	}

	oldNum := hunk.OldStart
	newNum := hunk.NewStart
	i := start + 1

	for i < len(lines) {
		line := lines[i]
		if strings.HasPrefix(line, "@@") || strings.HasPrefix(line, "diff --git") {
			break
		}

		dl := DiffLine{Content: line}
		switch {
		case strings.HasPrefix(line, "+"):
			dl.Type = LineAdded
			dl.Content = strings.TrimPrefix(line, "+")
			dl.NewNum = newNum
			newNum++
		case strings.HasPrefix(line, "-"):
			dl.Type = LineDeleted
			dl.Content = strings.TrimPrefix(line, "-")
			dl.OldNum = oldNum
			oldNum++
		case strings.HasPrefix(line, " "):
			dl.Type = LineContext
			dl.Content = strings.TrimPrefix(line, " ")
			dl.OldNum = oldNum
			dl.NewNum = newNum
			oldNum++
			newNum++
		case line == `\ No newline at end of file`:
			i++
			continue
		default:
			// Empty context line
			dl.Type = LineContext
			dl.OldNum = oldNum
			dl.NewNum = newNum
			oldNum++
			newNum++
		}
		hunk.Lines = append(hunk.Lines, dl)
		i++
	}

	return hunk, i, nil
}
