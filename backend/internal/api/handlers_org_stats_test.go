package api

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BeLazy167/argus/backend/internal/pipeline"
)

func TestParsePeriodInterval(t *testing.T) {
	tests := []struct {
		query string
		want  int32
	}{
		{"7d", 7},
		{"30d", 30},
		{"90d", 90},
		{"", 30},
		{"1d", 30},
		{"365d", 30},
		{"-7d", 30},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			r, _ := http.NewRequest("GET", "/stats/overview?period="+tt.query, nil)
			got := parsePeriodInterval(r)
			if got.Days != tt.want {
				t.Errorf("parsePeriodInterval(%q).Days = %d, want %d", tt.query, got.Days, tt.want)
			}
			if !got.Valid {
				t.Errorf("parsePeriodInterval(%q).Valid = false", tt.query)
			}
		})
	}
}

func TestToFloat64(t *testing.T) {
	tests := []struct {
		name string
		val  any
		want float64
	}{
		{"float64", float64(3.14), 3.14},
		{"float32", float32(2.5), 2.5},
		{"int", int(42), 42},
		{"int32", int32(7), 7},
		{"int64", int64(99), 99},
		{"json.Number", json.Number("1.23"), 1.23},
		{"nil", nil, 0},
		{"string", "hello", 0},
		{"bool", true, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toFloat64(tt.val)
			// float32→float64 has imprecision
			if tt.name == "float32" {
				if got < 2.49 || got > 2.51 {
					t.Errorf("toFloat64(%v) = %v, want ~%v", tt.val, got, tt.want)
				}
				return
			}
			if got != tt.want {
				t.Errorf("toFloat64(%v) = %v, want %v", tt.val, got, tt.want)
			}
		})
	}
}

func TestParsePeriodIntervalType(t *testing.T) {
	r, _ := http.NewRequest("GET", "/stats/overview?period=90d", nil)
	got := parsePeriodInterval(r)
	want := pgtype.Interval{Days: 90, Valid: true}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// marshalUsage JSON-encodes a RunTokenUsage for aggregateStageCosts test seeds.
// Named to avoid collision with the general-purpose mustMarshal in handlers_reviews.go.
func marshalUsage(t *testing.T, u *pipeline.RunTokenUsage) []byte {
	t.Helper()
	b, err := json.Marshal(u)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// rowsByStage is a test helper that flattens aggregator output into a
// stage→(tokens,cost) map for easy assertion.
func rowsByStage(rows []stageCostAgg) map[string]stageCostAgg {
	m := make(map[string]stageCostAgg, len(rows))
	for _, r := range rows {
		m[r.Stage] = r
	}
	return m
}

// TestAggregateStageCosts_SplitsReviewBySpecialist pins the review-split
// behavior so a refactor that lumps `Review[]` back into one bucket fails
// loudly. Billing-sensitive — this was the core motivation for PR #70.
func TestAggregateStageCosts_SplitsReviewBySpecialist(t *testing.T) {
	usage := pipeline.RunTokenUsage{
		Triage: pipeline.StageTokens{TotalTokens: 100, Cost: 0.01},
		Review: []pipeline.StageTokens{
			{TotalTokens: 10, Cost: 0.01, Specialist: "bug_hunter"},
			{TotalTokens: 20, Cost: 0.02, Specialist: "security"},
			{TotalTokens: 30, Cost: 0.03, Specialist: "bug_hunter"}, // same specialist, second file
			{TotalTokens: 5, Cost: 0.005},                           // skim fallback (no Specialist)
		},
	}
	rows, errs := aggregateStageCosts([][]byte{marshalUsage(t, &usage)})
	if errs != 0 {
		t.Fatalf("unexpected unmarshal errs: %d", errs)
	}
	m := rowsByStage(rows)

	if r, ok := m["review.bug_hunter"]; !ok {
		t.Error("missing row review.bug_hunter")
	} else if r.TotalTokens != 40 || r.TotalCost != 0.04 {
		t.Errorf("review.bug_hunter = (%d, %.3f), want (40, 0.040) — dup specialist must sum",
			r.TotalTokens, r.TotalCost)
	}
	if r, ok := m["review.security"]; !ok {
		t.Error("missing row review.security")
	} else if r.TotalTokens != 20 {
		t.Errorf("review.security tokens = %d, want 20", r.TotalTokens)
	}
	if r, ok := m["review"]; !ok {
		t.Error("missing skim-fallback row 'review' (empty Specialist)")
	} else if r.TotalTokens != 5 {
		t.Errorf("review (skim) = %d, want 5", r.TotalTokens)
	}
	// Guard against an empty-specialist key leaking as "review.".
	if _, bad := m["review."]; bad {
		t.Error("empty-specialist leaked as 'review.' key")
	}
}

// TestAggregateStageCosts_KeepsCostOnlyStage covers the gpt-5.x reasoning
// case — cost > 0 with TotalTokens == 0. Gating on tokens alone would drop
// the row, creating a billing delta between the dashboard total and the
// sum of visible bars.
func TestAggregateStageCosts_KeepsCostOnlyStage(t *testing.T) {
	usage := pipeline.RunTokenUsage{
		Scoring: pipeline.StageTokens{TotalTokens: 0, Cost: 0.005},
	}
	rows, _ := aggregateStageCosts([][]byte{marshalUsage(t, &usage)})
	m := rowsByStage(rows)
	r, ok := m["scoring"]
	if !ok {
		t.Fatal("cost-only stage 'scoring' was silently dropped")
	}
	if r.TotalCost != 0.005 {
		t.Errorf("scoring cost = %.3f, want 0.005", r.TotalCost)
	}
}

// TestAggregateStageCosts_CountsUnmarshalFailures ensures the handler can
// surface silent drops as a telemetry signal rather than letting malformed
// JSONB rows vanish into the aggregation.
func TestAggregateStageCosts_CountsUnmarshalFailures(t *testing.T) {
	valid := pipeline.RunTokenUsage{Triage: pipeline.StageTokens{TotalTokens: 100, Cost: 0.01}}
	raws := [][]byte{
		marshalUsage(t, &valid),
		[]byte("not json"),
		[]byte("{broken"),
	}
	rows, errs := aggregateStageCosts(raws)
	if errs != 2 {
		t.Errorf("expected 2 unmarshal failures, got %d", errs)
	}
	m := rowsByStage(rows)
	if _, ok := m["triage"]; !ok {
		t.Error("valid row should still aggregate even when some rows fail to unmarshal")
	}
}

// TestAggregateStageCostsCoversAllStageFields is the reflection-based drift
// guard: every non-skipped json-tagged field on RunTokenUsage must appear as
// an aggregator output for a non-zero input. A future field like `Resolver`
// added to the struct without a matching addStage call fails this test,
// preventing a silent invisible-spend regression.
func TestAggregateStageCostsCoversAllStageFields(t *testing.T) {
	skip := map[string]bool{"mu": true, "total": true, "-": true}

	// Build a RunTokenUsage populated with 1 token/0.001 cost on every
	// non-skipped field. reflect tolerates both scalar StageTokens and
	// []StageTokens.
	var u pipeline.RunTokenUsage
	uv := reflect.ValueOf(&u).Elem()
	uType := uv.Type()
	var expectedStages []string
	stageTokensType := reflect.TypeOf(pipeline.StageTokens{})
	for i := 0; i < uType.NumField(); i++ {
		f := uType.Field(i)
		tag := f.Tag.Get("json")
		for j, c := range tag {
			if c == ',' {
				tag = tag[:j]
				break
			}
		}
		if skip[tag] || !f.IsExported() {
			continue
		}
		expectedStages = append(expectedStages, tag)
		val := reflect.New(f.Type).Elem()
		probe := reflect.New(stageTokensType).Elem()
		probe.FieldByName("TotalTokens").SetInt(1)
		probe.FieldByName("Cost").SetFloat(0.001)
		switch f.Type.Kind() {
		case reflect.Slice:
			val.Set(reflect.Append(val, probe))
		default:
			val.Set(probe)
		}
		uv.Field(i).Set(val)
	}

	rows, _ := aggregateStageCosts([][]byte{marshalUsage(t, &u)})
	got := rowsByStage(rows)
	for _, stage := range expectedStages {
		if _, ok := got[stage]; !ok {
			t.Errorf("RunTokenUsage.%s (json:%q) not reflected in aggregator output — "+
				"add a matching addStage call in aggregateStageCosts (handlers_org_stats.go)", stage, stage)
		}
	}
}
