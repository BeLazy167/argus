package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"testing"
)

type fakeFlags struct {
	raw string
	err error
}

func (f fakeFlags) GetInstallationFeatureFlags(context.Context, int64) (json.RawMessage, error) {
	if f.err != nil {
		return nil, f.err
	}
	return json.RawMessage(f.raw), nil
}

// TestBackendSkipPolarity pins both refusals. This tool writes Supermemory
// only, so proceeding against a Postgres-flipped install writes a Supermemory
// doc id over patterns.memory_doc_id — which for that install holds a
// PGIndexer custom_id — after which dashboard pattern deletion matches zero
// rows and returns 200 while never tombstoning the memory.
//
// The second refusal is the one that is easy to omit: a flag READ ERROR must
// also skip. Assuming Supermemory there makes the guard fail OPEN, so a single
// pool timeout during a sweep performs exactly the corruption it exists to
// prevent.
func TestBackendSkipPolarity(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cases := map[string]struct {
		flags    fakeFlags
		wantSkip bool
		want     installStatus
	}{
		"postgres install skips":   {fakeFlags{raw: `{"memory_backend":"postgres"}`}, true, statusNotSupermemory},
		"unreadable flag skips":    {fakeFlags{err: fmt.Errorf("pool exhausted")}, true, statusBackendUnknown},
		"supermemory install runs": {fakeFlags{raw: `{"memory_backend":"supermemory"}`}, false, statusProcessed},
		"absent flag runs":         {fakeFlags{raw: `{}`}, false, statusProcessed},
		"unrecognized value runs":  {fakeFlags{raw: `{"memory_backend":"Postgres"}`}, false, statusProcessed},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			status, skip := backendSkip(context.Background(), logger, c.flags, 42)
			if skip != c.wantSkip {
				t.Errorf("skip = %v, want %v", skip, c.wantSkip)
			}
			if status != c.want {
				t.Errorf("status = %v, want %v", status, c.want)
			}
		})
	}
}
