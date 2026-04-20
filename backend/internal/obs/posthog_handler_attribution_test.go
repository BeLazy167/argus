package obs

// Regression guard for the SHIP-BLOCKER fixed in PR #76: verifies that events
// fired with ONLY ctx-based attribution (SetClerkUser / SetGithubLogin /
// SetInstallationID) resolve a real distinct_id and do NOT land in the
// droppedUnattributed bucket.
//
// If any future refactor removes the SetX plumbing at the call sites
// (jwtAuth middleware, HandlePREvent, webhook detach goroutines), this test
// will NOT catch that directly — but it pins the contract that the Handler
// itself honors ctx-based attribution so the call-site plumbing has a
// guaranteed receiver.

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

// TestAttribution_CtxOnly_NoDropped asserts that each of the three ctx-based
// attribution routes (ClerkUser, GithubLogin, InstallationID) produces a
// valid distinct_id without any slog.Attr hint, and that no events end up
// unattributed.
func TestAttribution_CtxOnly_NoDropped(t *testing.T) {
	m := newMockServer(false)
	defer m.Close()

	cli := newIntegrationClient(t, m.URL())
	inner := slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug})
	h := NewPostHogHandler(inner, cli)
	logger := slog.New(h)

	// Case 1: Clerk user → distinct_id = "u_123" (no prefix).
	ctxClerk := SetClerkUser(context.Background(), "u_123")
	logger.InfoContext(ctxClerk, "review started",
		slog.String("event", "review.started"),
		slog.String("repo", "octo/hello"),
	)

	// Case 2: GitHub login (no Clerk) → distinct_id = "github:<login>".
	ctxGH := SetGithubLogin(context.Background(), "author")
	logger.InfoContext(ctxGH, "webhook received",
		slog.String("event", "webhook.received"),
		slog.String("repo", "octo/hello"),
	)

	// Case 3: Installation only → distinct_id = "installation:456".
	ctxInst := SetInstallationID(context.Background(), 456)
	logger.InfoContext(ctxInst, "stage completed",
		slog.String("event", "stage.completed"),
		slog.String("stage", "review"),
		slog.String("repo", "octo/hello"),
	)

	// Flush handler then client so all batches land on the mock.
	if err := h.Close(); err != nil {
		t.Fatalf("handler close: %v", err)
	}
	if err := cli.Close(); err != nil {
		t.Fatalf("client close: %v", err)
	}

	// Poll until all 3 arrive (drain + HTTP flush are async).
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && len(m.snapshot()) < 3 {
		time.Sleep(20 * time.Millisecond)
	}
	events := m.snapshot()
	if len(events) != 3 {
		t.Fatalf("want 3 events, got %d", len(events))
	}

	// Build an event→distinct_id map for assertion.
	got := make(map[string]string, 3)
	for _, ev := range events {
		name, _ := ev["event"].(string)
		id, _ := ev["distinct_id"].(string)
		got[name] = id
	}
	want := map[string]string{
		"review.started":   "u_123",
		"webhook.received": "github:author",
		"stage.completed":  "installation:456",
	}
	for name, wantID := range want {
		if gotID := got[name]; gotID != wantID {
			t.Fatalf("event %q: distinct_id = %q, want %q", name, gotID, wantID)
		}
	}

	// The real assertion — zero unattributed events. Any regression where
	// someone deletes the SetX ctx plumbing or the handler's ctx fallback
	// would bump this counter above zero.
	if dropped := h.DroppedUnattributed(); dropped != 0 {
		t.Fatalf("DroppedUnattributed = %d, want 0 — ctx attribution regression", dropped)
	}
}

// TestAttribution_EmptyCtx_DropsToCounter is the inverse test — with NO
// attribution at all, events MUST be dropped and the counter MUST bump.
// Without this, a future bug where the counter stops incrementing would go
// unnoticed (silent failure).
func TestAttribution_EmptyCtx_DropsToCounter(t *testing.T) {
	m := newMockServer(false)
	defer m.Close()

	cli := newIntegrationClient(t, m.URL())
	inner := slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug})
	h := NewPostHogHandler(inner, cli)
	logger := slog.New(h)

	// No ctx attribution, no slog attr hints either.
	logger.InfoContext(context.Background(), "orphan event",
		slog.String("event", "orphan"),
	)

	if err := h.Close(); err != nil {
		t.Fatalf("handler close: %v", err)
	}
	if err := cli.Close(); err != nil {
		t.Fatalf("client close: %v", err)
	}

	if dropped := h.DroppedUnattributed(); dropped != 1 {
		t.Fatalf("DroppedUnattributed = %d, want 1", dropped)
	}
	if sent := h.Sent(); sent != 0 {
		t.Fatalf("Sent = %d, want 0 (unattributed must not reach Enqueue)", sent)
	}
}
