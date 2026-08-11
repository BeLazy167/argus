package app

import "testing"

func TestGraphIndexWorstCaseHourlyGitHubBudget(t *testing.T) {
	if graphIndexMaxCallsPerWindow != 3+2*graphIndexFileCap {
		t.Fatalf("call accounting = %d, want branch + commit/tree + two calls per file", graphIndexMaxCallsPerWindow)
	}
	got := graphIndexMaxCallsPerWindow * int(graphIndexBudgetHour/graphIndexMinimumSpacing)
	if got > graphIndexMaxCallsPerHour {
		t.Fatalf("worst-case graph calls/hour = %d, budget = %d", got, graphIndexMaxCallsPerHour)
	}
	if minimumGitHubInstallationQuota-got < graphIndexReservedReviewCallsPerHour {
		t.Fatalf("only %d minimum-quota calls remain for live reviews", minimumGitHubInstallationQuota-got)
	}
}
