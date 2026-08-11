package memory

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/BeLazy167/argus/backend/internal/store"
)

type fakeMirrorOutbox struct {
	events    []store.MemoryMirrorOutboxEvent
	succeeded []int64
	failed    []int64
}

func (f *fakeMirrorOutbox) ClaimMemoryMirrorEvents(_ context.Context, _ int, _ time.Duration) ([]store.MemoryMirrorOutboxEvent, error) {
	return f.events, nil
}
func (f *fakeMirrorOutbox) MarkMemoryMirrorEventProcessed(_ context.Context, event store.MemoryMirrorOutboxEvent) error {
	f.succeeded = append(f.succeeded, event.ID)
	return nil
}
func (f *fakeMirrorOutbox) MarkMemoryMirrorEventFailed(_ context.Context, event store.MemoryMirrorOutboxEvent, _ error) error {
	f.failed = append(f.failed, event.ID)
	return nil
}

type fakeMirrorIndexer struct {
	upserted []string
	deleted  []string
	fail     error
}

func (f *fakeMirrorIndexer) IndexRule(_ context.Context, _ string, r RuleMemory) error {
	f.upserted = append(f.upserted, RuleCustomID(r.RuleID))
	return f.fail
}
func (f *fakeMirrorIndexer) IndexPattern(_ context.Context, _ string, p PatternMemory) (*IndexResult, error) {
	f.upserted = append(f.upserted, p.CustomID)
	return &IndexResult{ID: p.CustomID}, f.fail
}
func (f *fakeMirrorIndexer) IndexSharedPattern(_ context.Context, p PatternMemory) (*IndexResult, error) {
	f.upserted = append(f.upserted, p.CustomID)
	return &IndexResult{ID: p.CustomID}, f.fail
}
func (f *fakeMirrorIndexer) DeleteDocument(_ context.Context, id string) error {
	f.deleted = append(f.deleted, id)
	return f.fail
}

func TestMirrorWorkerAppliesIdempotentOperationsAndAcknowledges(t *testing.T) {
	t.Parallel()
	patternPayload, _ := NewPatternMirrorPayload("pattern--1", "repo", false, PatternMemory{Content: "guard writes", CustomID: "pattern--1", Source: "dashboard"})
	disabledRulePayload, _ := NewRuleMirrorPayload(RuleMemory{RuleID: 2, Content: "do not panic"}, false)
	outbox := &fakeMirrorOutbox{events: []store.MemoryMirrorOutboxEvent{
		{ID: 1, InstallationID: 7, AggregateType: "pattern", AggregateID: 1, Operation: "upsert", Payload: patternPayload},
		{ID: 2, InstallationID: 7, AggregateType: "rule", AggregateID: 2, Operation: "upsert", Payload: disabledRulePayload},
	}}
	indexer := &fakeMirrorIndexer{}
	worker := NewMirrorWorker(outbox, func(context.Context, int64) MirrorIndexer { return indexer }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	processed, err := worker.RunOnce(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if processed != 2 || len(outbox.succeeded) != 2 || len(outbox.failed) != 0 {
		t.Fatalf("processed=%d success=%v failed=%v", processed, outbox.succeeded, outbox.failed)
	}
	if len(indexer.upserted) != 1 || indexer.upserted[0] != "pattern--1" {
		t.Fatalf("upserted=%v", indexer.upserted)
	}
	if len(indexer.deleted) != 1 || indexer.deleted[0] != RuleCustomID(2) {
		t.Fatalf("deleted=%v", indexer.deleted)
	}
}

func TestMirrorWorkerRetriesFailuresWithoutBlockingLaterEvents(t *testing.T) {
	t.Parallel()
	payload, _ := NewPatternMirrorPayload("p", "", true, PatternMemory{Content: "x", CustomID: "p"})
	outbox := &fakeMirrorOutbox{events: []store.MemoryMirrorOutboxEvent{{ID: 1, InstallationID: 7, AggregateType: "pattern", Operation: "upsert", Payload: payload}, {ID: 2, InstallationID: 7, AggregateType: "pattern", Operation: "delete", Payload: payload}}}
	indexer := &fakeMirrorIndexer{fail: errors.New("memory unavailable")}
	worker := NewMirrorWorker(outbox, func(context.Context, int64) MirrorIndexer { return indexer }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	processed, err := worker.RunOnce(context.Background(), 10)
	if err == nil || processed != 0 || len(outbox.failed) != 2 {
		t.Fatalf("processed=%d err=%v failed=%v", processed, err, outbox.failed)
	}
}
