package api

import "testing"

func TestCanRememberRequiresScopeAppropriateTrust(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		association string
		orgWide     bool
		want        bool
	}{
		{name: "owner repo", association: "OWNER", want: true},
		{name: "member repo", association: " member ", want: true},
		{name: "collaborator repo", association: "collaborator", want: true},
		{name: "contributor repo", association: "CONTRIBUTOR", want: false},
		{name: "unknown repo", association: "", want: false},
		{name: "owner org", association: "OWNER", orgWide: true, want: true},
		{name: "member org", association: "member", orgWide: true, want: true},
		{name: "collaborator org", association: "COLLABORATOR", orgWide: true, want: false},
		{name: "contributor org", association: "CONTRIBUTOR", orgWide: true, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := canRemember(tt.association, tt.orgWide); got != tt.want {
				t.Fatalf("canRemember(%q, %v) = %v, want %v", tt.association, tt.orgWide, got, tt.want)
			}
		})
	}
}
