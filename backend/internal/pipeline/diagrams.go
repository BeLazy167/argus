package pipeline

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/BeLazy167/argus/backend/internal/store/db"
	"github.com/BeLazy167/argus/backend/internal/util"
)

const (
	maxDiagramGroundingNodes = 24
	maxDiagramGroundingEdges = 32
	maxDiagramDiffBytes      = 6_000
	maxDiagramDiffFileBytes  = 1_200
)

type diagramResult struct {
	Type     string   `json:"type"`
	Title    string   `json:"title"`
	Mermaid  string   `json:"mermaid"`
	Evidence []string `json:"evidence"`
}

type diagramSpec struct {
	Type        string
	Title       string
	Instruction string
	MaxNodes    int
}

type groundedDiagramEdge struct {
	EvidenceID string
	Source     string
	Target     string
	Kind       string
}

type groundedDiagramDiff struct {
	EvidenceID string
	Node       string
	Source     string
}

type diagramGrounding struct {
	Nodes    map[string]string
	ByPath   map[string]string
	Edges    []groundedDiagramEdge
	Diffs    []groundedDiagramDiff
	Evidence map[string]bool
}

func (o *Orchestrator) loadDiagramGrounding(ctx context.Context, run *PipelineRun) diagramGrounding {
	if o == nil || o.st == nil || run == nil || run.DBRepoID == 0 {
		return buildDiagramGrounding(run, nil)
	}
	rows, err := o.st.ListArchFileEdges(ctx, run.DBRepoID)
	if err != nil {
		if o.logger != nil {
			o.logger.Warn("diagram grounding graph query failed", "error", err)
		}
		return buildDiagramGrounding(run, nil)
	}
	return buildDiagramGrounding(run, rows)
}

func formatDiagramGrounding(grounding diagramGrounding) string {
	var builder strings.Builder
	builder.WriteString("Grounded node identifiers:\n")
	aliases := make([]string, 0, len(grounding.Nodes))
	for alias := range grounding.Nodes {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	for _, alias := range aliases {
		builder.WriteString(fmt.Sprintf("- %s = %s\n", alias, sanitizeUserInput(grounding.Nodes[alias])))
	}
	builder.WriteString("Grounded directed edges:\n")
	for _, edge := range grounding.Edges {
		builder.WriteString(fmt.Sprintf("- %s: %s -> %s (%s)\n", edge.EvidenceID, edge.Source, edge.Target, sanitizeUserInput(edge.Kind)))
	}
	builder.WriteString("Bounded diff evidence:\n")
	for _, diff := range grounding.Diffs {
		content := fmt.Sprintf("%s (%s):\n%s", diff.EvidenceID, diff.Node, sanitizeUserInput(diff.Source))
		builder.WriteString(wrapSafeDelimiters("diagram_diff", content))
		builder.WriteString("\n")
	}
	return wrapSafeDelimiters("diagram_evidence", builder.String())
}

func buildDiagramGrounding(run *PipelineRun, rows []db.ListArchFileEdgesRow) diagramGrounding {
	grounding := diagramGrounding{
		Nodes: map[string]string{}, ByPath: map[string]string{}, Evidence: map[string]bool{},
	}
	if run == nil || run.Diff == nil {
		return grounding
	}
	files := diagramChangedFiles(run)
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
	if len(files) > maxDiagramGroundingNodes {
		files = files[:maxDiagramGroundingNodes]
	}
	for i, file := range files {
		alias := fmt.Sprintf("N%d", i+1)
		grounding.Nodes[alias] = file.path
		grounding.ByPath[file.path] = alias
	}
	type edgeKey struct{ source, target, kind string }
	seen := map[edgeKey]bool{}
	sort.Slice(rows, func(i, j int) bool {
		a := rows[i].SourcePath + "\x00" + rows[i].TargetPath + "\x00" + rows[i].Kind
		b := rows[j].SourcePath + "\x00" + rows[j].TargetPath + "\x00" + rows[j].Kind
		return a < b
	})
	for _, row := range rows {
		source, sourceOK := grounding.ByPath[row.SourcePath]
		target, targetOK := grounding.ByPath[row.TargetPath]
		key := edgeKey{row.SourcePath, row.TargetPath, row.Kind}
		if !sourceOK || !targetOK || seen[key] || len(grounding.Edges) >= maxDiagramGroundingEdges {
			continue
		}
		seen[key] = true
		id := fmt.Sprintf("edge:E%d", len(grounding.Edges)+1)
		grounding.Edges = append(grounding.Edges, groundedDiagramEdge{EvidenceID: id, Source: source, Target: target, Kind: row.Kind})
		grounding.Evidence[id] = true
	}
	remaining := maxDiagramDiffBytes
	for _, file := range files {
		if remaining == 0 || file.rawDiff == "" {
			continue
		}
		limit := min(maxDiagramDiffFileBytes, remaining)
		snippet := util.Truncate(file.rawDiff, limit, false)
		if len(snippet) > remaining {
			snippet = snippet[:remaining]
		}
		id := fmt.Sprintf("diff:D%d", len(grounding.Diffs)+1)
		grounding.Diffs = append(grounding.Diffs, groundedDiagramDiff{EvidenceID: id, Node: grounding.ByPath[file.path], Source: snippet})
		grounding.Evidence[id] = true
		remaining -= len(snippet)
	}
	return grounding
}

type diffFileEvidence struct {
	path    string
	rawDiff string
}

func diagramChangedFiles(run *PipelineRun) []diffFileEvidence {
	files := make([]diffFileEvidence, 0, len(run.Diff.Files))
	seen := map[string]bool{}
	for _, file := range run.Diff.Files {
		path := file.NewName
		if path == "" || path == "/dev/null" {
			path = file.OldName
		}
		if path == "" || path == "/dev/null" || seen[path] {
			continue
		}
		seen[path] = true
		files = append(files, diffFileEvidence{path: path, rawDiff: file.RawDiff})
	}
	return files
}

func selectDiagramTypes(run *PipelineRun, grounding diagramGrounding) []diagramSpec {
	if run == nil || run.Diff == nil || len(grounding.Edges) == 0 || len(grounding.Diffs) == 0 {
		return nil
	}
	fileCount := len(run.Diff.Files)
	var specs []diagramSpec
	if fileCount >= 3 {
		specs = append(specs, diagramSpec{Type: "sequence", Title: "Call Sequence", MaxNodes: 12,
			Instruction: "Use only the listed N identifiers and exact grounded edge directions in a sequenceDiagram."})
	}
	if diagramNeedsDataflow(run) {
		specs = append(specs, diagramSpec{Type: "dataflow", Title: "Data Flow", MaxNodes: 10,
			Instruction: "Use only the listed N identifiers and exact grounded edges in a flowchart TD; diff evidence may label the flow."})
	}
	if fileCount >= 10 {
		specs = append(specs, diagramSpec{Type: "dependency", Title: "Dependency Graph", MaxNodes: 12,
			Instruction: "Use only the listed N identifiers and exact grounded edges in a graph LR."})
	}
	if len(specs) > 2 {
		specs = specs[:2]
	}
	return specs
}

func diagramNeedsDataflow(run *PipelineRun) bool {
	for _, review := range run.FileReviews {
		for _, comment := range review.Comments {
			if strings.EqualFold(string(comment.Category), "security") {
				return true
			}
			text := strings.ToLower(comment.What + " " + comment.Body)
			for _, keyword := range []string{"injection", "xss", "ssrf", "redirect", "sanitiz", "escap"} {
				if strings.Contains(text, keyword) {
					return true
				}
			}
		}
	}
	for _, file := range run.Diff.Files {
		path := strings.ToLower(file.NewName)
		for _, signal := range []string{"auth", "token", "session", "fetch", "api", "login", "oauth", "password", "credential", "validate", "input", "config"} {
			if strings.Contains(path, signal) {
				return true
			}
		}
	}
	return false
}

var (
	diagramNodeRE         = regexp.MustCompile(`\bN\d+\b`)
	sequenceParticipantRE = regexp.MustCompile(`^\s*(?:participant|actor)\s+(N\d+)(?:\s+as\s+(.+))?\s*$`)
	sequenceEdgeRE        = regexp.MustCompile(`^\s*(N\d+)\s*(?:->>|-->>|->|-->)\s*(N\d+)\s*:\s*(.+?)\s*$`)
	flowchartEdgeRE       = regexp.MustCompile(`^\s*(N\d+)(?:\["?([^"\]]+)"?\])?\s*(?:-->|-\.->|==>)\s*(N\d+)(?:\["?([^"\]]+)"?\])?\s*$`)
)

func validateDiagramCandidate(candidate diagramResult, spec diagramSpec, grounding diagramGrounding) error {
	if candidate.Type != spec.Type {
		return fmt.Errorf("diagram type %q was not requested as %q", candidate.Type, spec.Type)
	}
	source := strings.TrimSpace(candidate.Mermaid)
	lines := strings.Split(source, "\n")
	header := strings.TrimSpace(lines[0])
	switch spec.Type {
	case "sequence":
		if header != "sequenceDiagram" {
			return errors.New("sequence diagram has the wrong Mermaid type")
		}
	case "dataflow":
		if header != "flowchart TD" {
			return errors.New("dataflow diagram has the wrong Mermaid type")
		}
	case "dependency":
		if header != "graph LR" && header != "flowchart LR" {
			return errors.New("dependency diagram has the wrong Mermaid type")
		}
	default:
		return fmt.Errorf("unsupported diagram type %q", spec.Type)
	}
	providedEvidence := map[string]bool{}
	for _, id := range candidate.Evidence {
		if !grounding.Evidence[id] {
			return fmt.Errorf("unknown evidence %q", id)
		}
		providedEvidence[id] = true
	}
	usedNodes := map[string]bool{}
	for _, node := range diagramNodeRE.FindAllString(source, -1) {
		if _, ok := grounding.Nodes[node]; !ok {
			return fmt.Errorf("diagram uses ungrounded node %q", node)
		}
		usedNodes[node] = true
	}
	if len(usedNodes) == 0 || len(usedNodes) > spec.MaxNodes {
		return fmt.Errorf("diagram node count %d exceeds requested range 1..%d", len(usedNodes), spec.MaxNodes)
	}
	hasRelevantDiff := false
	for _, diff := range grounding.Diffs {
		hasRelevantDiff = hasRelevantDiff || (providedEvidence[diff.EvidenceID] && usedNodes[diff.Node])
	}
	if !hasRelevantDiff {
		return errors.New("diagram has no bounded diff evidence for a drawn node")
	}
	allowedEdges := map[[2]string][]string{}
	for _, edge := range grounding.Edges {
		pair := [2]string{edge.Source, edge.Target}
		allowedEdges[pair] = append(allowedEdges[pair], edge.EvidenceID)
	}
	edgeCount := 0
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if spec.Type == "sequence" {
			if participant := sequenceParticipantRE.FindStringSubmatch(line); len(participant) == 3 {
				alias := participant[1]
				if !usedNodes[alias] || (participant[2] != "" && participant[2] != grounding.Nodes[alias]) {
					return fmt.Errorf("ungrounded participant %q", strings.TrimSpace(line))
				}
				continue
			}
		}
		match := flowchartEdgeRE.FindStringSubmatch(line)
		if spec.Type == "sequence" {
			match = sequenceEdgeRE.FindStringSubmatch(line)
		}
		sourceIndex, targetIndex := 1, 3
		if spec.Type == "sequence" {
			targetIndex = 2
		}
		if len(match) <= targetIndex {
			return fmt.Errorf("unsupported or ungrounded Mermaid statement %q", strings.TrimSpace(line))
		}
		if spec.Type != "sequence" {
			if (match[2] != "" && match[2] != grounding.Nodes[match[1]]) ||
				(match[4] != "" && match[4] != grounding.Nodes[match[3]]) {
				return fmt.Errorf("node label lacks file evidence in %q", strings.TrimSpace(line))
			}
		}
		pair := [2]string{match[sourceIndex], match[targetIndex]}
		evidenceIDs := allowedEdges[pair]
		grounded := false
		for _, id := range evidenceIDs {
			if !providedEvidence[id] {
				continue
			}
			grounded = true
			if spec.Type == "sequence" {
				for _, edge := range grounding.Edges {
					if edge.EvidenceID == id && match[3] != edge.Kind {
						return fmt.Errorf("edge label %q is not grounded by %s", match[3], id)
					}
				}
			}
		}
		if !grounded {
			return fmt.Errorf("edge %s -> %s lacks matching graph evidence", pair[0], pair[1])
		}
		edgeCount++
	}
	if edgeCount == 0 {
		return errors.New("diagram has no grounded edge")
	}
	return nil
}

func validateDiagrams(ctx context.Context, validator MermaidValidator, candidates []diagramResult, specs []diagramSpec, grounding diagramGrounding) []diagramResult {
	if validator == nil || len(candidates) == 0 || len(candidates) != len(specs) {
		return nil
	}
	byType := make(map[string]diagramResult, len(candidates))
	for _, candidate := range candidates {
		if _, duplicate := byType[candidate.Type]; duplicate {
			return nil
		}
		byType[candidate.Type] = candidate
	}
	valid := make([]diagramResult, 0, len(specs))
	for _, spec := range specs {
		candidate, ok := byType[spec.Type]
		if !ok || validateDiagramCandidate(candidate, spec, grounding) != nil {
			return nil
		}
		if err := validator.Validate(ctx, candidate.Mermaid); err != nil {
			return nil
		}
		candidate.Title = spec.Title
		valid = append(valid, candidate)
	}
	return valid
}
