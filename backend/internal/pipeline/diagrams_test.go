package pipeline

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/BeLazy167/argus/backend/internal/store/db"
	pkgdiff "github.com/BeLazy167/argus/backend/pkg/diff"
)

type fakeMermaidValidator struct {
	err   error
	calls int
}

func (f *fakeMermaidValidator) Validate(_ context.Context, _ string) error {
	f.calls++
	return f.err
}

func groundedDiagramFixture() (diagramGrounding, []diagramSpec) {
	run := &PipelineRun{Diff: &pkgdiff.PatchSet{Files: []pkgdiff.FileDiff{
		{NewName: "a.go", RawDiff: "+func A() {}"},
		{NewName: "b.go", RawDiff: "+func B() {}"},
		{NewName: "c.go", RawDiff: "+func C() {}"},
	}}}
	grounding := buildDiagramGrounding(run, []db.ListArchFileEdgesRow{{SourcePath: "a.go", TargetPath: "b.go", Kind: "calls"}})
	return grounding, selectDiagramTypes(run, grounding)
}

func TestSelectDiagramTypesSkipsCleanUngroundedPR(t *testing.T) {
	run := &PipelineRun{Diff: &pkgdiff.PatchSet{Files: []pkgdiff.FileDiff{{NewName: "a.go"}, {NewName: "b.go"}, {NewName: "c.go"}}}}
	if specs := selectDiagramTypes(run, buildDiagramGrounding(run, nil)); len(specs) != 0 {
		t.Fatalf("clean PR requested ungrounded diagrams: %+v", specs)
	}
}

func TestValidateDiagramCandidateRejectsHallucinatedEdge(t *testing.T) {
	grounding, specs := groundedDiagramFixture()
	candidate := diagramResult{
		Type: "sequence", Title: "Call Sequence",
		Mermaid:  "sequenceDiagram\n  participant N1 as a.go\n  participant N3 as c.go\n  N1->>N3: calls",
		Evidence: []string{"edge:E1", "diff:D1"},
	}
	if err := validateDiagramCandidate(candidate, specs[0], grounding); err == nil {
		t.Fatal("hallucinated N1->N3 edge passed deterministic validation")
	}
}

func TestValidateDiagramCandidateRejectsStatementsHiddenInHeader(t *testing.T) {
	grounding, _ := groundedDiagramFixture()
	spec := diagramSpec{Type: "dataflow", MaxNodes: 2}
	candidate := diagramResult{
		Type: "dataflow",
		Mermaid: `flowchart TD;N2-->N1;click N1 "https://evil.example" _blank
N1["a.go"] --> N2["b.go"]`,
		Evidence: []string{"edge:E1", "diff:D1"},
	}
	if err := validateDiagramCandidate(candidate, spec, grounding); err == nil {
		t.Fatal("semicolon-separated header statements bypassed grounding validation")
	}
}

func TestValidateDiagramsFailsClosedBeforePersistence(t *testing.T) {
	grounding, specs := groundedDiagramFixture()
	candidate := diagramResult{
		Type: "sequence", Title: "Call Sequence",
		Mermaid:  "sequenceDiagram\n  participant N1 as a.go\n  participant N2 as b.go\n  N1->>N2: calls",
		Evidence: []string{"edge:E1", "diff:D1"},
	}
	validator := &fakeMermaidValidator{err: errors.New("parser unavailable")}
	got := validateDiagrams(context.Background(), validator, []diagramResult{candidate}, specs, grounding)
	if len(got) != 0 || validator.calls != 1 {
		t.Fatalf("validator outage returned %+v after %d calls; want no persistable diagrams", got, validator.calls)
	}
}

func TestValidateDiagramCandidateEnforcesNodeLimitAndEvidence(t *testing.T) {
	grounding, specs := groundedDiagramFixture()
	spec := specs[0]
	spec.MaxNodes = 1
	candidate := diagramResult{
		Type: "sequence", Mermaid: "sequenceDiagram\n  N1->>N2: calls",
		Evidence: []string{"edge:E1", "diff:D1"},
	}
	if err := validateDiagramCandidate(candidate, spec, grounding); err == nil {
		t.Fatal("diagram over requested node limit passed")
	}
	candidate.Mermaid = "sequenceDiagram\n  N1->>N2: calls"
	spec.MaxNodes = 2
	candidate.Evidence = []string{"edge:E999", "diff:D1"}
	if err := validateDiagramCandidate(candidate, spec, grounding); err == nil {
		t.Fatal("unknown evidence id passed")
	}
}

func TestValidateDiagramsDoesNotCallParserForUngroundedSource(t *testing.T) {
	grounding, specs := groundedDiagramFixture()
	validator := &fakeMermaidValidator{}
	candidate := diagramResult{
		Type: "sequence", Mermaid: "sequenceDiagram\n  N1->>N3: invented",
		Evidence: []string{"edge:E1", "diff:D1"},
	}
	if got := validateDiagrams(context.Background(), validator, []diagramResult{candidate}, specs, grounding); len(got) != 0 || validator.calls != 0 {
		t.Fatalf("ungrounded source reached parser/persistence: diagrams=%+v parser_calls=%d", got, validator.calls)
	}
}

func TestBuildDiagramGroundingBoundsDiffEvidence(t *testing.T) {
	run := &PipelineRun{Diff: &pkgdiff.PatchSet{Files: []pkgdiff.FileDiff{
		{NewName: "a.go", RawDiff: "+" + string(make([]byte, maxDiagramDiffBytes*2))},
		{NewName: "b.go", RawDiff: "+small"},
	}}}
	grounding := buildDiagramGrounding(run, []db.ListArchFileEdgesRow{{SourcePath: "a.go", TargetPath: "b.go", Kind: "calls"}})
	total := 0
	for _, diff := range grounding.Diffs {
		total += len(diff.Source)
		if len(diff.Source) > maxDiagramDiffFileBytes {
			t.Fatalf("per-file diff evidence is %d bytes", len(diff.Source))
		}
	}
	if total > maxDiagramDiffBytes {
		t.Fatalf("total diff evidence is %d bytes", total)
	}
}

func TestValidateDiagramCandidateRejectsStandaloneUngroundedNode(t *testing.T) {
	grounding, specs := groundedDiagramFixture()
	candidate := diagramResult{
		Type:     "sequence",
		Mermaid:  "sequenceDiagram\n  participant Evil\n  N1->>N2: calls",
		Evidence: []string{"edge:E1", "diff:D1"},
	}
	if err := validateDiagramCandidate(candidate, specs[0], grounding); err == nil {
		t.Fatal("standalone ungrounded participant passed")
	}
}

func TestBuildEnrichmentPromptIncludesGroundedGraphAndDiff(t *testing.T) {
	grounding, specs := groundedDiagramFixture()
	run := &PipelineRun{Diff: &pkgdiff.PatchSet{Files: []pkgdiff.FileDiff{{NewName: "a.go"}, {NewName: "b.go"}, {NewName: "c.go"}}}}
	prompt := buildEnrichmentPrompt(run, grounding, specs)
	for _, want := range []string{"N1 = a.go", "edge:E1: N1 -> N2", "diff:D1", "Do not invent nodes or edges"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}
