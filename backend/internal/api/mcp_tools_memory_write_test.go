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

func newMemoryWriteFixture(t *testing.T) (context.Context, *Server, int64, int64, *recordingIndexer, *stubIndexers) {
	t.Helper()
	pool, ctx := architectureTestPool(t)
	installID, repoID := seedArchitectureRepo(t, ctx, pool)
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM memory_mirror_outbox WHERE installation_id = $1`, installID)
		_, _ = pool.Exec(bg, `DELETE FROM patterns WHERE installation_id = $1`, installID)
	})
	idx := &recordingIndexer{}
	src := &stubIndexers{indexer: idx, available: true}
	s := &Server{store: store.NewWithDB(pool), logger: slog.New(slog.NewTextHandler(io.Discard, nil)), indexers: src}
	return ctx, s, installID, repoID, idx, src
}

func repoShortName(t *testing.T, ctx context.Context, s *Server, installID, repoID int64) string {
	t.Helper()
	repo, err := s.store.GetRepoScoped(ctx, repoID, []int64{installID})
	if err != nil {
		t.Fatal(err)
	}
	_, short, _ := strings.Cut(repo.FullName, "/")
	return short
}

func TestCreateMemoryCreatedThenExisting(t *testing.T) {
	ctx, s, installID, repoID, _, _ := newMemoryWriteFixture(t)
	tools := &mcpTools{srv: s, scope: writeScope(installID)}
	in := createMemoryInput{InstallationID: installID, RepoID: &repoID, Content: "Always guard shared writes with a lock"}

	_, out, err := tools.createMemory(ctx, nil, in)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != "created" || out.PatternID == 0 || out.Scope != "repo" || out.MirrorState != "pending" {
		t.Fatalf("out = %+v", out)
	}
	if want := memory.PatternCustomID("", repoShortName(t, ctx, s, installID, repoID), "manual", in.Content); out.CustomID != want {
		t.Fatalf("custom_id = %q, want the dashboard-compatible %q", out.CustomID, want)
	}
	p, err := s.store.GetPattern(ctx, out.PatternID)
	if err != nil || p.Source != "manual" || p.CreatedBy == nil || *p.CreatedBy != "user_w" || p.RepoID == nil || *p.RepoID != repoID {
		t.Fatalf("row = %+v err=%v", p, err)
	}

	// Exact retry, even with confirm_duplicate, is status existing: same id,
	// nothing written.
	in.ConfirmDuplicate = true
	_, again, err := tools.createMemory(ctx, nil, in)
	if err != nil || again.Status != "existing" || again.PatternID != out.PatternID || again.MirrorState != "" {
		t.Fatalf("retry = %+v err=%v", again, err)
	}
	var events int
	if err := s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM memory_mirror_outbox WHERE installation_id=$1 AND aggregate_type='pattern'`, installID).Scan(&events); err != nil || events != 1 {
		t.Fatalf("outbox events = %d err=%v, want exactly 1", events, err)
	}
}

func TestCreateMemoryNearDuplicateRequiresAcknowledgment(t *testing.T) {
	ctx, s, installID, repoID, idx, _ := newMemoryWriteFixture(t)
	idx.matches = []memory.PatternMatch{{ID: "existing", Content: "Guard shared writes with a lock", Score: 0.95}}
	tools := &mcpTools{srv: s, scope: writeScope(installID)}
	in := createMemoryInput{InstallationID: installID, RepoID: &repoID, Content: "Always guard shared writes with a lock"}

	_, out, err := tools.createMemory(ctx, nil, in)
	if err != nil || out.Status != "confirmation_required" || len(out.Similar) != 1 || out.Similar[0].CustomID != "existing" || out.PatternID != 0 {
		t.Fatalf("near-duplicate: out=%+v err=%v", out, err)
	}
	if idx.lastQuery.Type != memory.TypePattern || idx.lastQuery.Limit != 5 || idx.lastQuery.Threshold != memory.NewThresholds().FindingEnrich {
		t.Fatalf("similarity query = %+v", idx.lastQuery)
	}
	var n int
	if err := s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM patterns WHERE installation_id=$1`, installID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("confirmation_required must write nothing; patterns=%d", n)
	}
	in.ConfirmDuplicate = true
	_, out, err = tools.createMemory(ctx, nil, in)
	if err != nil || out.Status != "created" {
		t.Fatalf("acknowledged retry: out=%+v err=%v", out, err)
	}
}

func TestCreateMemorySimilarityUnavailableNeedsAcknowledgment(t *testing.T) {
	ctx, s, installID, repoID, idx, src := newMemoryWriteFixture(t)
	tools := &mcpTools{srv: s, scope: writeScope(installID)}
	in := createMemoryInput{InstallationID: installID, RepoID: &repoID, Content: "no embedder here"}

	src.available = false
	if _, _, err := tools.createMemory(ctx, nil, in); err == nil || !strings.Contains(err.Error(), "confirm_duplicate") {
		t.Fatalf("embedder off without acknowledgment: %v", err)
	}
	src.available = true
	idx.err = errors.New("search exploded")
	if _, _, err := tools.createMemory(ctx, nil, in); err == nil || !strings.Contains(err.Error(), "confirm_duplicate") {
		t.Fatalf("failed search without acknowledgment: %v", err)
	}
	in.ConfirmDuplicate = true
	if _, out, err := tools.createMemory(ctx, nil, in); err != nil || out.Status != "created" {
		t.Fatalf("acknowledged bypass: out=%+v err=%v", out, err)
	}
}

func TestCreateMemoryScopeMatrix(t *testing.T) {
	ctx, s, installID, repoID, _, _ := newMemoryWriteFixture(t)
	pool, _ := architectureTestPool(t)
	_, foreignRepo := seedArchitectureRepo(t, ctx, pool)
	tools := &mcpTools{srv: s, scope: writeScope(installID)}
	reject := []createMemoryInput{
		{InstallationID: installID, Content: "no repo, not shared"},
		{InstallationID: installID, Content: "no repo, not shared, flag set", ConfirmShared: true},
		{InstallationID: installID, RepoID: &repoID, Shared: true, ConfirmShared: true, Content: "shared with repo"},
		{InstallationID: installID, Shared: true, Content: "shared without confirm"},
		{InstallationID: installID, RepoID: &foreignRepo, Content: "repo from another installation"},
		{InstallationID: installID + 99999, RepoID: &repoID, Content: "foreign installation"},
		{InstallationID: installID, RepoID: &repoID, Content: "   \x00 "},
		{InstallationID: installID, RepoID: &repoID, Content: strings.Repeat("x", 4001)},
	}
	for i, in := range reject {
		if _, out, err := tools.createMemory(ctx, nil, in); err == nil {
			t.Fatalf("case %d accepted: %+v", i, out)
		}
	}
	var n int
	if err := s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM patterns WHERE installation_id=$1`, installID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rejected inputs must reach neither patterns nor outbox; patterns=%d", n)
	}
	_, out, err := tools.createMemory(ctx, nil, createMemoryInput{InstallationID: installID, Shared: true, ConfirmShared: true, Content: "org rule"})
	if err != nil || out.Status != "created" || out.Scope != "shared" || out.CustomID != memory.SharedPatternCustomID("manual", "org rule") {
		t.Fatalf("shared write: out=%+v err=%v", out, err)
	}
	// An explicit repo_id of 0 is what a generated client emits for an unset
	// optional int. It names no repo, and the repo branch already treats it as
	// absent, so the shared branch must too rather than reporting a conflict
	// the caller cannot see.
	zeroRepo := int64(0)
	_, out, err = tools.createMemory(ctx, nil, createMemoryInput{InstallationID: installID, RepoID: &zeroRepo, Shared: true, ConfirmShared: true, Content: "org rule with explicit zero repo"})
	if err != nil || out.Scope != "shared" {
		t.Fatalf("shared with explicit repo_id=0: out=%+v err=%v", out, err)
	}
	noWrite := &mcpTools{srv: s, scope: readScope(installID)}
	if _, _, err := noWrite.createMemory(ctx, nil, createMemoryInput{InstallationID: installID, RepoID: &repoID, Content: "x", ConfirmShared: true, ConfirmDuplicate: true}); err == nil || err.Error() != errInsufficientScope(scopeMemoryWrite).Error() {
		t.Fatalf("read-only token with every flag set must be denied by scope: %v", err)
	}
}

func TestCreateMemorySanitizesBeforeIdentity(t *testing.T) {
	ctx, s, installID, repoID, _, _ := newMemoryWriteFixture(t)
	tools := &mcpTools{srv: s, scope: writeScope(installID)}
	_, a, err := tools.createMemory(ctx, nil, createMemoryInput{InstallationID: installID, RepoID: &repoID, Content: "  nul\x00inside  "})
	if err != nil {
		t.Fatal(err)
	}
	_, b, err := tools.createMemory(ctx, nil, createMemoryInput{InstallationID: installID, RepoID: &repoID, Content: "nulinside"})
	if err != nil || b.Status != "existing" || b.PatternID != a.PatternID {
		t.Fatalf("sanitized content must dedup against its clean form: a=%+v b=%+v err=%v", a, b, err)
	}
	p, _ := s.store.GetPattern(ctx, a.PatternID)
	if strings.ContainsRune(p.Content, 0) || p.Content != "nulinside" {
		t.Fatalf("stored content = %q", p.Content)
	}
}

func seedPatternWithSource(t *testing.T, ctx context.Context, s *Server, installID int64, repoID *int64, content, source string) (int64, string) {
	t.Helper()
	customID := memory.SharedPatternCustomID(source, content)
	if repoID != nil {
		customID = memory.PatternCustomID("", repoShortName(t, ctx, s, installID, *repoID), source, content)
	}
	src := source
	p, err := s.store.CreatePattern(ctx, installID, repoID, content, nil, nil, &src, nil, nil, &customID, nil)
	if err != nil {
		t.Fatalf("seed pattern: %v", err)
	}
	return p.ID, customID
}

func TestDeleteMemory(t *testing.T) {
	ctx, s, installID, repoID, _, _ := newMemoryWriteFixture(t)
	tools := &mcpTools{srv: s, scope: writeScope(installID)}

	id, cid := seedPatternWithSource(t, ctx, s, installID, &repoID, "delete me", "manual")
	_, out, err := tools.deleteMemory(ctx, nil, deleteMemoryInput{PatternID: id})
	if err != nil {
		t.Fatal(err)
	}
	if !out.PatternDeleted || out.CustomID != cid || out.Source != "manual" || out.SiblingsAtDelete != 0 || out.RetentionExpected || out.MirrorState != "pending" {
		t.Fatalf("out = %+v", out)
	}

	learned, _ := seedPatternWithSource(t, ctx, s, installID, &repoID, "learned", "auto_learn")
	if _, _, err := tools.deleteMemory(ctx, nil, deleteMemoryInput{PatternID: learned}); err == nil || !strings.Contains(err.Error(), "confirm_pipeline_learned") {
		t.Fatalf("pipeline-learned without acknowledgment: %v", err)
	}
	if _, out, err := tools.deleteMemory(ctx, nil, deleteMemoryInput{PatternID: learned, ConfirmPipelineLearned: true}); err != nil || out.Source != "auto_learn" {
		t.Fatalf("acknowledged: out=%+v err=%v", out, err)
	}

	// Sibling survives → retention expected.
	doc := "sm_api_pair"
	src := "manual"
	a, err := s.store.CreatePattern(ctx, installID, &repoID, "pair", &doc, nil, &src, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.CreatePattern(ctx, installID, &repoID, "pair", &doc, nil, &src, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, out, err := tools.deleteMemory(ctx, nil, deleteMemoryInput{PatternID: a.ID}); err != nil || out.SiblingsAtDelete != 1 || !out.RetentionExpected {
		t.Fatalf("sibling: out=%+v err=%v", out, err)
	}

	pool, _ := architectureTestPool(t)
	foreignInstall, foreignRepo := seedArchitectureRepo(t, ctx, pool)
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM memory_mirror_outbox WHERE installation_id = $1`, foreignInstall)
		_, _ = pool.Exec(bg, `DELETE FROM patterns WHERE installation_id = $1`, foreignInstall)
	})
	foreign, _ := seedPatternWithSource(t, ctx, s, foreignInstall, &foreignRepo, "not yours", "manual")
	if _, _, err := tools.deleteMemory(ctx, nil, deleteMemoryInput{PatternID: foreign, ConfirmPipelineLearned: true}); !errors.Is(err, errNotAccessible) {
		t.Fatalf("foreign: %v", err)
	}
	if _, err := s.store.GetPattern(ctx, foreign); err != nil {
		t.Fatalf("foreign row must be untouched: %v", err)
	}
	if _, _, err := tools.deleteMemory(ctx, nil, deleteMemoryInput{PatternID: 1 << 40}); !errors.Is(err, errNotAccessible) {
		t.Fatalf("missing must match foreign: %v", err)
	}
	if _, _, err := tools.deleteMemory(ctx, nil, deleteMemoryInput{}); err == nil || !strings.Contains(err.Error(), "pattern_id is required") {
		t.Fatalf("absent pattern_id: %v", err)
	}
	noWrite := &mcpTools{srv: s, scope: readScope(installID)}
	if _, _, err := noWrite.deleteMemory(ctx, nil, deleteMemoryInput{PatternID: id, ConfirmPipelineLearned: true}); err == nil || err.Error() != errInsufficientScope(scopeMemoryWrite).Error() {
		t.Fatalf("read-only token: %v", err)
	}
}

func TestRetireMemory(t *testing.T) {
	ctx, s, installID, _, idx, _ := newMemoryWriteFixture(t)
	tools := &mcpTools{srv: s, scope: writeScope(installID)}
	var got memory.RetireRequest
	idx.retireFn = func(req memory.RetireRequest) (memory.RetireResult, error) {
		got = req
		switch req.CustomID {
		case "human":
			return memory.RetireResult{Mode: memory.RetireModeInvalidated, Source: "manual"}, nil
		case "learned":
			if !req.AllowPipelineLearned {
				return memory.RetireResult{}, memory.ErrPipelineLearnedMemory
			}
			return memory.RetireResult{Mode: memory.RetireModeSuperseded, Source: "auto_learn"}, nil
		case "orphan":
			return memory.RetireResult{}, memory.ErrUnknownProvenance
		case "badrepl":
			return memory.RetireResult{}, memory.ErrReplacementNotLive
		}
		return memory.RetireResult{}, memory.ErrDocumentNotFound
	}

	if _, _, err := tools.retireMemory(ctx, nil, retireMemoryInput{InstallationID: installID, CustomID: "human"}); err == nil {
		t.Fatal("reason is required")
	}
	_, out, err := tools.retireMemory(ctx, nil, retireMemoryInput{InstallationID: installID, CustomID: "  human  ", Reason: "team decision"})
	if err != nil || out.Mode != "invalidated" || out.Source != "manual" || out.CustomID != "human" || got.CustomID != "human" {
		t.Fatalf("human: out=%+v err=%v", out, err)
	}
	if _, _, err := tools.retireMemory(ctx, nil, retireMemoryInput{InstallationID: installID, CustomID: "learned", Reason: "r"}); err == nil || !strings.Contains(err.Error(), "confirm_pipeline_learned") {
		t.Fatalf("learned without acknowledgment: %v", err)
	}
	repl := " new "
	_, out, err = tools.retireMemory(ctx, nil, retireMemoryInput{InstallationID: installID, CustomID: "learned", ReplacedByCustomID: &repl, Reason: "r", ConfirmPipelineLearned: true})
	if err != nil || out.Mode != "superseded" || out.ReplacedBy == nil || *out.ReplacedBy != "new" || got.ReplacementCustomID != "new" || !got.AllowPipelineLearned {
		t.Fatalf("superseded: out=%+v req=%+v err=%v", out, got, err)
	}
	// The refusal is permanent for these memories, so the message must not ask
	// the caller to resolve a source no writer ever stamps.
	if _, _, err := tools.retireMemory(ctx, nil, retireMemoryInput{InstallationID: installID, CustomID: "orphan", Reason: "r", ConfirmPipelineLearned: true}); err == nil ||
		!strings.Contains(err.Error(), "has no recorded provenance") || !strings.Contains(err.Error(), "cannot be retired through MCP") {
		t.Fatalf("unknown provenance: %v", err)
	}
	if _, _, err := tools.retireMemory(ctx, nil, retireMemoryInput{InstallationID: installID, CustomID: "badrepl", ReplacedByCustomID: &repl, Reason: "r"}); err == nil || !strings.Contains(err.Error(), "replacement") {
		t.Fatalf("bad replacement: %v", err)
	}
	// A whitespace-only replacement must not reach the seam: there it would
	// look like "no replacement" and silently downgrade supersede to plain
	// invalidation while the output still reported replaced_by.
	blank := "   "
	if _, _, err := tools.retireMemory(ctx, nil, retireMemoryInput{InstallationID: installID, CustomID: "human", ReplacedByCustomID: &blank, Reason: "r"}); err == nil || !strings.Contains(err.Error(), "replaced_by_custom_id must not be blank") {
		t.Fatalf("blank replacement: %v", err)
	}
	if got.CustomID != "badrepl" {
		t.Fatalf("blank replacement must be rejected before RetireDocument, but it ran with %+v", got)
	}
	// The reason is logged verbatim, so it carries the same cap as memory
	// content, and it is enforced before the seam runs. Counted in runes: a
	// multi-byte reason at the limit must be accepted, which a len() check
	// would refuse at roughly a third of it.
	got = memory.RetireRequest{}
	overLimit := strings.Repeat("a", mcpMaxTextRunes+1)
	if _, _, err := tools.retireMemory(ctx, nil, retireMemoryInput{InstallationID: installID, CustomID: "human", Reason: overLimit}); err == nil || !strings.Contains(err.Error(), "reason exceeds") {
		t.Fatalf("over-limit reason: err = %v, want a validation error", err)
	}
	if got.CustomID != "" {
		t.Fatalf("an over-limit reason must be rejected before RetireDocument, but it ran with %+v", got)
	}
	atLimit := strings.Repeat("é", mcpMaxTextRunes)
	if _, _, err := tools.retireMemory(ctx, nil, retireMemoryInput{InstallationID: installID, CustomID: "human", Reason: atLimit}); err != nil {
		t.Fatalf("a multi-byte reason at the rune limit must be accepted: %v", err)
	}

	if _, _, err := tools.retireMemory(ctx, nil, retireMemoryInput{InstallationID: installID, CustomID: "missing", Reason: "r"}); !errors.Is(err, errNotAccessible) {
		t.Fatalf("missing: %v", err)
	}
	if _, _, err := tools.retireMemory(ctx, nil, retireMemoryInput{InstallationID: installID + 99999, CustomID: "human", Reason: "r"}); !errors.Is(err, errNotAccessible) {
		t.Fatalf("foreign installation: %v", err)
	}
	s.indexers = nil
	if _, _, err := tools.retireMemory(ctx, nil, retireMemoryInput{InstallationID: installID, CustomID: "human", Reason: "r"}); !errors.Is(err, errMemoryUnavailable) {
		t.Fatalf("unwired: %v", err)
	}
	noWrite := &mcpTools{srv: s, scope: readScope(installID)}
	if _, _, err := noWrite.retireMemory(ctx, nil, retireMemoryInput{InstallationID: installID, CustomID: "human", Reason: "r", ConfirmPipelineLearned: true}); err == nil || err.Error() != errInsufficientScope(scopeMemoryWrite).Error() {
		t.Fatalf("read-only token: %v", err)
	}
}

// The cap the jsonschema advertises is characters, so the code must count
// runes. Counting bytes refused CJK and emoji at roughly a third of the stated
// limit; the model was told 4000 and got 1333.
func TestCreateMemoryCapsRunesNotBytes(t *testing.T) {
	ctx, s, installID, repoID, _, _ := newMemoryWriteFixture(t)
	tools := &mcpTools{srv: s, scope: writeScope(installID)}

	// 2000 three-byte runes: 6000 bytes, well past a byte cap, well inside the
	// character cap the schema promises.
	long := strings.Repeat("界", 2000)
	if len(long) <= mcpMaxTextRunes {
		t.Fatalf("fixture is not multi-byte enough: %d bytes", len(long))
	}
	_, out, err := tools.createMemory(ctx, nil, createMemoryInput{InstallationID: installID, RepoID: &repoID, Content: long})
	if err != nil || out.Status != "created" {
		t.Fatalf("2000 CJK characters must be accepted: out=%+v err=%v", out, err)
	}
	// One rune past the cap is still refused.
	over := strings.Repeat("界", mcpMaxTextRunes+1)
	if _, _, err := tools.createMemory(ctx, nil, createMemoryInput{InstallationID: installID, RepoID: &repoID, Content: over}); err == nil ||
		!strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("4001 characters must be refused: %v", err)
	}
}

// Category reaches the same jsonb insert as Content. A NUL there aborted the
// transaction and the caller saw only the fixed "create_memory failed" string.
func TestCreateMemoryScrubsCategory(t *testing.T) {
	ctx, s, installID, repoID, _, _ := newMemoryWriteFixture(t)
	tools := &mcpTools{srv: s, scope: writeScope(installID)}

	_, out, err := tools.createMemory(ctx, nil, createMemoryInput{
		InstallationID: installID, RepoID: &repoID,
		Content: "prefer table-driven tests", Category: " sec\x00urity ",
	})
	if err != nil || out.Status != "created" {
		t.Fatalf("NUL in category must not fail the write: out=%+v err=%v", out, err)
	}
	p, err := s.store.GetPattern(ctx, out.PatternID)
	if err != nil {
		t.Fatal(err)
	}
	if p.Category == nil || *p.Category != "security" {
		t.Fatalf("category = %v, want the scrubbed and trimmed \"security\"", p.Category)
	}
	// A category that is only NUL and space is no category at all, not "".
	_, blank, err := tools.createMemory(ctx, nil, createMemoryInput{
		InstallationID: installID, RepoID: &repoID,
		Content: "prefer explicit timeouts", Category: " \x00 ",
	})
	if err != nil || blank.Status != "created" {
		t.Fatalf("blank category: out=%+v err=%v", blank, err)
	}
	if bp, err := s.store.GetPattern(ctx, blank.PatternID); err != nil || bp.Category != nil {
		t.Fatalf("category = %v err=%v, want nil", bp.Category, err)
	}
}
