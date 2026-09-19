// jev_resolver_test.go: the BYOK/env precedence contract — a stored 'typesafe'
// key always wins (and needs no flag), the env client serves only
// jev_classifier opt-ins, and resolution failures fall through to env.
package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/internal/jev"
)

// fakeJevKeyResolver emulates the store's repo→installation cascade for the
// 'typesafe' provider slot without a DB.
type fakeJevKeyResolver struct {
	repoKey, repoBaseURL string
	hasRepo              bool
	instKey, instBaseURL string
	hasInst              bool
	err                  error
	calls                int
	gotRepoID            *int64
	gotProvider          string
}

func (f *fakeJevKeyResolver) ResolveAPIKey(_ context.Context, _ int64, repoID *int64, provider string) (string, string, bool, error) {
	f.calls++
	f.gotRepoID = repoID
	f.gotProvider = provider
	if f.err != nil {
		return "", "", false, f.err
	}
	if repoID != nil && f.hasRepo {
		return f.repoKey, f.repoBaseURL, true, nil
	}
	if f.hasInst {
		return f.instKey, f.instBaseURL, true, nil
	}
	return "", "", false, nil
}

// jevCapture starts a server that records the bearer token of each eval. The
// resolved *jev.Client hides its key, so the auth header is the only way to
// prove which stored row built it.
func jevCapture(t *testing.T) (baseURL string, auth *string, hits *int) {
	t.Helper()
	a, n := "", 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		a = r.Header.Get("Authorization")
		if r.URL.Path != "/v1/systemone" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		p := 0.9
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":   "jev-1.13.0",
			"answers": map[string]any{"q": map[string]any{"type": jev.TypeNoul, "noul": p}},
			"usage":   map[string]any{"input_tokens": 1, "output_tokens": 0},
		})
	}))
	t.Cleanup(srv.Close)
	return srv.URL, &a, &n
}

// repoID9 is the repo-context arg shared by every call.
func repoID9() *int64 { v := int64(9); return &v }

// evalOnce fires one eval so the capture server sees the resolved client's
// key and base URL.
func evalOnce(t *testing.T, ev jevEvaluator) {
	t.Helper()
	_, err := ev.Evaluate(t.Context(), map[string]any{"s": 1},
		map[string]jev.Question{"q": jev.NoulQuestion("q?", "yes", "no")}, "test")
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
}

func TestResolveJevEvaluator_StoredInstallationKey(t *testing.T) {
	keys := &fakeJevKeyResolver{hasInst: true, instKey: "ts-inst"}
	env := &fakeJev{}
	ev := resolveJevEvaluator(t.Context(), keys, env, 7, repoID9(), FeatureFlags{JevClassifier: true})
	if ev == nil {
		t.Fatal("stored key must resolve an evaluator")
	}
	if ev == jevEvaluator(env) {
		t.Fatal("stored key must win over the env client")
	}
	if keys.calls != 1 || keys.gotProvider != jev.ProviderName {
		t.Fatalf("resolve called %d times with provider %q, want 1 x %q",
			keys.calls, keys.gotProvider, jev.ProviderName)
	}
	if keys.gotRepoID == nil || *keys.gotRepoID != 9 {
		t.Fatalf("repoID not forwarded: %v", keys.gotRepoID)
	}
}

func TestResolveJevEvaluator_RepoKeyWinsOverInstallation(t *testing.T) {
	baseURL, auth, _ := jevCapture(t)
	keys := &fakeJevKeyResolver{
		hasRepo: true, repoKey: "ts-repo", repoBaseURL: baseURL,
		hasInst: true, instKey: "ts-inst", instBaseURL: "http://127.0.0.1:1",
	}
	ev := resolveJevEvaluator(t.Context(), keys, nil, 7, repoID9(), FeatureFlags{})
	if ev == nil {
		t.Fatal("stored key must resolve an evaluator")
	}
	evalOnce(t, ev)
	if *auth != "Bearer ts-repo" {
		t.Fatalf("repo-level key must win, got auth %q", *auth)
	}
}

func TestResolveJevEvaluator_InstallationFallback(t *testing.T) {
	baseURL, auth, _ := jevCapture(t)
	keys := &fakeJevKeyResolver{hasInst: true, instKey: "ts-inst", instBaseURL: baseURL}
	ev := resolveJevEvaluator(t.Context(), keys, nil, 7, repoID9(), FeatureFlags{})
	if ev == nil {
		t.Fatal("installation-level key must resolve when no repo row exists")
	}
	evalOnce(t, ev)
	if *auth != "Bearer ts-inst" {
		t.Fatalf("installation-level key must be used, got auth %q", *auth)
	}
}

func TestResolveJevEvaluator_BaseURLPassthrough(t *testing.T) {
	baseURL, _, hits := jevCapture(t)
	keys := &fakeJevKeyResolver{hasInst: true, instKey: "ts-inst", instBaseURL: baseURL}
	ev := resolveJevEvaluator(t.Context(), keys, nil, 7, repoID9(), FeatureFlags{})
	if ev == nil {
		t.Fatal("stored key must resolve an evaluator")
	}
	evalOnce(t, ev)
	if *hits != 1 {
		t.Fatalf("stored base_url not applied — custom endpoint saw %d requests", *hits)
	}
}

func TestResolveJevEvaluator_BYOKNeedsNoFlag(t *testing.T) {
	keys := &fakeJevKeyResolver{hasInst: true, instKey: "ts-inst"}
	env := &fakeJev{}
	ev := resolveJevEvaluator(t.Context(), keys, env, 7, repoID9(), FeatureFlags{JevClassifier: false})
	if ev == nil {
		t.Fatal("BYOK must resolve without jev_classifier — the key IS the consent")
	}
	if ev == jevEvaluator(env) {
		t.Fatal("BYOK must build its own client, not the env client")
	}
}

func TestResolveJevEvaluator_EnvClientWhenFlagOn(t *testing.T) {
	env := &fakeJev{}
	for name, keys := range map[string]jevKeyResolver{
		"no stored row": &fakeJevKeyResolver{},
		"nil resolver":  nil,
		"empty key":     &fakeJevKeyResolver{hasInst: true, instKey: ""},
		"lookup error":  &fakeJevKeyResolver{err: errors.New("db down")},
	} {
		t.Run(name, func(t *testing.T) {
			ev := resolveJevEvaluator(t.Context(), keys, env, 7, repoID9(), FeatureFlags{JevClassifier: true})
			if ev != jevEvaluator(env) {
				t.Fatal("no usable stored key + flag ON must return the env client")
			}
		})
	}
}

func TestResolveJevEvaluator_NoKeyNoEnvOrFlagIsNil(t *testing.T) {
	for name, tc := range map[string]struct {
		keys  jevKeyResolver
		env   jevEvaluator
		flags FeatureFlags
	}{
		"no key, flag off":       {keys: &fakeJevKeyResolver{}, env: &fakeJev{}, flags: FeatureFlags{}},
		"no key, no env":         {keys: &fakeJevKeyResolver{}, env: nil, flags: FeatureFlags{JevClassifier: true}},
		"lookup error, flag off": {keys: &fakeJevKeyResolver{err: errors.New("db down")}, env: &fakeJev{}, flags: FeatureFlags{}},
		"empty key, flag off":    {keys: &fakeJevKeyResolver{hasInst: true, instKey: ""}, env: &fakeJev{}, flags: FeatureFlags{}},
	} {
		t.Run(name, func(t *testing.T) {
			if ev := resolveJevEvaluator(t.Context(), tc.keys, tc.env, 7, repoID9(), tc.flags); ev != nil {
				t.Fatal("expected nil evaluator — straight to LLM")
			}
		})
	}
}

// TestResolveCandidates_BYOKSelectsJevCascade: a stored 'typesafe' key swaps
// the per-push judge for the Jev cascade — the resolved client hits the stored
// base_url with the stored bearer token, a confident answer resolves the
// thread, and the LLM judge never runs. Covers the wrap branch a nil-store
// harness cannot reach.
func TestResolveCandidates_BYOKSelectsJevCascade(t *testing.T) {
	var auth string
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		auth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "jev-1.13.0",
			"answers": map[string]any{
				"addressed": map[string]any{"type": jev.TypeNoul, "noul": 0.99},
				"evidence":  map[string]any{"type": jev.TypeNoul, "noul": 0.9},
			},
			"usage": map[string]any{"input_tokens": 5, "output_tokens": 1},
		})
	}))
	t.Cleanup(srv.Close)

	llmJudge := &fakeAddressedJudge{addressed: false}
	o, gh := newResolveHarness(llmJudge)
	o.jevKeys = &fakeJevKeyResolver{hasInst: true, instKey: "ts-byok", instBaseURL: srv.URL}
	threads, patch := candidateThreads(1)

	stats, _ := o.resolveCandidates(t.Context(),
		ghpkg.PREvent{InstallationID: 9, PRNumber: 1}, "o", "r", threads, nil, patch, 1, 2)

	if hits != 1 {
		t.Fatalf("BYOK eval must hit the stored endpoint once, got %d", hits)
	}
	if auth != "Bearer ts-byok" {
		t.Fatalf("stored key must auth the eval, got %q", auth)
	}
	if llmJudge.calls != 0 {
		t.Fatalf("confident jev verdict must skip the LLM judge, got %d calls", llmJudge.calls)
	}
	if stats.resolved != 1 || len(gh.resolved) != 1 {
		t.Fatalf("resolved=%d github=%v, want 1/1", stats.resolved, gh.resolved)
	}
}

// TestResolveCandidates_NoCandidatesSkipsResolution: a push with zero proximity
// candidates never touches the key resolver — the lazy judge resolution is the
// whole point of resolving inside the loop.
func TestResolveCandidates_NoCandidatesSkipsResolution(t *testing.T) {
	keys := &fakeJevKeyResolver{hasInst: true, instKey: "ts-byok"}
	o, _ := newResolveHarness(&fakeAddressedJudge{addressed: true})
	o.jevKeys = keys
	threads, patch := candidateThreads(1)
	threads[0].AuthorLogin = "someone-else" // not an Argus thread → skipped before candidacy

	o.resolveCandidates(t.Context(),
		ghpkg.PREvent{InstallationID: 9, PRNumber: 1}, "o", "r", threads, nil, patch, 1, 2)
	if keys.calls != 0 {
		t.Fatalf("no candidates must mean zero key lookups, got %d", keys.calls)
	}
}
