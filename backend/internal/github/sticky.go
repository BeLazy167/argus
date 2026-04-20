// Package github — sticky.go provides marker-bounded section editing for
// Argus's primary PR-review comment.
//
// The "sticky" resource is a GitHub PR Review (created via
// PullRequests.CreateReview and stored in reviews.github_review_id as a
// *int64). Async stages — cross-PR coverage, issue acceptance refresh, etc.
// — upsert their output into named sections of that review body without
// disturbing content from other stages or from the original synthesis.
//
// Sections are delimited by HTML comment markers:
//
//	<!-- argus:{section}:start -->
//	...inner markdown, including a footer timestamp...
//	<!-- argus:{section}:end -->
//
// The pure string logic lives in replaceOrAppendSection so it can be
// table-tested without touching the GitHub API.
package github

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrStickyNotFound indicates the referenced review no longer exists on
// GitHub (404). The caller should log at Warn level and skip; the next
// refresh cycle will find it again only if someone re-creates it, which
// we don't attempt.
var ErrStickyNotFound = errors.New("sticky review not found")

// ErrMarkersCorrupt indicates exactly one of the start/end markers was
// present in the body — a torn write from a previous run or manual edit.
// We refuse to "recover" because either branch (truncate-to-start,
// append-missing-end) risks eating user content.
var ErrMarkersCorrupt = errors.New("sticky section markers corrupt (only one of start/end present)")

// stickyFooterLayout is the Updated-at footer appended inside every section.
// The format must match updatedAtFooterRegex-equivalent stripping in
// stripFooter, so idempotence check ignores timestamp churn.
const stickyFooterLayout = "_Updated at 15:04 UTC on 2006-01-02_"

// stickyFooterPrefix is the invariant prefix that identifies a footer line
// for stripping before the idempotence diff. Kept as a package const so
// tests don't drift from production.
const stickyFooterPrefix = "_Updated at "

// UpdateStickySection replaces or inserts a named section inside an
// existing review body, bounded by HTML comment markers. See package doc.
//
// Idempotent on sectionMD: if the inner content (modulo the Updated-at
// footer line) matches what's already posted, the GitHub API is not
// called. This guards against cron-driven refresh storms polluting the
// review history with timestamp-only diffs.
//
// Markers absent → the new section is appended after existing content
// with one blank line separator.
// One marker present (corrupt) → returns ErrMarkersCorrupt.
// Review 404 → returns ErrStickyNotFound.
//
// Parameters:
//   - stickyReviewID: the int64 from reviews.github_review_id.
//   - section: marker name, e.g. "crosspr". Must be [a-z][a-z0-9_-]*.
//   - sectionMD: inner markdown for the section, WITHOUT markers or footer.
//
// Raw GitHub errors are wrapped with %w plus the review id + section for
// diagnosability.
func (c *Client) UpdateStickySection(
	ctx context.Context,
	installationID int64,
	owner, repo string,
	prNumber int,
	stickyReviewID int64,
	section string,
	sectionMD string,
) error {
	if stickyReviewID <= 0 {
		return fmt.Errorf("sticky review id is zero or negative (section=%s): %w",
			section, ErrStickyNotFound)
	}
	if !isValidSectionName(section) {
		return fmt.Errorf("invalid section name %q: must match [a-z][a-z0-9_-]*", section)
	}

	client, err := c.app.ClientForInstallation(installationID)
	if err != nil {
		return fmt.Errorf("sticky client for installation %d: %w", installationID, err)
	}

	// Fetch existing body.
	if err := c.restLimiter.Wait(ctx); err != nil {
		return fmt.Errorf("sticky rate limit wait: %w", err)
	}
	review, resp, err := client.PullRequests.GetReview(ctx, owner, repo, prNumber, stickyReviewID)
	if err != nil {
		if resp != nil && resp.StatusCode == 404 {
			return fmt.Errorf("sticky review %d in %s/%s#%d section %s: %w",
				stickyReviewID, owner, repo, prNumber, section, ErrStickyNotFound)
		}
		return fmt.Errorf("fetching sticky review %d (%s/%s#%d section=%s): %w",
			stickyReviewID, owner, repo, prNumber, section, err)
	}

	existingBody := review.GetBody()
	footer := time.Now().UTC().Format(stickyFooterLayout)
	newBody, changed, err := replaceOrAppendSection(existingBody, section, sectionMD, footer)
	if err != nil {
		return fmt.Errorf("replace section %s in review %d: %w", section, stickyReviewID, err)
	}
	if !changed {
		// Inner content stable; skip the PATCH to avoid timestamp-only churn
		// in the review's edit history.
		return nil
	}

	if err := c.restLimiter.Wait(ctx); err != nil {
		return fmt.Errorf("sticky rate limit wait (update): %w", err)
	}
	if _, _, err := client.PullRequests.UpdateReview(ctx, owner, repo, prNumber, stickyReviewID, newBody); err != nil {
		return fmt.Errorf("updating sticky review %d (%s/%s#%d section=%s): %w",
			stickyReviewID, owner, repo, prNumber, section, err)
	}
	return nil
}

// replaceOrAppendSection is the pure string core of UpdateStickySection,
// exported within the package for table tests. Returns (newBody, changed)
// where changed=false means the inner content (sans footer) is already
// what's there — caller can skip the API call.
//
// sectionMD is the inner markdown WITHOUT markers. footer is a single
// pre-formatted line (no trailing newline) appended inside the section
// below sectionMD.
func replaceOrAppendSection(body, section, sectionMD, footer string) (string, bool, error) {
	startMarker := stickyStartMarker(section)
	endMarker := stickyEndMarker(section)

	// Duplicate-marker guard: if the user-authored body ever contains a
	// pasted Argus section (copy from another review, a template, etc.)
	// extractInner would use the first occurrence and replace user content.
	// Treating duplicate markers as corrupt forces a human look rather
	// than silently clobbering.
	startCount := strings.Count(body, startMarker)
	endCount := strings.Count(body, endMarker)
	if startCount > 1 || endCount > 1 {
		return "", false, ErrMarkersCorrupt
	}

	hasStart := startCount == 1
	hasEnd := endCount == 1

	// Build the fresh inner block: sectionMD + blank line + footer.
	innerNew := buildInner(sectionMD, footer)
	blockNew := startMarker + "\n" + innerNew + "\n" + endMarker

	switch {
	case hasStart && hasEnd:
		// Replace between markers.
		startIdx := strings.Index(body, startMarker)
		endIdx := strings.Index(body, endMarker) + len(endMarker)
		if startIdx > endIdx-len(endMarker) {
			// End appears before start — treat as corrupt.
			return "", false, ErrMarkersCorrupt
		}
		existingBlock := body[startIdx:endIdx]
		existingInner := extractInner(existingBlock, startMarker, endMarker)
		if stripFooter(existingInner) == stripFooter(innerNew) {
			// Content identical ignoring footer timestamp — no-op.
			return body, false, nil
		}
		return body[:startIdx] + blockNew + body[endIdx:], true, nil

	case hasStart != hasEnd:
		return "", false, ErrMarkersCorrupt

	default:
		// Neither marker — append with separator.
		trimmed := strings.TrimRight(body, "\n")
		if trimmed == "" {
			return blockNew, true, nil
		}
		return trimmed + "\n\n" + blockNew, true, nil
	}
}

// buildInner joins the section markdown with its footer separated by a
// blank line. sectionMD is trimmed of trailing whitespace to keep output
// canonical.
func buildInner(sectionMD, footer string) string {
	s := strings.TrimRight(sectionMD, "\n\t ")
	if s == "" {
		return footer
	}
	return s + "\n\n" + footer
}

// extractInner returns the content between the two markers, excluding
// the markers themselves and the single surrounding newlines we add in
// blockNew. Lenient about the newline layout so it also matches blocks
// written by hand.
func extractInner(block, startMarker, endMarker string) string {
	inner := strings.TrimPrefix(block, startMarker)
	inner = strings.TrimSuffix(inner, endMarker)
	return strings.Trim(inner, "\n")
}

// stripFooter returns inner with any trailing footer line removed. Only
// strips one footer — we never emit more than one.
func stripFooter(inner string) string {
	trimmed := strings.TrimRight(inner, "\n\t ")
	nl := strings.LastIndex(trimmed, "\n")
	lastLine := trimmed
	if nl >= 0 {
		lastLine = trimmed[nl+1:]
	}
	if strings.HasPrefix(lastLine, stickyFooterPrefix) {
		if nl < 0 {
			return ""
		}
		return strings.TrimRight(trimmed[:nl], "\n\t ")
	}
	return trimmed
}

// isValidSectionName keeps marker comments predictable and safe for
// literal-string search. We refuse spaces, quotes, or `-->` fragments
// because a mis-named section would corrupt the marker itself.
func isValidSectionName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
			if i == 0 {
				return false
			}
		case r == '-' || r == '_':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func stickyStartMarker(section string) string {
	return "<!-- argus:" + section + ":start -->"
}

func stickyEndMarker(section string) string {
	return "<!-- argus:" + section + ":end -->"
}
