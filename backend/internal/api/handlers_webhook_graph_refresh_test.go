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
	installationID int64
	owner, repo    string
	calls          int
}

func (f *stubRepositoryMetadataClient) GetRepositoryMetadata(_ context.Context, installationID int64, owner, repo string) (ghpkg.RepositoryMetadata, error) {
	f.calls++
	f.installationID, f.owner, f.repo = installationID, owner, repo
	return f.metadata, f.err
}

func TestVerifyCurrentDefaultBranchRequiresExactLiveRepositoryIdentity(t *testing.T) {
	update := ghpkg.DefaultBranchUpdate{
		InstallationID: 11,
		RepoID:         22,
		RepoFullName:   "owner/repo",
		DefaultBranch:  "trunk",
	}

	for name, metadata := range map[string]ghpkg.RepositoryMetadata{
		"repository id":  {ID: 99, FullName: "owner/repo", DefaultBranch: "trunk"},
		"full name":      {ID: 22, FullName: "other/repo", DefaultBranch: "trunk"},
		"default branch": {ID: 22, FullName: "owner/repo", DefaultBranch: "main"},
	} {
		t.Run("rejects mismatched "+name, func(t *testing.T) {
			client := &stubRepositoryMetadataClient{metadata: metadata}
			matches, err := verifyCurrentDefaultBranch(context.Background(), client, update)
			if err != nil {
				t.Fatalf("verify: %v", err)
			}
			if matches {
				t.Fatal("mismatched live repository metadata was accepted")
			}
		})
	}

	client := &stubRepositoryMetadataClient{metadata: ghpkg.RepositoryMetadata{
		ID: 22, FullName: "owner/repo", DefaultBranch: "trunk",
	}}
	matches, err := verifyCurrentDefaultBranch(context.Background(), client, update)
	if err != nil || !matches {
		t.Fatalf("exact live metadata: matches=%v err=%v", matches, err)
	}
	if client.calls != 1 || client.installationID != 11 || client.owner != "owner" || client.repo != "repo" {
		t.Fatalf("lookup routing = calls %d installation %d repo %q/%q", client.calls, client.installationID, client.owner, client.repo)
	}
}

func TestVerifyCurrentDefaultBranchFailsClosed(t *testing.T) {
	update := ghpkg.DefaultBranchUpdate{
		InstallationID: 11, RepoID: 22, RepoFullName: "owner/repo", DefaultBranch: "trunk",
	}
	lookupErr := errors.New("metadata unavailable")
	matches, err := verifyCurrentDefaultBranch(context.Background(), &stubRepositoryMetadataClient{err: lookupErr}, update)
	if matches || !errors.Is(err, lookupErr) {
		t.Fatalf("lookup error: matches=%v err=%v", matches, err)
	}

	update.RepoFullName = "invalid"
	matches, err = verifyCurrentDefaultBranch(context.Background(), &stubRepositoryMetadataClient{}, update)
	if matches || err == nil {
		t.Fatalf("invalid full name: matches=%v err=%v", matches, err)
	}
}
