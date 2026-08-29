package app

import (
	"testing"
	"time"
)

func TestGraphIndexWorstCaseHourlyGitHubBudget(t *testing.T) {
	if graphIndexMaxCallsPerWindow != graphIndexFixedCallsPerWindow+graphIndexCallsPerFile*graphIndexFileCap {
		t.Fatalf("call accounting = %d, want ref + commit/tree + one immutable blob call per file", graphIndexMaxCallsPerWindow)
	}
	// A reservation immediately before GitHub resets its quota can make its calls
	// land in the next hour. Include that straddling window in the actual-call
	// bound rather than counting reservation timestamps alone.
	windowsPerQuotaHour := int(graphIndexBudgetHour/graphIndexMinimumSpacing) + 1
	got := graphIndexMaxCallsPerWindow * windowsPerQuotaHour
	if got > graphIndexMaxCallsPerHour {
		t.Fatalf("worst-case graph calls/hour = %d, budget = %d", got, graphIndexMaxCallsPerHour)
	}
	if minimumGitHubInstallationQuota-got < graphIndexReservedReviewCallsPerHour {
		t.Fatalf("only %d minimum-quota calls remain for live reviews", minimumGitHubInstallationQuota-got)
	}
}

func TestGraphIndexLargeRepoCompletionBound(t *testing.T) {
	const sourceFiles = 1371
	windows := (sourceFiles + graphIndexFileCap - 1) / graphIndexFileCap
	elapsedFromFirstWindow := time.Duration(windows-1) * graphIndexMinimumSpacing
	if windows != 15 || elapsedFromFirstWindow > 168*time.Minute {
		t.Fatalf("1371-file completion = %d windows/%s from first window, want 15/<=168m", windows, elapsedFromFirstWindow)
	}
}
