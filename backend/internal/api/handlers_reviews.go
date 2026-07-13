package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	if _, err := s.store.GetRepoScoped(r.Context(), review.RepoID, getInstallationIDs(r.Context())); err != nil {
		s.handleDBError(w, err, "review not found")
		return
	}
	comments, err := s.store.GetReviewComments(r.Context(), id)
	if err != nil {
		s.logger.Error("fetching review comments", "error", err, "review_id", id)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load review comments"})
		return
	}

	writeJSON(w, http.StatusOK, ReviewDetailResponse{Review: review, Comments: comments})
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
	// Refuse if the previous run is still live (e.g. a review cancelled but not
	// yet halted whose run is still executing): retrying now would double-run
	// the pipeline and post twice. Synchronous so we can surface 409.
	if err := s.orchestrator.EnsureNotRunning(r.Context(), id); err != nil {
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
	launchErr := s.launcher.Launch(pipeline.LaunchSpec{
		Repo:     repo.FullName,
		PR:       review.PRNumber,
		BaseCtx:  obs.SetTraceID(context.Background(), obs.TraceID(r.Context())),
		ReviewID: &id,
		BeforeSpawn: func(bsCtx context.Context) error {
			return s.store.UpdateReviewStatus(bsCtx, id, "pending", "", nil)
		},
		Run: func(ctx context.Context) error { return s.orchestrator.RetryReview(ctx, id) },
		OnDone: func(err error) {
			// context.Canceled means a Stop halted the retry — the state machine
			// already marked it cancelled, so it isn't a failure to log. The
			// rollback + EventError for real failures are owned by the launcher.
			if err != nil && !errors.Is(err, context.Canceled) {
				s.logger.Error("retry review failed", "error", err, "review_id", id)
			}
		},
	})
	if errors.Is(launchErr, pipeline.ErrInFlight) {
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

	// Terminal state: send final event and close
	if review.Status == "completed" || review.Status == "failed" {
		evtType := pipeline.EventCompleted
		if review.Status == "failed" {
			evtType = pipeline.EventError
		}
		_ = wsjson.Write(ctx, conn, pipeline.Event{
			Type:      evtType,
			Timestamp: time.Now(),
			Data:      mustMarshal(map[string]string{"status": review.Status}),
		})
		conn.Close(websocket.StatusNormalClosure, "review already "+review.Status)
		return
	}

	if s.eventBus == nil {
		conn.Close(websocket.StatusInternalError, "streaming not available")
		return
	}

	events, history, unsub := s.eventBus.Subscribe(id)
	if events == nil {
		conn.Close(websocket.StatusNormalClosure, "no active stream")
		return
	}
	defer unsub()

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

	// Replay history
	for _, evt := range history {
		if err := wsjson.Write(ctx, conn, evt); err != nil {
			return
		}
	}

	// Stream live events
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-events:
			if !ok {
				conn.Close(websocket.StatusNormalClosure, "stream ended")
				return
			}
			if err := wsjson.Write(ctx, conn, evt); err != nil {
				return
			}
		}
	}
}

func mustMarshal(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic("mustMarshal: " + err.Error())
	}
	return b
}
