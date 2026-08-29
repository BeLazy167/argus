package pipeline

import (
	"reflect"
	"strings"
	"testing"
)

type llmJSONTestItem struct {
	Criterion string `json:"criterion"`
	Pattern   string `json:"pattern"`
}

func TestRepairInvalidJSONEscapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "repairs unsupported escape inside string",
			input: `{"criterion":"git grep \"type Vendor\|VendorName\""}`,
			want:  `{"criterion":"git grep \"type Vendor\\|VendorName\""}`,
		},
		{
			name:  "leaves all valid JSON escapes untouched",
			input: `{"criterion":"quote: \" slash: \\ solidus: \/ controls: \b\f\n\r\t unicode: \u263a"}`,
			want:  `{"criterion":"quote: \" slash: \\ solidus: \/ controls: \b\f\n\r\t unicode: \u263a"}`,
		},
		{
			name:  "leaves backslash outside string untouched",
			input: `not-json\|{"criterion":"ok"}`,
			want:  `not-json\|{"criterion":"ok"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := repairInvalidJSONEscapes(tt.input); got != tt.want {
				t.Fatalf("repairInvalidJSONEscapes() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestUnmarshalLLMArrayRepairsAndUnwraps(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		content     string
		want        []llmJSONTestItem
		wantErrPart string
	}{
		{
			name:    "repairs observed invalid shell escape",
			content: `[{"criterion":"git grep \"type Vendor\|VendorName\""}]`,
			want:    []llmJSONTestItem{{Criterion: `git grep "type Vendor\|VendorName"`}},
		},
		{
			name:    "preserves valid escapes",
			content: `[{"criterion":"line one\nquote: \"ok\"; slash: \\; unicode: \u263a"}]`,
			want:    []llmJSONTestItem{{Criterion: "line one\nquote: \"ok\"; slash: \\; unicode: ☺"}},
		},
		{
			name:    "repairs invalid escape in prose wrapped array",
			content: `Result: [{"criterion":"git grep \"type Vendor\|VendorName\""}] done`,
			want:    []llmJSONTestItem{{Criterion: `git grep "type Vendor\|VendorName"`}},
		},
		{
			name:    "unwraps single field JSON object",
			content: `{"patterns":[{"pattern":"repo convention"}]}`,
			want:    []llmJSONTestItem{{Pattern: "repo convention"}},
		},
		{
			name:    "unwraps only array valued field",
			content: `{"summary":"ok","patterns":[{"pattern":"repo convention"}]}`,
			want:    []llmJSONTestItem{{Pattern: "repo convention"}},
		},
		{
			name:    "recovers array from truncated object wrapper",
			content: `{"patterns":[{"pattern":"kept"}],"summary":"truncated`,
			want:    []llmJSONTestItem{{Pattern: "kept"}},
		},
		{
			name:        "rejects ambiguous object with multiple arrays",
			content:     `{"patterns":[{"pattern":"one"}],"other":[{"pattern":"two"}]}`,
			wantErrPart: "no JSON array found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := unmarshalLLMArray[llmJSONTestItem](tt.content)
			if tt.wantErrPart != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrPart) {
					t.Fatalf("unmarshalLLMArray() error = %v, want error containing %q", err, tt.wantErrPart)
				}
				return
			}
			if err != nil {
				t.Fatalf("unmarshalLLMArray() unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("unmarshalLLMArray() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestUnmarshalLLMObjectRecoversCompleteValuesFromTruncation(t *testing.T) {
	t.Parallel()

	type node struct {
		Name string `json:"name"`
	}
	type edge struct {
		Source string `json:"source"`
		Target string `json:"target"`
	}
	type graph struct {
		Nodes []node `json:"nodes"`
		Edges []edge `json:"edges"`
	}

	tests := []struct {
		name    string
		content string
		want    graph
	}{
		{
			name:    "drops partial node after complete node",
			content: `{"nodes":[{"name":"vendors"},{"name":"part`,
			want:    graph{Nodes: []node{{Name: "vendors"}}},
		},
		{
			name:    "drops partial node cut after nested object",
			content: `{"nodes":[{"name":"complete"},{"name":"partial","meta":{"language":"go"}`,
			want:    graph{Nodes: []node{{Name: "complete"}}},
		},
		{
			name:    "drops partial node cut after nested array",
			content: `{"nodes":[{"name":"complete"},{"name":"partial","tags":["go"]`,
			want:    graph{Nodes: []node{{Name: "complete"}}},
		},
		{
			name:    "does not synthesize node closure after completed nested children array",
			content: `{"nodes":[{"name":"complete"},{"name":"partial","meta":{"children":[{"name":"a"},{"name":"b"}]`,
			want:    graph{Nodes: []node{{Name: "complete"}}},
		},
		{
			name:    "drops partial node even when required fields precede nested cut",
			content: `{"nodes":[{"name":"complete"},{"name":"partial","kind":"module","file_path":"x.go","meta":{"language":"go"}`,
			want:    graph{Nodes: []node{{Name: "complete"}}},
		},
		{
			name:    "keeps nodes and complete edges before partial edge",
			content: `{"nodes":[{"name":"vendors"}],"edges":[{"source":"vendors","target":"db"},{"source":"part`,
			want: graph{
				Nodes: []node{{Name: "vendors"}},
				Edges: []edge{{Source: "vendors", Target: "db"}},
			},
		},
		{
			name:    "handles fenced object with prose",
			content: "```json\n{\"nodes\":[{\"name\":\"vendors\"}],\"edges\":[{\"source\":\"vendors",
			want:    graph{Nodes: []node{{Name: "vendors"}}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var got graph
			if err := unmarshalLLMObject(tt.content, &got); err != nil {
				t.Fatalf("unmarshalLLMObject() unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("unmarshalLLMObject() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestUnmarshalLLMObjectWithSalvageReportsEmptyPartialResult(t *testing.T) {
	t.Parallel()

	var got struct {
		Nodes []llmJSONTestItem `json:"nodes"`
		Edges []llmJSONTestItem `json:"edges"`
	}
	salvaged, err := unmarshalLLMObjectWithSalvage(`{"nodes":[],"edges":[],"explanation":"unfinished`, &got)
	if err != nil {
		t.Fatalf("unmarshalLLMObjectWithSalvage() unexpected error: %v", err)
	}
	if !salvaged {
		t.Fatal("salvaged = false, want true")
	}
	if len(got.Nodes) != 0 || len(got.Edges) != 0 {
		t.Fatalf("empty partial result = %#v, want empty graph", got)
	}
}

func TestRecoverTruncatedJSONScansPrefixOfOversizedInput(t *testing.T) {
	t.Parallel()

	content := `[{"pattern":"kept"},` + strings.Repeat(" ", maxLLMJSONSalvageBytes)
	recovered, ok := recoverTruncatedJSON(content, '[')
	if !ok {
		t.Fatal("recoverTruncatedJSON() did not recover complete prefix")
	}
	if want := `[{"pattern":"kept"}]`; recovered != want {
		t.Fatalf("recoverTruncatedJSON() = %q, want %q", recovered, want)
	}
}

func TestUnmarshalLLMObjectWithSalvageReportsMissingJointCriteria(t *testing.T) {
	t.Parallel()

	var got JointAcceptanceResult
	salvaged, err := unmarshalLLMObjectWithSalvage(`{"schema_version":1,"criteria":[{"text":"truncated`, &got)
	if err != nil {
		t.Fatalf("unmarshalLLMObjectWithSalvage() unexpected error: %v", err)
	}
	if !salvaged {
		t.Fatal("salvaged = false, want true")
	}
	if got.SchemaVersion != 1 || len(got.Criteria) != 0 {
		t.Fatalf("joint result = %#v, want version 1 with no complete criteria", got)
	}
}

func TestUnmarshalLLMObjectRepairsJointAcceptanceCriterion(t *testing.T) {
	t.Parallel()

	content := `{"schema_version":1,"criteria":[{"text":"git grep \"type Vendor\|VendorName\"","addressed_by":"acme/api#7","evidence":"vendor.go:12","status":"addressed"}],"verdict":"addressed"}`
	var got JointAcceptanceResult
	if err := unmarshalLLMObject(content, &got); err != nil {
		t.Fatalf("unmarshalLLMObject() unexpected error: %v", err)
	}
	if len(got.Criteria) != 1 {
		t.Fatalf("criteria count = %d, want 1", len(got.Criteria))
	}
	if want := `git grep "type Vendor\|VendorName"`; got.Criteria[0].Text != want {
		t.Fatalf("criterion text = %q, want %q", got.Criteria[0].Text, want)
	}
}
