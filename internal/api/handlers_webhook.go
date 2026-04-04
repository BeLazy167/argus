package api

import (
	"context"
	"net/http"
	"strings"

	gh "github.com/google/go-github/v68/github"

	ghpkg "github.com/BeLazy167/argus/internal/github"
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
		go func() {
			defer s.releaseSem()
			defer s.releaseReview(prEvent.RepoFullName, prEvent.PRNumber)
			if err := s.orchestrator.HandlePREvent(context.Background(), *prEvent); err != nil {
				s.logger.Error("review pipeline failed", "error", err, "pr", prEvent.PRNumber)
			}
		}()

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
		if event.Action == "created" {
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
