package pipeline

import (
	"fmt"
	"sort"
	"strings"
	"time"

	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/internal/util"
)

// ComposedReview is the pure output of Compose: the GitHub-ready review payload
// plus the derived render counts that post() logs and publishes. Every field is
// a deterministic function of the PipelineRun and the injected elapsed duration
// — Compose performs no I/O, holds no DB/GitHub/logger handle, and reads NO
// clock (the caller passes the duration; see Compose). It does mutate run
// exactly as the old inline renderer did: rebalanceSeverity may downgrade the
// lowest-confidence criticals, and nits on blocking files are appended to
// run.MinorNotes.
type ComposedReview struct {
	// GitHub is the payload handed verbatim to ghClient.PostReview.
	GitHub ghpkg.ReviewSubmission
	// Counts are the derived fold/cap/dedup tallies post() logs and publishes.
	Counts RenderCounts
}

// RenderCounts are the derived comment tallies Compose surfaces for post()'s
// observability log and the posted-to-GitHub event. The log keys post() emits
// are unchanged: FoldedImportant→folded_important, FoldedMinor→folded_minor,
// InlineCandidates→total, CapOverflow→overflow, DedupRemoved→dedup_removed.
type RenderCounts struct {
	// FoldedImportant counts critical/warning findings folded into the summary
	// (rendered prominently, not inline), post-cap. Logged as folded_important.
	FoldedImportant int
	// FoldedMinor counts suggestion/praise findings folded into the collapsed
	// summary section, post-cap. Logged as folded_minor.
	FoldedMinor int
	// InlineCandidates is the number of inline-eligible comments before the
	// maxInlineComments cap and post-selection dedup — the "total" the old
	// capping log reported. len(GitHub.Comments) is the final posted count.
	InlineCandidates int
	// CapOverflow is how many inline candidates were dropped by the cap and
	// summarized as "plus N similar" (0 when under the cap).
	CapOverflow int
	// DedupRemoved is how many capped candidates post-selection dedup removed.
	DedupRemoved int
}

// maxInlineComments caps inline comments posted to GitHub (caps, not floors):
// overflow is summarized as "plus N similar"; all findings still persist to the
// dashboard via indexComments. Shared with post()'s observability log.
const maxInlineComments = 10

// Compose renders the full GitHub submission for a completed run: the summary
// body (header, brief, unreviewable/scope/truncation notes, suppressed-by-team
// line, folded out-of-diff findings, minor notes, findings pill, token
// breakdown, glass-box footer) and the inline comment list (severity-then-score
// ordering, 10-cap with "plus N similar" overflow, out-of-diff folding,
// blocking-file nit demotion, post-selection dedup). took is the review's
// elapsed time, injected by the caller (post() passes time.Since(run.CreatedAt))
// so Compose reads no clock and stays deterministic under test. It is pure apart
// from the two run mutations noted on ComposedReview. Callers must have verified
// run.Synthesis is non-nil.
//
// The unconfigured-scoring notice rides in on run.Synthesis.Brief (appended once
// at synthesis); Compose writes Brief verbatim and MUST NOT re-append it.
//
// dashboardBaseURL is the web dashboard origin (cfg.DashboardBaseURL) used for
// the audit and dashboard links in the summary body. appSlug is the GitHub App
// slug (cfg.GitHubAppSlug) used for the @mention in the footer.
func Compose(run *PipelineRun, took time.Duration, dashboardBaseURL, appSlug string) ComposedReview {
	// Rebalance severity: if >50% critical, downgrade lowest-confidence criticals.
	rebalanceSeverity(run.FileReviews)

	// Header format: `## 🔎 Argus · 8/10 — <verdict one-liner>`. One-liner comes
	// from Synthesis.Headline, captured in synthesize() before FormatIntentHeader
	// mutates Brief — using Brief directly here would bleed the intent disclaimer
	// text into the H2.
	headerPrefix := "Argus"
	if run.IsIncremental {
		headerPrefix = "Argus (Incremental)"
	}
	reviewHeader := fmt.Sprintf("## 🔎 %s · %d/10", headerPrefix, run.Synthesis.Score)
	if run.Synthesis.Headline != "" {
		reviewHeader += " — " + run.Synthesis.Headline
	}
	reviewHeader += "\n\n"

	// Build valid-line sets from diff to avoid 422 "line could not be resolved".
	validLines := make(map[string]map[int]bool)
	for _, f := range run.Diff.Files {
		validLines[f.NewName] = f.ValidCommentLines()
	}

	// One-round ordering (anti nits-then-bombshell): when a blocking finding
	// targets a file, that file's nit/fyi findings are demoted to the Minor
	// notes section instead of being posted inline next to the blocker.
	blockingFiles := collectBlockingFiles(run.FileReviews)

	// Split: inline comments (valid lines) go in the review, invalid-line comments
	// are folded into the summary body so everything ships in ONE atomic API call.
	// Critical/warning findings on non-diff lines are shown prominently; others are collapsed.
	var rawInline []rankedComment
	var importantFolded []string // critical/warning — shown prominently
	var minorFolded []string     // suggestion/praise — collapsed
	for _, fr := range run.FileReviews {
		fileValid := validLines[fr.Path]
		for _, c := range fr.Comments {
			if c.Suppressed {
				continue // dismissal-match drop: never posted inline or folded into the summary
			}
			if blockingFiles[fr.Path] && severityRank(c.Severity) <= severityRank(SeveritySuggestion) {
				run.MinorNotes = append(run.MinorNotes, minorNoteFrom(fr.Path, c))
				continue
			}
			if fileValid == nil || !fileValid[c.Line] {
				title := c.What
				if title == "" {
					title = util.Truncate(c.Body, 100, true)
				}
				emoji := severityEmoji(c.Severity)
				entry := fmt.Sprintf("- %s `%s:L%d` **[%s]** %s", emoji, fr.Path, c.Line, c.Severity, title)
				if c.Why != "" {
					entry += fmt.Sprintf("\n  > %s", util.Truncate(c.Why, 200, true))
				}
				if c.Severity == SeverityCritical || c.Severity == SeverityWarning {
					importantFolded = append(importantFolded, entry)
				} else {
					minorFolded = append(minorFolded, entry)
				}
				continue
			}
			startLine := c.StartLine
			if startLine > 0 && !fileValid[startLine] {
				startLine = 0
			}
			rawInline = append(rawInline, rankedComment{
				comment: ghpkg.ReviewComment{
					Path:      fr.Path,
					Body:      formatCommentBody(c),
					Line:      c.Line,
					StartLine: startLine,
					Side:      "RIGHT",
				},
				severity: c.Severity,
				score:    c.Score,
			})
		}
	}

	// Severity-first ordering, ALWAYS — blocking findings lead the round even
	// when under the cap (one-round ordering, anti nits-then-bombshell).
	sort.SliceStable(rawInline, func(i, j int) bool {
		ri, rj := severityRank(rawInline[i].severity), severityRank(rawInline[j].severity)
		if ri != rj {
			return ri > rj
		}
		return rawInline[i].score > rawInline[j].score
	})

	// Cap inline comments at maxInlineComments (caps, not floors). Overflow is
	// summarized as "plus N similar"; all findings are persisted to the
	// dashboard via indexComments (pre-post).
	inlineCandidates := len(rawInline)
	inlineOverflow := 0
	if len(rawInline) > maxInlineComments {
		inlineOverflow = len(rawInline) - maxInlineComments
		rawInline = rawInline[:maxInlineComments]
	}

	// Post-selection dedup: remove near-identical comments in the final selection.
	beforePostDedup := len(rawInline)
	rawInline = postSelectionDedup(rawInline)
	dedupRemoved := beforePostDedup - len(rawInline)

	inlineComments := make([]ghpkg.ReviewComment, len(rawInline))
	for i, rc := range rawInline {
		inlineComments[i] = rc.comment
	}

	// Build summary: header + brief + scope warning + affected-file findings + score.
	var summaryBody strings.Builder
	summaryBody.WriteString(reviewHeader)
	summaryBody.WriteString(run.Synthesis.Brief)
	// The full contract line moved into the Glass Box footer below; only the
	// unreviewable-size warning stays up top where it can't be missed.
	if note := run.Contract.UnreviewableNote(); note != "" {
		summaryBody.WriteString("\n\n")
		summaryBody.WriteString(note)
	}
	// Team-feedback suppression audit line: one line, never per-finding noise.
	if n := countSuppressedFindings(run.FileReviews); n > 0 {
		summaryBody.WriteString(fmt.Sprintf(
			"\n\n_%d %s suppressed by team feedback ([audit](%s/reviews/%s))_",
			n, pluralize("finding", n), dashboardBaseURL, run.ReviewID.String()))
	}
	if scopeNote := assessPRScope(run); scopeNote != "" {
		summaryBody.WriteString("\n\n")
		summaryBody.WriteString(scopeNote)
	}
	if len(run.TruncatedFiles) > 0 {
		summaryBody.WriteString("\n\n> ⚠️ Review for ")
		for i, f := range run.TruncatedFiles {
			if i > 0 {
				summaryBody.WriteString(", ")
			}
			summaryBody.WriteString(fmt.Sprintf("`%s`", f))
		}
		summaryBody.WriteString(" was truncated — additional findings may exist.\n")
	}
	// Unconfigured-scoring notice: appended once at synthesis (it rides in on
	// run.Synthesis.Brief above) — do NOT append again here.
	totalFolded := len(importantFolded) + len(minorFolded)
	// Important findings (critical/warning) shown visibly — cap at 10 to prevent summary bloat.
	totalImportant := len(importantFolded)
	if totalImportant > 10 {
		importantFolded = importantFolded[:10]
	}
	if len(importantFolded) > 0 {
		header := fmt.Sprintf("\n\n### Affected code outside the diff (%d", totalImportant)
		if totalImportant > 10 {
			header += ", showing top 10"
		}
		header += ")\n\n"
		summaryBody.WriteString(header)
		summaryBody.WriteString("_These findings are on lines not in the diff but may be impacted by this change._\n\n")
		summaryBody.WriteString(strings.Join(importantFolded, "\n"))
	}
	// Minor findings (suggestion/praise) collapsed — cap at 10.
	totalMinor := len(minorFolded)
	if totalMinor > 10 {
		minorFolded = minorFolded[:10]
	}
	if len(minorFolded) > 0 {
		summaryBody.WriteString("\n\n<details><summary>")
		summaryBody.WriteString(fmt.Sprintf("%d additional findings on lines outside the diff", len(minorFolded)))
		summaryBody.WriteString("</summary>\n\n")
		summaryBody.WriteString(strings.Join(minorFolded, "\n"))
		summaryBody.WriteString("\n\n</details>")
	}
	// Overflow beyond the inline cap: one line, not more comments.
	if inlineOverflow > 0 {
		summaryBody.WriteString(fmt.Sprintf("\n\n_…plus %d similar %s not shown inline — the full list is on the dashboard._",
			inlineOverflow, pluralize("finding", inlineOverflow)))
	}
	// Minor notes: near-miss findings from scoring plus nits demoted from files
	// carrying a blocking finding. Collapsed, capped, never inline.
	if len(run.MinorNotes) > 0 {
		notes := run.MinorNotes
		total := len(notes)
		if total > 15 {
			notes = notes[:15]
		}
		summaryBody.WriteString(fmt.Sprintf("\n\n<details><summary>Minor notes (%d)</summary>\n\n", total))
		summaryBody.WriteString("_Low-confidence or minor observations — safe to ignore._\n\n")
		for _, n := range notes {
			summaryBody.WriteString(fmt.Sprintf("- `%s:L%d` [%s] %s\n", n.Path, n.Line, n.Severity, n.Title))
		}
		summaryBody.WriteString("\n</details>")
	}
	// Findings pill: single scan-line showing totals and inline/folded split.
	// Omitted when there are no findings at all — keeps the summary terse on
	// clean PRs.
	totalFindings := countComments(run)
	if totalFindings > 0 {
		summaryBody.WriteString(fmt.Sprintf("\n\n**%d %s** · %d inline · %d folded",
			totalFindings, pluralize("finding", totalFindings),
			len(inlineComments), totalFolded))
	}

	// Token/cost breakdown per stage and per specialist (collapsible).
	if breakdown := renderTokenBreakdown(&run.Tokens); breakdown != "" {
		summaryBody.WriteString("\n\n")
		summaryBody.WriteString(breakdown)
	}

	// Footer: Glass Box line (contract/depth, reviewers, suppression count,
	// duration) + single dashboard link + engagement tips in <sub> blocks.
	summaryBody.WriteString("\n\n---\n<sub>")
	summaryBody.WriteString(BuildGlassBoxLine(run.Contract, checkedReviewers(run), countSuppressed(run), took))
	summaryBody.WriteString("</sub><br>\n")
	summaryBody.WriteString(fmt.Sprintf(
		"<sub>[Dashboard →](%s/reviews/%s) · "+
			"React 👎 to dismiss · "+
			"Reply to any inline comment or use `@%s help` to chat</sub>",
		dashboardBaseURL, run.ReviewID.String(), appSlug))

	return ComposedReview{
		GitHub: ghpkg.ReviewSubmission{
			Summary:  summaryBody.String(),
			HeadSHA:  run.PREvent.HeadSHA,
			Comments: inlineComments,
		},
		Counts: RenderCounts{
			FoldedImportant:  len(importantFolded),
			FoldedMinor:      len(minorFolded),
			InlineCandidates: inlineCandidates,
			CapOverflow:      inlineOverflow,
			DedupRemoved:     dedupRemoved,
		},
	}
}

type rankedComment struct {
	comment  ghpkg.ReviewComment
	severity Severity
	score    int
}

// collectBlockingFiles returns the set of file paths carrying at least one
// non-suppressed blocking (critical) finding. Used by post() to demote nit/fyi
// findings on those files to Minor notes (one-round ordering).
func collectBlockingFiles(reviews []FileReview) map[string]bool {
	out := make(map[string]bool)
	for _, fr := range reviews {
		for _, c := range fr.Comments {
			if !c.Suppressed && c.Severity == SeverityCritical {
				out[fr.Path] = true
				break
			}
		}
	}
	return out
}

// assessPRScope flags PRs that bundle too many features or touch too many
// unrelated areas. Returns a markdown warning block to include in the review
// summary, or "" when scope looks reasonable.
//
// Heuristics (intentionally simple):
//   - >= 25 files changed  → "large PR"
//   - >= 5 distinct top-level directories → "multi-area PR"
//
// Both trigger a soft warning — the review still proceeds. The goal is to
// nudge authors toward focused PRs without blocking legitimate large changes
// (e.g., generated code, repo-wide refactors). If we need per-repo overrides
// later, surface these as `repoSettings` fields.
func assessPRScope(run *PipelineRun) string {
	if run == nil || run.Diff == nil {
		return ""
	}
	if len(run.Diff.Files) == 0 {
		return ""
	}
	const (
		largeFileThreshold = 25
		multiAreaThreshold = 5
	)
	fileCount := len(run.Diff.Files)

	topDirs := make(map[string]struct{})
	for _, f := range run.Diff.Files {
		// Local name `p` to avoid shadowing the imported "path" package.
		p := f.NewName
		if p == "" {
			p = f.OldName
		}
		if i := strings.Index(p, "/"); i > 0 {
			topDirs[p[:i]] = struct{}{}
		}
	}

	tooManyFiles := fileCount >= largeFileThreshold
	multiArea := len(topDirs) >= multiAreaThreshold
	if !tooManyFiles && !multiArea {
		return ""
	}

	dirs := make([]string, 0, len(topDirs))
	for d := range topDirs {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)

	var reasons []string
	if tooManyFiles {
		reasons = append(reasons, fmt.Sprintf("changes **%d files**", fileCount))
	}
	if multiArea {
		reasons = append(reasons, fmt.Sprintf("touches **%d top-level areas** (`%s`)", len(topDirs), strings.Join(dirs, "`, `")))
	}
	return "> ⚠️ **Scope concern:** This PR " + strings.Join(reasons, " and ") +
		". Consider splitting into focused PRs — bundled changes are harder to review, harder to revert, and can mask regressions in unrelated areas."
}

// formatCommentBody builds the GitHub review comment body.
// Structure: emoji severity + category title, then why (impact), then fix.
func formatCommentBody(c FileComment) string {
	emoji := severityEmoji(c.Severity)
	prio := priorityLabel(c.Severity)
	conf := confidenceScore(c)

	var header string
	if prio != "" {
		header = fmt.Sprintf("%s **%s (%d/10) · %s:** %s", emoji, prio, conf, capitalizeCategory(string(c.Category)), commentTitle(c))
	} else {
		// Praise — no priority label
		header = fmt.Sprintf("%s **%s:** %s", emoji, capitalizeCategory(string(c.Category)), commentTitle(c))
	}

	var body string
	if c.Why != "" {
		body = header + "\n\n" + c.Why
	} else if c.Body != "" {
		body = header + "\n\n" + c.Body
	} else {
		body = header
	}

	if c.Suggestion != "" {
		body += "\n\n```suggestion\n" + strings.TrimRight(c.Suggestion, "\n") + "\n```"
	}

	if tag := renderMemoryTag(c); tag != "" {
		body += "\n\n" + tag
	}

	// Dismissal-downgrade attribution: the finding matched a previously-dismissed
	// similar finding (0.60–0.85) so its severity was lowered. Idiom-matched to
	// renderMemoryTag (italic, em-dash lead-in).
	if c.DismissedDowngrade {
		note := "_— Previously dismissed a similar finding"
		if c.DismissedMatchPR > 0 {
			note += fmt.Sprintf(" (PR #%d)", c.DismissedMatchPR)
		}
		note += "._"
		body += "\n\n" + note
	}

	if c.Severity == SeverityCritical || c.Severity == SeverityWarning {
		body += "\n\n---\n<sub>React 👎 to dismiss · Argus learns from feedback</sub>"
	}

	// Wrap medium-confidence findings in collapsible details
	if c.Confidence == "medium" {
		inner := strings.TrimPrefix(body, header)
		inner = strings.TrimPrefix(inner, "\n\n")
		body = fmt.Sprintf("<details><summary>%s (medium confidence)</summary>\n\n%s\n</details>", header, inner)
	}

	return body
}

// postSelectionDedup removes near-duplicate comments from the final selection.
// Uses token-set Jaccard similarity on the comment body. If overlap > 0.8, drops the lower-scored one.
func postSelectionDedup(selected []rankedComment) []rankedComment {
	if len(selected) <= 1 {
		return selected
	}
	// Tokenize each comment body
	tokenSets := make([]map[string]bool, len(selected))
	for i, rc := range selected {
		tokens := tokenize(rc.comment.Body)
		set := make(map[string]bool, len(tokens))
		for _, t := range tokens {
			set[t] = true
		}
		tokenSets[i] = set
	}
	// Mark duplicates via Jaccard similarity
	drop := make(map[int]bool)
	for i := 0; i < len(selected); i++ {
		if drop[i] {
			continue
		}
		for j := i + 1; j < len(selected); j++ {
			if drop[j] {
				continue
			}
			if jaccardSimilarity(tokenSets[i], tokenSets[j]) > 0.8 {
				// Use severity-first tiebreaker, consistent with upstream sort
				si, sj := severityRank(selected[i].severity), severityRank(selected[j].severity)
				if si > sj || (si == sj && selected[i].score >= selected[j].score) {
					drop[j] = true
				} else {
					drop[i] = true
					break
				}
			}
		}
	}
	if len(drop) == 0 {
		return selected
	}
	result := make([]rankedComment, 0, len(selected)-len(drop))
	for i, rc := range selected {
		if !drop[i] {
			result = append(result, rc)
		}
	}
	return result
}

// renderTokenBreakdown returns a collapsible markdown block showing token and
// cost consumption per pipeline stage, with the review stage broken down by
// specialist (correctness, security, architecture, regression). Returns ""
// when no tokens were consumed (e.g., skipped review, cancelled early).
//
// Format: `<details>` block so it doesn't dominate the review summary unless
// the reader wants to inspect costs.
func renderTokenBreakdown(tu *RunTokenUsage) string {
	if tu == nil || tu.Total.TotalTokens == 0 {
		return ""
	}

	// Aggregate review-stage tokens per specialist. Empty specialist means a
	// skim single-pass (no deep review); we bucket those under "review".
	type agg struct {
		tokens int
		cost   float64
	}
	bySpecialist := make(map[string]*agg)
	for _, t := range tu.Review {
		key := t.Specialist
		if key == "" {
			key = "review"
		}
		a, ok := bySpecialist[key]
		if !ok {
			a = &agg{}
			bySpecialist[key] = a
		}
		a.tokens += t.TotalTokens
		a.cost += t.Cost
	}
	// Build final specialist render order: canonical first (in SpecialistOrder),
	// then any unknown keys appended (future-proof for new specialists shipped
	// without a labels.go update). Dropping duplicates via a seen-set.
	specialistOrder := make([]string, 0, len(bySpecialist))
	seen := make(map[string]bool, len(bySpecialist))
	for _, key := range SpecialistOrder {
		if _, ok := bySpecialist[key]; ok {
			specialistOrder = append(specialistOrder, key)
			seen[key] = true
		}
	}
	for key := range bySpecialist {
		if !seen[key] {
			specialistOrder = append(specialistOrder, key)
		}
	}

	var rows []string
	addRow := func(label string, tokens int, cost float64) {
		// Gate on tokens AND cost: some providers (gpt-5.x reasoning path,
		// see commit 1070dac) return cost without token counts, so a bare
		// `tokens == 0` guard would drop real spend AND leave the header
		// "Total" misaligned with the sum of visible rows.
		if tokens == 0 && cost == 0 {
			return
		}
		rows = append(rows, fmt.Sprintf("| %s | %s | $%.4f |", label, formatTokens(tokens), cost))
	}
	addStage := func(key string, st StageTokens) {
		addRow(StageLabel(key), st.TotalTokens, st.Cost)
	}
	// sumArray collapses an array-valued stage (file_synthesis, simulation)
	// into a single row — PR comment stays curated; per-entry rows live on
	// the web dashboard's review detail page.
	sumArray := func(key string, arr []StageTokens) {
		var tokens int
		var cost float64
		for _, t := range arr {
			tokens += t.TotalTokens
			cost += t.Cost
		}
		addRow(StageLabel(key), tokens, cost)
	}

	// Non-review stages rendered in StageOrder sequence so the PR comment
	// table matches the dashboard's stage ordering. `graph` runs early in
	// the pipeline (before review), not at the end.
	addStage("intent", tu.Intent)
	addStage("triage", tu.Triage)
	addStage("enrichment", tu.Enrichment)
	addStage("conventions", tu.Conventions)
	addStage("patterns", tu.Patterns)
	addStage("lead_agent", tu.LeadAgent)
	addStage("graph", tu.Graph)
	sumArray("file_synthesis", tu.FileSynthesis)

	// Review stage — per-specialist sub-rows. StageLabel handles the
	// "review.review" suppression (skim fallback renders as plain "Review").
	for _, key := range specialistOrder {
		a := bySpecialist[key]
		addRow(StageLabel("review."+key), a.tokens, a.cost)
	}

	addStage("acceptance", tu.Acceptance)
	addStage("cross_pr", tu.CrossPR)
	sumArray("simulation", tu.Simulation)
	addStage("scoring", tu.Scoring)
	addStage("synthesis", tu.Synthesis)
	addStage("reply", tu.Reply)

	if len(rows) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("<details><summary><sub>🔢 %s tokens · $%.4f total</sub></summary>\n\n",
		formatTokens(tu.Total.TotalTokens), tu.Total.Cost))
	sb.WriteString("| Stage | Tokens | Cost |\n")
	sb.WriteString("|---|---:|---:|\n")
	for _, r := range rows {
		sb.WriteString(r)
		sb.WriteString("\n")
	}
	sb.WriteString("\n</details>")
	return sb.String()
}
