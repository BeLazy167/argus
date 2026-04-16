package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	gh "github.com/google/go-github/v68/github"

	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/internal/pipeline"
)

// issueLabelsForScenario are labels that trigger auto-scenario creation from issues.
var issueLabelsForScenario = map[string]bool{"argus": true, "bug": true}

func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	event, err := ghpkg.ParseWebhook(r, s.webhookSecret)
	if err != nil {
		s.logger.Error("webhook parse failed", "error", err)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid webhook"})
		return
	}

	switch event.Type {
	case "pull_request":
		prEvent, err := ghpkg.ToPREvent(event)
		if err != nil {
			s.logger.Error("parsing PR event", "error", err)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		orgLogin := strings.SplitN(prEvent.RepoFullName, "/", 2)[0]
		if !s.rateLimiter.AllowReview(prEvent.RepoFullName, orgLogin, false) {
			s.logger.Warn("rate limited", "repo", prEvent.RepoFullName)
			break
		}
		// Check review limit for this installation
		inst, instErr := s.store.GetInstallationByGitHubID(r.Context(), prEvent.InstallationID)
		if instErr != nil {
			s.logger.Warn("installation lookup for plan check", "error", instErr)
		} else {
			reviewCount, countErr := s.store.CountReviewsThisMonth(r.Context(), inst.ID)
			if countErr != nil {
				s.logger.Warn("review count check failed", "error", countErr)
			} else {
				limit := 50 // free tier
				if inst.PlanTier == "pro" {
					limit = 500
				}
				if reviewCount >= limit {
					s.logger.Info("review limit reached", "installation", inst.ID, "count", reviewCount, "limit", limit)
					break
				}
			}
		}

		if !s.tryAcquireReview(prEvent.RepoFullName, prEvent.PRNumber) {
			s.logger.Info("review already in-flight", "repo", prEvent.RepoFullName, "pr", prEvent.PRNumber)
			break
		}
		if !s.acquireSem() {
			s.releaseReview(prEvent.RepoFullName, prEvent.PRNumber)
			s.logger.Warn("webhook semaphore full")
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "server busy"})
			return
		}
		ctx, cancel := context.WithCancel(context.Background())
		s.storeCancel(prEvent.RepoFullName, prEvent.PRNumber, cancel)
		go func() {
			defer s.releaseSem()
			defer s.releaseReview(prEvent.RepoFullName, prEvent.PRNumber)
			defer s.removeCancel(prEvent.RepoFullName, prEvent.PRNumber)
			defer cancel()
			if err := s.orchestrator.HandlePREvent(ctx, *prEvent); err != nil && !errors.Is(err, context.Canceled) {
				s.logger.Error("review pipeline failed", "error", err, "pr", prEvent.PRNumber)
			}
		}()

		// Fire-and-forget reaction sweep. GitHub doesn't webhook reactions on
		// PR review comments, so we opportunistically re-check reactions on
		// every pull_request event. Decoupled from the review goroutine so a
		// slow sweep doesn't delay the review pipeline. Guarded by the same
		// webhook semaphore as every other handler goroutine so a burst of
		// PR events can't spawn unbounded sweepers.
		if s.reactionAnalyzer != nil {
			if s.acquireSem() {
				go func(installationID int64, fullName string, pr int) {
					defer s.releaseSem()
					sweepCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
					defer cancel()
					if err := s.reactionAnalyzer.SweepPRReactions(sweepCtx, installationID, fullName, pr); err != nil {
						s.logger.Warn("reaction sweep failed", "error", err, "pr", pr)
					}
				}(prEvent.InstallationID, prEvent.RepoFullName, prEvent.PRNumber)
			} else {
				s.logger.Warn("webhook semaphore full for reaction sweep", "pr", prEvent.PRNumber)
			}
		}

	case "pull_request_review_comment":
		if event.Action == "created" {
			commentEvent, err := ghpkg.ToCommentEvent(event)
			if err != nil {
				s.logger.Error("parsing comment event", "error", err)
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			if strings.HasSuffix(commentEvent.CommentAuthor, "[bot]") {
				break
			}
			// Reply analysis (existing)
			if commentEvent.InReplyToID > 0 && s.replyAnalyzer != nil {
				if s.acquireSem() {
					go func() {
						defer s.releaseSem()
						if err := s.replyAnalyzer.Analyze(context.Background(), *commentEvent); err != nil {
							s.logger.Error("reply analysis failed", "error", err, "comment_id", commentEvent.CommentID)
						}
					}()
				} else {
					s.logger.Warn("webhook semaphore full for reply analysis")
				}
			}
			// Reaction analysis: check reactions on the parent Argus comment
			if commentEvent.InReplyToID > 0 && s.reactionAnalyzer != nil {
				parentEvent := *commentEvent
				parentEvent.CommentID = commentEvent.InReplyToID
				if s.acquireSem() {
					go func() {
						defer s.releaseSem()
						if err := s.reactionAnalyzer.HandleCommentReactions(context.Background(), parentEvent); err != nil {
							s.logger.Error("reaction analysis failed", "error", err, "comment_id", parentEvent.CommentID)
						}
					}()
				} else {
					s.logger.Warn("webhook semaphore full for reaction analysis")
				}
			}
		}

	case "issue_comment":
		switch event.Action {
		case "created":
			issueEvent, err := ghpkg.ToIssueCommentEvent(event)
			if err != nil {
				s.logger.Error("parsing issue comment event", "error", err)
				break
			}
			if issueEvent == nil || strings.HasSuffix(issueEvent.CommentAuthor, "[bot]") {
				break
			}
			if !s.acquireSem() {
				s.logger.Warn("webhook semaphore full for command dispatch")
				break
			}
			go func() {
				defer s.releaseSem()
				s.dispatchCommand(context.Background(), *issueEvent)
			}()
		case "edited":
			// Detect "Trigger Argus review" checkbox toggles on Argus-authored
			// trigger comments. Guards, in order:
			//   - editor is not a bot (drops Argus's own swap-to-Running edits)
			//   - comment AUTHOR is Argus (prevents hijack: a collaborator
			//     pasting the marker+checkbox into their own comment and
			//     toggling it should NOT trigger a review)
			//   - checkbox transitioned [ ] -> [x]
			issueEvent, err := ghpkg.ToIssueCommentEvent(event)
			if err != nil {
				s.logger.Error("parsing issue comment edited event", "error", err)
				break
			}
			if issueEvent == nil {
				break
			}
			if strings.HasSuffix(issueEvent.EditorLogin, "[bot]") {
				break
			}
			if !isArgusCommentAuthor(issueEvent.CommentAuthor) {
				break
			}
			if !pipeline.CheckboxToggled(issueEvent.CommentBodyBefore, issueEvent.CommentBody) {
				break
			}
			if !s.acquireSem() {
				s.logger.Warn("webhook semaphore full for checkbox trigger")
				break
			}
			go func() {
				defer s.releaseSem()
				s.handleCheckboxTrigger(context.Background(), *issueEvent)
			}()
		}

	case "issues":
		issueEvent, ok := event.Payload.(*gh.IssuesEvent)
		if !ok {
			break
		}
		action := issueEvent.GetAction()
		if action != "opened" && action != "labeled" {
			break
		}
		hasLabel := false
		for _, l := range issueEvent.GetIssue().Labels {
			if issueLabelsForScenario[strings.ToLower(l.GetName())] {
				hasLabel = true
				break
			}
		}
		if !hasLabel {
			break
		}
		if !s.acquireSem() {
			s.logger.Warn("webhook semaphore full for issue scenario")
			break
		}
		go func() {
			defer s.releaseSem()
			s.generateScenarioFromIssue(context.WithoutCancel(r.Context()), issueEvent)
		}()

	case "installation":
		s.logger.Info("installation event", "action", event.Action)
		instEvent, ok := event.Payload.(*gh.InstallationEvent)
		if !ok {
			s.logger.Error("unexpected installation event payload type")
			break
		}
		ghInstID := instEvent.GetInstallation().GetID()
		accountLogin := instEvent.GetInstallation().GetAccount().GetLogin()

		switch event.Action {
		case "created":
			inst, err := s.store.CreateInstallation(r.Context(), ghInstID, accountLogin)
			if err != nil {
				s.logger.Error("create installation", "error", err, "gh_id", ghInstID)
				break
			}
			var synced int
			for _, repo := range instEvent.Repositories {
				_, err := s.store.UpsertRepo(r.Context(), inst.ID, repo.GetID(), repo.GetFullName(), repo.GetDefaultBranch())
				if err != nil {
					s.logger.Warn("upsert repo from installation event", "error", err, "repo", repo.GetFullName())
					continue
				}
				synced++
			}
			s.logger.Info("installation created", "gh_id", ghInstID, "account", accountLogin, "repos_synced", synced)

		case "deleted", "suspend":
			inst, err := s.store.GetInstallationByGitHubID(r.Context(), ghInstID)
			if err != nil {
				s.logger.Warn("installation lookup for suspend/delete", "error", err, "gh_id", ghInstID)
				break
			}
			if err := s.store.SuspendInstallation(r.Context(), inst.ID); err != nil {
				s.logger.Error("suspend installation", "error", err, "gh_id", ghInstID)
			}

		case "unsuspend":
			// Re-create clears suspended_at via ON CONFLICT
			_, err := s.store.CreateInstallation(r.Context(), ghInstID, accountLogin)
			if err != nil {
				s.logger.Error("unsuspend installation", "error", err, "gh_id", ghInstID)
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "accepted"})
}

// handleCheckboxTrigger dispatches a review when a user toggles the "Trigger
// Argus review" task-list checkbox on an Argus-posted trigger comment.
//
// This path runs when auto_run is disabled. It mirrors handleReviewCommand's
// dispatch flow but uses the tighter `force=true` rate-limit path (3/hr/repo)
// because a checkbox click is effectively a one-gesture trigger — easier to
// spam than typing `@argus-eye review` — and we want a tighter cap.
//
// force=true in AllowReview has two effects we want here: (1) tighter 3/hr
// cap, (2) bypass of the "already reviewed at this SHA" guard. The second is
// intentional: the user explicitly clicked the checkbox after seeing the cost
// estimate, so we honor that intent.
//
// Ordering notes:
//   - tryAcquireReview runs BEFORE AllowReview: a losing double-click would
//     otherwise burn a force-hourly token without running a review.
//   - The "Running..." body swap happens only after the PR lock is acquired
//     so a lost race doesn't leave the checkbox stuck.
//   - Pipeline-failure path restores the checkbox to unchecked with an error
//     suffix so the user can click again to retry.
func (s *Server) handleCheckboxTrigger(ctx context.Context, evt ghpkg.IssueCommentEvent) {
	parts := strings.SplitN(evt.RepoFullName, "/", 2)
	if len(parts) != 2 {
		return
	}
	owner, repoName := parts[0], parts[1]
	ghClient := ghpkg.NewClient(s.ghApp)

	// Acknowledge the click with a reaction before doing any heavy work.
	_ = ghClient.AddReaction(ctx, evt.InstallationID, owner, repoName, evt.CommentID, "eyes")

	prCtx, cancelPR := context.WithTimeout(ctx, 10*time.Second)
	prEvent, err := ghClient.GetPullRequest(prCtx, evt.InstallationID, owner, repoName, evt.PRNumber)
	cancelPR()
	if err != nil {
		s.logger.Error("checkbox trigger: fetch PR failed", "error", err, "pr", evt.PRNumber)
		_ = ghClient.AddReaction(ctx, evt.InstallationID, owner, repoName, evt.CommentID, "confused")
		return
	}

	if !s.tryAcquireReview(evt.RepoFullName, evt.PRNumber) {
		s.logger.Info("checkbox trigger: review already in progress", "repo", evt.RepoFullName, "pr", evt.PRNumber)
		return
	}
	defer s.releaseReview(evt.RepoFullName, evt.PRNumber)

	if !s.rateLimiter.AllowReview(evt.RepoFullName, owner, true) {
		_ = ghClient.CreateIssueComment(ctx, evt.InstallationID, owner, repoName, evt.PRNumber,
			"Rate limit exceeded for on-demand reviews (3/hour). Try again later.")
		_ = ghClient.AddReaction(ctx, evt.InstallationID, owner, repoName, evt.CommentID, "confused")
		return
	}

	// Swap the checkbox line for a "Running..." marker so the user sees
	// immediate feedback. Failure here is non-fatal.
	updated := pipeline.ReplaceTriggerWithRunning(evt.CommentBody)
	if updated != evt.CommentBody {
		if err := ghClient.UpdateIssueComment(ctx, evt.InstallationID, owner, repoName, evt.CommentID, updated); err != nil {
			s.logger.Warn("checkbox trigger: update comment body", "error", err, "comment_id", evt.CommentID)
		}
	}

	prEvent.Action = "manual"
	prEvent.RepoID = evt.RepoID
	s.logger.Info("checkbox-triggered review", "repo", evt.RepoFullName, "pr", evt.PRNumber, "by", evt.EditorLogin)

	if err := s.orchestrator.HandlePREvent(ctx, *prEvent); err != nil {
		s.logger.Error("checkbox trigger: pipeline failed", "error", err, "pr", evt.PRNumber)
		_ = ghClient.AddReaction(ctx, evt.InstallationID, owner, repoName, evt.CommentID, "confused")
		// Restore the checkbox so the user can click again to retry; rollback
		// is best-effort (failure here is worse than leaving the Running marker).
		if restored := pipeline.RestoreTriggerAfterFailure(updated); restored != updated {
			if uerr := ghClient.UpdateIssueComment(ctx, evt.InstallationID, owner, repoName, evt.CommentID, restored); uerr != nil {
				s.logger.Warn("checkbox trigger: restore comment body", "error", uerr, "comment_id", evt.CommentID)
			}
		}
		return
	}
	_ = ghClient.AddReaction(ctx, evt.InstallationID, owner, repoName, evt.CommentID, "rocket")
}
