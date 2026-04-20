// Package pipeline — crosspr.go hosts the cross-repo PR helper utilities
// (PR-link hydration, coverage section formatting, error summarization).
// The main execution path now lives in crosspr_stage.go as an async stage;
// this file retains only helpers still used by that stage and by synthesis.
package pipeline

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/BeLazy167/argus/backend/internal/util"
)

// hydratePRLink tries to fetch the linked PR's metadata + diff. Inaccessible
// PRs come back with Accessible=false and a FetchError string; accessible
// PRs have Title, HeadSHA, and Diff populated.
//
// Routes through Orchestrator.crossPRGithubDep so integration tests can
// inject canned PR metadata / diff without real GitHub calls.
func hydratePRLink(ctx context.Context, o *Orchestrator, run *PipelineRun, link PRLink) PRLink {
	fetchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	gh := o.crossPRGithubDep()

	// Try to fetch the PR metadata first.
	pr, err := gh.GetPullRequest(fetchCtx, run.PREvent.InstallationID, link.Owner, link.Repo, link.Number)
	if err != nil {
		link.Accessible = false
		link.FetchError = fmt.Sprintf("PR metadata: %s", summarizeErr(err))
		return link
	}
	link.Title = pr.PRTitle
	link.HeadSHA = pr.HeadSHA

	// Then the unified diff.
	diffText, err := gh.GetPRDiff(fetchCtx, run.PREvent.InstallationID, link.Owner, link.Repo, link.Number)
	if err != nil {
		link.Accessible = false
		link.FetchError = fmt.Sprintf("PR diff: %s", summarizeErr(err))
		return link
	}
	link.Diff = diffText
	link.Accessible = true
	return link
}

// summarizeErr produces a short reason string suitable for user display.
// Distinguishes "no access" (Argus not installed) from generic errors.
func summarizeErr(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "404"), strings.Contains(lower, "not found"):
		return "not found (Argus may not be installed on this repo)"
	case strings.Contains(lower, "403"), strings.Contains(lower, "forbidden"):
		return "access denied (Argus may not be installed on this repo)"
	default:
		return util.Truncate(msg, 120, true)
	}
}

// formatCrossPRCoverageSection builds the Markdown block inserted into the
// synthesis summary when run.CrossPRCoverage is non-nil.
func formatCrossPRCoverageSection(cov *CrossPRCoverage) string {
	if cov == nil || len(cov.LinkedPRs) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("\n## Cross-Repo PR Coverage\n\n")
	for _, link := range cov.LinkedPRs {
		if link.Accessible {
			sb.WriteString(fmt.Sprintf("- ✅ **[%s/%s#%d](%s)** — *%s* — compatible\n",
				link.Owner, link.Repo, link.Number, link.URL,
				util.Truncate(link.Title, 100, true)))
		} else {
			sb.WriteString(fmt.Sprintf("- ⚠️ **[%s/%s#%d](%s)** — %s\n",
				link.Owner, link.Repo, link.Number, link.URL, link.FetchError))
			sb.WriteString("  _Partial coverage: this change cannot be verified — reviewer should inspect manually._\n")
		}
	}
	if len(cov.Incompatibilities) > 0 {
		sb.WriteString("\n**Potential incompatibilities:**\n")
		for _, inc := range cov.Incompatibilities {
			sb.WriteString(fmt.Sprintf("- %s\n", inc))
		}
	}
	return sb.String()
}
