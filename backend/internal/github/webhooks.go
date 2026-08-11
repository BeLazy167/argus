package github

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/BeLazy167/argus/backend/internal/util"
	gh "github.com/google/go-github/v68/github"
)

// WebhookEvent represents a parsed GitHub webhook event.
type WebhookEvent struct {
	Type    string
	Action  string
	Payload any
}

// PREvent holds the parsed data from a pull_request webhook event.
type PREvent struct {
	Action         string
	InstallationID int64
	RepoFullName   string
	RepoID         int64
	PRNumber       int
	PRTitle        string
	PRAuthor       string
	HeadSHA        string
	BaseSHA        string
	BaseRef        string
	HeadRef        string
	BaseRepoID     int64
	MergeCommitSHA string
	MergedAt       time.Time
	PRBody         string // first ~8000 chars of PR description (feeds intent extraction)
	// PRBodyBefore is populated only on action="edited" from payload.changes.body.from.
	// Used by the cross-PR webhook handler to diff linked-PR refs between pre-
	// and post-edit bodies and trigger a refresh when the set changes.
	PRBodyBefore    string
	Merged          bool
	Draft           bool     // pr.draft — feeds ReviewContract depth gating
	Labels          []string // label names (truncated, capped) — feeds ReviewContract signals
	PersonaOverride string   `json:"-"` // set by @argus-eye review --persona X
}

// Caps on label data captured from the webhook payload. Labels are
// user-controlled strings that end up in LLM prompts and log lines.
const (
	maxLabels   = 20
	maxLabelLen = 100
)

// ParseWebhook validates the webhook signature and parses the event.
func ParseWebhook(r *http.Request, secret []byte) (*WebhookEvent, error) {
	// Limit webhook body to 10MB to prevent abuse
	const maxBodySize = 10 << 20
	defer r.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(r.Body, maxBodySize))
	if err != nil {
		return nil, fmt.Errorf("reading body: %w", err)
	}

	if err := gh.ValidateSignature(r.Header.Get("X-Hub-Signature-256"), payload, secret); err != nil {
		return nil, fmt.Errorf("invalid signature: %w", err)
	}

	eventType := r.Header.Get("X-GitHub-Event")
	event, err := gh.ParseWebHook(eventType, payload)
	if err != nil {
		return nil, fmt.Errorf("parsing webhook: %w", err)
	}

	return &WebhookEvent{
		Type:    eventType,
		Action:  extractAction(event),
		Payload: event,
	}, nil
}

// ToPREvent converts a pull_request webhook payload to a PREvent.
func ToPREvent(event *WebhookEvent) (*PREvent, error) {
	prEvent, ok := event.Payload.(*gh.PullRequestEvent)
	if !ok {
		return nil, fmt.Errorf("expected PullRequestEvent, got %T", event.Payload)
	}

	pe := &PREvent{
		Action:         event.Action,
		InstallationID: prEvent.GetInstallation().GetID(),
		RepoFullName:   prEvent.GetRepo().GetFullName(),
		RepoID:         prEvent.GetRepo().GetID(),
		PRNumber:       prEvent.GetPullRequest().GetNumber(),
		PRTitle:        prEvent.GetPullRequest().GetTitle(),
		PRAuthor:       prEvent.GetPullRequest().GetUser().GetLogin(),
		HeadSHA:        prEvent.GetPullRequest().GetHead().GetSHA(),
		BaseSHA:        prEvent.GetPullRequest().GetBase().GetSHA(),
		BaseRef:        prEvent.GetPullRequest().GetBase().GetRef(),
		HeadRef:        prEvent.GetPullRequest().GetHead().GetRef(),
		BaseRepoID:     prEvent.GetPullRequest().GetBase().GetRepo().GetID(),
		MergeCommitSHA: prEvent.GetPullRequest().GetMergeCommitSHA(),
		MergedAt:       prEvent.GetPullRequest().GetMergedAt().Time,
		PRBody:         util.Truncate(prEvent.GetPullRequest().GetBody(), 8000, false),
		Merged:         prEvent.GetPullRequest().GetMerged(),
		Draft:          prEvent.GetPullRequest().GetDraft(),
	}
	for _, l := range prEvent.GetPullRequest().Labels {
		if len(pe.Labels) >= maxLabels {
			break
		}
		if name := util.Truncate(l.GetName(), maxLabelLen, false); name != "" {
			pe.Labels = append(pe.Labels, name)
		}
	}
	// payload.changes.body.from is only populated on action="edited". We
	// truncate to the same budget as PRBody so the diff uses comparable
	// input (and so a 10MB body can't blow up memory on the edit path).
	if event.Action == "edited" {
		if changes := prEvent.GetChanges(); changes != nil {
			if body := changes.GetBody(); body != nil {
				pe.PRBodyBefore = util.Truncate(body.GetFrom(), 8000, false)
			}
		}
	}
	return pe, nil
}

// DefaultBranchUpdate is a signed webhook observation suitable for the
// tenant/repository-scoped graph refresh seam. CommitSHA is always the target
// repository's default-branch commit, never a pull request head.
type DefaultBranchUpdate struct {
	InstallationID int64
	RepoID         int64
	RepoFullName   string
	DefaultBranch  string
	CommitSHA      string
	ObservedAt     time.Time
	// branchIdentityAuthoritative is true only when the signed payload itself
	// declares the repository default branch. A merged PR identifies its target
	// branch, but does not prove that branch is the repository default.
	branchIdentityAuthoritative bool
}

// BranchIdentityIsAuthoritative reports whether this observation may replace
// the stored repository default branch as well as refresh its head.
func (u DefaultBranchUpdate) BranchIdentityIsAuthoritative() bool {
	return u.branchIdentityAuthoritative
}

// DefaultBranchUpdateFromPR accepts only a verified merged-close transition on
// the target repository. A fork PR is safe because its head is never consulted;
// a closed-unmerged PR and every synchronize event are ignored.
func DefaultBranchUpdateFromPR(event PREvent) (DefaultBranchUpdate, bool) {
	if event.Action != "closed" || !event.Merged || event.RepoID <= 0 || event.BaseRepoID != event.RepoID || event.BaseRef == "" || invalidGitCommit(event.MergeCommitSHA) {
		return DefaultBranchUpdate{}, false
	}
	return DefaultBranchUpdate{
		InstallationID: event.InstallationID,
		RepoID:         event.RepoID,
		RepoFullName:   event.RepoFullName,
		DefaultBranch:  event.BaseRef,
		CommitSHA:      event.MergeCommitSHA,
		ObservedAt:     event.MergedAt,
	}, true
}

// DefaultBranchUpdateFromPush accepts only a non-delete push whose ref exactly
// matches the repository-declared default branch.
func DefaultBranchUpdateFromPush(event *WebhookEvent) (DefaultBranchUpdate, bool) {
	push, ok := event.Payload.(*gh.PushEvent)
	if !ok || push.GetDeleted() || push.GetInstallation().GetID() <= 0 {
		return DefaultBranchUpdate{}, false
	}
	repo := push.GetRepo()
	branch := repo.GetDefaultBranch()
	if repo.GetID() <= 0 || branch == "" || push.GetRef() != "refs/heads/"+branch || invalidGitCommit(push.GetAfter()) {
		return DefaultBranchUpdate{}, false
	}
	return DefaultBranchUpdate{
		InstallationID:              push.GetInstallation().GetID(),
		RepoID:                      repo.GetID(),
		RepoFullName:                repo.GetFullName(),
		DefaultBranch:               branch,
		CommitSHA:                   push.GetAfter(),
		ObservedAt:                  repo.GetPushedAt().Time,
		branchIdentityAuthoritative: true,
	}, true
}

func invalidGitCommit(sha string) bool {
	trimmed := strings.TrimSpace(sha)
	return trimmed == "" || len(trimmed) > 128 || strings.Trim(trimmed, "0") == ""
}

// CommentEvent holds parsed data from a pull_request_review_comment webhook event.
type CommentEvent struct {
	Action         string
	InstallationID int64
	RepoFullName   string
	RepoID         int64
	PRNumber       int
	CommentID      int64
	NodeID         string
	InReplyToID    int64
	CommentBody    string
	CommentAuthor  string
	// AuthorAssociation is the replier's relationship to the repo (GitHub's
	// author_association). Gates the reply path's privileged shortcut (resolving
	// the thread / writing terminal ledger state) — a review-comment replier is
	// the same untrusted population as a reactor.
	AuthorAssociation string
	FilePath          string
	DiffHunk          string
	CommitID          string
}

// ToCommentEvent converts a pull_request_review_comment webhook payload to a CommentEvent.
func ToCommentEvent(event *WebhookEvent) (*CommentEvent, error) {
	e, ok := event.Payload.(*gh.PullRequestReviewCommentEvent)
	if !ok {
		return nil, fmt.Errorf("expected PullRequestReviewCommentEvent, got %T", event.Payload)
	}

	c := e.GetComment()
	return &CommentEvent{
		Action:            event.Action,
		InstallationID:    e.GetInstallation().GetID(),
		RepoFullName:      e.GetRepo().GetFullName(),
		RepoID:            e.GetRepo().GetID(),
		PRNumber:          e.GetPullRequest().GetNumber(),
		CommentID:         c.GetID(),
		NodeID:            c.GetNodeID(),
		InReplyToID:       c.GetInReplyTo(),
		CommentBody:       c.GetBody(),
		CommentAuthor:     c.GetUser().GetLogin(),
		AuthorAssociation: c.GetAuthorAssociation(),
		FilePath:          c.GetPath(),
		DiffHunk:          c.GetDiffHunk(),
		CommitID:          c.GetCommitID(),
	}, nil
}

// IssueCommentEvent holds parsed data from an issue_comment webhook event (on a PR).
type IssueCommentEvent struct {
	Action         string
	InstallationID int64
	RepoFullName   string
	RepoID         int64
	PRNumber       int
	CommentID      int64
	CommentBody    string
	CommentAuthor  string
	// AuthorAssociation is the commenter's relationship to the repo (GitHub's
	// author_association: OWNER / MEMBER / COLLABORATOR / CONTRIBUTOR / NONE / …).
	// Used to authorize privileged commands (`@argus resolve`) — issue comments
	// come from the same untrusted population as reactions.
	AuthorAssociation string
	// CommentBodyBefore is populated only on action="edited". It holds the
	// pre-edit body from payload.changes.body.from so handlers can detect
	// transitions (e.g., task-list checkbox toggles on trigger comments).
	// Empty on created/deleted actions.
	CommentBodyBefore string
	// EditorLogin is the user who performed the edit, set only on
	// action="edited". Typically differs from CommentAuthor when a viewer
	// toggles a task-list checkbox in a bot-authored comment.
	EditorLogin string
}

// ToIssueCommentEvent converts an issue_comment webhook payload to an IssueCommentEvent.
// Returns nil if the comment is not on a pull request.
func ToIssueCommentEvent(event *WebhookEvent) (*IssueCommentEvent, error) {
	e, ok := event.Payload.(*gh.IssueCommentEvent)
	if !ok {
		return nil, fmt.Errorf("expected IssueCommentEvent, got %T", event.Payload)
	}
	if !e.GetIssue().IsPullRequest() {
		return nil, nil
	}
	ice := &IssueCommentEvent{
		Action:            event.Action,
		InstallationID:    e.GetInstallation().GetID(),
		RepoFullName:      e.GetRepo().GetFullName(),
		RepoID:            e.GetRepo().GetID(),
		PRNumber:          e.GetIssue().GetNumber(),
		CommentID:         e.GetComment().GetID(),
		CommentBody:       e.GetComment().GetBody(),
		CommentAuthor:     e.GetComment().GetUser().GetLogin(),
		AuthorAssociation: e.GetComment().GetAuthorAssociation(),
	}
	if event.Action == "edited" {
		if changes := e.GetChanges(); changes != nil {
			if body := changes.GetBody(); body != nil {
				ice.CommentBodyBefore = body.GetFrom()
			}
		}
		ice.EditorLogin = e.GetSender().GetLogin()
	}
	return ice, nil
}

func extractAction(event interface{}) string {
	switch e := event.(type) {
	case *gh.PullRequestEvent:
		return e.GetAction()
	case *gh.InstallationEvent:
		return e.GetAction()
	case *gh.PullRequestReviewCommentEvent:
		return e.GetAction()
	case *gh.IssueCommentEvent:
		return e.GetAction()
	case *gh.IssuesEvent:
		return e.GetAction()
	default:
		return ""
	}
}
