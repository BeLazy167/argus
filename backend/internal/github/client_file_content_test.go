package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	gh "github.com/google/go-github/v68/github"
)

func TestIsPermanentGitObjectError(t *testing.T) {
	apiError := func(status int) error {
		return fmt.Errorf("fetching file content: %w", &gh.ErrorResponse{
			Response: &http.Response{StatusCode: status},
		})
	}
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "bad immutable object request", err: apiError(http.StatusBadRequest), want: true},
		{name: "tree-listed path absent", err: apiError(http.StatusNotFound), want: true},
		{name: "immutable object gone", err: apiError(http.StatusGone), want: true},
		{name: "unprocessable immutable object", err: apiError(http.StatusUnprocessableEntity), want: true},
		{name: "unsupported immutable representation", err: &permanentFileContentError{err: errors.New("unsupported encoding")}, want: true},
		{name: "contents path guard", err: errors.New("fetching file content: path must not contain '..' due to auth vulnerability issue"), want: true},
		{name: "request timeout retries", err: apiError(http.StatusRequestTimeout)},
		{name: "too early retries", err: apiError(http.StatusTooEarly)},
		{name: "rate limit retries", err: apiError(http.StatusTooManyRequests)},
		{name: "server error retries", err: apiError(http.StatusInternalServerError)},
		{name: "bad gateway retries", err: apiError(http.StatusBadGateway)},
		{name: "transport error retries", err: errors.New("connection reset")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsPermanentGitObjectError(tt.err); got != tt.want {
				t.Fatalf("IsPermanentGitObjectError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDecodeRepositoryContentFallsBackToBoundedRawBlob(t *testing.T) {
	content := &gh.RepositoryContent{
		Encoding: gh.Ptr("none"),
		SHA:      gh.Ptr("blob-sha"),
		Size:     gh.Ptr((1 << 20) + 1),
	}
	called := false
	got, err := decodeRepositoryContent(context.Background(), content, func(_ context.Context, sha string) ([]byte, error) {
		called = true
		if sha != "blob-sha" {
			t.Fatalf("blob SHA = %q, want blob-sha", sha)
		}
		return []byte("package p\n"), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !called || got != "package p\n" {
		t.Fatalf("fallback called/content = %v/%q", called, got)
	}
}

func TestDecodeRepositoryContentRejectsBlobAboveBoundBeforeFetch(t *testing.T) {
	content := &gh.RepositoryContent{
		Encoding: gh.Ptr("none"),
		SHA:      gh.Ptr("blob-sha"),
		Size:     gh.Ptr(maxFileContentBytes + 1),
	}
	called := false
	_, err := decodeRepositoryContent(context.Background(), content, func(context.Context, string) ([]byte, error) {
		called = true
		return nil, nil
	})
	if err == nil || !IsPermanentGitObjectError(err) {
		t.Fatalf("error = %v, want permanent bounded-size failure", err)
	}
	if called {
		t.Fatal("oversized blob fetched before enforcing bound")
	}
}

func TestDecodeRepositoryContentRetriesRawBlobTransportFailure(t *testing.T) {
	content := &gh.RepositoryContent{
		Encoding: gh.Ptr("none"),
		SHA:      gh.Ptr("blob-sha"),
		Size:     gh.Ptr((1 << 20) + 1),
	}
	_, err := decodeRepositoryContent(context.Background(), content, func(context.Context, string) ([]byte, error) {
		return nil, errors.New("connection reset")
	})
	if err == nil || IsPermanentGitObjectError(err) {
		t.Fatalf("error = %v, want retryable raw-blob failure", err)
	}
}

func TestGetBlobContentUsesSingleRawBlobRequest(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet || r.URL.Path != "/repos/owner/repo/git/blobs/blob-sha" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Accept"); got != "application/vnd.github.v3.raw" {
			t.Fatalf("Accept = %q, want raw blob media type", got)
		}
		_, _ = w.Write([]byte("package p\n"))
	}))
	t.Cleanup(server.Close)

	installationClient := gh.NewClient(server.Client())
	baseURL := server.URL + "/"
	var err error
	installationClient.BaseURL, err = installationClient.BaseURL.Parse(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	app := NewApp(1, nil)
	app.clients[11] = installationClient

	got, err := NewClient(app, "argus").GetBlobContent(context.Background(), 11, "owner", "repo", RepoTreeFile{
		Path: "src/app/api/admin/[...slug]/route.ts", SHA: "blob-sha", Size: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "package p\n" || requests != 1 {
		t.Fatalf("content/requests = %q/%d, want package source/1", got, requests)
	}
}

func TestGetBlobContentRejectsInvalidTreeMetadataBeforeRequest(t *testing.T) {
	client := NewClient(NewApp(1, nil), "argus")
	for _, file := range []RepoTreeFile{
		{Path: "missing-sha.go", Size: 10},
		{Path: "too-large.go", SHA: "blob-sha", Size: maxFileContentBytes + 1},
	} {
		if _, err := client.GetBlobContent(context.Background(), 11, "owner", "repo", file); err == nil || !IsPermanentGitObjectError(err) {
			t.Fatalf("GetBlobContent(%+v) error = %v, want permanent metadata failure", file, err)
		}
	}
}

func TestGetRepoTreeReturnsImmutableBlobMetadata(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path+"?"+r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/repos/owner/repo/git/commits/commit-sha":
			_, _ = w.Write([]byte(`{"tree":{"sha":"tree-sha"}}`))
		case "/repos/owner/repo/git/trees/tree-sha":
			_, _ = w.Write([]byte(`{"truncated":false,"tree":[{"path":"main.go","mode":"100644","type":"blob","sha":"blob-sha","size":42},{"path":"src","type":"tree","sha":"subtree-sha"}]}`))
		default:
			t.Fatalf("unexpected request path %s", r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)

	installationClient := gh.NewClient(server.Client())
	baseURL := server.URL + "/"
	var err error
	installationClient.BaseURL, err = installationClient.BaseURL.Parse(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	app := NewApp(1, nil)
	app.clients[11] = installationClient

	tree, err := NewClient(app, "argus").GetRepoTree(context.Background(), 11, "owner", "repo", "commit-sha")
	if err != nil {
		t.Fatal(err)
	}
	want := RepoTreeFile{Path: "main.go", SHA: "blob-sha", Mode: "100644", Size: 42}
	if tree.Truncated || len(tree.Files) != 1 || tree.Files[0] != want {
		t.Fatalf("tree = %+v, want one immutable blob %+v", tree, want)
	}
	if len(paths) != 2 || paths[1] != "/repos/owner/repo/git/trees/tree-sha?recursive=1" {
		t.Fatalf("requests = %v, want commit then recursive tree", paths)
	}
}
