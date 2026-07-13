package pipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/BeLazy167/argus/backend/internal/memory"
	"github.com/BeLazy167/argus/backend/internal/store"
)

// TestScenarioDedupeThreshold pins the clamp: a non-positive configured
// threshold must fall back to the default, never reach the comparison as 0.
func TestScenarioDedupeThreshold(t *testing.T) {
	tests := []struct {
		name string
		in   float64
		want float64
	}{
		{"zero clamps to default", 0, memory.DefaultThresholdScenarioDedupe},
		{"negative clamps to default", -0.5, memory.DefaultThresholdScenarioDedupe},
		{"positive passes through", 0.9, 0.9},
		{"default passes through", memory.DefaultThresholdScenarioDedupe, memory.DefaultThresholdScenarioDedupe},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := scenarioDedupeThreshold(tc.in); got != tc.want {
				t.Errorf("scenarioDedupeThreshold(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestIsDuplicateScenario is the regression guard: with a misconfigured 0
// threshold a low-similarity neighbor must NOT be treated as a duplicate (the
// old `Similarity >= 0` path wrongly suppressed every distinct seed).
func TestIsDuplicateScenario(t *testing.T) {
	hit := func(sim float64) []memory.ScenarioSearchResult {
		return []memory.ScenarioSearchResult{{ID: 1, Content: "x", Similarity: sim}}
	}
	tests := []struct {
		name      string
		existing  []memory.ScenarioSearchResult
		threshold float64
		wantDup   bool
	}{
		{"no existing never dup", nil, 0.85, false},
		{"zero threshold, low sim NOT dup (regression)", hit(0.50), 0, false},
		{"zero threshold, high sim dup via default", hit(0.90), 0, true},
		{"zero threshold, just below default not dup", hit(0.84), 0, false},
		{"explicit threshold met", hit(0.86), 0.85, true},
		{"explicit threshold missed", hit(0.84), 0.85, false},
		{"exact boundary is dup", hit(0.85), 0.85, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isDuplicateScenario(tc.existing, tc.threshold); got != tc.wantDup {
				t.Errorf("isDuplicateScenario(sim, %v) = %v, want %v", tc.threshold, got, tc.wantDup)
			}
		})
	}
}

// fakeScenarioStore is the test double for scenarioStore.
type fakeScenarioStore struct {
	created   []string // descriptions passed to CreateScenario
	pending   []string // descriptions passed to CreatePendingScenario
	createErr error
	listed    []store.Scenario
	listFiles []string
}

func (f *fakeScenarioStore) CreateScenario(_ context.Context, _ int64, _ *int64, description, _, _ string, _, _ []string, _ string) (int64, error) {
	if f.createErr != nil {
		return 0, f.createErr
	}
	f.created = append(f.created, description)
	return int64(len(f.created)), nil
}

func (f *fakeScenarioStore) CreatePendingScenario(_ context.Context, _ int64, _ *int64, description, _, _ string, _, _ []string, _ string) (int64, error) {
	f.pending = append(f.pending, description)
	return int64(len(f.pending)), nil
}

func (f *fakeScenarioStore) SetScenarioSupermemoryID(_ context.Context, _ int64, _ string) error {
	return nil
}

func (f *fakeScenarioStore) ListScenariosForFiles(_ context.Context, _ int64, filePaths []string) ([]store.Scenario, error) {
	f.listFiles = filePaths
	return f.listed, nil
}

// TestScenarioStoreFake exercises the scenario persistence helpers against the
// fake — proving the narrow scenarioStore seam is substitutable without a DB.
func TestScenarioStoreFake(t *testing.T) {
	ctx := context.Background()
	seeds := []ScenarioSeed{{Description: "a"}, {Description: "b"}}

	t.Run("StoreScenarioSeeds persists each seed", func(t *testing.T) {
		fake := &fakeScenarioStore{}
		// nil indexer: no dedupe search, no SM index/mirror — DB writes only.
		StoreScenarioSeeds(ctx, fake, nil, "owner", "repo", 1, nil, 0.85, seeds)
		if len(fake.created) != 2 || fake.created[0] != "a" || fake.created[1] != "b" {
			t.Errorf("created = %v, want [a b]", fake.created)
		}
	})

	t.Run("StoreScenarioSeeds continues past create errors", func(t *testing.T) {
		fake := &fakeScenarioStore{createErr: errors.New("db down")}
		StoreScenarioSeeds(ctx, fake, nil, "owner", "repo", 1, nil, 0.85, seeds) // must not panic
		if len(fake.created) != 0 {
			t.Errorf("created = %v, want none", fake.created)
		}
	})

	t.Run("StorePendingScenarioSeeds persists as pending", func(t *testing.T) {
		fake := &fakeScenarioStore{}
		StorePendingScenarioSeeds(ctx, fake, 1, nil, seeds)
		if len(fake.pending) != 2 {
			t.Errorf("pending = %v, want 2 entries", fake.pending)
		}
	})

	t.Run("FindRelevantScenarios delegates to ListScenariosForFiles", func(t *testing.T) {
		fake := &fakeScenarioStore{listed: []store.Scenario{{ID: 7, Description: "known"}}}
		got, err := FindRelevantScenarios(ctx, fake, 1, []string{"a.go"})
		if err != nil || len(got) != 1 || got[0].ID != 7 {
			t.Errorf("FindRelevantScenarios = %v, %v; want the fake's scenario", got, err)
		}
		if len(fake.listFiles) != 1 || fake.listFiles[0] != "a.go" {
			t.Errorf("listFiles = %v, want [a.go]", fake.listFiles)
		}
	})
}
