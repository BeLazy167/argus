// Package pipeline — crosspr_sweeper_test.go covers the sweeper ticker and
// severity-normalizer drift behavior. The sweeper is load-bearing for
// memory-leak prevention (CP-EFF); a silent failure here regressed straight
// to unbounded map growth before the per-tick recover moved inside the
// for-loop. These tests pin the happy path + the panic-safe lifecycle.
package pipeline

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestStartCrossPRSweeper_TickerDropsStaleAfterInterval seeds each of the
// four cross-PR maps with entries older than their respective window and
// asserts a single tick of the sweeper evicts them all. Cancels the ctx
// afterwards to prove the goroutine exits cleanly (no leaked goroutine
// after test return).
func TestStartCrossPRSweeper_TickerDropsStaleAfterInterval(t *testing.T) {
	// Override the package-level interval so the ticker fires well inside
	// the test budget. Restore the production value on exit.
	prevInterval := crossPRSweepInterval
	crossPRSweepInterval = 10 * time.Millisecond
	t.Cleanup(func() { crossPRSweepInterval = prevInterval })
	t.Cleanup(resetCrossPRGlobals)

	// Seed mutex maps with entries older than crossPRMutexMaxAge.
	staleReview := uuid.New()
	crossPRMutexes.acquire(staleReview) // stamps lastAccessed = now
	// Backdate lastAccessed so the sweeper sees the entry as stale.
	pastNanos := time.Now().Add(-crossPRMutexMaxAge - time.Minute).UnixNano()
	crossPRMutexes.mu.Lock()
	crossPRMutexes.entries[staleReview].lastAccessed.Store(pastNanos)
	crossPRMutexes.mu.Unlock()

	staleJoint := uuid.New()
	jointAcceptanceMutexes.acquire(staleJoint)
	jointAcceptanceMutexes.mu.Lock()
	jointAcceptanceMutexes.entries[staleJoint].lastAccessed.Store(pastNanos)
	jointAcceptanceMutexes.mu.Unlock()

	// Seed timestamp counters with expired timestamps. The sweep condition
	// is "all timestamps older than window" → key deleted.
	staleRefresh := uuid.New()
	past := time.Now().Add(-crossPRRefreshWindow - time.Minute)
	crossPRRefreshMu.Lock()
	crossPRRefreshCount[staleRefresh] = []time.Time{past}
	crossPRRefreshMu.Unlock()

	staleInstall := int64(999)
	crossPRInstallMu.Lock()
	crossPRInstallCount[staleInstall] = []time.Time{
		time.Now().Add(-crossPRPerInstallWindow - time.Minute),
	}
	crossPRInstallMu.Unlock()

	// Baseline goroutine count BEFORE sweeper launch so we can assert clean
	// exit on ctx cancel.
	baseline := runtime.NumGoroutine()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	o := &Orchestrator{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	o.startCrossPRSweeper(ctx)

	// Wait up to ~2s for all four seeded entries to be swept. Polling keeps
	// the test fast on clean runs while giving CI slack on busy machines.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if mutexEmpty(crossPRMutexes) &&
			mutexEmpty(jointAcceptanceMutexes) &&
			timestampMapEmpty(crossPRRefreshCount, &crossPRRefreshMu) &&
			installMapEmpty() {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if !mutexEmpty(crossPRMutexes) {
		t.Errorf("crossPRMutexes still has %d entries after sweep", mutexLen(crossPRMutexes))
	}
	if !mutexEmpty(jointAcceptanceMutexes) {
		t.Errorf("jointAcceptanceMutexes still has %d entries after sweep", mutexLen(jointAcceptanceMutexes))
	}
	if !timestampMapEmpty(crossPRRefreshCount, &crossPRRefreshMu) {
		t.Errorf("crossPRRefreshCount still has entries after sweep")
	}
	if !installMapEmpty() {
		t.Errorf("crossPRInstallCount still has entries after sweep")
	}

	// Cancel the ctx; give the goroutine a moment to return. Absolute
	// goroutine count comparisons are noisy on shared runners so we only
	// require that the sweeper goroutine isn't *leaking* — i.e. the delta
	// from baseline must not grow unboundedly.
	cancel()
	time.Sleep(100 * time.Millisecond)
	if delta := runtime.NumGoroutine() - baseline; delta > 2 {
		// Allow a couple of goroutines of slack — runtime may keep a
		// GC assist or finalizer thread alive. Anything > 2 means we
		// probably leaked the sweeper goroutine.
		t.Errorf("goroutine delta after cancel = %d (baseline=%d); sweeper may have leaked",
			delta, baseline)
	}
}

func mutexEmpty(m *mutexMap) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.entries) == 0
}

func mutexLen(m *mutexMap) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.entries)
}

func timestampMapEmpty[K comparable](m map[K][]time.Time, mu *sync.Mutex) bool {
	mu.Lock()
	defer mu.Unlock()
	return len(m) == 0
}

func installMapEmpty() bool {
	crossPRInstallMu.Lock()
	defer crossPRInstallMu.Unlock()
	return len(crossPRInstallCount) == 0
}

// TestNormalizeRiskSeverity pins the canonical map + drift behavior. The
// "unknown → Warn" path guards against a silent severity downgrade when the
// judge emits "CRITICAL" / "blocker" / any string outside {low, medium, high}.
// An empty input is intentionally silent (documented "absent" shape).
func TestNormalizeRiskSeverity(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		want     RiskSeverity
		wantWarn bool
	}{
		{"canonical low", "low", RiskSeverityLow, false},
		{"canonical medium", "medium", RiskSeverityMedium, false},
		{"canonical high", "high", RiskSeverityHigh, false},
		{"uppercase LOW normalized", "LOW", RiskSeverityLow, false},
		{"whitespace-trimmed medium", " medium ", RiskSeverityMedium, false},
		{"mixed-case High normalized", "High", RiskSeverityHigh, false},
		{"empty string silent default", "", RiskSeverityMedium, false},
		{"unknown value logs Warn and defaults to Medium", "garbage", RiskSeverityMedium, true},
		{"critical is NOT canonical — Warn + default", "CRITICAL", RiskSeverityMedium, true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

			got := normalizeRiskSeverity(tc.input, logger)
			if got != tc.want {
				t.Errorf("normalizeRiskSeverity(%q) = %q, want %q", tc.input, got, tc.want)
			}
			logged := strings.Contains(buf.String(), "unknown risk severity")
			if logged != tc.wantWarn {
				t.Errorf("Warn-logged = %v, want %v (log=%q)", logged, tc.wantWarn, buf.String())
			}
		})
	}
}

// TestRiskCategory_Valid pins the closed set: every declared RiskCategory
// constant must pass Valid(), and any other value must not. This guards
// against a new category being added to the constants without also being
// added to ValidRiskCategories (a silent filter bypass that would let the
// judge smuggle unmapped categories downstream).
func TestRiskCategory_Valid(t *testing.T) {
	canonical := []RiskCategory{
		RiskCategorySchemaRace,
		RiskCategorySerializationContract,
		RiskCategoryTypeDrift,
		RiskCategoryConfigContradiction,
		RiskCategoryDeployOrdering,
		RiskCategorySecurityPosture,
		RiskCategoryEnumExhaustiveness,
		RiskCategoryLocaleTemporal,
		RiskCategoryPropagatedFinding,
	}
	for _, c := range canonical {
		if !c.Valid() {
			t.Errorf("canonical category %q failed Valid() — missing from ValidRiskCategories map", c)
		}
	}

	// Sanity: sizes match, so no accidental extra entry slipped into the
	// map without a const declaration.
	if got, want := len(ValidRiskCategories), len(canonical); got != want {
		t.Errorf("ValidRiskCategories size = %d, want %d — add/remove a canonical entry?", got, want)
	}

	for _, unknown := range []RiskCategory{"", "invented", "SCHEMA_RACE", "schema-race"} {
		if unknown.Valid() {
			t.Errorf("unknown category %q passed Valid()", unknown)
		}
	}
}
