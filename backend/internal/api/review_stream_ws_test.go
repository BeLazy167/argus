package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BeLazy167/argus/backend/internal/pipeline"
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
