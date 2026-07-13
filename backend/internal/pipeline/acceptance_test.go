package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// fakeFlagReader is the test double for featureFlagReader.
type fakeFlagReader struct {
	raw json.RawMessage
	err error
}

func (f fakeFlagReader) GetInstallationFeatureFlags(_ context.Context, _ int64) (json.RawMessage, error) {
	return f.raw, f.err
}

// TestLoadFeatureFlags pins the graceful-defaults contract: no store, no
// installation, a failing read, or malformed/empty JSON must all yield
// DefaultFeatureFlags — the pipeline never hard-fails on flag loading.
func TestLoadFeatureFlags(t *testing.T) {
	ctx := context.Background()
	defaults := DefaultFeatureFlags()

	tests := []struct {
		name string
		st   featureFlagReader
		id   int64
		want FeatureFlags
	}{
		{"nil reader -> defaults", nil, 42, defaults},
		// The regression this file exists for: a nil *store.Store must arrive
		// as a nil INTERFACE (via featureFlagReaderFor), not a typed nil that
		// defuses the guard and panics inside the method call.
		{"nil concrete store via featureFlagReaderFor -> defaults", featureFlagReaderFor(nil), 42, defaults},
		{"zero installation id -> defaults", fakeFlagReader{raw: json.RawMessage(`{"max_linked_prs":9}`)}, 0, defaults},
		{"read error -> defaults", fakeFlagReader{err: errors.New("db down")}, 42, defaults},
		{"empty payload -> defaults", fakeFlagReader{raw: json.RawMessage(``)}, 42, defaults},
		{"empty object -> defaults", fakeFlagReader{raw: json.RawMessage(`{}`)}, 42, defaults},
		{"malformed JSON -> defaults", fakeFlagReader{raw: json.RawMessage(`{not json`)}, 42, defaults},
		{
			"stored flags parsed",
			fakeFlagReader{raw: json.RawMessage(`{"cross_pr_checks":false,"issue_acceptance":true,"max_linked_prs":9}`)},
			42,
			FeatureFlags{CrossPRChecks: false, IssueAcceptance: true, MaxLinkedPRs: 9},
		},
		{
			"missing max_linked_prs backfilled from defaults",
			fakeFlagReader{raw: json.RawMessage(`{"cross_pr_checks":true,"issue_acceptance":false}`)},
			42,
			FeatureFlags{CrossPRChecks: true, IssueAcceptance: false, MaxLinkedPRs: defaults.MaxLinkedPRs},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := loadFeatureFlags(ctx, tc.st, tc.id); got != tc.want {
				t.Errorf("loadFeatureFlags() = %+v, want %+v", got, tc.want)
			}
		})
	}
}
