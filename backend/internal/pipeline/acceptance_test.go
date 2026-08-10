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
		// A blob carrying ONLY a key this loader does not know. Decoding
		// straight into FeatureFlags leaves both bools at Go's zero value, so
		// cross-PR checks and issue acceptance silently switch off for every
		// subsequent review — while the settings API, which defaults by
		// pointer, keeps rendering both toggles ON, so nothing reveals the
		// divergence. The column carries operator-written keys, which makes
		// this shape reachable for any installation an operator has touched.
		{
			"foreign key only -> every default preserved",
			fakeFlagReader{raw: json.RawMessage(`{"operator_only_flag":"set"}`)},
			42,
			defaults,
		},
		{
			"foreign key alongside a partial save",
			fakeFlagReader{raw: json.RawMessage(`{"operator_only_flag":"set","max_linked_prs":9}`)},
			42,
			FeatureFlags{CrossPRChecks: defaults.CrossPRChecks, IssueAcceptance: defaults.IssueAcceptance, MaxLinkedPRs: 9},
		},
		// Explicitly-stored false must still beat the default (migration 039
		// depends on that distinction), which is why the loader defaults by
		// pointer rather than by testing for zero values.
		{
			"explicit false preserved next to a foreign key",
			fakeFlagReader{raw: json.RawMessage(`{"operator_only_flag":"set","cross_pr_checks":false}`)},
			42,
			FeatureFlags{CrossPRChecks: false, IssueAcceptance: defaults.IssueAcceptance, MaxLinkedPRs: defaults.MaxLinkedPRs},
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
