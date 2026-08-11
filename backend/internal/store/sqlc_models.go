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
