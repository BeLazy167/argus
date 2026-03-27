package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"

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
	if err := s.store.UpdateReviewStatus(r.Context(), id, "pending", "", nil); err != nil {
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
