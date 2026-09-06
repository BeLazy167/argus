package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/BeLazy167/argus/backend/internal/memory"
	"github.com/BeLazy167/argus/backend/internal/store"
)

// recordingIndexer is a partial fake: only the methods these tools call are
// implemented; anything else nil-panics, the established idiom for
// memory.Indexer doubles outside memorytest.
type recordingIndexer struct {
	memory.Indexer
	lastQuery    memory.MemoryQuery
	lastBriefing memory.BriefingQuery
	matches      []memory.PatternMatch
	briefing     string
	err          error
	retireFn     func(memory.RetireRequest) (memory.RetireResult, error)
}

func (r *recordingIndexer) Search(_ context.Context, q memory.MemoryQuery) ([]memory.PatternMatch, error) {
	r.lastQuery = q
	return r.matches, r.err
}
func (r *recordingIndexer) Briefing(_ context.Context, q memory.BriefingQuery) (string, error) {
	r.lastBriefing = q
	return r.briefing, r.err
}
func (r *recordingIndexer) RetireDocument(_ context.Context, req memory.RetireRequest) (memory.RetireResult, error) {
	if r.retireFn == nil {
		return memory.RetireResult{}, errors.New("retireFn not stubbed")
	}
	return r.retireFn(req)
}

func readScope(installationIDs ...int64) tenantScope {
	return tenantScope{userID: "user_r", orgID: "org_r", installationIDs: installationIDs, grantedScopes: []string{scopeRead}}
}

func writeScope(installationIDs ...int64) tenantScope {
	return tenantScope{userID: "user_w", orgID: "org_w", installationIDs: installationIDs, grantedScopes: []string{scopeRead, scopeMemoryWrite}}
}

func newMemoryReadFixture(t *testing.T) (context.Context, *Server, int64, int64, *recordingIndexer, *stubIndexers) {
	t.Helper()
	pool, ctx := architectureTestPool(t)
	installID, repoID := seedArchitectureRepo(t, ctx, pool)
	idx := &recordingIndexer{}
	src := &stubIndexers{indexer: idx, available: true}
	s := &Server{store: store.NewWithDB(pool), logger: slog.New(slog.NewTextHandler(io.Discard, nil)), indexers: src}
	return ctx, s, installID, repoID, idx, src
}

func TestSearchMemoryBuildsScopedQueryAndMapsPatternIDs(t *testing.T) {
	ctx, s, installID, repoID, idx, _ := newMemoryReadFixture(t)
	// Two contributing pattern rows for m1 and none for m2.
	src := "manual"
	cid := "m1"
	a, err := s.store.CreatePattern(ctx, installID, &repoID, "guard writes", nil, nil, &src, nil, nil, &cid, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.store.CreatePattern(ctx, installID, &repoID, "guard writes legacy", &cid, nil, &src, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = s.store.Pool.Exec(bg, `DELETE FROM memory_mirror_outbox WHERE installation_id = $1`, installID)
		_, _ = s.store.Pool.Exec(bg, `DELETE FROM patterns WHERE installation_id = $1`, installID)
	})
	idx.matches = []memory.PatternMatch{
		{ID: "m1", Content: "guard writes", Score: 0.91, Metadata: map[string]string{"type": "pattern", "source": "manual", "created_at": "2026-09-01T00:00:00Z"}},
		{ID: "m2", Content: "pipeline learned", Score: 0.80, Metadata: map[string]string{"type": "pattern", "source": "auto_learn"}},
	}
	tools := &mcpTools{srv: s, scope: readScope(installID)}

	_, out, err := tools.searchMemory(ctx, nil, searchMemoryInput{RepoID: repoID, Query: "guard"})
	if err != nil {
		t.Fatal(err)
	}
	if idx.lastQuery.Scope != memory.ScopeRepo || idx.lastQuery.Repo == "" || idx.lastQuery.Limit != 10 || idx.lastQuery.Threshold != memory.NewThresholds().FindingEnrich || idx.lastQuery.PointLookup || len(idx.lastQuery.Filters) != 0 {
		t.Fatalf("query = %+v", idx.lastQuery)
	}
	if out.InstallationID != installID || len(out.Matches) != 2 || !out.EmbeddingsAvailable {
		t.Fatalf("out = %+v", out)
	}
	m1, m2 := out.Matches[0], out.Matches[1]
	if m1.CustomID != "m1" || len(m1.PatternIDs) != 2 || m1.PatternIDs[0] != a.ID || m1.PatternIDs[1] != b.ID || m1.Type != "pattern" || m1.Source != "manual" || m1.WrittenAt != "2026-09-01T00:00:00Z" || m1.ContainerTag != memory.RepoTagNew(idx.lastQuery.Repo) {
		t.Fatalf("m1 = %+v", m1)
	}
	if m2.CustomID != "m2" || m2.PatternIDs == nil || len(m2.PatternIDs) != 0 {
		t.Fatalf("m2 must carry an EMPTY (not null) pattern_ids: %+v", m2)
	}
}

// TestSearchMemoryScopeBothSurfacesPerMatchContainerTag: ScopeBoth sets no
// tool-level default container_tag (only ScopeRepo does), so each match's
// own Metadata["container_tag"] — set by the indexer's read projection, one
// per fan-out leg — is what the caller must fall back to; a match carrying
// none surfaces as "".
func TestSearchMemoryScopeBothSurfacesPerMatchContainerTag(t *testing.T) {
	ctx, s, installID, repoID, idx, _ := newMemoryReadFixture(t)
	idx.matches = []memory.PatternMatch{
		{ID: "m1", Content: "repo hit", Score: 0.9, Metadata: map[string]string{"container_tag": "custom-repo-tag"}},
		{ID: "m2", Content: "shared hit", Score: 0.8, Metadata: map[string]string{"container_tag": memory.SharedTag}},
		{ID: "m3", Content: "no tag", Score: 0.7, Metadata: map[string]string{}},
	}
	tools := &mcpTools{srv: s, scope: readScope(installID)}

	_, out, err := tools.searchMemory(ctx, nil, searchMemoryInput{RepoID: repoID, Scope: "both", Query: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if idx.lastQuery.Scope != memory.ScopeBoth {
		t.Fatalf("query scope = %v, want both", idx.lastQuery.Scope)
	}
	if len(out.Matches) != 3 {
		t.Fatalf("out = %+v", out)
	}
	if out.Matches[0].ContainerTag != "custom-repo-tag" {
		t.Fatalf("m1 container_tag = %q, want the row's own tag", out.Matches[0].ContainerTag)
	}
	if out.Matches[1].ContainerTag != memory.SharedTag {
		t.Fatalf("m2 container_tag = %q, want %q", out.Matches[1].ContainerTag, memory.SharedTag)
	}
	if out.Matches[2].ContainerTag != "" {
		t.Fatalf("m3 container_tag = %q, want empty when the match carries none", out.Matches[2].ContainerTag)
	}
}

func TestSearchMemoryScopeAndTenantGuards(t *testing.T) {
	ctx, s, installID, repoID, idx, src := newMemoryReadFixture(t)
	pool, _ := architectureTestPool(t)
	foreignInstall, foreignRepo := seedArchitectureRepo(t, ctx, pool)
	tools := &mcpTools{srv: s, scope: readScope(installID)}

	if _, _, err := tools.searchMemory(ctx, nil, searchMemoryInput{RepoID: foreignRepo, Query: "x"}); !errors.Is(err, errNotAccessible) {
		t.Fatalf("foreign repo: %v", err)
	}
	if _, _, err := tools.searchMemory(ctx, nil, searchMemoryInput{InstallationID: foreignInstall, Scope: "shared", Query: "x"}); !errors.Is(err, errNotAccessible) {
		t.Fatalf("foreign installation on shared scope: %v", err)
	}
	if _, _, err := tools.searchMemory(ctx, nil, searchMemoryInput{InstallationID: installID, Scope: "shared", Query: "x"}); err != nil || idx.lastQuery.Scope != memory.ScopeShared {
		t.Fatalf("shared scope: err=%v query=%+v", err, idx.lastQuery)
	}
	noRead := &mcpTools{srv: s, scope: tenantScope{installationIDs: []int64{installID}, grantedScopes: []string{scopeMemoryWrite}}}
	if _, _, err := noRead.searchMemory(ctx, nil, searchMemoryInput{RepoID: repoID, Query: "x"}); err == nil || err.Error() != errInsufficientScope(scopeRead).Error() {
		t.Fatalf("missing read scope: %v", err)
	}
	if _, _, err := tools.searchMemory(ctx, nil, searchMemoryInput{RepoID: repoID, Query: "x", Limit: 500}); err != nil || idx.lastQuery.Limit != 25 {
		t.Fatalf("limit clamp: err=%v limit=%d", err, idx.lastQuery.Limit)
	}

	src.available = false
	idx.matches = nil
	_, out, err := tools.searchMemory(ctx, nil, searchMemoryInput{RepoID: repoID, Query: "x"})
	if err != nil || out.EmbeddingsAvailable {
		t.Fatalf("embeddings off must be reported, not hidden as 'no matches': out=%+v err=%v", out, err)
	}
	s.indexers = nil
	if _, _, err := tools.searchMemory(ctx, nil, searchMemoryInput{RepoID: repoID, Query: "x"}); !errors.Is(err, errMemoryUnavailable) {
		t.Fatalf("unwired backend must be an explicit error, never an empty result: %v", err)
	}
}

func TestGetMemoryBriefing(t *testing.T) {
	ctx, s, installID, repoID, idx, _ := newMemoryReadFixture(t)
	idx.briefing = "## Institutional memory\n- guard writes"
	tools := &mcpTools{srv: s, scope: readScope(installID)}

	_, out, err := tools.getMemoryBriefing(ctx, nil, getMemoryBriefingInput{RepoID: repoID, Query: "writes", Profile: "specialist"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Empty || out.Markdown != idx.briefing || idx.lastBriefing.Options.Profile != memory.ProfileSpecialist || idx.lastBriefing.Options.CharCap != 2400 || idx.lastBriefing.Repo == "" || idx.lastBriefing.Owner == "" {
		t.Fatalf("out=%+v query=%+v", out, idx.lastBriefing)
	}
	idx.briefing = ""
	_, out, err = tools.getMemoryBriefing(ctx, nil, getMemoryBriefingInput{RepoID: repoID, Query: "writes"})
	if err != nil || !out.Empty || idx.lastBriefing.Options.Profile != memory.ProfileReview || idx.lastBriefing.Options.CharCap != 3200 {
		t.Fatalf("default profile: out=%+v query=%+v err=%v", out, idx.lastBriefing, err)
	}
}

// Every search and briefing query is embedded, and an embedding is a paid call
// sized by the string. Without a cap the only bound was the 1 MiB body limit,
// so one request could buy a 1 MiB embedding. The cap is the same rune cap the
// write tools apply to content and reason.
func TestSearchAndBriefingCapQueryLength(t *testing.T) {
	ctx, s, installID, repoID, idx, _ := newMemoryReadFixture(t)
	pool, _ := architectureTestPool(t)
	_, foreignRepo := seedArchitectureRepo(t, ctx, pool)
	tools := &mcpTools{srv: s, scope: readScope(installID)}

	over := strings.Repeat("界", mcpMaxTextRunes+1)
	atLimit := strings.Repeat("界", mcpMaxTextRunes)

	// repo_id is deliberately absent: the cap must be reported instead of the
	// missing-repo error, which pins the check ahead of the repo branch and so
	// ahead of every store and indexer call.
	if _, _, err := tools.searchMemory(ctx, nil, searchMemoryInput{Query: over}); err == nil || !strings.Contains(err.Error(), "query exceeds") {
		t.Fatalf("over-cap search query: err = %v, want a query-length validation error", err)
	}
	if idx.lastQuery.Query != "" {
		t.Fatalf("an over-cap query must never reach the indexer, got %+v", idx.lastQuery)
	}
	// A multi-byte query exactly at the rune cap is legal; a byte count would
	// have refused it at roughly a third of the advertised limit.
	if _, _, err := tools.searchMemory(ctx, nil, searchMemoryInput{RepoID: repoID, Query: atLimit}); err != nil {
		t.Fatalf("a multi-byte query at the rune cap must be accepted: %v", err)
	}
	if idx.lastQuery.Query != atLimit {
		t.Fatalf("query at the cap did not reach the indexer: %+v", idx.lastQuery)
	}

	// Same for the briefing, and a foreign repo id proves the cap runs before
	// the scoped repo lookup rather than after it.
	if _, _, err := tools.getMemoryBriefing(ctx, nil, getMemoryBriefingInput{RepoID: foreignRepo, Query: over}); err == nil || !strings.Contains(err.Error(), "query exceeds") {
		t.Fatalf("over-cap briefing query: err = %v, want a query-length validation error", err)
	}
	if idx.lastBriefing.Query != "" {
		t.Fatalf("an over-cap query must never reach the briefing seam, got %+v", idx.lastBriefing)
	}
	if _, _, err := tools.getMemoryBriefing(ctx, nil, getMemoryBriefingInput{RepoID: repoID, Query: atLimit}); err != nil {
		t.Fatalf("a multi-byte briefing query at the rune cap must be accepted: %v", err)
	}
	if idx.lastBriefing.Query != atLimit {
		t.Fatalf("briefing query at the cap did not reach the indexer: %+v", idx.lastBriefing)
	}
}

// An unknown type is worse than an error: it compiles into the reader's
// equality predicate, returns nothing, and reports embeddings_available true —
// the exact shape of a genuine no-hit. Refuse it like an unknown scope.
func TestSearchMemoryRejectsUnknownType(t *testing.T) {
	ctx, s, installID, repoID, idx, _ := newMemoryReadFixture(t)
	tools := &mcpTools{srv: s, scope: readScope(installID)}

	// "patterns" is the plural of a real type — the near miss a model makes.
	if _, _, err := tools.searchMemory(ctx, nil, searchMemoryInput{RepoID: repoID, Query: "q", Type: "patterns"}); err == nil ||
		!strings.Contains(err.Error(), "type must be one of:") || !strings.Contains(err.Error(), "pr_summary") {
		t.Fatalf("type=patterns: err = %v, want a validation error listing the allowed values", err)
	}
	if idx.lastQuery.Query != "" {
		t.Fatalf("an unknown type must be refused before the indexer, got %+v", idx.lastQuery)
	}

	// The empty string keeps meaning "any type".
	if _, _, err := tools.searchMemory(ctx, nil, searchMemoryInput{RepoID: repoID, Query: "q"}); err != nil {
		t.Fatalf("empty type must still mean any type: %v", err)
	}
	if idx.lastQuery.Type != "" {
		t.Fatalf("empty type reached the indexer as %q", idx.lastQuery.Type)
	}

	// Every constant the memory package defines is accepted and forwarded
	// verbatim. Driving off MemoryTypeNames means a new type cannot be added
	// without this test covering it.
	names := memory.MemoryTypeNames()
	if len(names) != 9 {
		t.Fatalf("expected 9 memory types, got %d: %v", len(names), names)
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			idx.lastQuery = memory.MemoryQuery{}
			if _, _, err := tools.searchMemory(ctx, nil, searchMemoryInput{RepoID: repoID, Query: "q", Type: name}); err != nil {
				t.Fatalf("type=%s must be accepted: %v", name, err)
			}
			if idx.lastQuery.Type != memory.MemoryType(name) {
				t.Fatalf("type=%s reached the indexer as %q", name, idx.lastQuery.Type)
			}
		})
	}
}
