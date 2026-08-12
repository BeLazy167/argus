package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/BeLazy167/argus/backend/internal/obs"
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

// legacyPatternMirrorCustomID reconstructs the memory identity for patterns
// written before patterns.memory_doc_id and memory_custom_id were populated.
// Pipeline sources need their historical source-segment mapping; other sources
// use the current pattern writer's canonical derivation.
func legacyPatternMirrorCustomID(payload MirrorPayload) (string, error) {
	if payload.Pattern == nil || payload.Pattern.Content == "" {
		return "", fmt.Errorf("legacy pattern payload is incomplete")
	}
	if !payload.Shared && payload.Repo == "" {
		return "", fmt.Errorf("legacy repo pattern payload has empty repo")
	}
	category := payload.Pattern.Category
	if customID := PipelinePatternCustomID(payload.Repo, payload.Pattern.Source, payload.Pattern.Content, &category, payload.Shared); customID != "" {
		return customID, nil
	}
	if payload.Shared {
		return SharedPatternCustomID(patternSource(*payload.Pattern), payload.Pattern.Content), nil
	}
	return PatternCustomID("", payload.Repo, patternSource(*payload.Pattern), payload.Pattern.Content), nil
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
	BindMemoryMirrorEventCustomID(context.Context, store.MemoryMirrorOutboxEvent, string) error
	ProcessMemoryMirrorEvent(context.Context, store.MemoryMirrorOutboxEvent, string, store.MemoryMirrorLegacyOwner, store.MemoryMirrorApply) error
	MarkMemoryMirrorEventFailed(context.Context, store.MemoryMirrorOutboxEvent, error) error
}

type MirrorIndexer interface {
	IndexRule(context.Context, string, RuleMemory) error
	// MirrorPattern repairs relational state without replacing an already-live
	// pipeline write, whose review attribution and metadata are richer.
	MirrorPattern(context.Context, string, bool, PatternMemory) error
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
	operationID := obs.NewLogID()
	started := time.Now()
	w.logger.DebugContext(ctx, "memory mirror cycle started", "operation_id", operationID, "limit", limit)
	processed, err := w.runOnce(ctx, limit)
	level := slog.LevelDebug
	if processed > 0 {
		level = slog.LevelInfo
	}
	message := "memory mirror cycle completed"
	if err != nil {
		level = slog.LevelError
		message = "memory mirror cycle failed"
	}
	w.logger.Log(ctx, level, message, "operation_id", operationID, "limit", limit,
		"processed", processed, "duration_ms", time.Since(started).Milliseconds(), "error", err)
	return processed, err
}

func (w *MirrorWorker) runOnce(ctx context.Context, limit int) (int, error) {
	events, err := w.outbox.ClaimMemoryMirrorEvents(ctx, limit, w.staleAfter)
	if err != nil {
		return 0, err
	}
	processed := 0
	failures := make([]error, 0)
	for _, event := range events {
		applyErr := w.process(ctx, event)
		if applyErr != nil {
			if markErr := w.outbox.MarkMemoryMirrorEventFailed(ctx, event, applyErr); markErr != nil {
				applyErr = errors.Join(applyErr, markErr)
			}
			failures = append(failures, fmt.Errorf("mirror event %d: %w", event.ID, applyErr))
			continue
		}
		processed++
	}
	return processed, errors.Join(failures...)
}

func (w *MirrorWorker) process(ctx context.Context, event store.MemoryMirrorOutboxEvent) error {
	var payload MirrorPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("decode payload: %w", err)
	}
	if payload.CustomID == "" && event.AggregateType == store.MemoryMirrorPattern {
		customID, err := legacyPatternMirrorCustomID(payload)
		if err != nil {
			return err
		}
		if err := w.outbox.BindMemoryMirrorEventCustomID(ctx, event, customID); err != nil {
			return err
		}
		payload.CustomID = customID
	}
	if payload.CustomID == "" {
		return fmt.Errorf("payload has empty custom_id")
	}

	var legacyOwner store.MemoryMirrorLegacyOwner
	if event.AggregateType == store.MemoryMirrorPattern && event.Operation == store.MemoryMirrorDelete {
		legacyOwner = func(identity store.MemoryMirrorPatternIdentity) (bool, error) {
			category := identity.Category
			candidate := MirrorPayload{
				Repo: identity.Repo, Shared: identity.Shared,
				Pattern: &PatternMemory{
					Content: identity.Content, Source: identity.Source, Category: category,
				},
			}
			candidateID, err := legacyPatternMirrorCustomID(candidate)
			if err != nil {
				return false, fmt.Errorf("derive legacy pattern %d identity: %w", identity.ID, err)
			}
			return candidateID == payload.CustomID, nil
		}
	}
	return w.outbox.ProcessMemoryMirrorEvent(ctx, event, payload.CustomID, legacyOwner, func(ctx context.Context, deleteAuthorized bool) error {
		if event.AggregateType == store.MemoryMirrorPattern && event.Operation == store.MemoryMirrorDelete && !deleteAuthorized {
			return nil
		}
		return w.applyPayload(ctx, event, payload)
	})
}

func (w *MirrorWorker) applyPayload(ctx context.Context, event store.MemoryMirrorOutboxEvent, payload MirrorPayload) error {
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
		if !payload.Shared && payload.Repo == "" {
			return fmt.Errorf("repo pattern payload has empty repo")
		}
		return indexer.MirrorPattern(ctx, payload.Repo, payload.Shared, *payload.Pattern)
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
	operationID := obs.NewLogID()
	started := time.Now()
	w.logger.InfoContext(ctx, "memory mirror worker started", "operation_id", operationID, "interval", interval)
	defer func() {
		w.logger.InfoContext(context.WithoutCancel(ctx), "memory mirror worker stopped",
			"operation_id", operationID, "duration_ms", time.Since(started).Milliseconds(), "error", ctx.Err())
	}()
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
