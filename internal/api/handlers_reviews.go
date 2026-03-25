package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/BeLazy167/argus/internal/pipeline"
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

	writeJSON(w, http.StatusOK, map[string]any{
		"review":   review,
		"comments": comments,
	})
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
	if err := s.store.UpdateReviewStatus(r.Context(), id, "pending", ""); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "update failed"})
		return
	}

	go func() {
		if err := s.orchestrator.RetryReview(context.Background(), id); err != nil {
			s.logger.Error("retry review failed", "error", err, "review_id", id)
		}
	}()

	writeJSON(w, http.StatusAccepted, map[string]string{"status": "retrying", "review_id": id.String()})
}

// --- SSE Stream ---

func (s *Server) streamReview(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "reviewID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid review id"})
		return
	}

	// Auth: verify review belongs to caller's installation scope
	review, err := s.store.GetReview(r.Context(), id)
	if err != nil {
		s.handleDBError(w, err, "review not found")
		return
	}
	if _, err := s.store.GetRepoScoped(r.Context(), review.RepoID, getInstallationIDs(r.Context())); err != nil {
		s.handleDBError(w, err, "review not found")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming not supported"})
		return
	}

	// If review is terminal, send final event and return
	if review.Status == "completed" || review.Status == "failed" {
		setSSEHeaders(w)
		evtType := pipeline.EventCompleted
		if review.Status == "failed" {
			evtType = pipeline.EventError
		}
		writeSSE(w, pipeline.Event{
			Type:      evtType,
			Timestamp: time.Now(),
			Data:      mustMarshal(map[string]string{"status": review.Status}),
		})
		flusher.Flush()
		return
	}

	// Subscribe to live events
	if s.eventBus == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "streaming not available"})
		return
	}

	events, history, unsub := s.eventBus.Subscribe(id)
	if events == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no active stream for this review"})
		return
	}
	defer unsub()

	setSSEHeaders(w)

	// Replay history
	for _, evt := range history {
		if err := writeSSE(w, evt); err != nil {
			return
		}
	}
	flusher.Flush()

	// Stream live events
	for {
		select {
		case <-r.Context().Done():
			return
		case evt, ok := <-events:
			if !ok {
				return // topic closed
			}
			if err := writeSSE(w, evt); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func setSSEHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
}

func writeSSE(w http.ResponseWriter, evt pipeline.Event) error {
	_, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", evt.Type, evt.Data)
	return err
}

func mustMarshal(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic("mustMarshal: " + err.Error())
	}
	return b
}
