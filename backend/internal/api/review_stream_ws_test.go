package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BeLazy167/argus/backend/internal/pipeline"
	"github.com/google/uuid"
	"nhooyr.io/websocket" //nolint:staticcheck // Matches the production websocket transport.
	"nhooyr.io/websocket/wsjson"
)

//nolint:staticcheck // The production websocket seam still uses nhooyr.io/websocket.
func TestReviewStreamSubscriberCloseContract(t *testing.T) {
	tests := []struct {
		name       string
		reason     pipeline.SubscriberCloseReason
		event      *pipeline.Event
		wantStatus websocket.StatusCode
	}{
		{
			name:       "durable overflow asks browser to reconnect",
			reason:     pipeline.SubscriberCloseDurableOverflow,
			wantStatus: websocket.StatusTryAgainLater,
		},
		{
			name:       "dedup exhaustion asks browser to reconnect",
			reason:     pipeline.SubscriberCloseDedupExhausted,
			wantStatus: websocket.StatusTryAgainLater,
		},
		{
			name:       "bounded replay page asks browser for the next page",
			reason:     pipeline.SubscriberCloseReplayPageExhausted,
			wantStatus: websocket.StatusTryAgainLater,
		},
		{
			name:       "terminal completion stays clean",
			reason:     pipeline.SubscriberCloseTopic,
			event:      &pipeline.Event{Type: pipeline.EventCompleted},
			wantStatus: websocket.StatusNormalClosure,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					t.Errorf("accept: %v", err)
					return
				}
				defer func() { _ = conn.CloseNow() }()
				events := make(chan pipeline.Event, 1)
				closed := make(chan pipeline.SubscriberCloseReason, 1)
				if tc.event != nil {
					events <- *tc.event
				}
				closed <- tc.reason
				close(closed)
				close(events)
				streamLiveReviewEvents(r.Context(), conn, events, closed)
			}))
			defer server.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conn.CloseNow() }()
			if tc.event != nil {
				var got pipeline.Event
				if err := wsjson.Read(ctx, conn, &got); err != nil {
					t.Fatalf("read terminal event: %v", err)
				}
				if got.Type != tc.event.Type {
					t.Fatalf("event=%q want %q", got.Type, tc.event.Type)
				}
			}
			_, _, err = conn.Read(ctx)
			if status := websocket.CloseStatus(err); status != tc.wantStatus {
				t.Fatalf("close status=%d want %d (err=%v)", status, tc.wantStatus, err)
			}
		})
	}
}

//nolint:staticcheck // The production websocket seam still uses nhooyr.io/websocket.
func TestWriteReviewStreamReconcilesTerminalStatusAfterSubscription(t *testing.T) {
	tests := []struct {
		name     string
		status   string
		terminal pipeline.EventType
		arrange  func(*pipeline.EventBus, uuid.UUID)
		recheck  func(*pipeline.EventBus, uuid.UUID) func(context.Context) (string, error)
	}{
		{
			name:     "terminal before subscribe survives lost ephemeral topic",
			status:   "failed",
			terminal: pipeline.EventError,
			arrange: func(bus *pipeline.EventBus, reviewID uuid.UUID) {
				bus.OpenTopic(reviewID)
				bus.Publish(reviewID, pipeline.EventError, map[string]string{"source": "lost-local"})
				// A durable subscription replaces the closed topic and can replay
				// only PostgreSQL rows. Model that replacement after the ID=0 event.
				bus.OpenTopic(reviewID)
			},
		},
		{
			name:     "terminal between subscribe and recheck is live and converges once",
			status:   "cancelled",
			terminal: pipeline.EventCancelled,
			arrange: func(bus *pipeline.EventBus, reviewID uuid.UUID) {
				bus.OpenTopic(reviewID)
			},
			recheck: func(bus *pipeline.EventBus, reviewID uuid.UUID) func(context.Context) (string, error) {
				return func(context.Context) (string, error) {
					bus.Publish(reviewID, pipeline.EventCancelled, map[string]string{"source": "live-race"})
					return "cancelled", nil
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bus := pipeline.NewEventBus()
			reviewID := uuid.New()
			tc.arrange(bus, reviewID)
			recheck := func(context.Context) (string, error) { return tc.status, nil }
			if tc.recheck != nil {
				recheck = tc.recheck(bus, reviewID)
			}

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					t.Errorf("accept: %v", err)
					return
				}
				defer func() { _ = conn.CloseNow() }()
				_ = writeReviewStream(r.Context(), conn, bus, reviewID, 0, recheck)
			}))
			defer server.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conn.CloseNow() }()

			var got pipeline.Event
			if err := wsjson.Read(ctx, conn, &got); err != nil {
				t.Fatalf("read terminal event: %v", err)
			}
			if got.Type != tc.terminal || got.ID != 0 {
				t.Fatalf("terminal event=%+v want synthesized %q with ID 0", got, tc.terminal)
			}
			if !strings.Contains(string(got.Data), `"status":"`+tc.status+`"`) {
				t.Fatalf("terminal data=%s want reconciled status %q", got.Data, tc.status)
			}
			if strings.Contains(string(got.Data), "source") {
				t.Fatalf("terminal data=%s came from the raced local event, not reconciliation", got.Data)
			}
			_, _, err = conn.Read(ctx)
			if status := websocket.CloseStatus(err); status != websocket.StatusNormalClosure {
				t.Fatalf("close status=%d want %d (err=%v)", status, websocket.StatusNormalClosure, err)
			}
		})
	}
}
