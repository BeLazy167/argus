package pipeline

import (
	"testing"

	ghpkg "github.com/BeLazy167/argus/internal/github"
)

func TestTallyReactions(t *testing.T) {
	tests := []struct {
		name      string
		reactions []ghpkg.CommentReaction
		wantUp    int
		wantDown  int
	}{
		{
			name:      "empty",
			reactions: nil,
			wantUp:    0,
			wantDown:  0,
		},
		{
			name: "only thumbs up",
			reactions: []ghpkg.CommentReaction{
				{ID: 1, User: "alice", Content: "+1"},
				{ID: 2, User: "bob", Content: "+1"},
			},
			wantUp:   2,
			wantDown: 0,
		},
		{
			name: "only thumbs down",
			reactions: []ghpkg.CommentReaction{
				{ID: 1, User: "alice", Content: "-1"},
			},
			wantUp:   0,
			wantDown: 1,
		},
		{
			name: "mixed with irrelevant",
			reactions: []ghpkg.CommentReaction{
				{ID: 1, User: "alice", Content: "+1"},
				{ID: 2, User: "bob", Content: "-1"},
				{ID: 3, User: "charlie", Content: "heart"},
				{ID: 4, User: "dave", Content: "rocket"},
				{ID: 5, User: "eve", Content: "+1"},
				{ID: 6, User: "frank", Content: "laugh"},
			},
			wantUp:   2,
			wantDown: 1,
		},
		{
			name: "all irrelevant",
			reactions: []ghpkg.CommentReaction{
				{ID: 1, User: "alice", Content: "heart"},
				{ID: 2, User: "bob", Content: "eyes"},
				{ID: 3, User: "charlie", Content: "confused"},
			},
			wantUp:   0,
			wantDown: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sig := TallyReactions(tt.reactions)
			if sig.Confirmed != tt.wantUp {
				t.Errorf("Confirmed = %d, want %d", sig.Confirmed, tt.wantUp)
			}
			if sig.Dismissed != tt.wantDown {
				t.Errorf("Dismissed = %d, want %d", sig.Dismissed, tt.wantDown)
			}
		})
	}
}

func TestReactionSignal_DominantSignal(t *testing.T) {
	tests := []struct {
		name string
		sig  ReactionSignal
		want string
	}{
		{"zero", ReactionSignal{0, 0}, ""},
		{"confirmed wins", ReactionSignal{3, 1}, "confirmed"},
		{"dismissed wins", ReactionSignal{1, 2}, "dismissed"},
		{"tied", ReactionSignal{2, 2}, ""},
		{"only confirmed", ReactionSignal{1, 0}, "confirmed"},
		{"only dismissed", ReactionSignal{0, 5}, "dismissed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.sig.DominantSignal()
			if got != tt.want {
				t.Errorf("DominantSignal() = %q, want %q", got, tt.want)
			}
		})
	}
}
