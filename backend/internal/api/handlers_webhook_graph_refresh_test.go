package api

import (
	"context"
	"errors"
	"testing"

	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
)

type stubRepositoryMetadataClient struct {
	metadata       ghpkg.RepositoryMetadata
	err            error
	headSHA        string
	headErr        error
	installationID int64
	owner, repo    string
	metadataCalls  int
	headCalls      int
	resolvedBranch string
}

func (f *stubRepositoryMetadataClient) GetRepositoryMetadata(_ context.Context, installationID int64, owner, repo string) (ghpkg.RepositoryMetadata, error) {
	f.metadataCalls++
	f.installationID, f.owner, f.repo = installationID, owner, repo
	return f.metadata, f.err
}

func (f *stubRepositoryMetadataClient) ResolveDefaultBranchCommit(_ context.Context, installationID int64, owner, repo, branch string) (string, error) {
	f.headCalls++
	f.installationID, f.owner, f.repo, f.resolvedBranch = installationID, owner, repo, branch
	return f.headSHA, f.headErr
}

func TestVerifyCurrentDefaultHeadRequiresExactLiveRepositoryIdentity(t *testing.T) {
	update := ghpkg.DefaultBranchUpdate{
		InstallationID: 11,
		RepoID:         22,
		RepoFullName:   "owner/repo",
		DefaultBranch:  "trunk",
		CommitSHA:      "live-head",
	}

	for name, metadata := range map[string]ghpkg.RepositoryMetadata{
		"repository id":  {ID: 99, FullName: "owner/repo", DefaultBranch: "trunk"},
		"full name":      {ID: 22, FullName: "other/repo", DefaultBranch: "trunk"},
		"default branch": {ID: 22, FullName: "owner/repo", DefaultBranch: "main"},
	} {
		t.Run("rejects mismatched "+name, func(t *testing.T) {
			client := &stubRepositoryMetadataClient{metadata: metadata}
			matches, err := verifyCurrentDefaultHead(context.Background(), client, update)
			if err != nil {
				t.Fatalf("verify: %v", err)
			}
			if matches {
				t.Fatal("mismatched live repository metadata was accepted")
			}
			if client.headCalls != 0 {
				t.Fatal("head resolved before live repository identity matched")
			}
		})
	}

	client := &stubRepositoryMetadataClient{
		metadata: ghpkg.RepositoryMetadata{ID: 22, FullName: "owner/repo", DefaultBranch: "trunk"},
		headSHA:  "live-head",
	}
	matches, err := verifyCurrentDefaultHead(context.Background(), client, update)
	if err != nil || !matches {
		t.Fatalf("exact live metadata: matches=%v err=%v", matches, err)
	}
	if client.metadataCalls != 1 || client.headCalls != 1 || client.installationID != 11 ||
		client.owner != "owner" || client.repo != "repo" || client.resolvedBranch != "trunk" {
		t.Fatalf("lookup routing = metadata %d head %d installation %d repo %q/%q branch %q",
			client.metadataCalls, client.headCalls, client.installationID, client.owner, client.repo, client.resolvedBranch)
	}

	client.headSHA = "different-head"
	matches, err = verifyCurrentDefaultHead(context.Background(), client, update)
	if err != nil {
		t.Fatalf("verify conflicting head: %v", err)
	}
	if matches {
		t.Fatal("non-current webhook commit was accepted")
	}
}

func TestVerifyCurrentDefaultHeadFailsClosed(t *testing.T) {
	update := ghpkg.DefaultBranchUpdate{
		InstallationID: 11, RepoID: 22, RepoFullName: "owner/repo", DefaultBranch: "trunk", CommitSHA: "live-head",
	}
	lookupErr := errors.New("metadata unavailable")
	matches, err := verifyCurrentDefaultHead(context.Background(), &stubRepositoryMetadataClient{err: lookupErr}, update)
	if matches || !errors.Is(err, lookupErr) {
		t.Fatalf("lookup error: matches=%v err=%v", matches, err)
	}

	headErr := errors.New("head unavailable")
	client := &stubRepositoryMetadataClient{
		metadata: ghpkg.RepositoryMetadata{ID: 22, FullName: "owner/repo", DefaultBranch: "trunk"},
		headErr:  headErr,
	}
	matches, err = verifyCurrentDefaultHead(context.Background(), client, update)
	if matches || !errors.Is(err, headErr) {
		t.Fatalf("head error: matches=%v err=%v", matches, err)
	}

	update.RepoFullName = "invalid"
	matches, err = verifyCurrentDefaultHead(context.Background(), &stubRepositoryMetadataClient{}, update)
	if matches || err == nil {
		t.Fatalf("invalid full name: matches=%v err=%v", matches, err)
	}
}
