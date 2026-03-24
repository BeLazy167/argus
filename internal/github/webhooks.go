package github

import (
	"fmt"
	"io"
	"net/http"

	"github.com/BeLazy167/argus/internal/util"
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
	PRBody          string // first ~500 chars of PR description
	PersonaOverride string `json:"-"` // set by @argus-eye review --persona X
}

// ParseWebhook validates the webhook signature and parses the event.
func ParseWebhook(r *http.Request, secret []byte) (*WebhookEvent, error) {
	// Limit webhook body to 10MB to prevent abuse
	const maxBodySize = 10 << 20
	payload, err := io.ReadAll(io.LimitReader(r.Body, maxBodySize))
	if err != nil {
		return nil, fmt.Errorf("reading body: %w", err)
	}
	defer r.Body.Close()

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

	return &PREvent{
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
		PRBody:         util.Truncate(prEvent.GetPullRequest().GetBody(), 500, false),
	}, nil
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
	return &IssueCommentEvent{
		Action:         event.Action,
		InstallationID: e.GetInstallation().GetID(),
		RepoFullName:   e.GetRepo().GetFullName(),
		RepoID:         e.GetRepo().GetID(),
		PRNumber:       e.GetIssue().GetNumber(),
		CommentID:      e.GetComment().GetID(),
		CommentBody:    e.GetComment().GetBody(),
		CommentAuthor:  e.GetComment().GetUser().GetLogin(),
	}, nil
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
