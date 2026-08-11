package pipeline

import (
	"reflect"
	"testing"

	"github.com/BeLazy167/argus/backend/pkg/diff"
)

func TestGraphIndexPathsReconcilesDeletedAndRenamedFiles(t *testing.T) {
	tests := []struct {
		name        string
		files       []diff.FileDiff
		wantActive  []string
		wantRemoved []string
	}{
		{name: "modified", files: []diff.FileDiff{{NewName: "a.go", Status: diff.FileModified}}, wantActive: []string{"a.go"}},
		{name: "deleted uses old path", files: []diff.FileDiff{{OldName: "gone.go", Status: diff.FileDeleted}}, wantRemoved: []string{"gone.go"}},
		{name: "rename removes old and indexes new", files: []diff.FileDiff{{OldName: "old.go", NewName: "new.go", Status: diff.FileRenamed}}, wantActive: []string{"new.go"}, wantRemoved: []string{"old.go"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			active, removed := graphIndexPaths(&diff.PatchSet{Files: tt.files})
			if !reflect.DeepEqual(active, tt.wantActive) || !reflect.DeepEqual(removed, tt.wantRemoved) {
				t.Fatalf("active/removed = %v/%v, want %v/%v", active, removed, tt.wantActive, tt.wantRemoved)
			}
		})
	}
}
