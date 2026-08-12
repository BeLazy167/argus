package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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
		{name: "tree-listed path absent at immutable commit", err: apiError(http.StatusNotFound), want: true},
		{name: "unprocessable immutable object", err: apiError(http.StatusUnprocessableEntity), want: true},
		{name: "unsupported immutable representation", err: &permanentFileContentError{err: errors.New("unsupported encoding")}, want: true},
		{name: "authentication may recover", err: apiError(http.StatusUnauthorized)},
		{name: "rate limit or permission may recover", err: apiError(http.StatusForbidden)},
		{name: "server error retries", err: apiError(http.StatusServiceUnavailable)},
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
