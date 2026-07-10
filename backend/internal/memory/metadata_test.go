package memory

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestMetadataToMap_HappyPaths(t *testing.T) {
	createdAt := time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		m    Metadata
		want map[string]string
	}{
		{
			name: "pattern_minimal",
			m:    Metadata{Type: TypePattern},
			want: map[string]string{"type": "pattern"},
		},
		{
			name: "pattern_full",
			m: Metadata{
				Type: TypePattern, Subtype: "auto_learned", Category: "security",
				Severity: "high", Score: 85, Source: "argus:auto_learn", PRNumber: 42, PRAuthor: "alice",
				CreatedAt: createdAt, FilePath: "src/auth.ts",
			},
			want: map[string]string{
				"type": "pattern", "subtype": "auto_learned", "category": "security",
				"severity": "high", "score": "85", "source": "argus:auto_learn",
				"pr_number": "42", "pr_author": "alice",
				"created_at": "2026-04-18T12:00:00Z", "file_path": "src/auth.ts",
			},
		},
		{
			name: "scenario_valid",
			m:    Metadata{Type: TypeScenario, ScenarioID: 17, Severity: "critical"},
			want: map[string]string{"type": "scenario", "scenario_id": "17", "severity": "critical"},
		},
		{
			name: "synthesis_valid",
			m:    Metadata{Type: TypeSynthesis, FilePath: "src/billing.ts", PRNumber: 100},
			want: map[string]string{"type": "synthesis", "file_path": "src/billing.ts", "pr_number": "100"},
		},
		{
			name: "feedback_positive",
			m: Metadata{
				Type: TypeFeedback, Polarity: PolarityPositive, Action: "confirmed",
				FilePath: "a.go", Category: "bug",
			},
			want: map[string]string{
				"type": "feedback", "polarity": "positive", "action": "confirmed",
				"file_path": "a.go", "category": "bug",
			},
		},
		{
			name: "feedback_negative",
			m: Metadata{
				Type: TypeFeedback, Polarity: PolarityNegative, Action: "dismissed",
				FilePath: "a.go", Category: "security",
			},
			want: map[string]string{
				"type": "feedback", "polarity": "negative", "action": "dismissed",
				"file_path": "a.go", "category": "security",
			},
		},
		{
			name: "pr_summary_valid",
			m:    Metadata{Type: TypePRSummary, PRNumber: 331, PRAuthor: "bob"},
			want: map[string]string{"type": "pr_summary", "pr_number": "331", "pr_author": "bob"},
		},
		{
			name: "trace_valid",
			m:    Metadata{Type: TypeTrace, Subtype: "review_finding", FilePath: "a.go", Severity: "low"},
			want: map[string]string{"type": "trace", "subtype": "review_finding", "file_path": "a.go", "severity": "low"},
		},
		{
			name: "review_valid",
			m:    Metadata{Type: TypeReview, FilePath: "a.go", Severity: "medium", Category: "bug", PRNumber: 1},
			want: map[string]string{"type": "review", "file_path": "a.go", "severity": "medium", "category": "bug", "pr_number": "1"},
		},
		{
			name: "topology_minimal",
			m:    Metadata{Type: TypeTopology},
			want: map[string]string{"type": "topology"},
		},
		{
			name: "rule_minimal",
			m:    Metadata{Type: TypeRule, Category: "error_handling"},
			want: map[string]string{"type": "rule", "category": "error_handling"},
		},
		{
			name: "extra_merged",
			m: Metadata{
				Type:  TypePattern,
				Extra: map[string]string{"custom_key": "value", "owner": "acme"},
			},
			want: map[string]string{"type": "pattern", "custom_key": "value", "owner": "acme"},
		},
		{
			name: "zero_numerics_omitted",
			m:    Metadata{Type: TypePattern, PRNumber: 0, ScenarioID: 0, Score: 0},
			want: map[string]string{"type": "pattern"},
		},
		{
			name: "zero_createdat_omitted",
			m:    Metadata{Type: TypePattern}, // CreatedAt is zero-value
			want: map[string]string{"type": "pattern"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.m.ToMap()
			if err != nil {
				t.Fatalf("ToMap() err = %v, want nil", err)
			}
			// schema_version is always present post-Bundle-6. Assert it then
			// strip it from `got` so the per-case want maps stay focused on
			// the type-specific fields.
			if got["schema_version"] != "1" {
				t.Errorf("ToMap() schema_version = %q, want %q", got["schema_version"], "1")
			}
			delete(got, "schema_version")
			if len(got) != len(tc.want) {
				t.Errorf("ToMap() len = %d, want %d. got=%v", len(got), len(tc.want), got)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("ToMap()[%q] = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

// TestMetadataToMap_SchemaVersion asserts that the SchemaVersion field
// round-trips correctly and that zero-value input normalizes to the current
// version. Regression guard against future bumps that forget to update
// CurrentSchemaVersion-driven callers.
func TestMetadataToMap_SchemaVersion(t *testing.T) {
	t.Run("default_is_current", func(t *testing.T) {
		got, err := Metadata{Type: TypePattern}.ToMap()
		if err != nil {
			t.Fatalf("ToMap err = %v", err)
		}
		if got["schema_version"] != "1" {
			t.Errorf("default schema_version = %q, want 1", got["schema_version"])
		}
	})
	t.Run("explicit_version_honored", func(t *testing.T) {
		got, err := Metadata{Type: TypePattern, SchemaVersion: 2}.ToMap()
		if err != nil {
			t.Fatalf("ToMap err = %v", err)
		}
		if got["schema_version"] != "2" {
			t.Errorf("explicit SchemaVersion=2 produced schema_version=%q", got["schema_version"])
		}
	})
	t.Run("extra_cannot_override_schema_version", func(t *testing.T) {
		_, err := Metadata{
			Type:  TypePattern,
			Extra: map[string]string{"schema_version": "99"},
		}.ToMap()
		if err == nil {
			t.Error("expected error when Extra contains schema_version, got nil")
		}
	})
}

func TestMetadataToMap_ValidationFailures(t *testing.T) {
	cases := []struct {
		name   string
		m      Metadata
		errSub string
	}{
		{"missing_type", Metadata{}, "Type is required"},
		{"unknown_type", Metadata{Type: MemoryType("bogus")}, "unknown Type"},
		{"feedback_missing_polarity", Metadata{Type: TypeFeedback, Action: "confirmed"}, "requires Polarity"},
		{"feedback_invalid_polarity", Metadata{Type: TypeFeedback, Polarity: "weird", Action: "confirmed"}, "invalid Polarity"},
		{"feedback_missing_action", Metadata{Type: TypeFeedback, Polarity: PolarityPositive}, "requires Action"},
		{"feedback_invalid_action", Metadata{Type: TypeFeedback, Polarity: PolarityPositive, Action: "maybe"}, "requires Action"},
		{"scenario_zero_id", Metadata{Type: TypeScenario, ScenarioID: 0}, "ScenarioID > 0"},
		{"synthesis_empty_path", Metadata{Type: TypeSynthesis}, "requires FilePath"},
		{"pr_summary_zero", Metadata{Type: TypePRSummary}, "PRNumber > 0"},
		{
			"extra_collides_with_typed",
			Metadata{Type: TypePattern, Extra: map[string]string{"file_path": "override"}},
			"collides",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.m.ToMap()
			if err == nil {
				t.Fatalf("ToMap() err = nil, want error containing %q", tc.errSub)
			}
			if !strings.Contains(err.Error(), tc.errSub) {
				t.Errorf("ToMap() err = %v, want substring %q", err, tc.errSub)
			}
		})
	}
}

func TestBuildFiltersJSON(t *testing.T) {
	t.Run("nil_filters", func(t *testing.T) {
		got, err := BuildFiltersJSON(nil)
		if err != nil {
			t.Fatalf("BuildFiltersJSON(nil) err = %v", err)
		}
		if got != "" {
			t.Errorf("BuildFiltersJSON(nil) = %q, want empty", got)
		}
	})

	t.Run("empty_filters", func(t *testing.T) {
		got, err := BuildFiltersJSON(&SearchFilters{})
		if err != nil {
			t.Fatalf("BuildFiltersJSON(empty) err = %v", err)
		}
		if got != "" {
			t.Errorf("BuildFiltersJSON(empty) = %q, want empty", got)
		}
	})

	t.Run("and_single_condition", func(t *testing.T) {
		f := &SearchFilters{
			AND: []FilterCondition{{Key: "type", Value: "pattern"}},
		}
		got, err := BuildFiltersJSON(f)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		// Round-trip: unmarshal back and assert shape matches docs example.
		var parsed map[string][]map[string]any
		if err := json.Unmarshal([]byte(got), &parsed); err != nil {
			t.Fatalf("round-trip unmarshal: %v", err)
		}
		and, ok := parsed["AND"]
		if !ok || len(and) != 1 {
			t.Fatalf("expected single AND condition, got %v", parsed)
		}
		if and[0]["key"] != "type" || and[0]["value"] != "pattern" {
			t.Errorf("condition = %v", and[0])
		}
	})

	t.Run("or_multiple_conditions", func(t *testing.T) {
		f := &SearchFilters{
			OR: []FilterCondition{
				{Key: "type", Value: "pattern"},
				{Key: "type", Value: "scenario"},
				{Key: "type", Value: "feedback"},
			},
		}
		got, err := BuildFiltersJSON(f)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		var parsed map[string][]map[string]any
		if err := json.Unmarshal([]byte(got), &parsed); err != nil {
			t.Fatalf("round-trip: %v", err)
		}
		if len(parsed["OR"]) != 3 {
			t.Errorf("OR len = %d, want 3", len(parsed["OR"]))
		}
	})
}

func TestFilterNumeric(t *testing.T) {
	got := FilterNumeric("pr_number", ">=", "100")
	if got.Key != "pr_number" || got.Value != "100" {
		t.Errorf("key/value = %q/%q", got.Key, got.Value)
	}
	if got.FilterType != "numeric" {
		t.Errorf("FilterType = %q, want numeric", got.FilterType)
	}
	if got.NumericOperator != ">=" {
		t.Errorf("NumericOperator = %q, want >=", got.NumericOperator)
	}
}

func TestRepoTagNewAndSharedTag(t *testing.T) {
	// Sanitizer-safe names are returned unchanged so the common case keeps
	// stable, human-readable tags.
	safe := []struct{ in, want string }{
		{"web", "web"},
		{"my_repo", "my_repo"},
		{"repo-123", "repo-123"},
	}
	for _, tc := range safe {
		if got := RepoTagNew(tc.in); got != tc.want {
			t.Errorf("RepoTagNew(%q) = %q, want %q (safe names unchanged)", tc.in, got, tc.want)
		}
	}
	// Lossy names keep the sanitized readable prefix but gain a deterministic
	// disambiguating suffix so distinct repos never share a container.
	lossy := []string{"my.repo", "owner/repo"}
	for _, in := range lossy {
		got := RepoTagNew(in)
		if !strings.HasPrefix(got, tagSanitizer.Replace(in)+"-") {
			t.Errorf("RepoTagNew(%q) = %q, want sanitized prefix + hash suffix", in, got)
		}
	}
	if SharedTag != "_shared" {
		t.Errorf("SharedTag = %q, want _shared", SharedTag)
	}
}

// TestRepoTagNew_CollisionSafe pins the idx 22/33 fixes: distinct repo names
// that sanitize to the same string get distinct containers + customID segments,
// and a repo literally named "_shared" never collides with SharedTag.
func TestRepoTagNew_CollisionSafe(t *testing.T) {
	t.Run("dot_vs_hyphen_distinct_containers", func(t *testing.T) {
		if RepoTagNew("sdk.js") == RepoTagNew("sdk-js") {
			t.Errorf("RepoTagNew collides: sdk.js and sdk-js both -> %q", RepoTagNew("sdk.js"))
		}
	})
	t.Run("dot_vs_hyphen_distinct_customids", func(t *testing.T) {
		a := SynthesisCustomID("o", "sdk.js", "src/index.ts")
		b := SynthesisCustomID("o", "sdk-js", "src/index.ts")
		if a == b {
			t.Errorf("SynthesisCustomID collides across sdk.js/sdk-js: %q", a)
		}
		pa := PatternCustomID("o", "sdk.js", "learned", "same content")
		pb := PatternCustomID("o", "sdk-js", "learned", "same content")
		if pa == pb {
			t.Errorf("PatternCustomID collides across sdk.js/sdk-js: %q", pa)
		}
	})
	t.Run("shared_named_repo_not_shared_container", func(t *testing.T) {
		if got := RepoTagNew("_shared"); got == SharedTag {
			t.Errorf("RepoTagNew(%q) = %q, must not equal SharedTag", "_shared", got)
		}
	})
	t.Run("deterministic", func(t *testing.T) {
		first := RepoTagNew("sdk.js")
		second := RepoTagNew("sdk.js")
		if first != second {
			t.Errorf("RepoTagNew not deterministic: %q vs %q", first, second)
		}
	})
	t.Run("collision_safe_tags_stay_valid", func(t *testing.T) {
		// Disambiguated tags/IDs must still contain only Supermemory-legal chars.
		if !supermemoryAllowedRe.MatchString(RepoTagNew("sdk.js")) {
			t.Errorf("RepoTagNew(sdk.js) = %q contains illegal chars", RepoTagNew("sdk.js"))
		}
		if !supermemoryAllowedRe.MatchString(SynthesisCustomID("o", "sdk.js", "src/index.ts")) {
			t.Error("disambiguated SynthesisCustomID contains illegal chars")
		}
	})
}
