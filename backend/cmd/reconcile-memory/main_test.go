package main

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/BeLazy167/argus/backend/internal/memory"
)

func strp(s string) *string { return &s }

func rfc3339DaysAgo(days float64) string {
	return time.Now().Add(-time.Duration(days*24) * time.Hour).Format(time.RFC3339)
}

// TestPipelinePatternCustomID_MatchesPipeline pins the reconciler's customID
// reconstruction to the EXACT ids the pipeline writes (orchestrator.go), so a
// --full re-push upserts the existing docs instead of duplicating them. If the
// pipeline's segment strings ever change, this test breaks loudly.
func TestPipelinePatternCustomID_MatchesPipeline(t *testing.T) {
	const repo = "myrepo"
	confirmedContent := "Confirmed pattern [bug]: nil deref (file: a.go)"
	learnedContent := "Always validate webhook signatures"
	conventionContent := "Convention [style]: prefer tabs over spaces"

	tests := []struct {
		name     string
		repo     string
		source   string
		content  string
		category *string
		shared   bool
		want     string
	}{
		{
			name:    "scoring_confirmed maps to confirmed segment",
			repo:    repo,
			source:  "scoring_confirmed",
			content: confirmedContent,
			want:    memory.PatternCustomID("", repo, "confirmed", confirmedContent),
		},
		{
			name:    "auto_learn repo maps to learned segment",
			repo:    repo,
			source:  "auto_learn",
			content: learnedContent,
			want:    memory.PatternCustomID("", repo, "learned", learnedContent),
		},
		{
			name:    "auto_learn shared maps to org_learned (repo-less)",
			source:  "auto_learn",
			content: learnedContent,
			shared:  true,
			want:    memory.PatternCustomID("", "", "org_learned", learnedContent),
		},
		{
			name:     "convention hashes the raw convention, not the wrapper",
			repo:     repo,
			source:   "convention",
			content:  conventionContent,
			category: strp("style"),
			want:     memory.PatternCustomID("", repo, "convention", "prefer tabs over spaces"),
		},
		{
			name:    "unknown source falls back to indexer default",
			repo:    repo,
			source:  "manual",
			content: learnedContent,
			want:    "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pipelinePatternCustomID(tt.repo, tt.source, tt.content, tt.category, tt.shared)
			if got != tt.want {
				t.Fatalf("pipelinePatternCustomID = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestPipelinePatternCustomID_ConventionDiffersFromDefault guards the specific
// duplication bug: the reconciler's old default (hash the wrapper via the DB
// source) must NOT equal the pipeline's convention id (hash the raw convention).
func TestPipelinePatternCustomID_ConventionDiffersFromDefault(t *testing.T) {
	const repo = "myrepo"
	content := "Convention [style]: prefer tabs over spaces"
	reconstructed := pipelinePatternCustomID(repo, "convention", content, strp("style"), false)
	naiveDefault := memory.PatternCustomID("", repo, "convention", content) // hashes the wrapper
	if reconstructed == naiveDefault {
		t.Fatalf("convention reconstruction should differ from naive wrapper hash; both = %q", reconstructed)
	}
}

func TestRawConvention(t *testing.T) {
	if got := rawConvention("Convention [style]: use tabs", strp("style")); got != "use tabs" {
		t.Fatalf("rawConvention = %q, want %q", got, "use tabs")
	}
	// Nil category → empty-category wrapper.
	if got := rawConvention("Convention []: use tabs", nil); got != "use tabs" {
		t.Fatalf("rawConvention(nil cat) = %q, want %q", got, "use tabs")
	}
	// Missing wrapper → returned unchanged (deterministic fallback).
	if got := rawConvention("no wrapper here", strp("style")); got != "no wrapper here" {
		t.Fatalf("rawConvention(no wrapper) = %q, want unchanged", got)
	}
}

func TestSharedDocCustomID(t *testing.T) {
	content := "generic learned pattern"
	// auto_learn → pipeline org_learned id.
	orgDoc := &memory.Document{Content: content, Metadata: map[string]string{"source": "auto_learn"}}
	if got, want := sharedDocCustomID(orgDoc), memory.PatternCustomID("", "", "org_learned", content); got != want {
		t.Fatalf("sharedDocCustomID(auto_learn) = %q, want %q", got, want)
	}
	// reply_feedback → SharedPatternCustomID default path.
	fbDoc := &memory.Document{Content: content, Metadata: map[string]string{"source": "reply_feedback"}}
	if got, want := sharedDocCustomID(fbDoc), memory.SharedPatternCustomID("reply_feedback", content); got != want {
		t.Fatalf("sharedDocCustomID(reply_feedback) = %q, want %q", got, want)
	}
	// empty source → indexer default of "pattern".
	emptyDoc := &memory.Document{Content: content, Metadata: map[string]string{}}
	if got, want := sharedDocCustomID(emptyDoc), memory.SharedPatternCustomID("pattern", content); got != want {
		t.Fatalf("sharedDocCustomID(empty) = %q, want %q", got, want)
	}
}

func TestComputeDecay(t *testing.T) {
	tests := []struct {
		name       string
		metadata   map[string]string
		updatedAt  string
		wantAction decayAction
		checkConf  func(t *testing.T, conf float64)
	}{
		{
			name:       "fresh doc within grace is noop",
			metadata:   map[string]string{"confidence": "1.00"},
			updatedAt:  rfc3339DaysAgo(5),
			wantAction: decayActionNoop,
		},
		{
			name:       "dormant doc past grace decays",
			metadata:   map[string]string{"confidence": "1.00"},
			updatedAt:  rfc3339DaysAgo(45),
			wantAction: decayActionDecay,
			checkConf: func(t *testing.T, conf float64) {
				// 1.0 - ((45-30)/7)*0.05 ≈ 0.893
				if conf < 0.88 || conf > 0.90 {
					t.Fatalf("decayed confidence = %v, want ~0.89", conf)
				}
			},
		},
		{
			name:       "very dormant doc retires",
			metadata:   map[string]string{"confidence": "1.00"},
			updatedAt:  rfc3339DaysAgo(200),
			wantAction: decayActionRetire,
		},
		{
			name:       "decay computed from base 1.0, not the already-decayed stored value",
			metadata:   map[string]string{"confidence": "0.50"}, // stored lower, must NOT compound
			updatedAt:  rfc3339DaysAgo(45),
			wantAction: decayActionDecay,
			checkConf: func(t *testing.T, conf float64) {
				if conf < 0.88 || conf > 0.90 { // ~0.89 from base 1.0, not 0.39 from 0.50
					t.Fatalf("confidence = %v, want ~0.89 (base 1.0, non-compounding)", conf)
				}
			},
		},
		{
			name:       "no write when 2-dp confidence unchanged",
			metadata:   map[string]string{"confidence": "0.89"},
			updatedAt:  rfc3339DaysAgo(45), // recomputes to ~0.89 → same 2dp → noop
			wantAction: decayActionNoop,
		},
		{
			name:       "decay_anchor drives the clock even when UpdatedAt is fresh",
			metadata:   map[string]string{"confidence": "1.00", decayAnchorKey: rfc3339DaysAgo(45)},
			updatedAt:  rfc3339DaysAgo(0), // a write-back just bumped UpdatedAt
			wantAction: decayActionDecay,
		},
		{
			name:       "malformed confidence is a conservative noop",
			metadata:   map[string]string{"confidence": "not-a-number"},
			updatedAt:  rfc3339DaysAgo(200),
			wantAction: decayActionNoop,
		},
		{
			name:       "unparseable timestamp is a conservative noop",
			metadata:   map[string]string{"confidence": "1.00"},
			updatedAt:  "garbage",
			wantAction: decayActionNoop,
		},
		{
			name:       "missing confidence ages from base 1.0",
			metadata:   map[string]string{},
			updatedAt:  rfc3339DaysAgo(45),
			wantAction: decayActionDecay,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := &memory.Document{Metadata: tt.metadata, UpdatedAt: tt.updatedAt}
			conf, action := computeDecay(doc)
			if action != tt.wantAction {
				t.Fatalf("computeDecay action = %v, want %v (conf=%v)", action, tt.wantAction, conf)
			}
			if tt.checkConf != nil {
				tt.checkConf(t, conf)
			}
		})
	}
}

func TestHandleRowErr(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Permanent failures must NOT advance the breaker.
	permErr := fmt.Errorf("repo id 7 not found: %w", errPermanent)
	if got := handleRowErr(logger, "reindex pattern failed", "pattern_id", 7, permErr, 3); got != 3 {
		t.Fatalf("permanent err advanced breaker: got %d, want 3", got)
	}

	// Transient failures advance the breaker.
	transErr := errors.New("supermemory 503")
	if got := handleRowErr(logger, "reindex pattern failed", "pattern_id", 7, transErr, 3); got != 4 {
		t.Fatalf("transient err did not advance breaker: got %d, want 4", got)
	}
}

func TestReconcileModeAndScope(t *testing.T) {
	if got := reconcileMode(runConfig{plan: true, full: true}); got != "plan" {
		t.Fatalf("plan precedence: got %q", got)
	}
	if got := reconcileMode(runConfig{full: true}); got != "full" {
		t.Fatalf("full: got %q", got)
	}
	if got := reconcileMode(runConfig{}); got != "drift" {
		t.Fatalf("drift default: got %q", got)
	}
	if got := patternScopeName(nil); got != "shared" {
		t.Fatalf("nil repo scope: got %q", got)
	}
	id := int64(1)
	if got := patternScopeName(&id); got != "repo" {
		t.Fatalf("repo scope: got %q", got)
	}
}
