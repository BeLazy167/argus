package pipeline

import "testing"

func TestMatchesSkipBranches(t *testing.T) {
	tests := []struct {
		name     string
		branch   string
		patterns []string
		want     bool
	}{
		{name: "empty patterns", branch: "main", patterns: nil, want: false},
		{name: "exact match", branch: "main", patterns: []string{"main"}, want: true},
		{name: "no match", branch: "staging", patterns: []string{"main"}, want: false},
		{name: "glob star", branch: "release/v1.2", patterns: []string{"release/*"}, want: true},
		{name: "glob no match", branch: "feat/login", patterns: []string{"release/*"}, want: false},
		{name: "multiple patterns first", branch: "main", patterns: []string{"main", "develop"}, want: true},
		{name: "multiple patterns second", branch: "develop", patterns: []string{"main", "develop"}, want: true},
		{name: "multiple patterns none", branch: "staging", patterns: []string{"main", "develop"}, want: false},
		{name: "question mark", branch: "v1", patterns: []string{"v?"}, want: true},
		{name: "question mark no match", branch: "v12", patterns: []string{"v?"}, want: false},
		{name: "bracket glob", branch: "staging", patterns: []string{"[sm]*"}, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchesSkipBranches(tt.branch, tt.patterns)
			if got != tt.want {
				t.Errorf("matchesSkipBranches(%q, %v) = %v, want %v", tt.branch, tt.patterns, got, tt.want)
			}
		})
	}
}
