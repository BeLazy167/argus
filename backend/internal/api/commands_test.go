package api

import (
	"errors"
	"testing"

	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
)

// TestIsArgusCommentAuthor pins the api-layer alias to the canonical
// ghpkg.IsArgusThread helper. Exhaustive truth-table lives in
// internal/github/identity_test.go — this test only guards the aliasing.
func TestIsArgusCommentAuthor(t *testing.T) {
	for _, login := range []string{"argus-eye", "argus-eye[bot]", "dependabot[bot]", ""} {
		want := ghpkg.IsArgusThread(login)
		if got := isArgusCommentAuthor(login); got != want {
			t.Errorf("isArgusCommentAuthor(%q) = %v, want %v (mismatch with canonical helper)",
				login, got, want)
		}
	}
}

// TestClassifyResolveError covers the error-classification logic used by
// handleResolveCommand to decide whether to abort early (fatal) and what
// to show the user. The phrases are user-facing and should not be leaked
// across HTTP status codes.
func TestClassifyResolveError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantPhrase string
		wantFatal  bool
	}{
		{
			name:       "nil error",
			err:        nil,
			wantPhrase: "",
			wantFatal:  false,
		},
		{
			name:       "401 unauthorized",
			err:        errors.New("GET /repos/...: 401 Unauthorized"),
			wantPhrase: "authentication failed — Argus may need to be reinstalled on this repo",
			wantFatal:  true,
		},
		{
			name:       "403 forbidden",
			err:        errors.New("403 Forbidden: requires write access"),
			wantPhrase: "missing permission — grant the Argus GitHub App `Pull requests: write` access (https://github.com/settings/installations)",
			wantFatal:  true,
		},
		{
			name:       "graphql resource not accessible by integration",
			err:        errors.New("graphql errors: Resource not accessible by integration"),
			wantPhrase: "missing permission — grant the Argus GitHub App `Pull requests: write` access (https://github.com/settings/installations)",
			wantFatal:  true,
		},
		{
			name:       "404 stale thread",
			err:        errors.New("404 Not Found: thread does not exist"),
			wantPhrase: "thread already resolved or deleted upstream",
			wantFatal:  false,
		},
		{
			name:       "rate limit",
			err:        errors.New("API rate limit exceeded"),
			wantPhrase: "GitHub API rate limit hit — retry in a few minutes",
			wantFatal:  true,
		},
		{
			name:       "unknown error",
			err:        errors.New("unexpected EOF from upstream"),
			wantPhrase: "",
			wantFatal:  false,
		},
		{
			name:       "permission keyword without status code",
			err:        errors.New("resource not accessible: permission denied"),
			wantPhrase: "missing permission — grant the Argus GitHub App `Pull requests: write` access (https://github.com/settings/installations)",
			wantFatal:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			phrase, fatal := classifyResolveError(tc.err)
			if phrase != tc.wantPhrase {
				t.Errorf("phrase = %q, want %q", phrase, tc.wantPhrase)
			}
			if fatal != tc.wantFatal {
				t.Errorf("fatal = %v, want %v", fatal, tc.wantFatal)
			}
		})
	}
}
