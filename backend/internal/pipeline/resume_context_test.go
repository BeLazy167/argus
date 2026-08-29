// Package pipeline — resume_context_test.go guards the resume ingress against
// silently continuing a review with a differently-configured pipeline.
//
// PipelineRun's resolved context lives on json:"-" fields, so a run that
// round-trips through pipeline_states comes back with feature flags, similarity
// thresholds, the memory indexer and the review contract all zeroed. These
// tests do that round trip for real (marshal → unmarshal) and then assert the
// hydration step puts the context back. DB-less: the two settings reads and the
// indexer lookup are injected through resumeContextDeps.
package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/internal/memory"
	"github.com/BeLazy167/argus/backend/internal/memory/memorytest"
	"github.com/BeLazy167/argus/backend/pkg/diff"
	"github.com/google/uuid"
)

// fakeResumeDeps serves canned settings blobs and indexer, or errors, for the
// three reads hydrateResumeContext performs.
type fakeResumeDeps struct {
	settings    json.RawMessage
	settingsErr error
	flags       json.RawMessage
	flagsErr    error
	indexer     memory.Indexer
	// installations records every installation id the deps were asked about, so
	// a test can prove hydration resolved from the run's own installation rather
	// than a zero id (which loadFeatureFlags short-circuits to defaults).
	installations []int64
}

func (f *fakeResumeDeps) GetMergedSettings(_ context.Context, installationID, _ int64) (json.RawMessage, error) {
	f.installations = append(f.installations, installationID)
	if f.settingsErr != nil {
		return nil, f.settingsErr
	}
	return f.settings, nil
}

func (f *fakeResumeDeps) GetInstallationFeatureFlags(_ context.Context, installationID int64) (json.RawMessage, error) {
	f.installations = append(f.installations, installationID)
	if f.flagsErr != nil {
		return nil, f.flagsErr
	}
	return f.flags, nil
}

func (f *fakeResumeDeps) ResolveIndexer(_ context.Context, installationID int64) memory.Indexer {
	f.installations = append(f.installations, installationID)
	return f.indexer
}

// midFlightRun is a run persisted mid-pipeline with every json:"-" context
// field populated — the shape the state machine writes to pipeline_states just
// before a crash or a deploy restart.
func midFlightRun() *PipelineRun {
	return &PipelineRun{
		ID:       uuid.New(),
		ReviewID: uuid.New(),
		State:    StateReviewing,
		PREvent: github.PREvent{
			RepoFullName: "acme/widgets",
			PRNumber:     42,
			HeadRef:      "docs/readme",
			PRTitle:      "Document the widget",
		},
		DBInstallationID: 7,
		DBRepoID:         9,
		Diff:             &diff.PatchSet{Files: []diff.FileDiff{{NewName: "README.md"}}},
		FeatureFlags:     FeatureFlags{IssueAcceptance: true, CrossPRChecks: true, MaxLinkedPRs: 5},
		Thresholds:       memory.NewThresholds(),
		Indexer:          &memorytest.Fake{},
		Contract:         &ReviewContract{ChangeClass: ChangeClassDocs, Depth: DepthFull, Source: ContractSourceDeterministic},
	}
}

// roundTrip marshals and unmarshals a run exactly as persistState/loadState do,
// so a test operates on what the resume ingress actually gets back — not on a
// hand-zeroed struct that could drift from the real persistence behavior.
func roundTrip(t *testing.T, run *PipelineRun) *PipelineRun {
	t.Helper()
	payload, err := json.Marshal(run)
	if err != nil {
		t.Fatalf("marshaling run: %v", err)
	}
	var loaded PipelineRun
	if err := json.Unmarshal(payload, &loaded); err != nil {
		t.Fatalf("unmarshaling run: %v", err)
	}
	return &loaded
}

// TestPersistedRunLosesPipelineContext pins the defect the hydration exists to
// repair: everything on a json:"-" field is gone after the round trip, so a
// resumed review would run with issue acceptance off, every similarity gate at
// zero, no memory writer and no contract.
func TestPersistedRunLosesPipelineContext(t *testing.T) {
	loaded := roundTrip(t, midFlightRun())

	if loaded.FeatureFlags.IssueAcceptance {
		t.Error("FeatureFlags survived persistence — this test's premise is stale, revisit the json tags")
	}
	if !loaded.Thresholds.IsZero() {
		t.Error("Thresholds survived persistence — this test's premise is stale, revisit the json tags")
	}
	if loaded.Indexer != nil {
		t.Error("Indexer survived persistence — this test's premise is stale, revisit the json tags")
	}
	if loaded.Contract != nil {
		t.Error("Contract survived persistence — this test's premise is stale, revisit the json tags")
	}
	// The identifiers hydration resolves from must survive, or nothing can be
	// re-resolved at all.
	if loaded.DBInstallationID == 0 || loaded.DBRepoID == 0 {
		t.Fatalf("DBInstallationID/DBRepoID lost (%d/%d) — hydration has nothing to resolve from",
			loaded.DBInstallationID, loaded.DBRepoID)
	}
}

// TestHydrateResumeContext_RoundTrippedRunRegainsContext is the acceptance
// case: a run reloaded from pipeline_states comes back with the settings-
// resolved flags and thresholds, a live indexer, and a contract.
func TestHydrateResumeContext_RoundTrippedRunRegainsContext(t *testing.T) {
	loaded := roundTrip(t, midFlightRun())
	fake := &fakeResumeDeps{
		// Every field is deliberately the OPPOSITE of DefaultFeatureFlags()
		// (on/on/5). An earlier version of this fixture stored the defaults'
		// own values and asserted them back, so it held whether the blob was
		// applied or loadFeatureFlags fell back — see #238.
		flags:    json.RawMessage(`{"issue_acceptance":false,"cross_pr_checks":false,"max_linked_prs":3}`),
		settings: json.RawMessage(`{"threshold_scenario_trigger":0.42}`),
		indexer:  &memorytest.Fake{},
	}
	storedFlags := FeatureFlags{CrossPRChecks: false, IssueAcceptance: false, MaxLinkedPRs: 3}
	if storedFlags == DefaultFeatureFlags() {
		t.Fatalf("the stored-flags fixture now equals DefaultFeatureFlags() (%+v) — the assertion below can no longer tell an applied blob from a fallback; pick differing values",
			DefaultFeatureFlags())
	}

	hydrateResumeContext(context.Background(), loaded, fake, discardLogger())

	// Whole-struct compare, not field probes: the resumed run must carry what
	// the installation stored. A fallback to defaults here re-enables, on every
	// crash-recovered run, the checks an installation explicitly turned off.
	if loaded.FeatureFlags != storedFlags {
		t.Errorf("FeatureFlags = %+v, want %+v — the stored blob was not applied, flags fell back to defaults",
			loaded.FeatureFlags, storedFlags)
	}
	if loaded.Thresholds.IsZero() {
		t.Fatal("Thresholds still zero — every similarity gate would accept any match")
	}
	if loaded.Thresholds.ScenarioTrigger != 0.42 {
		t.Errorf("ScenarioTrigger = %v, want 0.42 (the repo's configured value, not a default)", loaded.Thresholds.ScenarioTrigger)
	}
	if loaded.Thresholds.SuppressionDrop == 0 {
		t.Error("SuppressionDrop = 0 — fixed-policy gates must be seeded too, or dismissal suppression mutes everything")
	}
	// Identity, not non-nil: the run must carry the indexer the registry
	// resolved for ITS installation, so a hydration that fabricates some other
	// writer (or resolves against a different install) fails here.
	if loaded.Indexer != fake.indexer {
		t.Errorf("Indexer = %#v, want the resolved one — the resumed review would write nothing back to its own memory", loaded.Indexer)
	}
	if loaded.Contract == nil {
		t.Fatal("Contract nil after hydration — depth, evidence bar and the security floor all collapse to defaults")
	}
	for _, id := range fake.installations {
		if id != 7 {
			t.Fatalf("hydration resolved against installation %d, want 7 (the run's own installation)", id)
		}
	}
	if len(fake.installations) != 3 {
		t.Errorf("resolved %d dependencies, want 3 (flags, settings, indexer)", len(fake.installations))
	}
}

// TestHydrateResumeContext_ReadFailuresLeaveResolvedDefaults proves a DB blip
// during recovery degrades to the documented defaults, never to the zero
// values the resume ingress started with.
func TestHydrateResumeContext_ReadFailuresLeaveResolvedDefaults(t *testing.T) {
	loaded := roundTrip(t, midFlightRun())
	fake := &fakeResumeDeps{
		settingsErr: errors.New("connection refused"),
		flagsErr:    errors.New("connection refused"),
	}

	hydrateResumeContext(context.Background(), loaded, fake, discardLogger())

	// Whole struct, so a half-populated fallback (the bools set, MaxLinkedPRs
	// left at 0) is caught too — the contract is a RESOLVED default, not a
	// partially-zero one.
	if loaded.FeatureFlags != DefaultFeatureFlags() {
		t.Errorf("FeatureFlags = %+v after a failed flag read, want the documented defaults %+v",
			loaded.FeatureFlags, DefaultFeatureFlags())
	}
	if loaded.Thresholds.IsZero() {
		t.Fatal("Thresholds zero after a failed settings read — ScenarioTrigger 0 makes every scenario match a hit")
	}
	if loaded.Thresholds.ScenarioTrigger != memory.DefaultThresholdScenarioTrigger {
		t.Errorf("ScenarioTrigger = %v, want the %v default", loaded.Thresholds.ScenarioTrigger, memory.DefaultThresholdScenarioTrigger)
	}
}

// TestHydrateResumeContext_Contract covers the one field hydration recomputes
// rather than reads: rebuilt when the persistence round trip dropped it, left
// alone when a caller already resolved one.
func TestHydrateResumeContext_Contract(t *testing.T) {
	t.Run("rebuilt from the persisted PR event and diff", func(t *testing.T) {
		loaded := roundTrip(t, midFlightRun())
		hydrateResumeContext(context.Background(), loaded, &fakeResumeDeps{}, discardLogger())

		if loaded.Contract == nil {
			t.Fatal("Contract nil — nothing rebuilt it")
		}
		// docs/ branch prefix classified the original run; the rebuild must
		// reach the same class from the same persisted event.
		if loaded.Contract.ChangeClass != ChangeClassDocs {
			t.Errorf("ChangeClass = %q, want %q (rebuilt from the persisted head ref)", loaded.Contract.ChangeClass, ChangeClassDocs)
		}
		var tagged bool
		for _, s := range loaded.Contract.Signals {
			if s == ContractSignalResumeRebuild {
				tagged = true
			}
		}
		if !tagged {
			t.Errorf("Signals = %v, want %q — a rebuilt contract carries no LLM refinement and must be diagnosable as such",
				loaded.Contract.Signals, ContractSignalResumeRebuild)
		}
	})

	t.Run("an already-resolved contract is never clobbered", func(t *testing.T) {
		loaded := roundTrip(t, midFlightRun())
		resolved := &ReviewContract{ChangeClass: ChangeClassMigration, Depth: DepthFull, Source: ContractSourceLLM}
		loaded.Contract = resolved

		hydrateResumeContext(context.Background(), loaded, &fakeResumeDeps{}, discardLogger())

		if loaded.Contract.ChangeClass != ChangeClassMigration || loaded.Contract.Source != ContractSourceLLM {
			t.Errorf("Contract = %+v, want the LLM-resolved one untouched (a rebuild would downgrade it to the deterministic class)", loaded.Contract)
		}
	})
}

// TestResume_HydratesBeforeTheFirstStage proves the wiring, not just the
// helper: the stage the resumed run re-enters must already see the restored
// context. Hydrating inside Run — or after it — would leave the first stage,
// the most likely consumer, reading zeroes.
func TestResume_HydratesBeforeTheFirstStage(t *testing.T) {
	sm, _ := newTestSM()
	persisted := roundTrip(t, midFlightRun())
	sm.load = func(_ context.Context, _ uuid.UUID) (*PipelineRun, error) { return persisted, nil }
	fake := &fakeResumeDeps{
		// max_linked_prs 3 ≠ the default 5, so the stage below sees a value that
		// only a hydration honoring the stored blob can produce (#238).
		flags:    json.RawMessage(`{"issue_acceptance":true,"cross_pr_checks":true,"max_linked_prs":3}`),
		settings: json.RawMessage(`{"threshold_scenario_trigger":0.42}`),
		indexer:  &memorytest.Fake{},
	}
	sm.hydrate = func(ctx context.Context, run *PipelineRun) {
		hydrateResumeContext(ctx, run, fake, discardLogger())
	}

	type seen struct {
		acceptance   bool
		maxLinkedPRs int
		trigger      float64
		hasIndexer   bool
	}
	var first *seen
	for _, st := range pipelineStages {
		sm.RegisterStage(st, func(_ context.Context, run *PipelineRun) error {
			if first == nil {
				first = &seen{
					acceptance:   run.FeatureFlags.IssueAcceptance,
					maxLinkedPRs: run.FeatureFlags.MaxLinkedPRs,
					trigger:      run.Thresholds.ScenarioTrigger,
					hasIndexer:   run.Indexer != nil,
				}
			}
			return nil
		})
	}

	run, err := sm.Resume(context.Background(), persisted.ID)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if run.State != StateCompleted {
		t.Fatalf("run.State = %q, want %q", run.State, StateCompleted)
	}
	if first == nil {
		t.Fatal("no stage executed — the resumed run never re-entered the pipeline")
	}
	if !first.acceptance {
		t.Error("first resumed stage saw IssueAcceptance=false — hydration ran too late")
	}
	if first.maxLinkedPRs != 3 {
		t.Errorf("first resumed stage saw MaxLinkedPRs=%d, want 3 — hydration ran too late, or it ignored the installation's stored flags", first.maxLinkedPRs)
	}
	if first.trigger != 0.42 {
		t.Errorf("first resumed stage saw ScenarioTrigger=%v, want 0.42 — hydration ran too late", first.trigger)
	}
	if !first.hasIndexer {
		t.Error("first resumed stage saw a nil Indexer — hydration ran too late")
	}
}

// TestResume_TerminalRunSkipsHydration pins the one case that must NOT hydrate:
// a terminal run is returned for inspection and never re-enters the loop, so
// spending two DB reads and an embedder lookup on it is pure waste.
func TestResume_TerminalRunSkipsHydration(t *testing.T) {
	sm, _ := newTestSM()
	persisted := roundTrip(t, midFlightRun())
	persisted.State = StateFailed
	sm.load = func(_ context.Context, _ uuid.UUID) (*PipelineRun, error) { return persisted, nil }
	hydrated := false
	sm.hydrate = func(_ context.Context, _ *PipelineRun) { hydrated = true }

	if _, err := sm.Resume(context.Background(), persisted.ID); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if hydrated {
		t.Error("hydrated a terminal run — it never runs a stage, so the reads are wasted")
	}
}

// TestDefaultResumeDeps_UnwiredStoreDegrades proves the typed-nil defusal: a
// nil *store.Store must route both reads to their fallbacks instead of
// panicking inside a method call during crash recovery.
func TestDefaultResumeDeps_UnwiredStoreDegrades(t *testing.T) {
	deps := defaultResumeDeps{}
	loaded := roundTrip(t, midFlightRun())

	hydrateResumeContext(context.Background(), loaded, deps, discardLogger())

	if loaded.FeatureFlags != DefaultFeatureFlags() {
		t.Errorf("FeatureFlags = %+v with an unwired store, want the documented defaults %+v",
			loaded.FeatureFlags, DefaultFeatureFlags())
	}
	if loaded.Thresholds.IsZero() {
		t.Error("Thresholds zero with an unwired store — must fall back to the fixed-policy defaults")
	}
	if loaded.Indexer != nil {
		t.Error("Indexer non-nil with no registry wired")
	}
}
