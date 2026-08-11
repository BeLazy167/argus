package store

import (
	"testing"
	"time"

	"github.com/BeLazy167/argus/backend/internal/store/db"
	"github.com/google/uuid"
)

func TestPatternFromSQLCRejectsNullableTimestampDrift(t *testing.T) {
	now := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		createdAt *time.Time
		updatedAt *time.Time
		wantErr   bool
	}{
		{name: "complete row", createdAt: &now, updatedAt: &now},
		{name: "null created_at", updatedAt: &now, wantErr: true},
		{name: "null updated_at", createdAt: &now, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := patternFromSQLC(1, 2, nil, "content", nil, nil, "manual", nil, nil, tt.createdAt, tt.updatedAt)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && (got.CreatedAt != now || got.UpdatedAt != now) {
				t.Fatalf("timestamps = (%v, %v), want %v", got.CreatedAt, got.UpdatedAt, now)
			}
		})
	}
}

func TestReviewCommentFromSQLCRejectsNullableBooleanDrift(t *testing.T) {
	row := db.GetReviewCommentsRow{ID: uuid.New(), ReviewID: uuid.New(), CreatedAt: time.Now(), State: "posted"}
	if _, err := reviewCommentFromSQLC(row); err == nil {
		t.Fatal("reviewCommentFromSQLC accepted NULL is_new_finding")
	}
	isNew := false
	row.IsNewFinding = &isNew
	got, err := reviewCommentFromSQLC(row)
	if err != nil {
		t.Fatalf("reviewCommentFromSQLC: %v", err)
	}
	if got.IsNewFinding {
		t.Fatal("IsNewFinding = true, want false")
	}
}

func TestPromptTemplateFromSQLCRejectsNullableTimestampDrift(t *testing.T) {
	row := db.PromptTemplate{ID: 1}
	if _, err := promptTemplateFromSQLC(row); err == nil {
		t.Fatal("promptTemplateFromSQLC accepted NULL timestamps")
	}
	now := time.Now()
	row.CreatedAt, row.UpdatedAt = &now, &now
	if _, err := promptTemplateFromSQLC(row); err != nil {
		t.Fatalf("promptTemplateFromSQLC: %v", err)
	}
}
