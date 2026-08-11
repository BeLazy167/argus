package api

import (
	"context"
	"errors"
	"testing"
)

type stubRepoPermissionChecker struct {
	allowed bool
	err     error
	calls   int

	installationID int64
	owner          string
	repo           string
	login          string
}

func (s *stubRepoPermissionChecker) HasRepoWriteAccess(_ context.Context, installationID int64, owner, repo, login string) (bool, error) {
	s.calls++
	s.installationID = installationID
	s.owner = owner
	s.repo = repo
	s.login = login
	return s.allowed, s.err
}

func TestAuthorizeRememberUsesEffectiveRepoPermission(t *testing.T) {
	t.Parallel()

	lookupErr := errors.New("github permission lookup failed")
	tests := []struct {
		name        string
		association string
		permission  string
		allowed     bool
		err         error
		want        bool
		wantErr     bool
	}{
		{name: "org member with read denied", association: "MEMBER", permission: "read"},
		{name: "collaborator with triage denied", association: "COLLABORATOR", permission: "triage"},
		{name: "write allowed", association: "NONE", permission: "write", allowed: true, want: true},
		{name: "maintain allowed", association: "CONTRIBUTOR", permission: "maintain", allowed: true, want: true},
		{name: "admin allowed", association: "FIRST_TIME_CONTRIBUTOR", permission: "admin", allowed: true, want: true},
		{name: "none denied", association: "OWNER", permission: "none"},
		{name: "lookup error denied", association: "OWNER", err: lookupErr, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			checker := &stubRepoPermissionChecker{allowed: tt.allowed, err: tt.err}
			allowed, err := authorizeRemember(context.Background(), checker, rememberAuthorization{
				InstallationID:    42,
				Owner:             "acme",
				Repo:              "widgets",
				CommentAuthor:     "octocat",
				AuthorAssociation: tt.association,
			})
			if (err != nil) != tt.wantErr {
				t.Fatalf("authorizeRemember() error = %v, wantErr %v", err, tt.wantErr)
			}
			if allowed != tt.want {
				t.Fatalf("authorizeRemember() = %v, want %v (association=%s permission=%s)", allowed, tt.want, tt.association, tt.permission)
			}
			if checker.calls != 1 {
				t.Fatalf("permission lookup calls = %d, want 1", checker.calls)
			}
			if checker.installationID != 42 || checker.owner != "acme" || checker.repo != "widgets" || checker.login != "octocat" {
				t.Fatalf("permission lookup = (%d, %q, %q, %q), want actual event actor and repo", checker.installationID, checker.owner, checker.repo, checker.login)
			}
		})
	}
}

func TestAuthorizeRememberPreservesOrgWideTrustPolicy(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		association string
		want        bool
	}{
		{name: "owner", association: "OWNER", want: true},
		{name: "member", association: " member ", want: true},
		{name: "collaborator", association: "COLLABORATOR"},
		{name: "contributor", association: "CONTRIBUTOR"},
		{name: "unknown", association: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			checker := &stubRepoPermissionChecker{err: errors.New("org-wide authorization must not use repo permission")}
			allowed, err := authorizeRemember(context.Background(), checker, rememberAuthorization{
				OrgWide:           true,
				AuthorAssociation: tt.association,
			})
			if err != nil {
				t.Fatalf("authorizeRemember() error = %v", err)
			}
			if allowed != tt.want {
				t.Fatalf("authorizeRemember() = %v, want %v", allowed, tt.want)
			}
			if checker.calls != 0 {
				t.Fatalf("org-wide authorization performed %d repo permission lookups", checker.calls)
			}
		})
	}
}
