// Package pipeline — the Gauge: merge-time address-rate detection.
//
// When a PR closes, each posted finding gets an outcome:
//   - addressed_human / addressed_agent — the code within ±3 lines of the
//     finding's anchor changed after the comment was posted (proximity
//     heuristic; ~88/78 precision-recall in the literature), attributed to a
//     human or a bot/agent by the fixing commit's author login;
//   - ignored  — PR merged, anchor untouched;
//   - deferred — PR closed without merging.
//
// Outcomes feed vw_review_gauge (migration 050): human-weighted address rate
// per category per change_class. Detection runs async off the PR-closed
// webhook and is non-fatal on every error.
package pipeline

import (
	"context"
	"os"
	"strings"
	"time"

	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/internal/store"
	"github.com/BeLazy167/argus/backend/pkg/diff"
)

// addressProximityLines is the ± window around a finding's anchor line within
// which a later change counts as addressing the finding.
const addressProximityLines = 3

// maxGaugeFindings caps per-PR detection work so a pathological PR can't
// spend the whole webhook budget on compare/commit API calls.
const maxGaugeFindings = 100

// Outcome values written by the detection job (must match migration 049's
// comment_outcomes_outcome_check).
const (
	OutcomeAddressedHuman = "addressed_human"
	OutcomeAddressedAgent = "addressed_agent"
	OutcomeIgnored        = "ignored"
	OutcomeDeferred       = "deferred"
)

// configuredAgentLogins is the operator-extendable list of logins classified
// as agents (comma-separated ARGUS_AGENT_LOGINS), on top of the built-in
// "[bot]" / "-agent" / "-bot" suffix patterns.
var configuredAgentLogins = parseAgentLogins(os.Getenv("ARGUS_AGENT_LOGINS"))

// parseAgentLogins splits a comma-separated login list, trimming blanks.
func parseAgentLogins(raw string) []string {
	var out []string
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// IsAgentLogin classifies a commit author login as agent (true) or human
// (false). Agents: GitHub App bots ("[bot]" suffix), conventional agent
// account suffixes ("-agent", "-bot", "_bot"), and the configured list.
func IsAgentLogin(login string, extra []string) bool {
	l := strings.ToLower(strings.TrimSpace(login))
	if l == "" {
		return false
	}
	if strings.HasSuffix(l, "[bot]") || strings.HasSuffix(l, "-agent") ||
		strings.HasSuffix(l, "-bot") || strings.HasSuffix(l, "_bot") {
		return true
	}
	for _, e := range extra {
		if strings.EqualFold(strings.TrimSpace(e), l) {
			return true
		}
	}
	return false
}

// TouchedNearAnchor reports whether the patch set changes the given file
// within ±addressProximityLines of anchor. The anchor is a line number in the
// diff's OLD side (the compare base is the head SHA the finding was posted
// against, so review-time line numbers are old-side numbers). Deleted lines
// count at their old position; added lines count at the old-side insertion
// boundary.
func TouchedNearAnchor(files []diff.FileDiff, path string, anchor int) bool {
	if anchor <= 0 {
		return false
	}
	near := func(pos int) bool {
		d := pos - anchor
		return d >= -addressProximityLines && d <= addressProximityLines
	}
	for _, f := range files {
		if f.NewName != path && f.OldName != path {
			continue
		}
		for _, h := range f.Hunks {
			prevOld := h.OldStart - 1
			for _, l := range h.Lines {
				switch l.Type {
				case diff.LineDeleted:
					if near(l.OldNum) {
						return true
					}
					prevOld = l.OldNum
				case diff.LineAdded:
					// Insertion between prevOld and prevOld+1.
					if near(prevOld) || near(prevOld+1) {
						return true
					}
				default:
					if l.OldNum > 0 {
						prevOld = l.OldNum
					}
				}
			}
		}
	}
	return false
}

// WeightedAddressRate is the Gauge's human-weighted address rate: a
// human-addressed finding counts 1.0, an agent-addressed one 0.5, over all
// posted findings. Mirrors vw_review_gauge's SQL arithmetic; returns 0 when
// nothing was posted.
func WeightedAddressRate(human, agent, posted int) float64 {
	if posted <= 0 {
		return 0
	}
	return (float64(human) + 0.5*float64(agent)) / float64(posted)
}

// detectFindingOutcomes runs the Gauge detection pass for a closed PR.
//
// For merged PRs it compares, per distinct review head SHA, the code the
// findings were anchored to against the final head (first-vs-last: one
// compare covers every commit made after the review posted), then classifies
// each proximity hit as human- or agent-addressed via the most recent commit
// that touched the finding's file after the comment was posted. Unmatched
// findings on merged PRs are 'ignored'; every finding on an unmerged close is
// 'deferred'.
//
// Every failure is logged and skipped — this job must never fail the webhook.
func (o *Orchestrator) detectFindingOutcomes(ctx context.Context, event ghpkg.PREvent, repoID int64) {
	findings, err := o.st.ListPostedFindings(ctx, repoID, event.PRNumber)
	if err != nil {
		o.logger.Warn("[gauge] listing posted findings", "error", err, "pr", event.PRNumber, "repo", event.RepoFullName)
		return
	}
	if len(findings) == 0 {
		return
	}
	if len(findings) > maxGaugeFindings {
		findings = findings[:maxGaugeFindings]
	}

	if !event.Merged {
		for _, f := range findings {
			// comment_outcomes carries the gauge's 'deferred' signal; the ledger
			// carries the terminal state. Both are written so they never disagree.
			if err := o.st.RecordFindingOutcome(ctx, f.ID, OutcomeDeferred, nil); err != nil {
				o.logger.Warn("[gauge] recording deferred outcome", "error", err, "comment_id", f.ID)
			}
			// Revive the previously-dead 'deferred' ledger state (ledger-only; no
			// live thread worth resolving on a closed-unmerged PR).
			if _, err := o.findingLifecycle.Transition(ctx, FindingTransition{FindingID: f.ID, Event: EventDeferred}); err != nil {
				o.logger.Warn("[gauge] deferred lifecycle transition", "error", err, "comment_id", f.ID)
			}
		}
		o.logger.Info("[gauge] PR closed unmerged — findings deferred", "pr", event.PRNumber, "repo", event.RepoFullName, "findings", len(findings))
		return
	}

	owner, repo, err := splitRepoFullName(event.RepoFullName)
	if err != nil {
		o.logger.Warn("[gauge] split repo name", "error", err, "repo", event.RepoFullName)
		return
	}

	// One compare per distinct review head SHA (first-vs-last across all
	// commits made after that review's comments).
	patches := make(map[string]*diff.PatchSet)
	patchFor := func(base string) *diff.PatchSet {
		if ps, ok := patches[base]; ok {
			return ps
		}
		var ps *diff.PatchSet
		if base != "" && base != event.HeadSHA {
			raw, err := o.ghClient.GetCompareCommitsDiff(ctx, event.InstallationID, owner, repo, base, event.HeadSHA)
			if err != nil {
				o.logger.Warn("[gauge] compare commits", "error", err, "base", base, "pr", event.PRNumber)
			} else if parsed, err := diff.Parse(raw); err != nil {
				o.logger.Warn("[gauge] parsing compare diff", "error", err, "base", base, "pr", event.PRNumber)
			} else {
				ps = parsed
			}
		}
		patches[base] = ps // nil = no commits after review or compare failed
		return ps
	}

	// Author lookups are cached per (file, since-truncated-to-minute) — every
	// finding in the same review on the same file shares one API call.
	touches := make(map[touchKey][]ghpkg.CommitTouch)

	now := time.Now()
	var addressedHuman, addressedAgent, ignored int
	for _, f := range findings {
		ps := patchFor(f.HeadSHA)
		if ps == nil || !TouchedNearAnchor(ps.Files, f.FilePath, f.Line) {
			if err := o.st.RecordFindingOutcome(ctx, f.ID, OutcomeIgnored, nil); err != nil {
				o.logger.Warn("[gauge] recording ignored outcome", "error", err, "comment_id", f.ID)
			}
			ignored++
			continue
		}
		outcome := o.classifyAddressed(ctx, event, owner, repo, f, touches)
		if err := o.st.RecordFindingOutcome(ctx, f.ID, outcome, &now); err != nil {
			o.logger.Warn("[gauge] recording addressed outcome", "error", err, "comment_id", f.ID)
			continue
		}
		// Reconcile the ledger with the gauge's addressed_* signal: move the
		// finding to state=addressed so review_comments.state and comment_outcomes
		// agree. comment_outcomes keeps the human/agent axis the single ledger
		// state can't express. LEDGER-ONLY (EventAddressedAtMerge): the gauge runs
		// post-close and does not check thread openness, and thread resolution
		// during the PR's life was auto-resolve's job — the PR is now closed, the
		// thread moot. Usually a no-op (auto-resolve already set addressed).
		if _, err := o.findingLifecycle.Transition(ctx, FindingTransition{
			FindingID: f.ID,
			Event:     EventAddressedAtMerge,
		}); err != nil {
			o.logger.Warn("[gauge] addressed lifecycle transition", "error", err, "comment_id", f.ID)
		}
		if outcome == OutcomeAddressedHuman {
			addressedHuman++
		} else {
			addressedAgent++
		}
	}

	o.logger.Info("[gauge] finding outcomes recorded",
		"pr", event.PRNumber, "repo", event.RepoFullName,
		"addressed_human", addressedHuman, "addressed_agent", addressedAgent, "ignored", ignored,
		"address_rate", WeightedAddressRate(addressedHuman, addressedAgent, len(findings)))
}

// touchKey caches commits-touching-file lookups per (file, since-minute).
type touchKey struct {
	path  string
	since int64
}

// classifyAddressed resolves human-vs-agent for a proximity-matched finding:
// the most recent commit touching the finding's file after the comment was
// posted decides. Lookup failures default to addressed_human (agents are the
// exception, humans the rule).
func (o *Orchestrator) classifyAddressed(ctx context.Context, event ghpkg.PREvent, owner, repo string, f store.PostedFinding, cache map[touchKey][]ghpkg.CommitTouch) string {
	key := touchKey{path: f.FilePath, since: f.PostedAt.Truncate(time.Minute).Unix()}
	commits, ok := cache[key]
	if !ok {
		var err error
		commits, err = o.ghClient.ListCommitsTouchingFile(ctx, event.InstallationID, owner, repo, f.FilePath, event.HeadSHA, f.PostedAt)
		if err != nil {
			o.logger.Warn("[gauge] listing commits touching file", "error", err, "file", f.FilePath, "pr", event.PRNumber)
			commits = nil
		}
		cache[key] = commits
	}
	if len(commits) == 0 {
		return OutcomeAddressedHuman
	}
	last := commits[len(commits)-1]
	if IsAgentLogin(last.Login, configuredAgentLogins) {
		return OutcomeAddressedAgent
	}
	return OutcomeAddressedHuman
}
