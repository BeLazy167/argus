package github

import (
	"testing"
	"time"

	gh "github.com/google/go-github/v68/github"
)

func TestDefaultBranchUpdateFromPRUsesMergedTargetCommit(t *testing.T) {
	mergedAt := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	event := PREvent{
		Action: "closed", InstallationID: 11, RepoID: 22, RepoFullName: "owner/repo",
		BaseRepoID: 22, BaseRef: "main", HeadRef: "feature", HeadSHA: "fork-head",
		Merged: true, MergeCommitSHA: "abcdef123456", MergedAt: mergedAt,
	}

	update, ok := DefaultBranchUpdateFromPR(event)
	if !ok {
		t.Fatal("merged target-default-branch PR was ignored")
	}
	if update.CommitSHA != event.MergeCommitSHA || update.CommitSHA == event.HeadSHA {
		t.Fatalf("refresh commit = %q, want target merge commit %q", update.CommitSHA, event.MergeCommitSHA)
	}
	if update.DefaultBranch != "main" || !update.ObservedAt.Equal(mergedAt) {
		t.Fatalf("update = %+v", update)
	}
	if update.BranchIdentityIsAuthoritative() {
		t.Fatal("merged PR target was treated as authoritative default-branch identity")
	}
}

func TestDefaultBranchUpdateFromPRRejectsUnmergedAndMismatchedBaseRepo(t *testing.T) {
	base := PREvent{
		Action: "closed", InstallationID: 11, RepoID: 22, RepoFullName: "owner/repo",
		BaseRepoID: 22, BaseRef: "main", Merged: true, MergeCommitSHA: "abcdef123456",
	}
	for name, mutate := range map[string]func(*PREvent){
		"closed unmerged":      func(event *PREvent) { event.Merged = false },
		"synchronize":          func(event *PREvent) { event.Action = "synchronize" },
		"foreign base repo":    func(event *PREvent) { event.BaseRepoID = 99 },
		"missing merge commit": func(event *PREvent) { event.MergeCommitSHA = "" },
	} {
		t.Run(name, func(t *testing.T) {
			event := base
			mutate(&event)
			if _, ok := DefaultBranchUpdateFromPR(event); ok {
				t.Fatal("unsafe PR state scheduled a graph refresh")
			}
		})
	}
}

func TestDefaultBranchUpdateFromPushRequiresExactDefaultBranch(t *testing.T) {
	pushedAt := time.Date(2026, time.February, 3, 4, 5, 6, 0, time.UTC)
	push := &gh.PushEvent{
		After: gh.Ptr("abcdef123456"), Ref: gh.Ptr("refs/heads/main"),
		Installation: &gh.Installation{ID: gh.Ptr(int64(11))},
		Repo: &gh.PushEventRepository{
			ID: gh.Ptr(int64(22)), FullName: gh.Ptr("owner/repo"), DefaultBranch: gh.Ptr("main"),
			PushedAt: &gh.Timestamp{Time: pushedAt},
		},
	}
	update, ok := DefaultBranchUpdateFromPush(&WebhookEvent{Type: "push", Payload: push})
	if !ok || update.CommitSHA != "abcdef123456" || !update.ObservedAt.Equal(pushedAt) {
		t.Fatalf("default push update = %+v, ok=%v", update, ok)
	}
	if !update.BranchIdentityIsAuthoritative() {
		t.Fatal("push repository default branch was not marked authoritative")
	}

	push.Ref = gh.Ptr("refs/heads/feature")
	if _, ok := DefaultBranchUpdateFromPush(&WebhookEvent{Type: "push", Payload: push}); ok {
		t.Fatal("feature branch push scheduled graph refresh")
	}
	push.Ref = gh.Ptr("refs/heads/main")
	push.Deleted = gh.Ptr(true)
	push.After = gh.Ptr("0000000000000000000000000000000000000000")
	if _, ok := DefaultBranchUpdateFromPush(&WebhookEvent{Type: "push", Payload: push}); ok {
		t.Fatal("deleted default branch scheduled graph refresh")
	}
}
