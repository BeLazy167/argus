package github

import (
	"fmt"
	"io"
	"net/http"

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
	HeadRef         string
	PRBody          string // first ~8000 chars of PR description (feeds intent extraction)
	// PRBodyBefore is populated only on action="edited" from payload.changes.body.from.
	// Used by the cross-PR webhook handler to diff linked-PR refs between pre-
	// and post-edit bodies and trigger a refresh when the set changes.
	PRBodyBefore    string
	Merged          bool
	PersonaOverride string `json:"-"` // set by @argus-eye review --persona X
}

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
		PRBody:         util.Truncate(prEvent.GetPullRequest().GetBody(), 8000, false),
		Merged:         prEvent.GetPullRequest().GetMerged(),
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
	FilePath       string
	DiffHunk       string
	CommitID       string
}

// ToCommentEvent converts a pull_request_review_comment webhook payload to a CommentEvent.
func ToCommentEvent(event *WebhookEvent) (*CommentEvent, error) {
	e, ok := event.Payload.(*gh.PullRequestReviewCommentEvent)
	if !ok {
		return nil, fmt.Errorf("expected PullRequestReviewCommentEvent, got %T", event.Payload)
	}

	c := e.GetComment()
	return &CommentEvent{
		Action:         event.Action,
		InstallationID: e.GetInstallation().GetID(),
		RepoFullName:   e.GetRepo().GetFullName(),
		RepoID:         e.GetRepo().GetID(),
		PRNumber:       e.GetPullRequest().GetNumber(),
		CommentID:      c.GetID(),
		NodeID:         c.GetNodeID(),
		InReplyToID:    c.GetInReplyTo(),
		CommentBody:    c.GetBody(),
		CommentAuthor:  c.GetUser().GetLogin(),
		FilePath:       c.GetPath(),
		DiffHunk:       c.GetDiffHunk(),
		CommitID:       c.GetCommitID(),
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
		Action:         event.Action,
		InstallationID: e.GetInstallation().GetID(),
		RepoFullName:   e.GetRepo().GetFullName(),
		RepoID:         e.GetRepo().GetID(),
		PRNumber:       e.GetIssue().GetNumber(),
		CommentID:      e.GetComment().GetID(),
		CommentBody:    e.GetComment().GetBody(),
		CommentAuthor:  e.GetComment().GetUser().GetLogin(),
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
