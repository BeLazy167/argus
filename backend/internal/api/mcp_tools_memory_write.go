package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/BeLazy167/argus/backend/internal/memory"
	"github.com/BeLazy167/argus/backend/internal/store"
)

const (
	// mcpPatternSource is the dashboard's manual source, NOT a new "mcp" value:
	// source is hashed into custom_id, so a distinct string would make the same
	// sentence written from the dashboard and from MCP two live copies that
	// never dedup. Authorship travels in mirrorExtra instead.
	mcpPatternSource   = "manual"
	nearDuplicateLimit = 5
)

// mcpMaxTextRunes caps every free-text field an MCP tool accepts from the
// caller: memory content, the retirement reason logged verbatim, and the search
// and briefing queries that each buy a paid embedding. One constant, because
// the tool schemas all promise the model "max 4000 chars" and a per-field
// literal would drift.
//
// Runes, not bytes: len() would refuse CJK or emoji text at roughly a third of
// the advertised limit.
const mcpMaxTextRunes = 4000

// checkTextLimit rejects an over-long caller-supplied field with a fixed error
// that names the field and the cap, never echoing the value back.
func checkTextLimit(field, value string) error {
	if utf8.RuneCountInString(value) > mcpMaxTextRunes {
		return fmt.Errorf("%s exceeds %d characters", field, mcpMaxTextRunes)
	}
	return nil
}

// sanitizeMemoryContent trims and strips NUL. It runs BEFORE validation,
// identity derivation, duplicate checks and insertion: jsonb rejects \x00
// (SQLSTATE 22P05), and a NUL that survived to hashing would fork identity.
func sanitizeMemoryContent(s string) string {
	return strings.TrimSpace(strings.ReplaceAll(s, "\x00", ""))
}

type createMemoryInput struct {
	InstallationID   int64  `json:"installation_id" jsonschema:"local installation id from list_repos"`
	RepoID           *int64 `json:"repo_id,omitempty" jsonschema:"local repo id; required unless shared=true"`
	Content          string `json:"content" jsonschema:"the convention, pattern or fact to remember; max 4000 chars"`
	Category         string `json:"category,omitempty" jsonschema:"optional category such as testing, security, performance"`
	Shared           bool   `json:"shared,omitempty" jsonschema:"write org-wide instead of to one repo; requires confirm_shared=true and no repo_id"`
	ConfirmShared    bool   `json:"confirm_shared,omitempty" jsonschema:"acknowledge that an org-wide memory influences every review in the installation"`
	ConfirmDuplicate bool   `json:"confirm_duplicate,omitempty" jsonschema:"acknowledge the similar memories returned by a confirmation_required result (or that the similarity check is unavailable) and write anyway; never bypasses exact-duplicate reuse"`
}

type similarMemory struct {
	CustomID string  `json:"custom_id"`
	Content  string  `json:"content"`
	Score    float64 `json:"score"`
}

type createMemoryOutput struct {
	Status      string          `json:"status" jsonschema:"created: new pattern and mirror event committed; existing: identical memory already existed, nothing changed; confirmation_required: similar memories exist, nothing written — review similar and retry with confirm_duplicate=true"`
	PatternID   int64           `json:"pattern_id,omitempty"`
	CustomID    string          `json:"custom_id,omitempty" jsonschema:"deterministic memory identity; pass to retire_memory"`
	Scope       string          `json:"scope,omitempty" jsonschema:"repo or shared"`
	MirrorState string          `json:"mirror_state,omitempty" jsonschema:"pending: written to patterns; becomes searchable when the mirror drains. Never asserts searchability."`
	Similar     []similarMemory `json:"similar,omitempty"`
}

func (t *mcpTools) createMemory(ctx context.Context, _ *mcp.CallToolRequest, in createMemoryInput) (*mcp.CallToolResult, createMemoryOutput, error) {
	var zero createMemoryOutput
	if err := t.requireScope(scopeMemoryWrite); err != nil {
		return nil, zero, err
	}
	if in.InstallationID == 0 || !containsID(t.scope.installationIDs, in.InstallationID) {
		return nil, zero, errNotAccessible
	}
	content := sanitizeMemoryContent(in.Content)
	if content == "" {
		return nil, zero, errors.New("content is required")
	}
	if err := checkTextLimit("content", content); err != nil {
		return nil, zero, err
	}

	// Scope matrix — validated before identity derivation or any store call.
	// The store derives shared scope from RepoID == nil, so a missing repo_id
	// must never reach it through the repo-write path.
	var (
		repoIDPtr *int64
		customID  string
		scopeName string
		query     memory.MemoryQuery
	)
	switch {
	// A zero repo_id counts as absent here, matching the repo branch below.
	// Without that a client sending an explicit {"shared":true,"repo_id":0} —
	// which a generated client emits for an unset optional int — was told it
	// had combined the two, a refusal for a repo id that names no repo.
	case in.Shared && in.RepoID != nil && *in.RepoID != 0:
		return nil, zero, errors.New("shared=true cannot be combined with repo_id")
	case in.Shared && !in.ConfirmShared:
		return nil, zero, errors.New("org-wide memories reach every review in the installation with maximum confidence; pass confirm_shared=true to acknowledge")
	case in.Shared:
		customID = memory.SharedPatternCustomID(mcpPatternSource, content)
		scopeName = "shared"
		query = memory.MemoryQuery{Query: content, Scope: memory.ScopeShared, Type: memory.TypePattern, Limit: nearDuplicateLimit, Threshold: memory.NewThresholds().FindingEnrich}
	case in.RepoID == nil || *in.RepoID == 0:
		return nil, zero, errors.New("repo_id is required (or set shared=true with confirm_shared=true for an org-wide memory)")
	default:
		repo, short, err := t.scopedRepo(ctx, *in.RepoID)
		if err != nil {
			if errors.Is(err, errNotAccessible) {
				return nil, zero, err
			}
			return nil, zero, t.internalErr(ctx, "create_memory", err)
		}
		if repo.InstallationID != in.InstallationID {
			return nil, zero, errNotAccessible
		}
		repoIDPtr = in.RepoID
		customID = memory.PatternCustomID("", short, mcpPatternSource, content)
		scopeName = "repo"
		query = memory.MemoryQuery{Query: content, Repo: short, Scope: memory.ScopeRepo, Type: memory.TypePattern, Limit: nearDuplicateLimit, Threshold: memory.NewThresholds().FindingEnrich}
	}

	// Exact identity first: same installation + same deterministic custom_id
	// is the same memory. Return it; confirm_duplicate never overrides this.
	if existingID, err := t.srv.store.FindPatternIDByIdentity(ctx, in.InstallationID, customID); err == nil {
		return nil, createMemoryOutput{Status: "existing", PatternID: existingID, CustomID: customID, Scope: scopeName}, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, zero, t.internalErr(ctx, "create_memory", err)
	}

	// Advisory near-duplicate check, before any lock. It sees only mirrored
	// memories, so it cannot promise the absence of concurrent near-duplicates.
	// If it cannot run, the caller must acknowledge writing blind.
	idx := t.srv.resolveIndexer(ctx, in.InstallationID)
	var similar []similarMemory
	switch {
	case idx == nil || !t.srv.indexers.EmbedderAvailable(ctx, in.InstallationID):
		if !in.ConfirmDuplicate {
			return nil, zero, errors.New("similarity check unavailable for this installation; pass confirm_duplicate=true to write without it")
		}
	default:
		matches, err := idx.Search(ctx, query)
		if err != nil {
			t.srv.logger.WarnContext(ctx, "create_memory similarity search failed", "error", err)
			if !in.ConfirmDuplicate {
				return nil, zero, errors.New("similarity check failed; pass confirm_duplicate=true to write without it")
			}
		}
		for _, m := range matches {
			if m.ID == customID {
				continue
			}
			similar = append(similar, similarMemory{CustomID: m.ID, Content: m.Content, Score: m.Score})
		}
		if len(similar) > 0 && !in.ConfirmDuplicate {
			return nil, createMemoryOutput{Status: "confirmation_required", Similar: similar}, nil
		}
	}

	createdBy, source := t.scope.userID, mcpPatternSource
	// Category takes the same scrub as Content, not a bare trim: it reaches the
	// same jsonb insert, and a \x00 there aborts the transaction (SQLSTATE
	// 22P05) — the caller would see the fixed "create_memory failed" string
	// instead of a written memory.
	var category *string
	if c := sanitizeMemoryContent(in.Category); c != "" {
		category = &c
	}
	// mirrorExtra is the ONLY channel that carries authorship into the memory
	// row; both existing human write paths pass nil and lose it.
	mirrorExtra := map[string]string{"created_by": createdBy, "origin": "mcp"}

	// N10: exact recheck + insert serialized under the per-identity lock.
	pattern, created, err := t.srv.store.CreateOrGetPattern(ctx, in.InstallationID, repoIDPtr, content, &createdBy, &source, category, customID, mirrorExtra)
	if err != nil {
		return nil, zero, t.internalErr(ctx, "create_memory", err)
	}
	out := createMemoryOutput{Status: "existing", PatternID: pattern.ID, CustomID: customID, Scope: scopeName, Similar: similar}
	if created {
		out.Status, out.MirrorState = "created", "pending"
		t.srv.logger.InfoContext(ctx, "mcp memory created", "user", createdBy, "org", t.scope.orgID, "installation_id", in.InstallationID, "pattern_id", pattern.ID, "scope", scopeName)
	}
	return nil, out, nil
}

type deleteMemoryInput struct {
	PatternID              int64 `json:"pattern_id" jsonschema:"a contributing pattern id from search_memory.pattern_ids or create_memory"`
	ConfirmPipelineLearned bool  `json:"confirm_pipeline_learned,omitempty" jsonschema:"acknowledge deleting a pattern the review pipeline learned (any source other than manual or remember_command); irreversible"`
}

type deleteMemoryOutput struct {
	PatternDeleted    bool   `json:"pattern_deleted"`
	CustomID          string `json:"custom_id" jsonschema:"the memory identity this pattern contributed to"`
	Source            string `json:"source" jsonschema:"the deleted pattern's source"`
	SiblingsAtDelete  int    `json:"siblings_at_delete" jsonschema:"other pattern rows still carrying the same identity at the moment of deletion"`
	RetentionExpected bool   `json:"retention_expected" jsonschema:"true when siblings remain, so the memory is expected to stay searchable"`
	MirrorState       string `json:"mirror_state" jsonschema:"pending: the memory row is removed later by the mirror worker, and only if no sibling still owns it. Use retire_memory to stop retrieval now."`
}

// deleteMemory removes ONE contributing pattern row. Tenant, source guard,
// delete and sibling snapshot all happen inside DeletePatternGuarded's
// transaction; a foreign or missing id gets the same fixed answer.
func (t *mcpTools) deleteMemory(ctx context.Context, _ *mcp.CallToolRequest, in deleteMemoryInput) (*mcp.CallToolResult, deleteMemoryOutput, error) {
	var zero deleteMemoryOutput
	if err := t.requireScope(scopeMemoryWrite); err != nil {
		return nil, zero, err
	}
	if in.PatternID == 0 {
		return nil, zero, errors.New("pattern_id is required")
	}
	res, err := t.srv.store.DeletePatternGuarded(ctx, in.PatternID, t.scope.installationIDs, in.ConfirmPipelineLearned)
	switch {
	case errors.Is(err, store.ErrPatternNotFound):
		return nil, zero, errNotAccessible
	case errors.Is(err, store.ErrPipelineLearnedPattern):
		return nil, zero, fmt.Errorf("pattern %d was learned by the review pipeline; pass confirm_pipeline_learned=true to acknowledge deleting it — this cannot be undone", in.PatternID)
	case err != nil:
		return nil, zero, t.internalErr(ctx, "delete_memory", err)
	}
	t.srv.logger.InfoContext(ctx, "mcp memory pattern deleted", "user", t.scope.userID, "org", t.scope.orgID, "pattern_id", in.PatternID, "custom_id", res.CustomID, "source", res.Source, "siblings_at_delete", res.SiblingsAtDelete)
	return nil, deleteMemoryOutput{
		PatternDeleted: true, CustomID: res.CustomID, Source: res.Source,
		SiblingsAtDelete: res.SiblingsAtDelete, RetentionExpected: res.SiblingsAtDelete > 0, MirrorState: "pending",
	}, nil
}

type retireMemoryInput struct {
	InstallationID         int64   `json:"installation_id" jsonschema:"local installation id from list_repos or search_memory"`
	CustomID               string  `json:"custom_id" jsonschema:"memory identity from search_memory or create_memory"`
	ReplacedByCustomID     *string `json:"replaced_by_custom_id,omitempty" jsonschema:"if set, the memory is superseded by this live memory (invalidate + forward pointer) instead of plainly invalidated"`
	Reason                 string  `json:"reason" jsonschema:"why this knowledge is being retired; recorded in the audit log; max 4000 chars"`
	ConfirmPipelineLearned bool    `json:"confirm_pipeline_learned,omitempty" jsonschema:"acknowledge retiring a memory the review pipeline learned; irreversible"`
}

type retireMemoryOutput struct {
	Mode       string  `json:"mode" jsonschema:"invalidated or superseded"`
	CustomID   string  `json:"custom_id"`
	Source     string  `json:"source" jsonschema:"the retired memory's recorded provenance"`
	ReplacedBy *string `json:"replaced_by,omitempty"`
}

// retireMemory makes a memory unretrievable. There is no un-invalidate, so a
// reason is required and logged. One tool covers both verbs: supersede is
// invalidate plus a forward pointer, and splitting them invites a caller to
// invalidate first and then fail to link. All checks and the transition run
// in RetireDocument's transaction.
func (t *mcpTools) retireMemory(ctx context.Context, _ *mcp.CallToolRequest, in retireMemoryInput) (*mcp.CallToolResult, retireMemoryOutput, error) {
	var zero retireMemoryOutput
	if err := t.requireScope(scopeMemoryWrite); err != nil {
		return nil, zero, err
	}
	if in.InstallationID == 0 || !containsID(t.scope.installationIDs, in.InstallationID) {
		return nil, zero, errNotAccessible
	}
	// Identity is trimmed ONCE and the trimmed value is what gets validated,
	// sent to the seam, logged and echoed. Validating a trimmed value and then
	// acting on the raw one is how " " passed as a replacement would silently
	// downgrade a supersede to a plain invalidation while the result still
	// reported replaced_by.
	customID := strings.TrimSpace(in.CustomID)
	if customID == "" {
		return nil, zero, errors.New("custom_id is required")
	}
	// The reason is caller-supplied and goes verbatim into the audit log, so
	// it gets the same cap as memory content. Unbounded, one call could write
	// an arbitrarily large line into the log stream. Runes, not bytes, for the
	// same reason createMemory counts runes.
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		return nil, zero, errors.New("reason is required: retirement cannot be undone")
	}
	if err := checkTextLimit("reason", reason); err != nil {
		return nil, zero, err
	}
	var replacedBy *string
	req := memory.RetireRequest{CustomID: customID, AllowPipelineLearned: in.ConfirmPipelineLearned}
	if in.ReplacedByCustomID != nil {
		req.ReplacementCustomID = strings.TrimSpace(*in.ReplacedByCustomID)
		if req.ReplacementCustomID == "" {
			return nil, zero, errors.New("replaced_by_custom_id must not be blank")
		}
		replacedBy = &req.ReplacementCustomID
	}
	idx := t.srv.resolveIndexer(ctx, in.InstallationID)
	if idx == nil {
		return nil, zero, errMemoryUnavailable
	}
	res, err := idx.RetireDocument(ctx, req)
	switch {
	case errors.Is(err, memory.ErrDocumentNotFound):
		return nil, zero, errNotAccessible
	case errors.Is(err, memory.ErrPipelineLearnedMemory):
		return nil, zero, fmt.Errorf("memory %s was learned by the review pipeline; pass confirm_pipeline_learned=true to acknowledge retiring it — this cannot be undone", customID)
	case errors.Is(err, memory.ErrUnknownProvenance):
		// No flag rescues this one: review, rule and scenario documents are
		// built with no source at all, so the caller has nothing to resolve.
		// Say so rather than asking for something no code path can supply.
		return nil, zero, fmt.Errorf("memory %s has no recorded provenance (review, rule and scenario memories, and patterns written without a source) and cannot be retired through MCP", customID)
	case errors.Is(err, memory.ErrReplacementNotLive):
		return nil, zero, errors.New("replacement must be a distinct live memory in the same installation")
	case err != nil:
		return nil, zero, t.internalErr(ctx, "retire_memory", err)
	}
	t.srv.logger.InfoContext(ctx, "mcp memory retired", "user", t.scope.userID, "org", t.scope.orgID, "installation_id", in.InstallationID,
		"custom_id", customID, "mode", res.Mode, "source", res.Source, "replaced_by", req.ReplacementCustomID, "reason", reason)
	return nil, retireMemoryOutput{Mode: res.Mode, CustomID: customID, Source: res.Source, ReplacedBy: replacedBy}, nil
}
