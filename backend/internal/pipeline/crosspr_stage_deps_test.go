// Package pipeline — crosspr_stage_deps_test.go drives the production
// crossPRStore adapter itself, not the interface it satisfies.
//
// Why a separate file: every other cross-PR test injects a fake through
// Orchestrator.crossPRHooks, so defaultCrossPRStore's method bodies are never
// executed by the suite. defaultCrossPRStore.LoadFeatureFlags is the ONLY
// adapter method that carries logic rather than pure delegation — it has to
// funnel the store through featureFlagReaderFor — and it feeds two live gates
// (crosspr_stage.go:781 and :1682). A wrapper that ignored the stored blob
// would silently re-enable cross-PR checks for every installation that turned
// them off, with the whole suite still green.
package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/BeLazy167/argus/backend/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// flagRow is a pgx.Row that yields one canned feature_flags JSONB payload,
// matching what GetInstallationFeatureFlags scans into.
type flagRow struct {
	raw json.RawMessage
	err error
}

func (r flagRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	out, ok := dest[0].(*json.RawMessage)
	if !ok {
		return errors.New("unexpected scan destination")
	}
	*out = r.raw
	return nil
}

// flagDBTX is the minimum db.DBTX that GetInstallationFeatureFlags needs.
// Exec/Query fail loudly so an adapter that reached the DB by some other
// route than the intended :one query is not silently tolerated.
type flagDBTX struct {
	row flagRow
	// args records every bound query argument so the test can prove the
	// adapter forwards its installationDBID instead of a constant.
	args []any
}

func (d *flagDBTX) Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("unexpected Exec")
}

func (d *flagDBTX) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	return nil, errors.New("unexpected Query")
}

func (d *flagDBTX) QueryRow(_ context.Context, _ string, args ...interface{}) pgx.Row {
	d.args = append(d.args, args...)
	return d.row
}

// TestDefaultCrossPRStore_LoadFeatureFlags pins the adapter used in
// production by crossPRStoreDep(): it must return the installation's STORED
// flags, and fall back to defaults only on the paths loadFeatureFlags
// defines (read failure, absent installation).
func TestDefaultCrossPRStore_LoadFeatureFlags(t *testing.T) {
	defaults := DefaultFeatureFlags()
	// Every field is the opposite of its default. Without this the assertions
	// below hold whether the adapter applies the blob or ignores it.
	stored := FeatureFlags{CrossPRChecks: false, IssueAcceptance: false, MaxLinkedPRs: 3}
	if stored == defaults {
		t.Fatalf("fixture equals DefaultFeatureFlags() %+v — this test could not fail", defaults)
	}

	const storedBlob = `{"cross_pr_checks":false,"issue_acceptance":false,"max_linked_prs":3}`

	tests := []struct {
		name string
		row  flagRow
		id   int64
		want FeatureFlags
	}{
		{"stored blob beats defaults", flagRow{raw: json.RawMessage(storedBlob)}, 2001, stored},
		{"read failure falls back to defaults", flagRow{err: errors.New("db down")}, 2001, defaults},
		{"zero installation id short-circuits to defaults", flagRow{raw: json.RawMessage(storedBlob)}, 0, defaults},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dbtx := &flagDBTX{row: tc.row}
			d := defaultCrossPRStore{st: store.NewWithDB(dbtx)}

			got := d.LoadFeatureFlags(context.Background(), tc.id)
			if got != tc.want {
				t.Fatalf("LoadFeatureFlags(%d) = %+v, want %+v", tc.id, got, tc.want)
			}
		})
	}

	t.Run("forwards the installation id it was given", func(t *testing.T) {
		dbtx := &flagDBTX{row: flagRow{raw: json.RawMessage(storedBlob)}}
		d := defaultCrossPRStore{st: store.NewWithDB(dbtx)}

		d.LoadFeatureFlags(context.Background(), 2001)
		if len(dbtx.args) != 1 || dbtx.args[0] != int64(2001) {
			t.Fatalf("query bound %v, want [2001] — the adapter dropped or rewrote its argument", dbtx.args)
		}
	})

	// A nil *store.Store must arrive at loadFeatureFlags as a nil INTERFACE.
	// Passing d.st straight through would hand it a typed nil, which defuses
	// the nil guard and panics inside the method call — the exact regression
	// featureFlagReaderFor exists to prevent, on the one path that constructs
	// the adapter without a store.
	t.Run("nil store yields defaults without panicking", func(t *testing.T) {
		d := defaultCrossPRStore{st: nil}
		if got := d.LoadFeatureFlags(context.Background(), 2001); got != defaults {
			t.Fatalf("LoadFeatureFlags on a nil store = %+v, want %+v", got, defaults)
		}
	})
}
