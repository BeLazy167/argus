package store

import (
	"fmt"
	"time"
)

func patternFromSQLC(id int, installationID int64, repoID *int64, content string, memoryDocID, createdBy *string, source string, category *string, prNumber *int, createdAt, updatedAt *time.Time) (Pattern, error) {
	if createdAt == nil || updatedAt == nil {
		return Pattern{}, fmt.Errorf("pattern %d has NULL timestamps", id)
	}
	return Pattern{
		ID: int64(id), InstallationID: installationID, RepoID: repoID, Content: content,
		MemoryDocID: memoryDocID, CreatedBy: createdBy, Source: source, Category: category,
		PRNumber: prNumber, CreatedAt: *createdAt, UpdatedAt: *updatedAt,
	}, nil
}

func installationFromSQLC(id, installationID int64, orgLogin string, clerkOrgID *string, createdAt time.Time, suspendedAt *time.Time) Installation {
	return Installation{ID: id, InstallationID: installationID, OrgLogin: orgLogin, ClerkOrgID: clerkOrgID, CreatedAt: createdAt, SuspendedAt: suspendedAt}
}

func repoFromSQLC(id, installationID, githubID int64, fullName, defaultBranch string, enabled bool, settingsJSON []byte, createdAt, updatedAt time.Time) Repo {
	return Repo{ID: id, InstallationID: installationID, GithubID: githubID, FullName: fullName, DefaultBranch: defaultBranch, Enabled: enabled, SettingsJSON: settingsJSON, CreatedAt: createdAt, UpdatedAt: updatedAt}
}
