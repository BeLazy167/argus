package sast

import (
	"context"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

// Finding represents a single issue found by a SAST tool.
type Finding struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Column   int    `json:"column,omitempty"`
	Rule     string `json:"rule"`
	Message  string `json:"message"`
	Severity string `json:"severity"` // "error", "warning", "info"
}

// Runner is the interface that SAST tool integrations must implement.
type Runner interface {
	Name() string
	CanRun(language string) bool
	Run(ctx context.Context, files map[string]string) ([]Finding, error)
}

// DefaultRunners returns all available SAST runners.
func DefaultRunners() []Runner {
	return []Runner{
		&StaticcheckRunner{},
		&ESLintRunner{},
		&SemgrepRunner{},
	}
}

// RunAll executes all eligible runners in parallel, collecting their findings.
// Each runner gets a 15-second timeout. Runners that error are skipped;
// findings from successful runners are still returned.
func RunAll(ctx context.Context, runners []Runner, language string, files map[string]string) ([]Finding, error) {
	var eligible []Runner
	for _, r := range runners {
		if r.CanRun(language) {
			eligible = append(eligible, r)
		}
	}
	if len(eligible) == 0 {
		return nil, nil
	}

	var mu sync.Mutex
	var all []Finding

	g, gctx := errgroup.WithContext(ctx)
	for _, r := range eligible {
		g.Go(func() error {
			rctx, cancel := context.WithTimeout(gctx, 15*time.Second)
			defer cancel()

			findings, err := r.Run(rctx, files)
			if err != nil {
				// Graceful degradation: skip failed runners.
				return nil
			}
			mu.Lock()
			all = append(all, findings...)
			mu.Unlock()
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return all, err
	}
	return all, nil
}
