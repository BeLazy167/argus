package github

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

func TestListReviewCommentsUsesPRLevelEndpointAndFiltersReview(t *testing.T) {
	const installationID = int64(73)
	var requests int
	client := reviewTestClient(t, installationID, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/repos/acme/widgets/pulls/17/comments" {
			t.Fatalf("path = %q, want PR-level review-comments endpoint", r.URL.Path)
		}
		if got := r.URL.Query().Get("per_page"); got != "100" {
			t.Fatalf("per_page = %q, want 100", got)
		}

		w.Header().Set("Content-Type", "application/json")
		switch requests {
		case 1:
			w.Header().Set("Link", fmt.Sprintf(`<http://%s%s?per_page=100&page=2>; rel="next"`, r.Host, r.URL.Path))
			_, _ = w.Write([]byte(`[
				{"id":101,"pull_request_review_id":9001,"path":"pkg/pay.go","position":31,"line":184,"original_line":184,"start_line":178,"side":"RIGHT","body":"target"},
				{"id":102,"pull_request_review_id":8000,"path":"pkg/other.go","position":7,"line":44,"body":"other review"}
			]`))
		case 2:
			if got := r.URL.Query().Get("page"); got != "2" {
				t.Fatalf("page = %q, want 2", got)
			}
			_, _ = w.Write([]byte(`[
				{"id":103,"pull_request_review_id":9001,"path":"pkg/more.go","position":9,"line":77,"original_line":77,"side":"RIGHT","body":"second page"}
			]`))
		default:
			t.Fatalf("unexpected request %d", requests)
		}
	}))

	comments, err := client.ListReviewComments(context.Background(), installationID, "acme", "widgets", 17, 9001)
	if err != nil {
		t.Fatalf("ListReviewComments() error = %v", err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
	if len(comments) != 2 {
		t.Fatalf("comments = %d, want 2", len(comments))
	}
	if comments[0].GetID() != 101 || comments[0].GetLine() != 184 || comments[0].GetOriginalLine() != 184 || comments[0].GetStartLine() != 178 {
		t.Fatalf("first comment missing PR-level line fields: %+v", comments[0])
	}
	if comments[1].GetID() != 103 || comments[1].GetLine() != 77 {
		t.Fatalf("second comment = %+v", comments[1])
	}
}
