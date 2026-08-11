package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/BeLazy167/argus/backend/internal/store"
)

type MirrorPayload struct {
	CustomID string         `json:"custom_id"`
	Repo     string         `json:"repo,omitempty"`
	Shared   bool           `json:"shared,omitempty"`
	Enabled  bool           `json:"enabled,omitempty"`
	Pattern  *PatternMemory `json:"pattern,omitempty"`
	Rule     *RuleMemory    `json:"rule,omitempty"`
}

func NewPatternMirrorPayload(customID, repo string, shared bool, pattern PatternMemory) (json.RawMessage, error) {
	if customID == "" || pattern.Content == "" {
		return nil, fmt.Errorf("pattern mirror payload requires custom_id and content")
	}
	pattern.CustomID = customID
	payload, err := json.Marshal(MirrorPayload{CustomID: customID, Repo: repo, Shared: shared, Pattern: &pattern})
	if err != nil {
		return nil, fmt.Errorf("marshal pattern mirror payload: %w", err)
	}
	return payload, nil
}

// NewDeleteMirrorPayload retains the deterministic identity needed after the
// relational source row has been removed.
func NewDeleteMirrorPayload(customID string) (json.RawMessage, error) {
	if customID == "" {
		return nil, fmt.Errorf("delete mirror payload requires custom_id")
	}
	payload, err := json.Marshal(MirrorPayload{CustomID: customID})
	if err != nil {
		return nil, fmt.Errorf("marshal delete mirror payload: %w", err)
	}
	return payload, nil
}

func NewRuleMirrorPayload(rule RuleMemory, enabled bool) (json.RawMessage, error) {
	if rule.RuleID <= 0 || rule.Content == "" {
		return nil, fmt.Errorf("rule mirror payload requires rule_id and content")
	}
	payload, err := json.Marshal(MirrorPayload{CustomID: RuleCustomID(rule.RuleID), Enabled: enabled, Rule: &rule})
	if err != nil {
		return nil, fmt.Errorf("marshal rule mirror payload: %w", err)
	}
	return payload, nil
}

type mirrorOutbox interface {
	ClaimMemoryMirrorEvents(context.Context, int, time.Duration) ([]store.MemoryMirrorOutboxEvent, error)
	MarkMemoryMirrorEventProcessed(context.Context, store.MemoryMirrorOutboxEvent) error
	MarkMemoryMirrorEventFailed(context.Context, store.MemoryMirrorOutboxEvent, error) error
}

type MirrorIndexer interface {
	IndexRule(context.Context, string, RuleMemory) error
	IndexPattern(context.Context, string, PatternMemory) (*IndexResult, error)
	IndexSharedPattern(context.Context, PatternMemory) (*IndexResult, error)
	DeleteDocument(context.Context, string) error
}

type MirrorWorker struct {
	outbox     mirrorOutbox
	getIndexer func(context.Context, int64) MirrorIndexer
	logger     *slog.Logger
	staleAfter time.Duration
}

func NewMirrorWorker(outbox mirrorOutbox, getIndexer func(context.Context, int64) MirrorIndexer, logger *slog.Logger) *MirrorWorker {
	if logger == nil {
		logger = slog.Default()
	}
	return &MirrorWorker{outbox: outbox, getIndexer: getIndexer, logger: logger, staleAfter: 5 * time.Minute}
}

// RunOnce claims and applies a bounded page. Each operation is idempotent:
// upserts use deterministic custom IDs and deletes are repeatable soft deletes.
func (w *MirrorWorker) RunOnce(ctx context.Context, limit int) (int, error) {
	events, err := w.outbox.ClaimMemoryMirrorEvents(ctx, limit, w.staleAfter)
	if err != nil {
		return 0, err
	}
	processed := 0
	failures := make([]error, 0)
	for _, event := range events {
		applyErr := w.apply(ctx, event)
		if applyErr != nil {
			if markErr := w.outbox.MarkMemoryMirrorEventFailed(ctx, event, applyErr); markErr != nil {
				applyErr = errors.Join(applyErr, markErr)
			}
			failures = append(failures, fmt.Errorf("mirror event %d: %w", event.ID, applyErr))
			continue
		}
		if err := w.outbox.MarkMemoryMirrorEventProcessed(ctx, event); err != nil {
			failures = append(failures, err)
			continue
		}
		processed++
	}
	return processed, errors.Join(failures...)
}

func (w *MirrorWorker) apply(ctx context.Context, event store.MemoryMirrorOutboxEvent) error {
	var payload MirrorPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("decode payload: %w", err)
	}
	if payload.CustomID == "" {
		return fmt.Errorf("payload has empty custom_id")
	}
	indexer := w.getIndexer(ctx, event.InstallationID)
	if indexer == nil {
		return fmt.Errorf("memory indexer unavailable for installation %d", event.InstallationID)
	}
	if event.Operation == store.MemoryMirrorDelete {
		return indexer.DeleteDocument(ctx, payload.CustomID)
	}
	if event.Operation != store.MemoryMirrorUpsert {
		return fmt.Errorf("unsupported operation %q", event.Operation)
	}
	switch event.AggregateType {
	case store.MemoryMirrorPattern:
		if payload.Pattern == nil || payload.Pattern.Content == "" {
			return fmt.Errorf("pattern payload is incomplete")
		}
		payload.Pattern.CustomID = payload.CustomID
		if payload.Shared {
			_, err := indexer.IndexSharedPattern(ctx, *payload.Pattern)
			return err
		}
		if payload.Repo == "" {
			return fmt.Errorf("repo pattern payload has empty repo")
		}
		_, err := indexer.IndexPattern(ctx, payload.Repo, *payload.Pattern)
		return err
	case store.MemoryMirrorRule:
		if payload.Rule == nil || payload.Rule.RuleID != event.AggregateID {
			return fmt.Errorf("rule payload is incomplete or mismatched")
		}
		if !payload.Enabled {
			return indexer.DeleteDocument(ctx, payload.CustomID)
		}
		return indexer.IndexRule(ctx, "", *payload.Rule)
	default:
		return fmt.Errorf("unsupported aggregate type %q", event.AggregateType)
	}
}

// Run drains ready events and polls until ctx is cancelled.
func (w *MirrorWorker) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		for {
			processed, err := w.RunOnce(ctx, 1)
			if err != nil {
				if ctx.Err() == nil {
					w.logger.Warn("memory mirror outbox", "error", err)
				}
				break
			}
			if processed == 0 {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
