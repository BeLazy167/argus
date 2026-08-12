package github

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	gh "github.com/google/go-github/v68/github"
	"golang.org/x/time/rate"
)

func reviewTestClient(t *testing.T, installationID int64, handler http.Handler) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := gh.NewClient(server.Client())
	baseURL, err := url.Parse(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	client.BaseURL = baseURL
	app := NewApp(1, nil)
	app.clients[installationID] = client
	return NewClient(app, "argus-eye")
}

func TestPostReviewReportsFailureCertainty(t *testing.T) {
	submission := &ReviewSubmission{Summary: "summary", HeadSHA: "abc123"}
	t.Run("final 4xx definitely did not create", func(t *testing.T) {
		client := reviewTestClient(t, 41, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s", r.Method)
			}
			http.Error(w, `{"message":"bad request"}`, http.StatusBadRequest)
		}))
		_, err := client.PostReview(context.Background(), 41, "acme", "widgets", 7, submission)
		if err == nil || !IsReviewDefinitelyNotCreated(err) {
			t.Fatalf("PostReview error = %v, want definitely-not-created", err)
		}
	})

	t.Run("successful response decode loss is ambiguous", func(t *testing.T) {
		client := reviewTestClient(t, 42, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":`))
		}))
		_, err := client.PostReview(context.Background(), 42, "acme", "widgets", 7, submission)
		if err == nil || IsReviewDefinitelyNotCreated(err) {
			t.Fatalf("PostReview error = %v, want ambiguous decode failure", err)
		}
	})

	t.Run("installation client creation failure definitely did not create", func(t *testing.T) {
		_, err := NewClient(nil, "argus-eye").PostReview(context.Background(), 99, "acme", "widgets", 7, submission)
		if err == nil || !IsReviewDefinitelyNotCreated(err) {
			t.Fatalf("PostReview error = %v, want definitely-not-created", err)
		}
	})

	t.Run("limiter cancellation before request definitely did not create", func(t *testing.T) {
		var calls atomic.Int32
		client := reviewTestClient(t, 43, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			t.Fatal("request escaped cancelled limiter")
		}))
		client.restLimiter = rate.NewLimiter(0, 0)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := client.PostReview(ctx, 43, "acme", "widgets", 7, submission)
		if err == nil || !IsReviewDefinitelyNotCreated(err) {
			t.Fatalf("PostReview error = %v, want definitely-not-created", err)
		}
		if calls.Load() != 0 {
			t.Fatalf("requests = %d, want 0", calls.Load())
		}
	})
}

func TestPostReviewDoesNotRepeatAmbiguousMutation(t *testing.T) {
	marker := ReviewMarker("review-5xx", 2)
	var posts atomic.Int32
	client := reviewTestClient(t, 44, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			posts.Add(1)
			http.Error(w, `{"message":"gateway timeout"}`, http.StatusGatewayTimeout)
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[]`))
		default:
			t.Fatalf("method = %s", r.Method)
		}
	}))
	_, err := client.PostReview(context.Background(), 44, "acme", "widgets", 7, &ReviewSubmission{Summary: "summary\n\n" + marker, HeadSHA: "head"})
	if err == nil || IsReviewDefinitelyNotCreated(err) {
		t.Fatalf("error=%v, want ambiguous", err)
	}
	if posts.Load() != 1 {
		t.Fatalf("CreateReview calls=%d, want 1", posts.Load())
	}
}

func TestPostReviewRecoversCommittedResponseLossByExactMarker(t *testing.T) {
	marker := ReviewMarker("review-loss", 3)
	var posts atomic.Int32
	client := reviewTestClient(t, 45, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts.Add(1)
			http.Error(w, `{"message":"bad gateway"}`, http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `[{"id":765,"body":%q,"commit_id":"head-loss","user":{"login":"argus-eye[bot]"}}]`, "summary\n\n"+marker)
	}))
	id, err := client.PostReview(context.Background(), 45, "acme", "widgets", 7, &ReviewSubmission{Summary: "summary\n\n" + marker, HeadSHA: "head-loss"})
	if err != nil || id != 765 {
		t.Fatalf("PostReview=(%d,%v)", id, err)
	}
	if posts.Load() != 1 {
		t.Fatalf("CreateReview calls=%d, want 1", posts.Load())
	}
}

func TestFindReviewByMarkerUsesInstallationAndPaginates(t *testing.T) {
	const marker = "<!-- argus-review:review-a:generation:3 -->"
	var pages []string
	client := reviewTestClient(t, 73, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/widgets/pulls/9/reviews" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		pages = append(pages, r.URL.Query().Get("page"))
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "2" {
			_, _ = fmt.Fprintf(w, `[{"id":991,"body":%q,"commit_id":"head-9","user":{"login":"argus-eye[bot]"}}]`, "summary\n\n"+marker)
			return
		}
		w.Header().Set("Link", "<"+strings.TrimSuffix(r.Host, "/")+">; rel=\"ignored\"")
		// go-github requires an absolute next URL.
		w.Header().Set("Link", fmt.Sprintf("<http://%s/repos/acme/widgets/pulls/9/reviews?page=2>; rel=\"next\"", r.Host))
		_, _ = w.Write([]byte(`[{"id":1,"body":"other","commit_id":"head-9","user":{"login":"argus-eye[bot]"}}]`))
	}))
	id, found, err := client.FindReviewByMarker(context.Background(), 73, "acme", "widgets", 9, marker, "head-9")
	if err != nil || !found || id != 991 {
		t.Fatalf("lookup = (%d,%v,%v)", id, found, err)
	}
	if len(pages) != 2 {
		t.Fatalf("pages = %v, want two", pages)
	}

	_, found, err = client.FindReviewByMarker(context.Background(), 999, "acme", "widgets", 9, marker, "head-9")
	if err == nil || found {
		t.Fatalf("wrong-tenant lookup = found %v err %v", found, err)
	}
}
