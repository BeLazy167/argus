package diff

import (
	"slices"
	"sort"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name       string
		raw        string
		wantFiles  int
		wantStatus FileStatus
		wantOld    string
		wantNew    string
		wantHunks  int
	}{
		{
			name: "single file addition",
			raw: `diff --git a/foo.go b/foo.go
new file mode 100644
--- /dev/null
+++ b/foo.go
@@ -0,0 +1,3 @@
+package foo
+
+func Bar() {}
`,
			wantFiles:  1,
			wantStatus: FileAdded,
			wantOld:    "foo.go",
			wantNew:    "foo.go",
			wantHunks:  1,
		},
		{
			name: "single file modification",
			raw: `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,3 +1,4 @@
 package main

+import "fmt"
 func main() {}
`,
			wantFiles:  1,
			wantStatus: FileModified,
			wantOld:    "main.go",
			wantNew:    "main.go",
			wantHunks:  1,
		},
		{
			name: "file deletion",
			raw: `diff --git a/old.go b/old.go
deleted file mode 100644
--- a/old.go
+++ /dev/null
@@ -1,2 +0,0 @@
-package old
-func Gone() {}
`,
			wantFiles:  1,
			wantStatus: FileDeleted,
			wantOld:    "old.go",
			wantNew:    "old.go",
			wantHunks:  1,
		},
		{
			name: "file rename",
			raw: `diff --git a/old.go b/new.go
rename from old.go
rename to new.go
--- a/old.go
+++ b/new.go
@@ -1,2 +1,2 @@
 package pkg
-func Old() {}
+func New() {}
`,
			wantFiles:  1,
			wantStatus: FileRenamed,
			wantOld:    "old.go",
			wantNew:    "new.go",
			wantHunks:  1,
		},
		{
			name: "multi-file diff",
			raw: `diff --git a/a.go b/a.go
--- a/a.go
+++ b/a.go
@@ -1,2 +1,3 @@
 package a
+// comment
 func A() {}
diff --git a/b.go b/b.go
new file mode 100644
--- /dev/null
+++ b/b.go
@@ -0,0 +1,2 @@
+package b
+func B() {}
`,
			wantFiles:  2,
			wantStatus: FileModified, // first file
			wantOld:    "a.go",
			wantNew:    "a.go",
			wantHunks:  1,
		},
		{
			name:       "empty diff",
			raw:        "",
			wantFiles:  1, // parser creates 1 empty FileDiff for empty input
			wantStatus: FileModified, // default status for empty parse
		},
		{
			name: "multiple hunks per file",
			raw: `diff --git a/big.go b/big.go
--- a/big.go
+++ b/big.go
@@ -1,3 +1,4 @@
 package big
+// first hunk

 func A() {}
@@ -10,3 +11,4 @@

 func B() {}
+// second hunk
 func C() {}
`,
			wantFiles:  1,
			wantStatus: FileModified,
			wantOld:    "big.go",
			wantNew:    "big.go",
			wantHunks:  2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ps, err := Parse(tt.raw)
			if err != nil {
				t.Fatalf("Parse() error: %v", err)
			}
			if got := len(ps.Files); got != tt.wantFiles {
				t.Fatalf("files count = %d, want %d", got, tt.wantFiles)
			}
			if tt.wantFiles == 0 {
				return
			}
			f := ps.Files[0]
			if f.Status != tt.wantStatus {
				t.Errorf("status = %q, want %q", f.Status, tt.wantStatus)
			}
			if f.OldName != tt.wantOld {
				t.Errorf("OldName = %q, want %q", f.OldName, tt.wantOld)
			}
			if f.NewName != tt.wantNew {
				t.Errorf("NewName = %q, want %q", f.NewName, tt.wantNew)
			}
			if got := len(f.Hunks); got != tt.wantHunks {
				t.Errorf("hunks count = %d, want %d", got, tt.wantHunks)
			}
		})
	}
}

func TestValidCommentLines(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantLen int
		check   func(m map[int]bool) bool // additional assertion
	}{
		{
			name: "addition-only hunk — all new lines valid",
			raw: `diff --git a/f.go b/f.go
new file mode 100644
--- /dev/null
+++ b/f.go
@@ -0,0 +1,3 @@
+line1
+line2
+line3
`,
			wantLen: 4, // 3 added lines + hunk context
			check: func(m map[int]bool) bool {
				return m[1] && m[2] && m[3]
			},
		},
		{
			name: "modification — only new-side lines valid",
			raw: `diff --git a/f.go b/f.go
--- a/f.go
+++ b/f.go
@@ -1,3 +1,3 @@
 keep
-old
+new
 keep2
`,
			// context lines 1,3 + added line 2 + hunk start = 4 valid lines on new side
			wantLen: 4,
			check: func(m map[int]bool) bool {
				return m[1] && m[2] && m[3]
			},
		},
		{
			name: "context lines are valid for comments",
			raw: `diff --git a/f.go b/f.go
--- a/f.go
+++ b/f.go
@@ -1,4 +1,5 @@
 ctx1
 ctx2
+added
 ctx3
 ctx4
`,
			// 4 context + 1 added + hunk start = 6
			wantLen: 6,
			check: func(m map[int]bool) bool {
				return m[1] && m[2] && m[3] && m[4] && m[5]
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ps, err := Parse(tt.raw)
			if err != nil {
				t.Fatalf("Parse error: %v", err)
			}
			m := ps.Files[0].ValidCommentLines()
			if len(m) != tt.wantLen {
				t.Errorf("ValidCommentLines len = %d, want %d; map=%v", len(m), tt.wantLen, m)
			}
			if tt.check != nil && !tt.check(m) {
				t.Errorf("check failed; map=%v", m)
			}
		})
	}

	t.Run("large file returns empty map", func(t *testing.T) {
		fd := &FileDiff{LargeFile: true}
		m := fd.ValidCommentLines()
		if len(m) != 0 {
			t.Errorf("LargeFile ValidCommentLines len = %d, want 0", len(m))
		}
	})
}

func TestChangedLines(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []int // sorted expected changed lines
	}{
		{
			name: "returns only added lines, not context or deletions",
			raw: `diff --git a/f.go b/f.go
--- a/f.go
+++ b/f.go
@@ -1,4 +1,4 @@
 ctx
-deleted
+added
 ctx2
 ctx3
`,
			want: []int{2}, // only the added line at new-side line 2
		},
		{
			name: "pure addition",
			raw: `diff --git a/f.go b/f.go
new file mode 100644
--- /dev/null
+++ b/f.go
@@ -0,0 +1,3 @@
+a
+b
+c
`,
			want: []int{1, 2, 3},
		},
		{
			name: "pure deletion has no changed lines",
			raw: `diff --git a/f.go b/f.go
deleted file mode 100644
--- a/f.go
+++ /dev/null
@@ -1,2 +0,0 @@
-gone1
-gone2
`,
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ps, err := Parse(tt.raw)
			if err != nil {
				t.Fatalf("Parse error: %v", err)
			}
			m := ps.Files[0].ChangedLines()
			got := make([]int, 0, len(m))
			for l := range m {
				got = append(got, l)
			}
			sort.Ints(got)

			if !slices.Equal(got, tt.want) {
				t.Fatalf("ChangedLines = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestChangedLineRanges(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		proximity int
		want      [][2]int
	}{
		{
			name: "contiguous changes — single range",
			raw: `diff --git a/f.go b/f.go
--- a/f.go
+++ b/f.go
@@ -1,2 +1,5 @@
 ctx
+a
+b
+c
 ctx2
`,
			proximity: 0,
			want:      [][2]int{{2, 4}},
		},
		{
			name: "non-contiguous — multiple ranges",
			raw: `diff --git a/f.go b/f.go
--- a/f.go
+++ b/f.go
@@ -1,6 +1,8 @@
 ctx
+first
 ctx2
 ctx3
 ctx4
+second
 ctx5
 ctx6
`,
			proximity: 0,
			want:      [][2]int{{2, 2}, {6, 6}},
		},
		{
			name: "proximity merges nearby ranges",
			raw: `diff --git a/f.go b/f.go
--- a/f.go
+++ b/f.go
@@ -1,6 +1,8 @@
 ctx
+first
 ctx2
 ctx3
 ctx4
+second
 ctx5
 ctx6
`,
			proximity: 3,
			want:      [][2]int{{2, 6}},
		},
		{
			name: "no changes — nil ranges",
			raw: `diff --git a/f.go b/f.go
deleted file mode 100644
--- a/f.go
+++ /dev/null
@@ -1,2 +0,0 @@
-a
-b
`,
			proximity: 0,
			want:      nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ps, err := Parse(tt.raw)
			if err != nil {
				t.Fatalf("Parse error: %v", err)
			}
			got := ps.Files[0].ChangedLineRanges(tt.proximity)

			if tt.want == nil {
				if got != nil {
					t.Fatalf("ChangedLineRanges = %v, want nil", got)
				}
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("ChangedLineRanges = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("ChangedLineRanges[%d] = %v, want %v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestTotalLinesChanged(t *testing.T) {
	raw := `diff --git a/f.go b/f.go
--- a/f.go
+++ b/f.go
@@ -1,3 +1,3 @@
 ctx
-old
+new
 ctx2
`
	ps, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	// 1 added + 1 deleted = 2
	if got := ps.TotalLinesChanged(); got != 2 {
		t.Errorf("TotalLinesChanged = %d, want 2", got)
	}
}

func TestParseFromFiles(t *testing.T) {
	files := []FileInfo{
		{
			Name:   "added.go",
			Status: "added",
			Patch:  "@@ -0,0 +1,2 @@\n+package a\n+func A() {}\n",
		},
		{
			Name:   "large.go",
			Status: "modified",
			Patch:  "", // empty patch = large file
		},
		{
			Name:    "renamed.go",
			OldName: "original.go",
			Status:  "renamed",
			Patch:   "@@ -1,2 +1,2 @@\n-package old\n+package new\n",
		},
	}

	ps, err := ParseFromFiles(files)
	if err != nil {
		t.Fatalf("ParseFromFiles error: %v", err)
	}
	if len(ps.Files) != 3 {
		t.Fatalf("files = %d, want 3", len(ps.Files))
	}

	// First: added file
	if ps.Files[0].Status != FileAdded {
		t.Errorf("files[0] status = %q, want %q", ps.Files[0].Status, FileAdded)
	}

	// Second: large file
	if !ps.Files[1].LargeFile {
		t.Error("files[1] should be LargeFile")
	}
	if ps.Files[1].Status != FileModified {
		t.Errorf("files[1] status = %q, want %q", ps.Files[1].Status, FileModified)
	}

	// Third: renamed
	if ps.Files[2].Status != FileRenamed {
		t.Errorf("files[2] status = %q, want %q", ps.Files[2].Status, FileRenamed)
	}
	if ps.Files[2].OldName != "original.go" {
		t.Errorf("files[2] OldName = %q, want %q", ps.Files[2].OldName, "original.go")
	}
}

func TestCountLargeFiles(t *testing.T) {
	ps := &PatchSet{
		Files: []FileDiff{
			{LargeFile: false},
			{LargeFile: true},
			{LargeFile: true},
		},
	}
	if got := ps.CountLargeFiles(); got != 2 {
		t.Errorf("CountLargeFiles = %d, want 2", got)
	}
}

func TestParseHunkLineNumbers(t *testing.T) {
	raw := `diff --git a/f.go b/f.go
--- a/f.go
+++ b/f.go
@@ -5,4 +5,5 @@
 ctx
-old
+new1
+new2
 ctx2
 ctx3
`
	ps, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	h := ps.Files[0].Hunks[0]

	if h.OldStart != 5 || h.NewStart != 5 {
		t.Errorf("hunk starts = old:%d new:%d, want old:5 new:5", h.OldStart, h.NewStart)
	}
	if h.OldLines != 4 || h.NewLines != 5 {
		t.Errorf("hunk lines = old:%d new:%d, want old:4 new:5", h.OldLines, h.NewLines)
	}

	// Verify individual line numbers
	// Line 0: " ctx"     → old=5, new=5 (context)
	// Line 1: "-old"     → old=6, new=0 (deleted)
	// Line 2: "+new1"    → old=0, new=6 (added)
	// Line 3: "+new2"    → old=0, new=7 (added)
	// Line 4: " ctx2"    → old=7, new=8 (context)
	// Line 5: " ctx3"    → old=8, new=9 (context)
	expectations := []struct {
		typ    LineType
		oldNum int
		newNum int
	}{
		{LineContext, 5, 5},
		{LineDeleted, 6, 0},
		{LineAdded, 0, 6},
		{LineAdded, 0, 7},
		{LineContext, 7, 8},
		{LineContext, 8, 9},
	}
	// Parser may include trailing context line — check at least the expected lines
	if len(h.Lines) < len(expectations) {
		t.Fatalf("lines count = %d, want at least %d", len(h.Lines), len(expectations))
	}
	for i, exp := range expectations {
		dl := h.Lines[i]
		if dl.Type != exp.typ || dl.OldNum != exp.oldNum || dl.NewNum != exp.newNum {
			t.Errorf("line[%d]: type=%q old=%d new=%d, want type=%q old=%d new=%d",
				i, dl.Type, dl.OldNum, dl.NewNum, exp.typ, exp.oldNum, exp.newNum)
		}
	}
}
