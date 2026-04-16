package api

import (
	"errors"
	"testing"
)

// TestIsArgusThread covers the bot-thread predicate. It is the gate that
// prevents `@argus-eye resolve` from closing human-authored threads, so it
// needs to be both inclusive (catch all Argus variants) and tight (reject
// anything that merely looks bot-like).
func TestIsArgusThread(t *testing.T) {
	tests := []struct {
		name  string
		login string
		want  bool
	}{
		{"exact argus-eye login", "argus-eye", true},
		{"argus-eye bot suffix", "argus-eye[bot]", true},
		// Other bots MUST NOT match — `@argus-eye resolve` should not touch
		// dependabot/codecov/renovate threads on the same PR.
		{"dependabot is not argus", "dependabot[bot]", false},
		{"codecov is not argus", "codecov[bot]", false},
		{"renovate is not argus", "renovate[bot]", false},
		{"generic bot suffix no match", "someapp[bot]", false},
		{"human login", "alice", false},
		{"human with digits", "alice42", false},
		{"human with argus in name", "argus-fan", false},
		{"human ending in bot (no brackets)", "robot", false},
		{"empty login", "", false},
		{"case-sensitive — uppercase variant must fail", "Argus-Eye", false},
		{"case-sensitive — [BOT] suffix must fail", "test[BOT]", false},
		{"substring match is not enough", "not-argus-eye", false},
		{"prefix match is not enough", "argus-eye-malicious", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isArgusThread(tc.login); got != tc.want {
				t.Errorf("isArgusThread(%q) = %v, want %v", tc.login, got, tc.want)
			}
		})
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
			wantPhrase: "missing permission — check the Argus GitHub App has write access to this repo",
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
			wantPhrase: "missing permission — check the Argus GitHub App has write access to this repo",
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
