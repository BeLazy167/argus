package pipeline

import (
	"reflect"
	"testing"

	"github.com/BeLazy167/argus/backend/pkg/diff"
)

func TestBlastRadiusBasePathsUsesPublishedPaths(t *testing.T) {
	tests := []struct {
		name  string
		files []diff.FileDiff
		want  []string
	}{
		{name: "modified uses current path", files: []diff.FileDiff{{NewName: "a.go", Status: diff.FileModified}}, want: []string{"a.go"}},
		{name: "deleted uses published old path", files: []diff.FileDiff{{OldName: "gone.go", Status: diff.FileDeleted}}, want: []string{"gone.go"}},
		{name: "rename uses published old path", files: []diff.FileDiff{{OldName: "old.go", NewName: "new.go", Status: diff.FileRenamed}}, want: []string{"old.go"}},
		{name: "deduplicates seeds", files: []diff.FileDiff{{NewName: "a.go", Status: diff.FileModified}, {NewName: "a.go", Status: diff.FileModified}}, want: []string{"a.go"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := blastRadiusBasePaths(&diff.PatchSet{Files: tt.files})
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("paths = %v, want %v", got, tt.want)
			}
		})
	}
}
