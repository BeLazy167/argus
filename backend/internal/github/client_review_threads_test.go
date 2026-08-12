package github

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestListReviewThreadsPaginates(t *testing.T) {
	var calls int
	client := reviewTestClient(t, 77, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body struct {
			Variables struct {
				After *string `json:"after"`
			} `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			if body.Variables.After != nil {
				t.Fatalf("first after=%q, want null", *body.Variables.After)
			}
			_, _ = w.Write([]byte(`{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[{"id":"T1","comments":{"nodes":[{"databaseId":101}]} }],"pageInfo":{"hasNextPage":true,"endCursor":"CURSOR-1"}}}}}}`))
			return
		}
		if calls == 2 {
			if body.Variables.After == nil || *body.Variables.After != "CURSOR-1" {
				t.Fatalf("second after=%v", body.Variables.After)
			}
			_, _ = w.Write([]byte(`{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[{"id":"T2","comments":{"nodes":[{"databaseId":202}]}}],"pageInfo":{"hasNextPage":false,"endCursor":"CURSOR-2"}}}}}}`))
			return
		}
		t.Fatalf("unexpected GraphQL call %d", calls)
	}))

	threads, err := client.ListReviewThreads(context.Background(), 77, "acme", "widgets", 9)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(threads) != 2 || threads[0].ID != "T1" || threads[0].FirstCommentID != 101 || threads[1].ID != "T2" || threads[1].FirstCommentID != 202 {
		t.Fatalf("calls=%d threads=%+v", calls, threads)
	}
}
