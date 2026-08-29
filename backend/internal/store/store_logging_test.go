package store

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
)

type storeLogRecordHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *storeLogRecordHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *storeLogRecordHandler) Handle(_ context.Context, record slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, record.Clone())
	return nil
}
func (h *storeLogRecordHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *storeLogRecordHandler) WithGroup(string) slog.Handler      { return h }

func TestStoreOperationLogsLifecycleAndResultSummary(t *testing.T) {
	handler := &storeLogRecordHandler{}
	previous := slog.Default()
	slog.SetDefault(slog.New(handler))
	t.Cleanup(func() { slog.SetDefault(previous) })

	finish := beginStoreOperation(context.Background(), "ListThings", "repo_id", int64(42))
	finish(nil, []int{1, 2, 3})

	if len(handler.records) != 2 {
		t.Fatalf("record count = %d, want 2", len(handler.records))
	}
	if got := handler.records[0].Message; got != "store operation started" {
		t.Fatalf("start message = %q", got)
	}
	if got := handler.records[1].Message; got != "store operation succeeded" {
		t.Fatalf("finish message = %q", got)
	}
	attrs := recordAttrs(handler.records[1])
	if attrs["operation"] != "ListThings" || attrs["repo_id"] != int64(42) {
		t.Fatalf("completion attrs = %#v", attrs)
	}
	result, ok := attrs["result_1"].(map[string]any)
	if !ok || result["count"] != 3 {
		t.Fatalf("result summary = %#v", attrs["result_1"])
	}
	if _, ok := attrs["duration_ms"]; !ok {
		t.Fatal("completion has no duration_ms")
	}
}

func TestStoreOperationLogsFailure(t *testing.T) {
	handler := &storeLogRecordHandler{}
	previous := slog.Default()
	slog.SetDefault(slog.New(handler))
	t.Cleanup(func() { slog.SetDefault(previous) })

	finish := beginStoreOperation(context.Background(), "WriteThing", "id", int64(7))
	finish(errors.New("write failed"), false)

	if len(handler.records) != 2 {
		t.Fatalf("record count = %d, want 2", len(handler.records))
	}
	if got := handler.records[1].Message; got != "store operation failed" {
		t.Fatalf("failure message = %q", got)
	}
	if handler.records[1].Level != slog.LevelError {
		t.Fatalf("failure level = %v", handler.records[1].Level)
	}
	if _, ok := recordAttrs(handler.records[1])["error"]; !ok {
		t.Fatal("failure record has no error")
	}
}

func recordAttrs(record slog.Record) map[string]any {
	attrs := make(map[string]any)
	record.Attrs(func(attr slog.Attr) bool {
		attrs[attr.Key] = attr.Value.Any()
		return true
	})
	return attrs
}

func TestStoreTransactionDistinguishesRollbackFromCommit(t *testing.T) {
	handler := &storeLogRecordHandler{}
	previous := slog.Default()
	slog.SetDefault(slog.New(handler))
	t.Cleanup(func() { slog.SetDefault(previous) })

	finish := beginStoreTransaction(context.Background(), "GuardedWrite")
	finish(nil, false)
	if got := handler.records[1].Message; got != "store transaction rolled back without error" {
		t.Fatalf("rollback message = %q", got)
	}
	if got := recordAttrs(handler.records[1])["committed"]; got != false {
		t.Fatalf("committed = %#v", got)
	}
}
