package pipeline

import (
	"fmt"
	"sort"
	"strings"
	"time"

	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/internal/store"
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
const maxVerdictWords = 10

func renderVerdictPhrase(headline string) string {
	replacer := strings.NewReplacer(
		"ready to merge", "has no blocking findings",
		"Ready to merge", "Has no blocking findings",
		"cleanly", "",
		"comprehensive", "complete",
		"seamless", "direct",
		"robust", "reliable",
		"—", ".",
	)
	headline = replacer.Replace(headline)
	words := strings.Fields(strings.TrimSpace(headline))
	if len(words) <= maxVerdictWords {
		return strings.Join(words, " ")
	}
	return strings.Join(words[:maxVerdictWords], " ")
}

func renderReviewVerdict(run *PipelineRun) string {
	criticals, warnings, suggestions, _ := findingCounts(run)
	switch {
	case criticals > 0:
		return fmt.Sprintf("**Verdict:** Argus found %d blocking %s. Fix the findings before you merge.", criticals, pluralize("finding", criticals))
	case warnings > 0:
		return fmt.Sprintf("**Verdict:** Argus found %d warning %s. Check the findings before you merge.", warnings, pluralize("finding", warnings))
	case suggestions > 0:
		return "**Verdict:** No blocking findings from Argus. Check the unverified suggestions before you merge."
	default:
		return "**Verdict:** No blocking findings from Argus. Test the behavior before you merge."
	}
}

func renderFindingCounts(run *PipelineRun) string {
	criticals, warnings, suggestions, praise := findingCounts(run)
	files := len(run.Diff.Files)
	return fmt.Sprintf("**Findings:** %d blocking · %d warning · %d suggestion · %d praise · %d %s reviewed",
		criticals, warnings, suggestions, praise, files, pluralize("file", files))
}

func findingCounts(run *PipelineRun) (criticals, warnings, suggestions, praise int) {
	for _, fr := range run.FileReviews {
		for _, c := range fr.Comments {
			if c.Suppressed {
				continue
			}
			switch c.Severity {
			case SeverityCritical:
				criticals++
			case SeverityWarning:
				warnings++
			case SeveritySuggestion:
				suggestions++
			case SeverityPraise:
				praise++
			}
		}
	}
	return criticals, warnings, suggestions, praise
}

func Compose(run *PipelineRun, took time.Duration, dashboardBaseURL, appSlug string) ComposedReview {
	// Rebalance severity: if >50% critical, downgrade lowest-confidence criticals.
	rebalanceSeverity(run.FileReviews)

	// Keep the headline short. The body gives the evidence and acceptance checks.
	headerPrefix := "Argus"
	if run.IsIncremental {
		headerPrefix = "Argus (Incremental)"
	}
	reviewHeader := fmt.Sprintf("## %s · %d/10", headerPrefix, run.Synthesis.Score)
	if headline := renderVerdictPhrase(run.Synthesis.Headline); headline != "" {
		reviewHeader += " — " + headline
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

	// Build the review summary. The order supports a quick merge check:
	// verdict, counts, purpose, author checks, findings, and optional details.
	var summaryBody strings.Builder
	summaryBody.WriteString(reviewHeader)
	summaryBody.WriteString(renderReviewVerdict(run))
	summaryBody.WriteString("\n\n")
	summaryBody.WriteString(renderFindingCounts(run))
	if _, _, suggestions, _ := findingCounts(run); suggestions > 0 {
		summaryBody.WriteString("\n\nUnverified: Argus did not compile or run this suggestion.")
	}

	if brief := strings.TrimSpace(run.Synthesis.Brief); brief != "" {
		summaryBody.WriteString("\n\n")
		summaryBody.WriteString(brief)
	}

	// The full contract line moved into the footer. Keep size warnings near the
	// author checks because a truncated review has an important evidence limit.
	if note := run.Contract.UnreviewableNote(); note != "" {
		summaryBody.WriteString("\n\n")
		summaryBody.WriteString(note)
	}
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
		summaryBody.WriteString("\n\n> ⚠️ Argus truncated the review for ")
		for i, f := range run.TruncatedFiles {
			if i > 0 {
				summaryBody.WriteString(", ")
			}
			summaryBody.WriteString(fmt.Sprintf("`%s`", f))
		}
		summaryBody.WriteString(". More findings can exist.\n")
	}

	totalFolded := len(importantFolded) + len(minorFolded)
	totalImportant := len(importantFolded)
	if totalImportant > 10 {
		importantFolded = importantFolded[:10]
	}
	if len(importantFolded) > 0 {
		header := fmt.Sprintf("\n\n### Findings outside the diff (%d", totalImportant)
		if totalImportant > 10 {
			header += ", top 10 shown"
		}
		header += ")\n\n"
		summaryBody.WriteString(header)
		summaryBody.WriteString("_These findings refer to lines outside the diff._\n\n")
		summaryBody.WriteString("Unverified: Argus did not compile or run this suggestion.\n\n")
		summaryBody.WriteString(strings.Join(importantFolded, "\n"))
	}

	totalMinor := len(minorFolded)
	if totalMinor > 10 {
		minorFolded = minorFolded[:10]
	}
	if len(minorFolded) > 0 {
		summaryBody.WriteString("\n\n<details><summary>")
		summaryBody.WriteString(fmt.Sprintf("Additional findings outside the diff (%d)", totalMinor))
		summaryBody.WriteString("</summary>\n\n")
		summaryBody.WriteString("Unverified: Argus did not compile or run this suggestion.\n\n")
		summaryBody.WriteString(strings.Join(minorFolded, "\n"))
		summaryBody.WriteString("\n\n</details>")
	}
	if inlineOverflow > 0 {
		summaryBody.WriteString(fmt.Sprintf("\n\n_The dashboard shows %d more similar %s._",
			inlineOverflow, pluralize("finding", inlineOverflow)))
	}
	if len(run.MinorNotes) > 0 {
		notes := run.MinorNotes
		total := len(notes)
		if total > 15 {
			notes = notes[:15]
		}
		summaryBody.WriteString(fmt.Sprintf("\n\n<details><summary>Minor notes (%d)</summary>\n\n", total))
		summaryBody.WriteString("Unverified: Argus did not compile or run this suggestion.\n\n")
		for _, n := range notes {
			summaryBody.WriteString(fmt.Sprintf("- `%s:L%d` [%s] %s\n", n.Path, n.Line, n.Severity, n.Title))
		}
		summaryBody.WriteString("\n</details>")
	}

	// totalFolded stays part of the returned render counts and the visible
	// findings line. The counts line above is the primary summary for readers.
	_ = totalFolded

	if breakdown := renderTokenBreakdown(&run.Tokens); breakdown != "" {
		summaryBody.WriteString("\n\n")
		summaryBody.WriteString(breakdown)
	}
	if run.BudgetNote != "" {
		summaryBody.WriteString("\n\n> [!NOTE]\n> ")
		summaryBody.WriteString(strings.ReplaceAll(run.BudgetNote, "\n", " "))
	}

	summaryBody.WriteString("\n\n---\n<sub>")
	summaryBody.WriteString(BuildGlassBoxLine(run.Contract, checkedReviewers(run), countSuppressed(run), took))
	summaryBody.WriteString("</sub><br>\n")
	summaryBody.WriteString(fmt.Sprintf(
		"<sub>[Dashboard →](%s/reviews/%s) · "+
			"React 👎 to dismiss · "+
			"Reply to an inline comment or use `@%s help`</sub>",
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

// maxLearnedBuckets caps how many type buckets the footnote names. The sticky
// comment is already dense — a previous fix existed purely to stop one of its
// rows wrapping to three lines — so an unusual run that touched every memory
// type must not be able to grow this into a paragraph.
const maxLearnedBuckets = 4

// RenderLearnedLine renders the one-line "what Argus learned" footnote for the
// posted review, or "" when the review wrote no memory.
//
// It exists because memory writes were completely invisible: indexing failures
// are non-fatal and log at Warn, so an org whose memory had silently stopped
// working saw exactly the same review comment as one whose memory was healthy.
// One line, appended after the footer, is the whole budget — the detail belongs
// on the dashboard, which links from the same footer.
//
// counts must be the tally read back from the memories table AFTER the writes,
// not the intent to write: reporting what was attempted would reintroduce the
// silence this line exists to break.
func RenderLearnedLine(counts []store.LearnedMemoryCount) string {
	parts := make([]string, 0, maxLearnedBuckets)
	for _, c := range counts {
		if c.Count <= 0 {
			continue
		}
		if len(parts) == maxLearnedBuckets {
			parts = append(parts, "…")
			break
		}
		// store.LearnedMemoryLabel is the single noun table, shared with the
		// dashboard panel through the `label` field on the wire, so the comment
		// and the page cannot describe the same review differently.
		parts = append(parts, fmt.Sprintf("%d %s", c.Count, store.LearnedMemoryLabel(c.Type, c.Count)))
	}
	if len(parts) == 0 {
		return ""
	}
	return "<br>\n<sub>🧠 Learned: " + strings.Join(parts, " · ") + "</sub>"
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
		". Split unrelated changes into separate pull requests. Smaller changes are easier to review and revert."
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
	// similar finding (the [SuppressionDowngrade, SuppressionDrop) band) so its
	// severity was lowered. No literal band here — it moved twice already and the
	// comment did not follow. Idiom-matched to
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

// joinModels renders the distinct models behind an aggregated row, in first-
// seen order. Stages fan out (four specialists, N file syntheses, N
// simulations) and an installation can point different stages — or different
// specialists — at different models, so collapsing to a single name would
// misreport which model produced the spend. Blank entries are dropped rather
// than rendered as gaps; more than two names collapse to a count, because the
// table lives inside a PR comment.
func joinModels(models []string) string {
	seen := make(map[string]bool, len(models))
	distinct := make([]string, 0, len(models))
	for _, m := range models {
		if m == "" || seen[m] {
			continue
		}
		seen[m] = true
		distinct = append(distinct, m)
	}
	switch len(distinct) {
	case 0:
		return ""
	case 1, 2:
		return strings.Join(distinct, ", ")
	default:
		return fmt.Sprintf("%s +%d more", distinct[0], len(distinct)-1)
	}
}

// renderTokenBreakdown returns a compact usage summary with a full table.
func renderTokenBreakdown(tu *RunTokenUsage) string {
	if tu == nil || tu.Total.TotalTokens == 0 {
		return ""
	}

	type row struct {
		label  string
		model  string
		tokens int
		cost   float64
	}
	var rows []row
	addRow := func(label string, tokens int, cost float64, model string) {
		if tokens == 0 && cost == 0 {
			return
		}
		if model == "" {
			model = "—"
		}
		rows = append(rows, row{label: label, model: model, tokens: tokens, cost: cost})
	}
	addStage := func(key string, st StageTokens) {
		addRow(StageLabel(key), st.TotalTokens, st.Cost, st.Model)
	}
	sumArray := func(key string, arr []StageTokens) {
		var tokens int
		var cost float64
		models := make([]string, 0, len(arr))
		for _, t := range arr {
			tokens += t.TotalTokens
			cost += t.Cost
			models = append(models, t.Model)
		}
		addRow(StageLabel(key), tokens, cost, joinModels(models))
	}

	addStage("intent", tu.Intent)
	addStage("triage", tu.Triage)
	addStage("enrichment", tu.Enrichment)
	addStage("conventions", tu.Conventions)
	addStage("patterns", tu.Patterns)
	addStage("lead_agent", tu.LeadAgent)
	addStage("graph", tu.Graph)
	sumArray("file_synthesis", tu.FileSynthesis)

	var specialistTokens int
	var specialistCost float64
	var specialistModels []string
	var specialistCount int
	var reviewTokens int
	var reviewCost float64
	var reviewModels []string
	for _, t := range tu.Review {
		if t.Specialist == "" {
			reviewTokens += t.TotalTokens
			reviewCost += t.Cost
			reviewModels = append(reviewModels, t.Model)
			continue
		}
		specialistTokens += t.TotalTokens
		specialistCost += t.Cost
		specialistModels = append(specialistModels, t.Model)
		specialistCount++
	}
	if specialistCount > 0 {
		addRow(fmt.Sprintf("Review specialists (%d)", specialistCount), specialistTokens, specialistCost, joinModels(specialistModels))
	}
	addRow("Review", reviewTokens, reviewCost, joinModels(reviewModels))

	addStage("acceptance", tu.Acceptance)
	addStage("cross_pr", tu.CrossPR)
	sumArray("simulation", tu.Simulation)
	addStage("scoring", tu.Scoring)
	addStage("synthesis", tu.Synthesis)
	addStage("reply", tu.Reply)

	if len(rows) == 0 {
		return ""
	}
	showCost := tu.Total.Cost != 0
	stageCount := len(rows)
	if specialistCount > 1 {
		stageCount += specialistCount - 1
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("<details><summary><sub>usage: %s tokens · %d stages</sub></summary>\n\n",
		formatTokens(tu.Total.TotalTokens), stageCount))
	if showCost {
		sb.WriteString("| Stage | Model | Tokens | Cost |\n")
		sb.WriteString("|---|---|---:|---:|\n")
		for _, r := range rows {
			sb.WriteString(fmt.Sprintf("| %s | `%s` | %s | $%.4f |\n", r.label, r.model, formatTokens(r.tokens), r.cost))
		}
	} else {
		sb.WriteString("| Stage | Model | Tokens |\n")
		sb.WriteString("|---|---|---:|\n")
		for _, r := range rows {
			sb.WriteString(fmt.Sprintf("| %s | `%s` | %s |\n", r.label, r.model, formatTokens(r.tokens)))
		}
	}
	sb.WriteString("\n</details>")
	return sb.String()
}
