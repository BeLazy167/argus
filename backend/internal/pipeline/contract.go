// Package pipeline — review contract.
//
// Every PR gets a computed ReviewContract that all pipeline stages can read.
// Deterministic signals (draft flag, labels, branch prefix, changed paths,
// title, size) are evaluated first; the intent-extraction LLM is only
// consulted when metadata was silent (Source == "llm-pending"). Routing
// consumers (triage, review fan-out, pass2, summary) gate depth on the
// contract. Production-class PRs behave exactly as before the contract
// existed.
package pipeline

import (
	"fmt"
	"path"
	"strings"
	"time"

	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/pkg/diff"
)

// ChangeClass values — what kind of change this PR is.
const (
	ChangeClassProduction    = "production"
	ChangeClassMigration     = "migration"
	ChangeClassOneTimeScript = "one_time_script"
	ChangeClassTest          = "test"
	ChangeClassConfig        = "config"
	ChangeClassDocs          = "docs"
	ChangeClassGenerated     = "generated"
	ChangeClassRevert        = "revert"
)

// ValidChangeClasses is the set of change-class values the LLM may emit.
var ValidChangeClasses = map[string]bool{
	ChangeClassProduction: true, ChangeClassMigration: true,
	ChangeClassOneTimeScript: true, ChangeClassTest: true,
	ChangeClassConfig: true, ChangeClassDocs: true,
	ChangeClassGenerated: true, ChangeClassRevert: true,
}

// EvidenceBar values — how much proof a finding needs before posting.
const (
	EvidenceBarNormal = "normal"
	EvidenceBarRaised = "raised"
	EvidenceBarMax    = "max"
)

// Depth values — how deeply the pipeline reviews the PR.
const (
	DepthFull   = "full"
	DepthSingle = "single"
	DepthSkim   = "skim"
)

// Contract sources.
const (
	ContractSourceDeterministic = "deterministic" // metadata decided the class
	ContractSourceLLMPending    = "llm-pending"   // metadata silent; awaiting intent LLM
	ContractSourceLLM           = "llm"           // intent LLM filled the class (confidence >= threshold)
	ContractSourceLLMDefault    = "llm-default"   // LLM unusable/low-confidence; defaulted to production
)

// contractLLMConfidenceFloor is the minimum change_class_confidence at which
// an LLM-emitted class is trusted; below it the class defaults to production.
const contractLLMConfidenceFloor = 0.6

// Unreviewable-size thresholds: beyond these the PR is still reviewed, but
// posting states reduced confidence and recommends a split.
const (
	unreviewableLOC   = 1500
	unreviewableFiles = 60
)

// ReviewContract is the per-PR routing contract read by all pipeline stages.
type ReviewContract struct {
	ChangeClass  string   `json:"change_class"` // empty while Source == "llm-pending"
	EvidenceBar  string   `json:"evidence_bar"`
	Depth        string   `json:"depth"`
	ScrutinyBump bool     `json:"scrutiny_bump"` // refactor/rename/cleanup title: force behavior-equivalence attention
	Unreviewable bool     `json:"unreviewable"`  // too large for confident review; still reviewed
	Signals      []string `json:"signals"`
	Source       string   `json:"source"`
}

// Is reports whether the contract exists and resolved to the given class.
// A nil contract or empty class always compares false, so consumers that gate
// on non-production classes preserve today's behavior when no contract is set.
func (c *ReviewContract) Is(class string) bool {
	return c != nil && c.ChangeClass == class
}

// SkipsPass2 reports whether the contract's class makes a second architecture
// pass pointless (throwaway scripts, docs, generated code).
func (c *ReviewContract) SkipsPass2() bool {
	return c.Is(ChangeClassOneTimeScript) || c.Is(ChangeClassDocs) || c.Is(ChangeClassGenerated)
}

// HasSecurityFloor reports whether the contract carries the security floor
// signal (security-relevant files changed). Scoring uses it to lower the
// critical threshold — security PRs get MORE sensitive review, never less.
func (c *ReviewContract) HasSecurityFloor() bool {
	if c == nil {
		return false
	}
	for _, s := range c.Signals {
		if s == "floor:security" {
			return true
		}
	}
	return false
}

// ResolveFromLLM fills ChangeClass on an llm-pending contract from the intent
// extraction output. Unknown classes or confidence below the floor default to
// production. No-op unless Source == "llm-pending".
func (c *ReviewContract) ResolveFromLLM(class string, confidence float64) {
	if c == nil || c.Source != ContractSourceLLMPending {
		return
	}
	if ValidChangeClasses[class] && confidence >= contractLLMConfidenceFloor {
		c.ChangeClass = class
		c.Source = ContractSourceLLM
		c.Signals = append(c.Signals, fmt.Sprintf("llm:%s@%.2f", class, confidence))
		return
	}
	c.ChangeClass = ChangeClassProduction
	c.Source = ContractSourceLLMDefault
	c.Signals = append(c.Signals, "llm:default-production")
}

// SummaryLine renders the one-line contract stub for the posted review
// summary, plus the reduced-confidence/split note when the PR is unreviewably
// large. Returns "" for a nil contract.
func (c *ReviewContract) SummaryLine() string {
	if c == nil {
		return ""
	}
	class := c.ChangeClass
	if class == "" {
		class = ChangeClassProduction
	}
	line := fmt.Sprintf("Review contract: %s · depth %s", class, c.Depth)
	if len(c.Signals) > 0 {
		line += " · signals: " + strings.Join(c.Signals, ", ")
	}
	if c.Unreviewable {
		line += "\n\n> ⚠️ This PR exceeds reviewable size — findings carry reduced confidence. Consider splitting it into smaller PRs."
	}
	return line
}

// UnreviewableNote returns the reduced-confidence/split-suggestion blockquote
// for unreviewably large PRs, or "" when the PR is a reviewable size.
func (c *ReviewContract) UnreviewableNote() string {
	if c == nil || !c.Unreviewable {
		return ""
	}
	return "> ⚠️ This PR exceeds reviewable size — findings carry reduced confidence. Consider splitting it into smaller PRs."
}

// BuildGlassBoxLine renders the Glass Box footer line for the posted review
// summary: what contract the review ran under, which reviewers checked the
// code, how many findings team feedback suppressed, and how long the review
// took. Nil contract renders as production/full (the nil-contract default
// behavior everywhere else). Zero-value parts are omitted.
//
// Example: "Contract: production/full · checked: bug_hunter, security,
// architecture, regression · 2 suppressed by team feedback · review took 1m42s"
func BuildGlassBoxLine(c *ReviewContract, checked []string, suppressed int, took time.Duration) string {
	class, depth := ChangeClassProduction, DepthFull
	if c != nil {
		if c.ChangeClass != "" {
			class = c.ChangeClass
		}
		if c.Depth != "" {
			depth = c.Depth
		}
	}
	line := fmt.Sprintf("Contract: %s/%s", class, depth)
	if len(checked) > 0 {
		line += " · checked: " + strings.Join(checked, ", ")
	}
	if suppressed > 0 {
		line += fmt.Sprintf(" · %d suppressed by team feedback", suppressed)
	}
	if took > 0 {
		line += " · review took " + took.Truncate(time.Second).String()
	}
	return line
}

// checkedReviewers reports which reviewer passes the run dispatched, for the
// Glass Box footer. Mirrors ReviewStage.Execute's fan-out: deep review runs
// the 4-specialist squad (one balanced script reviewer for one-time scripts);
// everything else is a single-pass review.
func checkedReviewers(run *PipelineRun) []string {
	switch {
	case run.DeepReview && run.Contract.Is(ChangeClassOneTimeScript):
		return []string{string(SpecialistScript)}
	case run.DeepReview:
		names := make([]string, 0, 4)
		for _, s := range AllSpecialists() {
			names = append(names, string(s))
		}
		return names
	default:
		return []string{"single-pass review"}
	}
}

// countSuppressed tallies findings dropped by dismissal-match suppression
// (team 👎 feedback) — persisted for the dashboard but never posted.
func countSuppressed(run *PipelineRun) int {
	n := 0
	for _, fr := range run.FileReviews {
		for _, c := range fr.Comments {
			if c.Suppressed {
				n++
			}
		}
	}
	return n
}

// raiseBar raises the evidence bar to at least the given level (never lowers).
func (c *ReviewContract) raiseBar(bar string) {
	rank := map[string]int{EvidenceBarNormal: 0, EvidenceBarRaised: 1, EvidenceBarMax: 2}
	if rank[bar] > rank[c.EvidenceBar] {
		c.EvidenceBar = bar
	}
}

// ComputeContract runs the deterministic classification pass over PR metadata
// and changed files. When no deterministic rule decides the change class, the
// contract is returned with ChangeClass empty and Source "llm-pending" so the
// intent-extraction stage can fill it.
//
// Rule order (first class hit wins): hotfix label > branch prefix > path
// majority. Depth/bar modifiers (draft, wip labels, title scrutiny, size,
// security/migration floor) apply regardless of which rule classified.
func ComputeContract(event *ghpkg.PREvent, files []diff.FileDiff) *ReviewContract {
	c := &ReviewContract{
		EvidenceBar: EvidenceBarNormal,
		Depth:       DepthFull,
		Source:      ContractSourceDeterministic,
	}

	if event.Draft {
		c.Depth = DepthSkim
		c.raiseBar(EvidenceBarRaised)
		c.Signals = append(c.Signals, "draft")
	}

	for _, raw := range event.Labels {
		l := strings.ToLower(strings.TrimSpace(raw))
		switch {
		case isWIPLabel(l):
			c.Depth = DepthSkim
			c.Signals = append(c.Signals, "label:"+l)
		case strings.Contains(l, "hotfix"):
			if c.ChangeClass == "" {
				c.ChangeClass = ChangeClassProduction
			}
			c.raiseBar(EvidenceBarRaised)
			c.Signals = append(c.Signals, "label:"+l)
		}
	}

	if c.ChangeClass == "" {
		if class, prefix := classFromBranch(event.HeadRef); class != "" {
			c.ChangeClass = class
			c.Signals = append(c.Signals, "branch:"+prefix)
		}
	}

	if c.ChangeClass == "" {
		if class := classFromPaths(files); class != "" {
			c.ChangeClass = class
			c.Signals = append(c.Signals, "paths:"+class)
		}
	}

	if titleIsRefactorLike(event.PRTitle) {
		c.ScrutinyBump = true
		c.raiseBar(EvidenceBarRaised)
		c.Signals = append(c.Signals, "title:refactor-like")
	}

	loc := 0
	for _, f := range files {
		loc += changedLines(f)
	}
	if loc > unreviewableLOC || len(files) > unreviewableFiles {
		c.Unreviewable = true
		c.Signals = append(c.Signals, fmt.Sprintf("size:%d-loc/%d-files", loc, len(files)))
	}

	// FLOOR: security-relevant files or migration class force max evidence
	// and Depth never below single. Migration/destructive-SQL NEVER skims.
	secRelevant := false
	for _, f := range files {
		if isSecurityRelevant(strings.ToLower(f.NewName)) {
			secRelevant = true
			break
		}
	}
	if secRelevant || c.ChangeClass == ChangeClassMigration {
		c.raiseBar(EvidenceBarMax)
		if c.Depth == DepthSkim {
			c.Depth = DepthSingle
		}
		if secRelevant {
			c.Signals = append(c.Signals, "floor:security")
		}
		if c.ChangeClass == ChangeClassMigration {
			c.Signals = append(c.Signals, "floor:migration")
		}
	}

	if c.ChangeClass == "" {
		c.Source = ContractSourceLLMPending
	}
	return c
}

// isWIPLabel reports whether a lowercased label asks reviewers to hold off.
func isWIPLabel(l string) bool {
	if l == "wip" || strings.HasPrefix(l, "wip:") || strings.HasPrefix(l, "wip ") {
		return true
	}
	for _, w := range []string{"work in progress", "work-in-progress", "do not review", "do-not-review", "dont review", "dont-review", "don't review", "no-review", "no review"} {
		if strings.Contains(l, w) {
			return true
		}
	}
	return false
}

// classFromBranch maps well-known branch prefixes to a change class.
// Returns the class and the matched prefix, or "","" when nothing matches.
func classFromBranch(headRef string) (class, prefix string) {
	ref := strings.ToLower(headRef)
	prefixes := []struct {
		prefix string
		class  string
	}{
		{"cutover/", ChangeClassMigration},
		{"migrate/", ChangeClassMigration},
		{"migration/", ChangeClassMigration},
		{"spike/", ChangeClassOneTimeScript},
		{"prototype/", ChangeClassOneTimeScript},
		{"poc/", ChangeClassOneTimeScript},
		{"revert/", ChangeClassRevert},
	}
	for _, p := range prefixes {
		if strings.HasPrefix(ref, p.prefix) {
			return p.class, strings.TrimSuffix(p.prefix, "/")
		}
	}
	return "", ""
}

// classFromPaths applies the shipped path-glob catalog with a
// majority-of-changed-files heuristic: a class is assigned only when more
// than half of the changed files match its catalog entry.
func classFromPaths(files []diff.FileDiff) string {
	if len(files) == 0 {
		return ""
	}
	counts := make(map[string]int)
	for _, f := range files {
		if class := classifyPath(f.NewName); class != "" {
			counts[class]++
		}
	}
	for class, n := range counts {
		if n*2 > len(files) {
			return class
		}
	}
	return ""
}

// classifyPath maps a single changed file to a catalog class, or "" when the
// path is unremarkable (presumed production code).
func classifyPath(p string) string {
	lower := strings.ToLower(p)
	base := path.Base(lower)
	switch {
	case isGeneratedPath(lower, base):
		return ChangeClassGenerated
	case pathInDir(lower, "migrations") || strings.HasSuffix(lower, ".sql"):
		return ChangeClassMigration
	case strings.Contains(base, "_test.") || pathInDir(lower, "tests"):
		return ChangeClassTest
	case pathInDir(lower, "scripts") || pathInDir(lower, "tools") || pathInDir(lower, "bin"):
		return ChangeClassOneTimeScript
	case pathInDir(lower, "docs") || strings.HasSuffix(lower, ".md"):
		return ChangeClassDocs
	default:
		return ""
	}
}

// isGeneratedPath matches lockfiles and generated-code markers.
func isGeneratedPath(lower, base string) bool {
	switch base {
	case "package-lock.json", "pnpm-lock.yaml", "yarn.lock", "go.sum":
		return true
	}
	return strings.HasSuffix(base, ".pb.go") ||
		strings.HasSuffix(base, "_gen.go") ||
		pathInDir(lower, "dist")
}

// pathInDir reports whether any path segment equals dir (matches dir/** and
// **/dir/** globs).
func pathInDir(lower, dir string) bool {
	return strings.HasPrefix(lower, dir+"/") || strings.Contains(lower, "/"+dir+"/")
}

// titleIsRefactorLike detects refactor/rename/cleanup titles (case-insensitive).
func titleIsRefactorLike(title string) bool {
	t := strings.ToLower(title)
	return strings.Contains(t, "refactor") || strings.Contains(t, "rename") || strings.Contains(t, "cleanup")
}

// changedLines counts added + deleted lines in a single file diff.
func changedLines(f diff.FileDiff) int {
	n := 0
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			if l.Type == diff.LineAdded || l.Type == diff.LineDeleted {
				n++
			}
		}
	}
	return n
}
