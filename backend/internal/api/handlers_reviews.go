package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/BeLazy167/argus/backend/internal/admission"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/BeLazy167/argus/backend/internal/obs"
	"github.com/BeLazy167/argus/backend/internal/util"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"

	"github.com/BeLazy167/argus/backend/internal/pipeline"
)

func (s *Server) listAllReviews(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	reviews, err := s.store.ListAllReviewsScoped(r.Context(), getInstallationIDs(r.Context()), limit, offset)
	if err != nil {
		s.logger.Error("list all reviews", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	writeJSON(w, http.StatusOK, reviews)
}

func (s *Server) listReviews(w http.ResponseWriter, r *http.Request) {
	repoID, err := strconv.ParseInt(chi.URLParam(r, "repoID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid repo id"})
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	reviews, err := s.store.ListReviewsScoped(r.Context(), repoID, getInstallationIDs(r.Context()), limit, offset)
	if err != nil {
		s.logger.Error("list reviews", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	writeJSON(w, http.StatusOK, reviews)
}

func (s *Server) getReview(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "reviewID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid review id"})
		return
	}
	review, err := s.store.GetReview(r.Context(), id)
	if err != nil {
		s.handleDBError(w, err, "review not found")
		return
	}
	// GetRepoScoped is the authorization check for this whole handler: it fails
	// unless the caller's JWT carries the installation that owns the repo. Its
	// result is also the tenant for the memory reads below — read off the repo,
	// never off the request, so the memory scope cannot drift from the scope
	// that granted access.
	repo, err := s.store.GetRepoScoped(r.Context(), review.RepoID, getInstallationIDs(r.Context()))
	if err != nil {
		s.handleDBError(w, err, "review not found")
		return
	}
	comments, err := s.store.GetReviewComments(r.Context(), id)
	if err != nil {
		s.logger.Error("fetching review comments", "error", err, "review_id", id)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load review comments"})
		return
	}
	minorNotes, err := s.store.GetReviewMinorNotes(r.Context(), id)
	if err != nil {
		s.logger.Error("fetching review minor notes", "error", err, "review_id", id)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load review minor notes"})
		return
	}

	// Incremental-history sidecars: the PR's per-SHA review passes and its
	// auto-resolve pushes. Auxiliary to the review itself, so a failure degrades
	// to an empty timeline rather than failing the whole detail view.
	history, err := s.store.ListPRReviewSummaries(r.Context(), review.RepoID, review.PRNumber)
	if err != nil {
		s.logger.Warn("fetching PR review history", "error", err, "review_id", id)
	}
	autoResolves, err := s.store.ListPRAutoResolveEvents(r.Context(), review.RepoID, review.PRNumber)
	if err != nil {
		s.logger.Warn("fetching PR auto-resolve events", "error", err, "review_id", id)
	}

	// What the review learned. Auxiliary like the two sidecars above: a failure
	// here degrades to an empty "learned nothing" panel rather than breaking the
	// review view. Both reads are scoped by the repo's installation.
	memories, err := s.store.ListReviewMemories(r.Context(), repo.InstallationID, id, 0)
	if err != nil {
		s.logger.Warn("fetching review memories", "error", err, "review_id", id)
	}
	memoryCounts, err := s.store.CountReviewMemoriesByType(r.Context(), repo.InstallationID, id)
	if err != nil {
		s.logger.Warn("counting review memories", "error", err, "review_id", id)
	}

	writeJSON(w, http.StatusOK, ReviewDetailResponse{
		Review:            review,
		Comments:          comments,
		MinorNotes:        minorNotes,
		History:           history,
		AutoResolveEvents: autoResolves,
		Memories:          memories,
		MemoryCounts:      memoryCounts,
	})
}

// exportReviewPublic handles the public export endpoint with HMAC signature verification.
// Falls through to the same export logic as the auth-protected route.
func (s *Server) exportReviewPublic(w http.ResponseWriter, r *http.Request) {
	reviewID := chi.URLParam(r, "reviewID")
	sig := r.URL.Query().Get("sig")
	exp := r.URL.Query().Get("exp")

	if !util.VerifyExportSig(reviewID, sig, exp) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid or expired signature"})
		return
	}
	s.exportReview(w, r)
}

func (s *Server) exportReview(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "reviewID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid review id"})
		return
	}
	review, err := s.store.GetReview(r.Context(), id)
	if err != nil {
		s.handleDBError(w, err, "review not found")
		return
	}
	_ = review // scope check skipped for signed URLs
	comments, err := s.store.GetReviewComments(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load comments"})
		return
	}
	minorNotes, err := s.store.GetReviewMinorNotes(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load minor notes"})
		return
	}

	format := r.URL.Query().Get("format")
	if format == "" {
		format = "json"
	}

	// Build a key set for posted comments so we can detect dropped ones from the unfiltered set.
	// Key is (file, line, specialist) since each specialist produces at most one
	// finding per file:line. Synthesis rephrases bodies, so body-prefix keys
	// misclassified rephrased comments as "dropped" duplicates.
	postedKey := func(file string, line int, specialist string) string {
		return fmt.Sprintf("%s:%d:%s", file, line, specialist)
	}
	postedKeys := make(map[string]bool, len(comments))
	for _, c := range comments {
		line := 0
		if c.EndLine != nil {
			line = *c.EndLine
		}
		spec := ""
		if c.Specialist != nil {
			spec = *c.Specialist
		}
		postedKeys[postedKey(c.FilePath, line, spec)] = true
	}

	findings := make([]ExportFinding, 0, len(comments))
	for _, c := range comments {
		sev := "suggestion"
		if c.Severity != nil {
			sev = *c.Severity
		}
		cat := ""
		if c.Category != nil {
			cat = *c.Category
		}
		spec := ""
		if c.Specialist != nil {
			spec = *c.Specialist
		}
		conf := 0
		if c.ConfidenceScore != nil {
			conf = *c.ConfidenceScore
		}
		line := 0
		if c.EndLine != nil {
			line = *c.EndLine
		}

		prio := "P2"
		switch sev {
		case "critical":
			prio = "P0"
		case "warning":
			prio = "P1"
		}

		findings = append(findings, ExportFinding{
			File:       c.FilePath,
			Line:       line,
			Priority:   prio,
			Confidence: conf,
			Category:   cat,
			Severity:   sev,
			Body:       c.Body,
			Specialist: spec,
			// GithubCommentID is stamped by backfillGitHubCommentIDs only for
			// findings that actually posted as inline GitHub comments. A nil
			// value on a row that exists in review_comments means the finding
			// was folded into the summary body (line outside the diff) —
			// BUT only once the review actually got posted. Nil on a failed or
			// unposted review means the finding never had the chance to go
			// inline, not that we chose to fold it.
			Folded: review.Status == "completed" && review.GithubReviewID != nil && c.GithubCommentID == nil,
		})
	}

	// Merge in dropped findings from the unfiltered pipeline payload.
	// These are comments the LLM generated but were filtered by dedup/scoring.
	rawPayload, perr := s.store.GetAllFileReviewsForReview(r.Context(), id)
	if perr != nil {
		s.logger.Warn("export: load unfiltered payload failed", "review_id", id, "error", perr)
	} else if len(rawPayload) > 0 {
		// rawPayload is json.RawMessage (via sqlc's jsonb → RawMessage override).
		// A null JSONB path result scans as literal bytes "null" or zero length.
		if string(rawPayload) == "null" {
			// No unfiltered reviews recorded for this run (legacy pipeline_states rows).
		} else {
			var allFileReviews []struct {
				Path     string `json:"Path"`
				Comments []struct {
					What       string `json:"what"`
					Body       string `json:"body"`
					Line       int    `json:"line"`
					StartLine  int    `json:"start_line"`
					Score      int    `json:"score"`
					Severity   string `json:"severity"`
					Category   string `json:"category"`
					Confidence string `json:"confidence"`
					Specialist string `json:"specialist"`
				} `json:"Comments"`
			}
			if err := json.Unmarshal(rawPayload, &allFileReviews); err != nil {
				s.logger.Warn("export: unfiltered payload unmarshal failed", "review_id", id, "error", err)
			} else {
				for _, fr := range allFileReviews {
					for _, c := range fr.Comments {
						line := c.Line
						if line == 0 {
							line = c.StartLine
						}
						body := c.Body
						if body == "" {
							body = c.What
						}
						if postedKeys[postedKey(fr.Path, line, c.Specialist)] {
							continue // already in posted set
						}
						prio := "P2"
						switch c.Severity {
						case "critical":
							prio = "P0"
						case "warning":
							prio = "P1"
						}
						findings = append(findings, ExportFinding{
							File:       fr.Path,
							Line:       line,
							Priority:   prio,
							Confidence: c.Score,
							Category:   c.Category,
							Severity:   c.Severity,
							Body:       body,
							Specialist: c.Specialist,
							Dropped:    true,
						})
					}
				}
			}
		}
	}

	switch format {
	case "md", "markdown":
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=argus-review-%s.md", id.String()[:8]))

		// Split findings into three buckets so the author can tell what reached
		// GitHub and how: posted inline (resolvable threads), folded to summary
		// (text only), or dropped entirely (filtered before posting).
		postedInline, folded, dropped := 0, 0, 0
		for _, f := range findings {
			switch {
			case f.Dropped:
				dropped++
			case f.Folded:
				folded++
			default:
				postedInline++
			}
		}

		var sb strings.Builder
		sb.WriteString("# Argus Review Export\n\n")
		sb.WriteString(fmt.Sprintf("- **Review:** %s\n", id.String()))
		sb.WriteString(fmt.Sprintf("- **PR:** #%d\n", review.PRNumber))
		score := 0
		if review.Score != nil {
			score = *review.Score
		}
		sb.WriteString(fmt.Sprintf("- **Score:** %d/10\n", score))
		sb.WriteString(fmt.Sprintf("- **Posted inline:** %d\n", postedInline))
		if folded > 0 {
			sb.WriteString(fmt.Sprintf("- **Folded to summary:** %d _(line outside PR diff)_\n", folded))
		}
		if dropped > 0 {
			sb.WriteString(fmt.Sprintf("- **Dropped findings:** %d _(filtered by dedup/scoring)_\n", dropped))
		}
		sb.WriteString(fmt.Sprintf("- **Total findings:** %d\n\n", len(findings)))

		// Group by file
		fileGroups := make(map[string][]ExportFinding)
		var fileOrder []string
		for _, f := range findings {
			if _, exists := fileGroups[f.File]; !exists {
				fileOrder = append(fileOrder, f.File)
			}
			fileGroups[f.File] = append(fileGroups[f.File], f)
		}

		for _, file := range fileOrder {
			sb.WriteString(fmt.Sprintf("## %s\n\n", file))
			for _, f := range fileGroups[file] {
				mark := ""
				switch {
				case f.Dropped:
					mark = " _(dropped)_"
				case f.Folded:
					mark = " _(folded to summary)_"
				}
				sb.WriteString(fmt.Sprintf("### %s L%d — %s [%s]%s\n\n", f.Priority, f.Line, f.Category, f.Severity, mark))
				sb.WriteString(f.Body)
				sb.WriteString("\n\n---\n\n")
			}
		}

		if _, err := w.Write([]byte(sb.String())); err != nil {
			s.logger.Warn("export write failed", "error", err)
		}

	default: // json
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=argus-review-%s.json", id.String()[:8]))

		export := ReviewExportResponse{
			ReviewID:      id.String(),
			PRNumber:      review.PRNumber,
			PRTitle:       review.PRTitle,
			Score:         review.Score,
			Status:        review.Status,
			TotalFindings: len(findings),
			Findings:      findings,
			MinorNotes:    minorNotes,
		}
		if err := json.NewEncoder(w).Encode(export); err != nil {
			s.logger.Warn("export encode failed", "error", err)
		}
	}
}

func (s *Server) retryReview(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "reviewID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid review id"})
		return
	}
	review, err := s.store.GetReview(r.Context(), id)
	if err != nil {
		s.handleDBError(w, err, "review not found")
		return
	}
	// Verify caller owns this review's repo (also needed for the cancel-fn key).
	repo, err := s.store.GetRepoScoped(r.Context(), review.RepoID, getInstallationIDs(r.Context()))
	if err != nil {
		s.handleDBError(w, err, "review not found")
		return
	}
	if review.Status != "failed" && review.Status != "cancelled" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "only failed or cancelled reviews can be retried"})
		return
	}
	// repo.InstallationID is the DB serial, not GitHub's installation id. The
	// marker lookup must authenticate as the GitHub installation.
	inst, instErr := s.store.GetInstallation(r.Context(), repo.InstallationID)
	if instErr != nil {
		s.handleDBError(w, instErr, "installation not found")
		return
	}
	reconciliation, reconcileErr := s.reconcileAmbiguousReviewPost(r.Context(), review, repo, inst.InstallationID, getUserID(r.Context()))
	if reconcileErr != nil {
		// Never expose GitHub, marker, or persistence details. A failed/incomplete
		// lookup and a still-recent claim both mean the same safe user action: wait
		// and retry. The detailed reason remains in structured server logs.
		s.logger.Warn("retry: review post reconciliation deferred", "error", reconcileErr, "review_id", id, "repo", repo.FullName)
		writeJSON(w, http.StatusConflict, map[string]string{"error": "review post reconciliation is still pending; retry later"})
		return
	}
	if reconciliation == reviewPostAlreadyDelivered {
		// The requested retry is already satisfied by the review found on GitHub.
		// Do not enter admission, BeginReviewRetry, or Launcher: all would charge a
		// redundant generation and hide the original attempt's comments.
		writeJSON(w, http.StatusOK, map[string]string{"status": "already delivered", "review_id": id.String()})
		return
	}

	// Refuse if the previous run is still live (e.g. a review cancelled but not
	// yet halted whose run is still executing): retrying now would double-run
	// the pipeline and post twice. Synchronous so we can surface 409.
	retrier := s.reviewRetrier
	if retrier == nil {
		retrier = s.orchestrator
	}
	if retrier == nil {
		s.logger.Error("retry precheck unavailable", "review_id", id)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "retry failed"})
		return
	}
	if err := retrier.EnsureNotRunning(r.Context(), id); err != nil {
		if errors.Is(err, pipeline.ErrReviewRunning) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		s.logger.Error("retry precheck failed", "error", err, "review_id", id)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "retry failed"})
		return
	}
	// The launcher owns the per-PR slot (same guard as the webhook path — the
	// shared repo:PR key means retrying while another review is live would
	// otherwise let both run concurrently), the paired cancel fn, the event-bus
	// topic, and — because this is a retry of an existing review row (ReviewID
	// set) — the pending→failed rollback + EventError publish when the pipeline
	// errors. BeforeSpawn flips the review to pending BEFORE spawn so the UI
	// shows "retrying"; its failure releases the slot and surfaces 500 (no
	// goroutine spawned). Trace_id is preserved onto the detached BaseCtx.
	// Retry was the least protected path: no rate limit, no concurrency token,
	// no check beyond installation scope — while re-running the whole pipeline
	// at the cost of a fresh review. It goes through Admission like everything.

	orgLogin, _, ok := strings.Cut(repo.FullName, "/")
	if !ok {
		// Without this the whole name becomes the org rate-limit bucket key,
		// silently mixing one repo's budget with an org that does not exist.
		s.logger.Error("retry: malformed repo full name", "repo", repo.FullName, "review_id", id)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "retry failed"})
		return
	}
	if verdict := s.admissionFor(inst.InstallationID).Decide(r.Context(), admission.Request{
		Actor:        actorFromRequestContext(r.Context()),
		RepoFullName: repo.FullName,
		OrgLogin:     orgLogin,
	}); !verdict.Allowed() {
		s.logger.Info("retry refused", "review_id", id, "repo", repo.FullName, "reason", verdict.Reason)
		writeJSON(w, http.StatusForbidden, map[string]string{"error": verdict.Reason})
		return
	}

	var attemptGeneration int
	launchErr := s.launcher.Launch(pipeline.LaunchSpec{
		Repo:              repo.FullName,
		PR:                review.PRNumber,
		BaseCtx:           obs.SetTraceID(context.Background(), obs.TraceID(r.Context())),
		ReviewID:          &id,
		AttemptGeneration: &attemptGeneration,
		BeforeSpawn: func(bsCtx context.Context) error {
			// The reaction sweep is a synchronous launch barrier. Do it before
			// claiming a new retry generation so a transient GitHub failure
			// cannot leave the review pending without a spawned pipeline.
			if err := s.reconcileReactionsBeforeReview(bsCtx, inst.InstallationID, repo.FullName, review.PRNumber); err != nil {
				return err
			}
			var claimed bool
			var err error
			attemptGeneration, claimed, err = s.store.BeginReviewRetry(bsCtx, id)
			if err != nil {
				return err
			}
			if !claimed {
				return pipeline.ErrReviewRunning
			}
			return nil
		},
		Run: func(ctx context.Context) error {
			return retrier.RetryReview(ctx, id, attemptGeneration)
		},
		OnDone: func(err error) {
			// context.Canceled means a Stop halted the retry — the state machine
			// already marked it cancelled, so it isn't a failure to log. The
			// rollback + EventError for real failures are owned by the launcher.
			if err != nil && !errors.Is(err, context.Canceled) {
				s.logger.Error("retry review failed", "error", err, "review_id", id)
			}
		},
	})
	if errors.Is(launchErr, pipeline.ErrInFlight) || errors.Is(launchErr, pipeline.ErrReviewRunning) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "another review for this PR is in flight"})
		return
	}
	if launchErr != nil {
		// BeforeSpawn (mark-pending) failed — slot already released by the launcher.
		s.logger.Error("retry: mark pending failed", "error", launchErr, "review_id", id)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "update failed"})
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]string{"status": "retrying", "review_id": id.String()})
}

func (s *Server) cancelReview(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "reviewID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid review id"})
		return
	}
	review, err := s.store.GetReview(r.Context(), id)
	if err != nil {
		s.handleDBError(w, err, "review not found")
		return
	}

	// Scoped repo check first — avoids leaking status of reviews caller doesn't own
	repo, err := s.store.GetRepoScoped(r.Context(), review.RepoID, getInstallationIDs(r.Context()))
	if err != nil {
		s.handleDBError(w, err, "review not found")
		return
	}

	if review.Status != "in_progress" && review.Status != "pending" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "only pending or in-progress reviews can be cancelled"})
		return
	}

	if s.inflight.Cancel(repo.FullName, review.PRNumber) {
		s.logger.Info("cancel requested", "review_id", id, "repo", repo.FullName, "pr", review.PRNumber)
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "cancelling", "review_id": id.String()})
		return
	}
	// No in-flight cancel function: the process restarted, or this request landed
	// on a different Fly machine than the one running the review. Mark the review
	// (and its latest run) cancelled directly so the UI leaves pending/in_progress
	// limbo and the recovery sweeper won't resurrect the orphaned run.
	if err := s.orchestrator.CancelStranded(r.Context(), id); err != nil {
		s.logger.Error("stranded cancel failed", "error", err, "review_id", id)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cancel failed"})
		return
	}
	s.logger.Info("stranded cancel: marked review cancelled", "review_id", id, "repo", repo.FullName, "pr", review.PRNumber)
	// The pipeline's own terminal hook cannot fire here: the run is on another
	// machine (or gone), which is why this branch exists at all. Rewrite the
	// PR's progress comment directly, or it keeps advertising "watch live" for
	// a review that is now cancelled.
	s.orchestrator.FinalizeStartedComment(r.Context(), id, pipeline.StartedOutcomeCancelled, "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled", "review_id": id.String()})
}

// --- WebSocket Stream ---

func (s *Server) streamReviewWS(w http.ResponseWriter, r *http.Request) {
	// Auth via query params (browser WebSocket API can't set headers)
	token := r.URL.Query().Get("token")
	installationHint := r.URL.Query().Get("installation_id")
	if token == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing token"})
		return
	}
	claims, err := validateToken(token)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}
	ids, err := s.resolveInstallationIDs(r.Context(), claims, installationHint)
	if err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		return
	}

	id, err := uuid.Parse(chi.URLParam(r, "reviewID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid review id"})
		return
	}

	review, err := s.store.GetReview(r.Context(), id)
	if err != nil {
		s.handleDBError(w, err, "review not found")
		return
	}
	if _, err := s.store.GetRepoScoped(r.Context(), review.RepoID, ids); err != nil {
		s.handleDBError(w, err, "review not found")
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // CORS handled by chi middleware
	})
	if err != nil {
		s.logger.Warn("websocket accept failed", "error", err)
		return
	}
	defer conn.CloseNow()

	ctx := conn.CloseRead(r.Context())

	terminal := review.Status == "completed" || review.Status == "failed" || review.Status == "cancelled"
	if s.eventBus == nil {
		if terminal {
			writeTerminalReviewEvent(ctx, conn, review.Status)
			return
		}
		conn.Close(websocket.StatusInternalError, "streaming not available")
		return
	}

	var afterID int64
	if cursor := r.URL.Query().Get("after"); cursor != "" {
		afterID, err = strconv.ParseInt(cursor, 10, 64)
		if err != nil || afterID < 0 {
			conn.Close(websocket.StatusPolicyViolation, "invalid event cursor")
			return
		}
	}
	if err := writeReviewStream(ctx, conn, s.eventBus, id, afterID, func(ctx context.Context) (string, error) {
		current, err := s.store.GetReview(ctx, id)
		if err != nil {
			return "", err
		}
		return current.Status, nil
	}); err != nil {
		s.logger.Error("review stream", "error", err, "review_id", id)
	}
}

// writeReviewStream registers the subscriber before re-reading review status.
// The ordering closes the only gap that durable replay cannot cover: a terminal
// event whose PostgreSQL persistence failed while the previous local topic was
// being closed. A terminal before the status read is synthesized; one after it
// is delivered through the already-registered live subscription.
func writeReviewStream(
	ctx context.Context,
	conn *websocket.Conn,
	eventBus *pipeline.EventBus,
	reviewID uuid.UUID,
	afterID int64,
	loadCurrentStatus func(context.Context) (string, error),
) error {
	events, history, subscriberClosed, unsub, err := eventBus.SubscribeContextWithCloseReason(ctx, reviewID, afterID)
	if err != nil {
		_ = conn.Close(websocket.StatusInternalError, "streaming not available")
		return fmt.Errorf("subscribing to review events: %w", err)
	}
	if events == nil {
		_ = conn.Close(websocket.StatusNormalClosure, "no active stream")
		return nil
	}
	defer unsub()

	status, err := loadCurrentStatus(ctx)
	if err != nil {
		_ = conn.Close(websocket.StatusTryAgainLater, "review status unavailable; reconnect")
		return fmt.Errorf("reloading review status after subscription: %w", err)
	}
	terminal := isTerminalReviewStatus(status)
	if terminal {
		defer eventBus.CloseTopic(reviewID)
	}

	// Keepalive: ping every 30s to prevent Fly proxy timeout
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := conn.Ping(ctx); err != nil {
					return
				}
			}
		}
	}()

	// A bounded reconnect page is closed before this handler starts. Capture its
	// typed reason now; other closures may still arrive while history is written.
	var knownCloseReason pipeline.SubscriberCloseReason
	select {
	case knownCloseReason = <-subscriberClosed:
	default:
	}
	replayHasMore := knownCloseReason == pipeline.SubscriberCloseReplayPageExhausted

	// Replay history. Defer a terminal marker found in a partial page: otherwise
	// the browser's terminal no-loop rule would prevent it from fetching the
	// remaining gap. On the final page, write every message before cleanly
	// closing even if sequence allocation placed the terminal marker earlier.
	terminalReplayed := false
	for _, evt := range history {
		if replayHasMore && isTerminalReviewEvent(evt.Type) {
			continue
		}
		if err := wsjson.Write(ctx, conn, evt); err != nil {
			return nil
		}
		terminalReplayed = terminalReplayed || isTerminalReviewEvent(evt.Type)
	}
	if replayHasMore {
		closeStatus, message := reviewStreamClose(knownCloseReason)
		_ = conn.Close(closeStatus, message)
		return nil
	}
	if terminalReplayed {
		_ = conn.Close(websocket.StatusNormalClosure, "review stream ended")
		return nil
	}

	// Replay the durable tail before synthesizing a degraded terminal marker so
	// findings and timeline entries are not skipped.
	if terminal {
		writeTerminalReviewEvent(ctx, conn, status)
		return nil
	}

	streamLiveReviewEventsAfterCloseReason(ctx, conn, events, subscriberClosed, knownCloseReason)
	return nil
}

func streamLiveReviewEvents(
	ctx context.Context,
	conn *websocket.Conn,
	events <-chan pipeline.Event,
	subscriberClosed <-chan pipeline.SubscriberCloseReason,
) {
	streamLiveReviewEventsAfterCloseReason(ctx, conn, events, subscriberClosed, "")
}

func streamLiveReviewEventsAfterCloseReason(
	ctx context.Context,
	conn *websocket.Conn,
	events <-chan pipeline.Event,
	subscriberClosed <-chan pipeline.SubscriberCloseReason,
	knownCloseReason pipeline.SubscriberCloseReason,
) {
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-events:
			if !ok {
				reason := knownCloseReason
				if reason == "" {
					reason = pipeline.SubscriberCloseTopic
					if closedReason, received := <-subscriberClosed; received {
						reason = closedReason
					}
				}
				status, message := reviewStreamClose(reason)
				_ = conn.Close(status, message)
				return
			}
			if err := wsjson.Write(ctx, conn, evt); err != nil {
				return
			}
			if isTerminalReviewEvent(evt.Type) {
				_ = conn.Close(websocket.StatusNormalClosure, "review stream ended")
				return
			}
		}
	}
}

func reviewStreamClose(reason pipeline.SubscriberCloseReason) (websocket.StatusCode, string) {
	switch reason {
	case pipeline.SubscriberCloseDurableOverflow,
		pipeline.SubscriberCloseDedupExhausted,
		pipeline.SubscriberCloseReplayPageExhausted:
		return websocket.StatusTryAgainLater, "review stream fell behind; reconnect to replay"
	default:
		return websocket.StatusNormalClosure, "stream ended"
	}
}

func writeTerminalReviewEvent(ctx context.Context, conn *websocket.Conn, status string) {
	evtType := pipeline.EventCompleted
	if status == "failed" {
		evtType = pipeline.EventError
	}
	if status == "cancelled" {
		evtType = pipeline.EventCancelled
	}
	_ = wsjson.Write(ctx, conn, pipeline.Event{
		Type:      evtType,
		Timestamp: time.Now(),
		Data:      mustMarshal(map[string]string{"status": status}),
	})
	_ = conn.Close(websocket.StatusNormalClosure, "review already "+status)
}

func isTerminalReviewStatus(status string) bool {
	return status == "completed" || status == "failed" || status == "cancelled"
}

func isTerminalReviewEvent(eventType pipeline.EventType) bool {
	return eventType == pipeline.EventCompleted || eventType == pipeline.EventError || eventType == pipeline.EventCancelled
}

func mustMarshal(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic("mustMarshal: " + err.Error())
	}
	return b
}
