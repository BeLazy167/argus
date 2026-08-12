package pipeline

import (
	"errors"
	"testing"

	"github.com/BeLazy167/argus/backend/internal/store"
)

func TestDurablePostOutcomeContinuesPastCleanupFailure(t *testing.T) {
	cleanupErr := errors.New("releasing review post authority: connection lost")
	for _, outcome := range []store.ReviewPostOutcome{store.ReviewPostRecorded, store.ReviewPostAlreadyRecorded} {
		if !durablePostSucceeded(1234, outcome, cleanupErr) {
			t.Fatalf("outcome %q with durable id false-failed", outcome)
		}
	}
	if durablePostSucceeded(1234, store.ReviewPostAttempted, cleanupErr) {
		t.Fatal("ambiguous attempt treated as durable")
	}
	if durablePostSucceeded(0, store.ReviewPostRecorded, cleanupErr) {
		t.Fatal("zero id treated as durable")
	}
}
