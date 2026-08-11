package store

import (
	"testing"
	"time"
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
