package store

import (
	"encoding/json"
	"testing"
	"time"
)

func TestMemoryMirrorEventValidation(t *testing.T) {
	t.Parallel()
	valid := MemoryMirrorEvent{InstallationID: 1, AggregateType: MemoryMirrorPattern, AggregateID: 2, Operation: MemoryMirrorUpsert, Payload: json.RawMessage(`{"custom_id":"p"}`)}
	tests := []struct {
		name   string
		mutate func(*MemoryMirrorEvent)
	}{
		{"missing installation", func(e *MemoryMirrorEvent) { e.InstallationID = 0 }},
		{"missing aggregate", func(e *MemoryMirrorEvent) { e.AggregateID = 0 }},
		{"bad type", func(e *MemoryMirrorEvent) { e.AggregateType = "review" }},
		{"bad operation", func(e *MemoryMirrorEvent) { e.Operation = "patch" }},
		{"bad payload", func(e *MemoryMirrorEvent) { e.Payload = json.RawMessage(`{`) }},
	}
	if err := valid.validate(); err != nil {
		t.Fatalf("valid event: %v", err)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := valid
			tt.mutate(&event)
			if err := event.validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestMemoryMirrorRetryBackoffIsBounded(t *testing.T) {
	t.Parallel()
	tests := []struct {
		attempt int
		want    time.Duration
	}{{0, time.Second}, {1, time.Second}, {2, 2 * time.Second}, {12, 2048 * time.Second}, {13, time.Hour}, {100, time.Hour}}
	for _, tt := range tests {
		if got := memoryMirrorRetryBackoff(tt.attempt); got != tt.want {
			t.Errorf("attempt %d=%s want=%s", tt.attempt, got, tt.want)
		}
	}
}
