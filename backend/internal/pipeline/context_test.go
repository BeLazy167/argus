package pipeline

import (
	"testing"
)

func TestAssignConfidence(t *testing.T) {
	tests := []struct {
		score int
		want  string
	}{
		{0, "low"},
		{64, "low"},
		{65, "medium"},
		{79, "medium"},
		{80, "high"},
		{100, "high"},
	}
	for _, tt := range tests {
		got := assignConfidence(tt.score)
		if got != tt.want {
			t.Errorf("assignConfidence(%d) = %q, want %q", tt.score, got, tt.want)
		}
	}
}

func TestFormatTypeContext_NilStore(t *testing.T) {
	got := FormatTypeContext(t.Context(), nil, 1, "main.go")
	if got != "" {
		t.Errorf("expected empty string for nil store, got %q", got)
	}
}
