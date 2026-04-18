package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestReasoningEffort_Valid pins the allowed-values set. Any drift in the
// prod set requires updating both adjustRequestForProvider (which sends the
// value on the wire) AND this test.
func TestReasoningEffort_Valid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   ReasoningEffort
		want bool
	}{
		{ReasoningNone, true}, // empty string is the "provider default" sentinel
		{ReasoningMinimal, true},
		{ReasoningLow, true},
		{ReasoningMedium, true},
		{ReasoningHigh, true},
		{ReasoningXHigh, true},
		{ReasoningEffort("ultra-maximum"), false},
		{ReasoningEffort("Minimal"), false},         // case-sensitive
		{ReasoningEffort("  low  "), false},         // whitespace not trimmed
		{ReasoningEffort("low,medium"), false},      // no multi-value
		{ReasoningEffort("reasoning_low"), false},   // no prefixes
		{ReasoningEffort("0"), false},
	}
	for _, tc := range cases {
		name := string(tc.in)
		if name == "" {
			name = "empty"
		}
		t.Run(name, func(t *testing.T) {
			if got := tc.in.Valid(); got != tc.want {
				t.Errorf("ReasoningEffort(%q).Valid() = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestComplete_RejectsInvalidEffort guards the fail-fast validation at the top
// of ChatProvider.Complete — an unrecognized ReasoningEffort must return a
// typed error BEFORE any HTTP request fires. Without this guard, garbage
// round-trips to Azure as HTTP 400 mid-pipeline with an opaque provider
// error; the specialist caller then can't tell whether it was its input
// that was bad or the upstream was down.
func TestComplete_RejectsInvalidEffort(t *testing.T) {
	t.Parallel()
	// httptest server that fails the test if ever called — reaching HTTP is
	// the whole class of bug this guard prevents.
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	p := NewChatProvider("test", "k", server.URL)
	_, err := p.Complete(context.Background(), CompletionRequest{
		Model:           "gpt-5.4",
		Messages:        []Message{{Role: "user", Content: "hi"}},
		MaxTokens:       100,
		ReasoningEffort: ReasoningEffort("ultra-maximum"),
	})
	if err == nil {
		t.Fatal("expected error on invalid ReasoningEffort; got nil")
	}
	if !strings.Contains(err.Error(), "invalid reasoning_effort") {
		t.Errorf("error should name the field; got %q", err.Error())
	}
	if hits != 0 {
		t.Errorf("HTTP server was called %d times; validation must fail before network", hits)
	}
}
