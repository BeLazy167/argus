package admission

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakePerms struct {
	allow  bool
	err    error
	asked  int
	lastID string
}

func (f *fakePerms) CanTriggerReview(_ context.Context, actor Actor, repo string) (bool, error) {
	f.asked++
	f.lastID = actor.Login
	return f.allow, f.err
}

type fakeRate struct {
	allow bool
	asked int
}

func (f *fakeRate) AllowReview(string, string, bool) bool { f.asked++; return f.allow }

func baseReq(a Actor) Request {
	return Request{
		Actor:        a,
		RepoFullName: "acme/widgets",
		OrgLogin:     "acme",
		Limits:       DefaultLimits,
		Size:         Size{Files: 3, Lines: 40},
	}
}

// The case that started this work: an outside contributor opens a pull request
// on a public repo and asks for a review.
func TestDecide_RefusesActorWithoutWriteAccess(t *testing.T) {
	perms := &fakePerms{allow: false}
	rate := &fakeRate{allow: true}
	v := New(perms, rate).Decide(context.Background(), baseReq(GitHubActor("outsider")))

	if v.Allowed() {
		t.Fatal("an actor without write access must be refused")
	}
	if !strings.Contains(v.Reason, "write access") {
		t.Errorf("refusal must say why: %q", v.Reason)
	}
	// A refused actor must not spend a rate-limit token, or anyone can exhaust
	// a repo's hourly budget without ever being allowed to run anything.
	if rate.asked != 0 {
		t.Errorf("rate limit consulted %d times for a refused actor; want 0", rate.asked)
	}
}

// THE INVARIANT. On the webhook path the actor carries the pull-request author
// for attribution. Authorizing them would let a fork pull request authorize its
// own review — the same hole, through a different door.
func TestDecide_NeverAuthorizesThePullRequestAuthor(t *testing.T) {
	perms := &fakePerms{allow: false} // would refuse if consulted
	req := baseReq(SystemActor("outside-contributor"))

	v := New(perms, &fakeRate{allow: true}).Decide(context.Background(), req)

	if perms.asked != 0 {
		t.Fatalf("permission was checked for the PR author (%q) — a fork PR would authorize itself", perms.lastID)
	}
	if !v.Allowed() {
		t.Errorf("auto_run is on, so the review must run: %q", v.Reason)
	}
}

func TestDecide_PermissionErrorFailsClosed(t *testing.T) {
	perms := &fakePerms{allow: true, err: errors.New("github down")}

	v := New(perms, &fakeRate{allow: true}).Decide(context.Background(), baseReq(GitHubActor("maintainer")))

	if v.Allowed() {
		t.Fatal("a permission lookup failure must refuse, not admit")
	}
}

func TestDecide_NilPermissionCheckerRefuses(t *testing.T) {
	v := New(nil, &fakeRate{allow: true}).Decide(context.Background(), baseReq(GitHubActor("maintainer")))
	if v.Allowed() {
		t.Fatal("an unwired permission checker must not read as permission")
	}
}

func TestDecide_RateLimitRefuses(t *testing.T) {
	v := New(&fakePerms{allow: true}, &fakeRate{allow: false}).Decide(context.Background(), baseReq(GitHubActor("maintainer")))
	if v.Allowed() {
		t.Fatal("a denied rate limit must refuse")
	}
}

func TestDecide_BudgetReducesLargeRequest(t *testing.T) {
	req := baseReq(GitHubActor("maintainer"))
	req.Size = Size{Files: 120, Lines: 900}

	v := New(&fakePerms{allow: true}, &fakeRate{allow: true}).Decide(context.Background(), req)

	if v.Outcome != OutcomeReduce {
		t.Fatalf("outcome = %s, want reduce (%q)", v.Outcome, v.Reason)
	}
	if !v.ForceShallow {
		t.Error("a reduced review must drop to one call per file")
	}
	if v.MaxFiles != DefaultLimits.ReducedMaxFiles {
		t.Errorf("MaxFiles = %d, want %d — depth alone does not bound a large PR", v.MaxFiles, DefaultLimits.ReducedMaxFiles)
	}
}

// A repo with no completed reviews yields no estimate. The token measure must
// then contribute nothing — but size must still decide, or a brand-new repo
// would have no limit at all. That is exactly the repo an unknown contributor
// is most likely to open a pull request against.
func TestCheckBudget_TokenMeasureAbstainsWithoutHistory(t *testing.T) {
	lim := DefaultLimits
	huge := TokenEstimate{AvgTokens: 9_000_000, Samples: 0} // over every limit, but unknown

	if v := Budget(Size{Files: 2, Lines: 10}, huge, lim); !v.Allowed() || v.Outcome != OutcomeAllow {
		t.Errorf("no history must not refuse a small PR: %s %q", v.Outcome, v.Reason)
	}
	if v := Budget(Size{Files: lim.HardFiles, Lines: 10}, huge, lim); v.Allowed() {
		t.Error("size must still refuse a huge PR on a repo with no history")
	}
}

// Worst wins: a request that only reduces on size but refuses on tokens is
// refused. Stopping at the first hit would let it through reduced.
func TestCheckBudget_WorstWins(t *testing.T) {
	lim := DefaultLimits
	v := Budget(
		Size{Files: lim.SoftFiles + 1, Lines: 10},                // reduce
		TokenEstimate{AvgTokens: lim.HardTokens + 1, Samples: 5}, // refuse
		lim)

	if v.Outcome != OutcomeRefuse {
		t.Fatalf("outcome = %s, want refuse — the most severe measure must win", v.Outcome)
	}
}

// A zero limit disables one measure without disabling the other.
func TestCheckBudget_ZeroLimitIsOff(t *testing.T) {
	lim := Limits{SoftFiles: 0, HardFiles: 0, SoftLines: 100, HardLines: 0, ReducedMaxFiles: 10}

	if v := Budget(Size{Files: 100_000, Lines: 5}, TokenEstimate{}, lim); !v.Allowed() {
		t.Errorf("zero file limits must not fire: %q", v.Reason)
	}
	if v := Budget(Size{Files: 1, Lines: 500}, TokenEstimate{}, lim); v.Outcome != OutcomeReduce {
		t.Errorf("line soft limit must still fire, got %s", v.Outcome)
	}
}

// Two reduce verdicts must keep the tighter caps from both, not the first one.
func TestVerdict_MergesReduceCaps(t *testing.T) {
	a := Reduce("files", 40, false)
	b := Reduce("tokens", 10, true)

	got := a.worst(b)
	if got.MaxFiles != 10 {
		t.Errorf("MaxFiles = %d, want the tighter 10", got.MaxFiles)
	}
	if !got.ForceShallow {
		t.Error("ForceShallow from either side must survive the merge")
	}
}

func TestAutoRun(t *testing.T) {
	cases := []struct {
		name       string
		selfHosted bool
		enabled    bool
		explicit   bool
		want       Outcome
	}{
		{"explicitly on reviews", false, true, true, OutcomeAllow},
		{"explicitly off signals", false, false, true, OutcomeSignal},
		{"nothing stored signals", false, false, false, OutcomeSignal},
		// Self-hosted has no bill to gate, so with nothing stored it works out
		// of the box.
		{"self-hosted, nothing stored, reviews", true, false, false, OutcomeAllow},
		// THE FIX. Self-hosted used to force reviews on unconditionally, so a
		// self-hoster who turned auto-review off still got reviews. Harmless
		// while the default was on; a trap once it became off.
		{"self-hosted must respect an explicit off", true, false, true, OutcomeSignal},
		{"self-hosted, explicitly on, reviews", true, true, true, OutcomeAllow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := AutoRun(tc.selfHosted, tc.enabled, tc.explicit).Outcome; got != tc.want {
				t.Errorf("AutoRun(selfHosted=%v, enabled=%v, explicit=%v) = %s, want %s",
					tc.selfHosted, tc.enabled, tc.explicit, got, tc.want)
			}
		})
	}
}

// A signal is not an allowed review. It also must not read as a refusal: the
// affordance is the point, and the two get different treatment on the PR.
func TestSignal_IsNeitherAllowedNorRefused(t *testing.T) {
	v := AutoRun(false, false, false)

	if v.Allowed() {
		t.Error("a signal must not run the review")
	}
	if v.Outcome == OutcomeRefuse {
		t.Error("a signal must stay distinct from a refusal — it offers the trigger checkbox")
	}
	if v.Reason == "" {
		t.Error("a signal must say why, like every other verdict")
	}
}

// Severity ordering: a refusal must win over a signal, or a repo with
// auto-review off could mask a hard-limit refusal.
func TestVerdict_RefuseBeatsSignal(t *testing.T) {
	if got := Signal("off").worst(Refuse("too big")).Outcome; got != OutcomeRefuse {
		t.Errorf("worst = %s, want refuse", got)
	}
	if got := Refuse("too big").worst(Signal("off")).Outcome; got != OutcomeRefuse {
		t.Errorf("worst = %s, want refuse regardless of order", got)
	}
}
