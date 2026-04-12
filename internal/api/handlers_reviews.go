package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"

	"github.com/BeLazy167/argus/internal/pipeline"
)

type exportFinding struct {
	File       string `json:"file"`
	Line       int    `json:"line,omitempty"`
	Priority   string `json:"priority"`
	Confidence int    `json:"confidence,omitempty"`
	Category   string `json:"category,omitempty"`
	Severity   string `json:"severity,omitempty"`
	Body       string `json:"body"`
	Specialist string `json:"specialist,omitempty"`
	Dropped    bool   `json:"dropped,omitempty"` // true = generated but filtered out (dedup/scoring)
}

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

	writeJSON(w, http.StatusOK, map[string]any{
		"review":   review,
		"comments": comments,
	})
}

func (s *Server) exportReview(w http.ResponseWriter, r *http.Request) {
	ids := getInstallationIDs(r.Context())

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

	findings := make([]exportFinding, 0, len(comments))
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

		findings = append(findings, exportFinding{
			File:       c.FilePath,
			Line:       line,
			Priority:   prio,
			Confidence: conf,
			Category:   cat,
			Severity:   sev,
			Body:       c.Body,
			Specialist: spec,
		})
	}

	// Merge in dropped findings from the unfiltered pipeline payload.
	// These are comments the LLM generated but were filtered by dedup/scoring.
	rawPayload, perr := s.store.Q.GetAllFileReviewsForReview(r.Context(), id)
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
						findings = append(findings, exportFinding{
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

		// Count posted vs dropped
		posted, dropped := 0, 0
		for _, f := range findings {
			if f.Dropped {
				dropped++
			} else {
				posted++
			}
		}

		var sb strings.Builder
		sb.WriteString("# Argus Review Export\n\n")
		sb.WriteString(fmt.Sprintf("- **Review:** %s\n", id.String()))
		sb.WriteString(fmt.Sprintf("- **PR:** #%d\n", review.PRNumber))
		sb.WriteString(fmt.Sprintf("- **Score:** %d/10\n", review.Score))
		sb.WriteString(fmt.Sprintf("- **Posted findings:** %d\n", posted))
		if dropped > 0 {
			sb.WriteString(fmt.Sprintf("- **Dropped findings:** %d _(filtered by dedup/scoring)_\n", dropped))
		}
		sb.WriteString(fmt.Sprintf("- **Total findings:** %d\n\n", len(findings)))

		// Group by file
		fileGroups := make(map[string][]exportFinding)
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
				dropMark := ""
				if f.Dropped {
					dropMark = " _(dropped)_"
				}
				sb.WriteString(fmt.Sprintf("### %s L%d — %s [%s]%s\n\n", f.Priority, f.Line, f.Category, f.Severity, dropMark))
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

		export := map[string]any{
			"review_id":      id.String(),
			"pr_number":      review.PRNumber,
			"pr_title":       review.PRTitle,
			"score":          review.Score,
			"status":         review.Status,
			"total_findings": len(findings),
			"findings":       findings,
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
	if review.Status != "failed" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "only failed reviews can be retried"})
		return
	}
	if err := s.store.UpdateReviewStatus(r.Context(), id, "pending", "", nil); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "update failed"})
		return
	}

	// Open event bus topic before goroutine so WebSocket can subscribe immediately.
	if s.eventBus != nil {
		s.eventBus.OpenTopic(id)
	}

	go func() {
		if s.eventBus != nil {
			defer s.eventBus.CloseTopic(id)
		}
		if err := s.orchestrator.RetryReview(context.Background(), id); err != nil {
			s.logger.Error("retry review failed", "error", err, "review_id", id)
		}
	}()

	writeJSON(w, http.StatusAccepted, map[string]string{"status": "retrying", "review_id": id.String()})
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
