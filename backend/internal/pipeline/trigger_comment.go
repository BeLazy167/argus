// Package pipeline: trigger_comment.go renders the "Trigger review" checkbox
// comment posted to a PR when auto_run is disabled, and provides helpers for
// detecting its markers and checkbox-state transitions on edited webhooks.
package pipeline

import (
	"fmt"
	"strings"
)

// TriggerMarker is an invisible HTML-comment marker embedded in Argus-authored
// trigger comments. It is used both to identify comments that Argus wrote (so
// we don't interpret unrelated edits) and as a version tag so future schema
// changes can coexist.
const TriggerMarker = "<!-- argus-trigger-v1 -->"

// TriggerCheckboxUnchecked is the exact task-list line emitted when posting a
// fresh trigger comment — an empty checkbox `- [ ]` that a reviewer can click.
const TriggerCheckboxUnchecked = "- [ ] Trigger Argus review"

// TriggerCheckboxChecked is the post-click form of the line. The `- [x]` form
// (lowercase x, matching GitHub's task-list encoding) is what GitHub writes
// into the comment body when a user toggles the box.
const TriggerCheckboxChecked = "- [x] Trigger Argus review"

// TriggerCheckboxRunning replaces the checkbox line after the review has been
// dispatched, giving users visual feedback that the click was acknowledged.
const TriggerCheckboxRunning = "_Running Argus review..._"

// TriggerCheckboxFailed replaces the running marker after a pipeline failure,
// pairing with a fresh unchecked checkbox so the user can retry by toggling.
const TriggerCheckboxFailed = "_Previous run failed — retry by ticking the box above._"

// TriggerEstimate holds the cost/size figures rendered in the trigger comment.
//
// DiffLines is the sum of additions + deletions across changed files. When the
// live ListFiles call fails (network, rate limit) the caller sets DiffLines to
// 0 and the renderer omits the "diff size" line gracefully.
//
// AvgCostUSD is a pointer so nil means "historical cost unknown" — e.g., OSS
// providers or reviews where cost wasn't recorded. In that case the renderer
// shows tokens only.
type TriggerEstimate struct {
	Files      int      // from webhook payload
	DiffLines  int      // live: sum of additions+deletions
	AvgTokens  int64    // averaged from last N completed reviews in this repo
	AvgCostUSD *float64 // nil => cost unknown/unavailable
	SampleSize int      // how many historical reviews backed the estimate
}

// BuildTriggerComment renders the Markdown body for a fresh trigger comment on
// a PR. The marker line is placed first so IsArgusTriggerBody can detect it
// with a cheap prefix/substring scan.
//
// The output is deterministic for a given TriggerEstimate so tests can assert
// on exact bodies.
func BuildTriggerComment(est TriggerEstimate, appSlug string) string {
	var b strings.Builder
	b.WriteString(TriggerMarker)
	b.WriteString("\n\n")
	b.WriteString("### Argus review\n\n")
	b.WriteString("Auto-review is off for this repo. Tick the box below to run a review on this PR.\n\n")
	b.WriteString(TriggerCheckboxUnchecked)
	b.WriteString("\n\n")

	if est.Files > 0 || est.DiffLines > 0 || est.SampleSize > 0 {
		b.WriteString("**Estimated cost**\n")
		if est.Files > 0 {
			fmt.Fprintf(&b, "- Files changed: %d\n", est.Files)
		}
		if est.DiffLines > 0 {
			fmt.Fprintf(&b, "- Diff lines (±): %d\n", est.DiffLines)
		}
		if est.SampleSize > 0 && est.AvgTokens > 0 {
			if est.AvgCostUSD != nil {
				fmt.Fprintf(
					&b,
					"- Historical avg: ~%s tokens · ~$%.2f · across last %d review(s)\n",
					humanizeTokens(est.AvgTokens), *est.AvgCostUSD, est.SampleSize,
				)
			} else {
				fmt.Fprintf(
					&b,
					"- Historical avg: ~%s tokens · across last %d review(s)\n",
					humanizeTokens(est.AvgTokens), est.SampleSize,
				)
			}
		}
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "_Tip: you can also comment `@%s review` at any time._\n", appSlug)
	return b.String()
}

// ReplaceTriggerWithRunning swaps the unchecked/checked checkbox line in a
// trigger comment body for a "running" marker, giving immediate visual feedback
// once a review has been dispatched. Non-trigger bodies are returned unchanged.
//
// Also strips any stale TriggerCheckboxFailed line left over from a previous
// failure cycle — otherwise every retry would accumulate another "Previous
// run failed..." hint in the body.
func ReplaceTriggerWithRunning(body string) string {
	if !IsArgusTriggerBody(body) {
		return body
	}
	body = strings.Replace(body, "\n"+TriggerCheckboxFailed, "", 1)
	body = strings.Replace(body, TriggerCheckboxFailed, "", 1)
	body = strings.Replace(body, TriggerCheckboxChecked, TriggerCheckboxRunning, 1)
	body = strings.Replace(body, TriggerCheckboxUnchecked, TriggerCheckboxRunning, 1)
	return body
}

// RestoreTriggerAfterFailure replaces the "Running..." marker with a fresh
// unchecked checkbox and a failure note, so a user can retry by toggling again
// without us re-posting a second trigger comment. Non-trigger or non-running
// bodies are returned unchanged.
func RestoreTriggerAfterFailure(body string) string {
	if !IsArgusTriggerBody(body) {
		return body
	}
	if !strings.Contains(body, TriggerCheckboxRunning) {
		return body
	}
	replacement := TriggerCheckboxUnchecked + "\n" + TriggerCheckboxFailed
	return strings.Replace(body, TriggerCheckboxRunning, replacement, 1)
}

// IsArgusTriggerBody reports whether the given comment body was authored by
// Argus as a trigger comment. Detection is by substring match on TriggerMarker.
func IsArgusTriggerBody(body string) bool {
	return strings.Contains(body, TriggerMarker)
}

// CheckboxToggled reports whether an edit transitioned the Argus trigger
// checkbox from unchecked (`- [ ]`) to checked (`- [x]`). It returns false for:
//   - edits that don't change the checkbox state
//   - checked → unchecked transitions (user unchecking does not re-trigger)
//   - bodies that aren't Argus trigger comments
//
// The pair (before, after) comes from GitHub's issue_comment.edited payload,
// where `before` is payload.changes.body.from and `after` is the current body.
func CheckboxToggled(before, after string) bool {
	if !IsArgusTriggerBody(after) {
		return false
	}
	beforeChecked := strings.Contains(before, TriggerCheckboxChecked)
	afterChecked := strings.Contains(after, TriggerCheckboxChecked)
	return !beforeChecked && afterChecked
}

// humanizeTokens renders integer token counts in a compact form suitable for
// inline Markdown: 1_234 -> "1.2k", 125_000 -> "125k", 1_500_000 -> "1.5M".
func humanizeTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}
