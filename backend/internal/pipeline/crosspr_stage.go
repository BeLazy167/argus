// Package pipeline — crosspr_stage.go runs cross-PR analysis as a
// standalone async stage. It fires on EventReviewCompleted (after the
// primary pipeline finishes and the review row is durably committed),
// loads the primary + each linked PR's diff + prior findings, and asks
// the LLM judge for combination risks. The output is written back to
// the sticky comment in place.
//
// Decoupled from validateStage so primary-review latency stays minimal
// and so a late-arriving sibling PR can trigger a refresh of earlier
// PRs' cross-PR sections without re-running the full pipeline.
package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/internal/llm"
	"github.com/BeLazy167/argus/backend/internal/obs"
	"github.com/BeLazy167/argus/backend/internal/store"
	"github.com/BeLazy167/argus/backend/internal/store/db"
	"github.com/BeLazy167/argus/backend/internal/util"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/sync/errgroup"
)

// jointAcceptanceJudgePrompt is the system prompt for the joint
// acceptance judge. Per-PR acceptance runs in acceptance.go and owns the
// "## Issue Coverage" section; this judge runs ONLY when 2+ linked PRs
// reference the same issue and produces a separate "## Joint Issue
// Coverage" section that asks "does the COMBINED change across these PRs
// satisfy each criterion?"
//
// Output is a strict JSON envelope with schema_version=1. Unknown
// versions are parsed as an empty result by runCrossPRAcceptanceStage
// (safe default — never panic, never speculate).
const jointAcceptanceJudgePrompt = `You are a cross-PR joint acceptance judge. You will receive:
- A linked issue with its criteria
- The diffs and findings of 2+ linked PRs that reference this issue

For EACH criterion in the issue, determine whether the COMBINED change
across all the linked PRs addresses it. Per criterion, output:
- status: "addressed" | "partial" | "unaddressed" | "ambiguous"
- addressed_by: the PR (owner/repo#N) that most directly addresses it,
  or empty string if unaddressed
- evidence: file:line of the diff line that addresses it, or empty

Verdict rollup:
- "addressed" if ALL criteria are addressed
- "partial"   if SOME criteria are addressed (others unaddressed/partial)
- "unaddressed" if NO criterion is addressed

Output EXACTLY this JSON schema — no prose, no markdown, no trailing
commentary:

{
  "schema_version": 1,
  "issue_owner": "<owner>",
  "issue_repo": "<repo>",
  "issue_number": 42,
  "issue_title": "<title>",
  "criteria": [
    {
      "text": "<criterion text verbatim from issue>",
      "status": "addressed|partial|unaddressed|ambiguous",
      "addressed_by": "<owner/repo#N or empty>",
      "evidence": "<path:line or empty>"
    }
  ],
  "verdict": "addressed|partial|unaddressed"
}
`

// jointMaxSiblings caps how many linked-review diffs we hydrate per shared
// issue. More than this and the prompt balloons past the LLM's useful
// attention budget.
const jointMaxSiblings = 5

// jointMaxSharedIssues caps how many shared issues are judged per stage
// run. Matches acceptance.go's maxIssuesPerRun for symmetry.
const jointMaxSharedIssues = 5

// jointAcceptanceJudgeResponse is the envelope for one shared-issue's
// joint judgment. Fields mirror JointAcceptanceResult exactly — we decode
// directly into the type below after SchemaVersion validation.
type jointAcceptanceJudgeResponse = JointAcceptanceResult

// crossPRCombinationRiskPrompt is the system prompt for the combination-risk
// judge. Each PR is already reviewed in isolation; this prompt probes the
// nine failure categories that only surface when PRs are composed. Output is a
// strict JSON envelope with schema_version=1 so we can evolve the payload
// safely — an unknown version is handled by the parser as empty risks.
const crossPRCombinationRiskPrompt = `You are a cross-PR combination risk judge. Each PR is valid in isolation
(already reviewed). Your job: surface risks that exist ONLY when the PRs
are combined.

You will receive the primary PR's diff, and for each linked PR:
- Its diff
- Its prior findings (open issues Argus flagged during its review)
- Whether it's been reviewed (if Reviewed=false, treat as diff-only context)

Probe these categories:
1. Schema/migration race — primary queries a column/route/table that the
   linked PR changes or drops.
2. Serialization contract — JSON/proto field renames, case shifts, null
   shape changes between producer and consumer.
3. Type/interface drift — narrowed or renamed shared type breaks indirect
   consumers (struct embed, generics).
4. Config contradiction — feature flags, env vars, config keys conflict.
5. Deployment ordering — primary requires linked PR to deploy first (or
   vice versa) to avoid runtime breakage.
6. Security posture — auth, rate-limit, validation change in one PR
   creates attack surface in another.
7. Enum exhaustiveness — new enum value added in one PR; consumer has
   switch without default in another.
8. Locale/temporal — timezone, unit, encoding assumption drift.
9. Propagated findings — existing finding in linked PR becomes
   exploitable via primary's consumer.

Output EXACTLY this JSON schema — no prose, no markdown, no trailing
commentary:

{
  "schema_version": 1,
  "combination_risks": [
    {
      "category": "schema_race|serialization_contract|type_drift|config_contradiction|deploy_ordering|security_posture|enum_exhaustiveness|locale_temporal|propagated_finding",
      "description": "<one sentence, concrete>",
      "linked_pr": "<owner/repo#N>",
      "primary_files": ["<path:line>", ...],
      "severity": "low|medium|high"
    }
  ]
}

If no risks exist, return combination_risks: [] (not omitted, not null).
`

// crossPRJudgeResponse is the envelope the combination-risk judge emits.
// SchemaVersion is validated at the envelope level against CrossPRJudgeSchemaV1;
// individual risks are version-agnostic today. A mismatched version triggers
// Warn + empty risks (safe default — see runCrossPRStage).
type crossPRJudgeResponse struct {
	SchemaVersion    int               `json:"schema_version"`
	CombinationRisks []CombinationRisk `json:"combination_risks"`
}

// crossPRFindingsPerLink caps prior findings emitted per linked PR. Noisy
// specialist reviews can ship hundreds; we truncate with an explicit marker
// so the judge knows context was trimmed rather than silently dropped.
const crossPRFindingsPerLink = 20

// Cross-PR stage constants.
//
// crossPRStageDebounce collapses a burst of sibling-PR completion events
// into a single LLM call per reviewID. crossPRRefreshCap + window bound
// cost for chatty PR families (a push that ripples across N repos would
// otherwise N-squared the stage).
const (
	crossPRStageDebounce = 30 * time.Second
	crossPRRefreshCap    = 2 // max refreshes per PR per window
	crossPRRefreshWindow = 10 * time.Minute
	// crossPRHydrateTimeout caps how long the stage will wait for linked-PR
	// diff + prior-finding hydration before falling through to a partial
	// output. Matches the acceptance-worker timeout budget.
	crossPRHydrateTimeout = 30 * time.Second
	// crossPRPerInstallCap bounds LLM spend across an entire installation.
	// Orthogonal to crossPRRefreshCap (per-review): one noisy PR family can't
	// exceed crossPRRefreshCap for a single review, and a customer can't
	// exceed crossPRPerInstallCap across all their cross-linked PRs.
	crossPRPerInstallCap    = 30
	crossPRPerInstallWindow = time.Hour

	// crossPRHydrationConcurrency bounds parallel hydration of linked PRs in
	// runCrossPRStage. Each link costs 2 GH API calls + 2 DB queries; at
	// max_linked_prs=5 serial hydration was ~2-5s per stage run. Matches the
	// max_linked_prs ceiling so a fully-linked PR fans out in one batch.
	crossPRHydrationConcurrency = 5

	// crossPRJointSiblingConcurrency bounds parallel hydration of sibling
	// reviews inside hydrateLinkedReviews (joint acceptance). Matches
	// jointMaxSiblings for the same fan-out-once rationale.
	crossPRJointSiblingConcurrency = 5

	// crossPRJointIssueConcurrency bounds parallel per-issue LLM calls in
	// runCrossPRAcceptanceStage. Lower than the sibling/hydration bounds
	// because each unit here is an LLM round trip, not a cheap fetch —
	// running all jointMaxSharedIssues in parallel would spike provider QPS.
	crossPRJointIssueConcurrency = 3

	// crossPRSiblingFanoutConcurrency bounds goroutines spawned by
	// enqueueSiblingRefreshes. Pathological cross-linked PR families could
	// otherwise N-squared the goroutine count; the buffered-channel
	// semaphore provides backpressure without adding a dependency.
	crossPRSiblingFanoutConcurrency = 4
)

// crossPRSiblingSem is a buffered-channel semaphore that bounds concurrent
// sibling-refresh goroutines across the process. Shared across all callers
// of enqueueSiblingRefreshes so a burst from one reviewID can't dodge the
// cap by racing with another.
var crossPRSiblingSem = make(chan struct{}, crossPRSiblingFanoutConcurrency)

// fanoutHopKey is the ctx key for the sibling-fanout hop counter.
// Unstamped ctx reads as hop=0. enqueueSiblingRefreshes stamps hop+1 on
// re-entry and refuses to fan out at hop>=fanoutHopLimit, breaking
// mutual-link ping-pong between two PRs that reference each other.
// Limit=1: direct-sibling refresh is the feature; grandchild cascades
// aren't — they'll refresh via their own hop-0 completion.
type fanoutHopKey struct{}

const fanoutHopLimit = 1

func fanoutHop(ctx context.Context) int {
	n, _ := ctx.Value(fanoutHopKey{}).(int)
	return n
}

func withFanoutHop(ctx context.Context, n int) context.Context {
	return context.WithValue(ctx, fanoutHopKey{}, n)
}

// mutexEntry is the value stored in mutexMap: a mutex plus a
// last-accessed timestamp (unix nano, atomic.Int64) so the sweeper can
// evict stale entries without touching the hot-path acquire lock.
type mutexEntry struct {
	mu           *sync.Mutex
	lastAccessed atomic.Int64
}

// mutexMap replaces sync.Map for per-reviewID mutexes so the sweeper can
// iterate + delete atomically. A single guarding mutex is fine — acquires
// are cheap (map lookup) and the sweeper runs every 10 min, not per-RPC.
type mutexMap struct {
	mu      sync.Mutex
	entries map[uuid.UUID]*mutexEntry
}

// newMutexMap constructs an empty mutexMap ready for use.
func newMutexMap() *mutexMap {
	return &mutexMap{entries: map[uuid.UUID]*mutexEntry{}}
}

// acquire returns the *sync.Mutex for key, creating it on first use. It
// also stamps lastAccessed so the sweeper won't evict a live entry.
func (m *mutexMap) acquire(key uuid.UUID) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[key]
	if !ok {
		e = &mutexEntry{mu: &sync.Mutex{}}
		m.entries[key] = e
	}
	e.lastAccessed.Store(time.Now().UnixNano())
	return e.mu
}

// sweep drops entries whose lastAccessed is older than maxAge AND whose
// mutex can be acquired via TryLock (i.e. it's not currently held). An
// actively-held mutex is left in place; its next acquire will refresh
// lastAccessed, and the following sweep can reconsider it.
//
// TryLock (not Lock): a held mutex signals an in-flight stage; blocking
// here would serialize the sweeper behind the longest LLM call and
// starve the sibling maps. Skipping stale-looking entries that happen
// to be locked is safe — they'll get another chance next tick.
func (m *mutexMap) sweep(maxAge time.Duration) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	cutoff := time.Now().Add(-maxAge).UnixNano()
	dropped := 0
	for k, e := range m.entries {
		if e.lastAccessed.Load() >= cutoff {
			continue
		}
		if !e.mu.TryLock() {
			// Entry is in active use; skip.
			continue
		}
		e.mu.Unlock()
		delete(m.entries, k)
		dropped++
	}
	return dropped
}

// reset drops every entry. Test-only — production code must not call this
// (a concurrent acquire would leak the freshly-created mutex).
func (m *mutexMap) reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = map[uuid.UUID]*mutexEntry{}
}

// crossPRMutexes serializes runCrossPRStage against a single reviewID to
// prevent overlapping sticky edits. Keyed by reviewID UUID; loaded lazily
// by acquireCrossPRMutex and swept periodically by startCrossPRSweeper.
var crossPRMutexes = newMutexMap()

// crossPRRefreshMu guards crossPRRefreshCount. The cap is a safety net
// against pathological refresh storms, not a security boundary — a restart
// resets the counter and that's fine.
var (
	crossPRRefreshMu    sync.Mutex
	crossPRRefreshCount = map[uuid.UUID][]time.Time{}
)

// crossPRDebounceMu guards crossPRDebounceTimers. Timer.Reset() returns true
// iff the timer was still pending; we drop on true so we don't double-fire.
var (
	crossPRDebounceMu     sync.Mutex
	crossPRDebounceTimers = map[uuid.UUID]*time.Timer{}
)

// crossPRInstallMu guards crossPRInstallCount. Same pattern as the per-review
// refresh counter: in-memory only, restart resets the quota, which is fine
// for a safety cap (not a billing boundary).
var (
	crossPRInstallMu    sync.Mutex
	crossPRInstallCount = map[int64][]time.Time{} // installationID → recent timestamps
)

// acquireCrossPRMutex returns the per-reviewID mutex, creating on first use.
func acquireCrossPRMutex(reviewID uuid.UUID) *sync.Mutex {
	return crossPRMutexes.acquire(reviewID)
}

// crossPRRefreshAllowed returns false when the per-review refresh cap
// has been hit inside the active window. Also trims old entries each call
// so the map doesn't grow unbounded.
func crossPRRefreshAllowed(reviewID uuid.UUID, now time.Time) bool {
	crossPRRefreshMu.Lock()
	defer crossPRRefreshMu.Unlock()
	cutoff := now.Add(-crossPRRefreshWindow)
	kept := crossPRRefreshCount[reviewID][:0]
	for _, t := range crossPRRefreshCount[reviewID] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= crossPRRefreshCap {
		crossPRRefreshCount[reviewID] = kept
		return false
	}
	kept = append(kept, now)
	crossPRRefreshCount[reviewID] = kept
	return true
}

// crossPRInstallPeek returns (allowed, resetAt) WITHOUT consuming a slot.
// Used by cheap pre-checks before we've committed to LLM work; the actual
// reservation is done by crossPRInstallTryAcquire once the stage has
// confirmed it will run. allowed=false means the installation has hit
// crossPRPerInstallCap inside the active rolling crossPRPerInstallWindow;
// resetAt is the earliest instant the oldest sample will age out. The
// slice is GC'd of out-of-window timestamps on every call so the map
// doesn't grow unbounded.
func crossPRInstallPeek(installationID int64, now time.Time) (bool, time.Time) {
	crossPRInstallMu.Lock()
	defer crossPRInstallMu.Unlock()
	kept := trimCrossPRInstallLocked(installationID, now)
	if len(kept) >= crossPRPerInstallCap {
		return false, kept[0].Add(crossPRPerInstallWindow)
	}
	return true, time.Time{}
}

// crossPRInstallTryAcquire atomically checks the cap and, if allowed,
// records a consumption timestamp in the same mutex hold. N concurrent
// goroutines cannot all observe allowed=true before any record — exactly
// one of them claims the last slot and the rest see allowed=false. Call
// only after the stage has committed to LLM work; early-returned runs
// (flag off, hash unchanged, all PRs inaccessible, provider not
// resolved) MUST NOT consume quota.
//
// Returns (true, zero-time) on success and (false, resetAt) when the cap
// is already saturated. resetAt reflects the window BEFORE this call —
// the caller never needs it in the allowed=true branch.
func crossPRInstallTryAcquire(installationID int64, now time.Time) (bool, time.Time) {
	crossPRInstallMu.Lock()
	defer crossPRInstallMu.Unlock()
	kept := trimCrossPRInstallLocked(installationID, now)
	if len(kept) >= crossPRPerInstallCap {
		return false, kept[0].Add(crossPRPerInstallWindow)
	}
	crossPRInstallCount[installationID] = append(kept, now)
	return true, time.Time{}
}

// trimCrossPRInstallLocked drops out-of-window timestamps for
// installationID and returns the surviving slice. Must be called with
// crossPRInstallMu held. Side-effects the map — callers mutating the
// returned slice further MUST write it back.
func trimCrossPRInstallLocked(installationID int64, now time.Time) []time.Time {
	cutoff := now.Add(-crossPRPerInstallWindow)
	kept := crossPRInstallCount[installationID][:0]
	for _, t := range crossPRInstallCount[installationID] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	crossPRInstallCount[installationID] = kept
	return kept
}

// crossPRSweepInterval is how often startCrossPRSweeper runs. Chosen to
// balance memory recovery with observer-effect overhead — every 10 min
// keeps steady-state memory close to the rolling-hour working set without
// adding measurable CPU wake-ups.
//
// Declared as var (not const) so tests can override to a short interval
// and exercise the ticker path without real-time sleeps.
var crossPRSweepInterval = 10 * time.Minute

// crossPRMutexMaxAge is the minimum idle time before a mutex map entry is
// eligible for eviction. An entry with no acquire in the last hour has no
// active stage referencing it; a future acquire just creates a fresh
// mutex under the map lock.
const crossPRMutexMaxAge = time.Hour

// sweepTimestampCounter trims out-of-window timestamps for each key in m
// and deletes the key entirely if no timestamps remain after the trim.
// Returns the number of keys dropped. Generic over key type so the same
// body services both uuid.UUID-keyed refresh counts and int64-keyed
// per-install counts.
func sweepTimestampCounter[K comparable](m map[K][]time.Time, mu *sync.Mutex, now time.Time, window time.Duration) int {
	mu.Lock()
	defer mu.Unlock()
	cutoff := now.Add(-window)
	dropped := 0
	for k, ts := range m {
		kept := ts[:0]
		for _, t := range ts {
			if t.After(cutoff) {
				kept = append(kept, t)
			}
		}
		if len(kept) == 0 {
			delete(m, k)
			dropped++
			continue
		}
		m[k] = kept
	}
	return dropped
}

// startCrossPRSweeper launches a single ticker goroutine that periodically
// evicts stale entries from the four cross-PR package maps so their memory
// footprint is bounded by the rolling-hour working set (not process
// lifetime). Invoked once from NewOrchestrator; runs until ctx is done or
// process exit. No cancellation required in production (Fly machine
// restarts reclaim the goroutine naturally).
//
// Logs a summary line only when at least one entry was dropped — a silent
// sweep on a quiet deployment produces no log noise.
func (o *Orchestrator) startCrossPRSweeper(ctx context.Context) {
	ticker := time.NewTicker(crossPRSweepInterval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// Per-tick recover: a panic during one sweep must NOT exit
				// the goroutine — that would silently disable the memory-
				// leak-prevention mechanism for the remainder of the process.
				// Recovering at the goroutine scope (the pre-fix layout)
				// would have been self-defeating: the very bug this sweeper
				// exists to prevent (unbounded map growth) could re-emerge
				// on the first transient fault.
				func() {
					defer func() {
						if r := recover(); r != nil {
							o.logger.Error("[crosspr-sweeper] panic — ticker continues",
								"recover", r,
								"stack", string(debug.Stack()))
							emitPipelinePanicEvent(ctx, o.logger, "crosspr_sweeper", r, obs.TraceID(ctx))
						}
					}()
					now := time.Now()
					mutexDropped := crossPRMutexes.sweep(crossPRMutexMaxAge)
					jointDropped := jointAcceptanceMutexes.sweep(crossPRMutexMaxAge)
					refreshDropped := sweepTimestampCounter(crossPRRefreshCount, &crossPRRefreshMu, now, crossPRRefreshWindow)
					installDropped := sweepTimestampCounter(crossPRInstallCount, &crossPRInstallMu, now, crossPRPerInstallWindow)
					if mutexDropped+jointDropped+refreshDropped+installDropped > 0 {
						o.logger.Info("[crosspr] swept stale state",
							"mutexes", mutexDropped,
							"joint_mutexes", jointDropped,
							"refresh_entries", refreshDropped,
							"install_entries", installDropped)
					}
				}()
			}
		}
	}()
}

// OnReviewCompleted is the subscriber for EventReviewCompleted.
// Subscribed in orchestrator.go:NewOrchestrator via EventBus.SubscribeGlobal.
// It debounces by reviewID, then dispatches runCrossPRStage for the
// completed review. It also reverse-looks-up *other* reviews that linked
// back to this PR so their cross-PR sections refresh — the asymmetric
// case where PR A's review links to PR B, and PR B later finishes its
// own review: A's cross-PR bundle now has new data (B's findings) and
// must be recomputed.
//
// The reverse lookup uses reviews.linked_pr_refs (added by migration
// 039) — a JSONB array of {owner, repo, number} populated at synthesis
// completion and indexed by GIN(jsonb_path_ops) for fast containment.
//
// Sibling re-entry is fire-and-forget: we recurse through OnReviewCompleted
// so the sibling's debounce + per-review cap + per-installation cap all
// apply. There is no retry on enqueue failure; the rate-limiter and the
// per-review refresh cap are the bounded backstop.
func (o *Orchestrator) OnReviewCompleted(ctx context.Context, reviewID uuid.UUID) {
	crossPRDebounceMu.Lock()
	if t, ok := crossPRDebounceTimers[reviewID]; ok {
		// Reset returns false if the timer already fired; either way we
		// collapse to a single trailing call.
		t.Stop()
	}
	rid := reviewID
	// Detach the context: the caller (event bus handler) may hold a
	// per-request ctx that cancels on handler return. The stage itself
	// sets its own timeouts.
	detached := context.WithoutCancel(ctx)
	crossPRDebounceTimers[rid] = time.AfterFunc(crossPRStageDebounce, func() {
		crossPRDebounceMu.Lock()
		delete(crossPRDebounceTimers, rid)
		crossPRDebounceMu.Unlock()
		o.runCrossPRStage(detached, rid)
	})
	crossPRDebounceMu.Unlock()

	// Reverse lookup: find all completed reviews that linked TO this PR.
	// Runs outside the debounce so concurrent sibling completions aren't
	// collapsed. Detached ctx for the same reason as runCrossPRStage —
	// the event-bus handler ctx can cancel unpredictably.
	go func() {
		defer func() {
			if r := recover(); r != nil {
				o.logger.Error("[crosspr] sibling fanout panic",
					"review_id", reviewID,
					"recover", r,
					"stack", string(debug.Stack()))
				emitPipelinePanicEvent(ctx, o.logger, "sibling_fanout", r, obs.TraceID(ctx))
			}
		}()
		o.enqueueSiblingRefreshes(context.WithoutCancel(ctx), reviewID)
	}()
}

// RefreshCrossPR dispatches both the cross-PR findings refresh and the
// joint acceptance refresh for a review. Used by webhook handlers that
// react to events OTHER than EventReviewCompleted (e.g. pull_request.edited)
// — the bus subscriber in NewOrchestrator only fires on completions, so
// a manual refresh path has to schedule both stages itself.
//
// Both calls are detached (context.Background) so the webhook's request
// context can't cancel an in-progress LLM call. Per-review mutexes
// inside each stage handle any parallelism against the bus-driven path.
func (o *Orchestrator) RefreshCrossPR(ctx context.Context, reviewID uuid.UUID) {
	o.OnReviewCompleted(ctx, reviewID)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				o.logger.Error("[crosspr] refresh joint-accept panic",
					"review_id", reviewID,
					"recover", r,
					"stack", string(debug.Stack()))
				emitPipelinePanicEvent(ctx, o.logger, "refresh_joint_accept", r, obs.TraceID(ctx))
			}
		}()
		o.runCrossPRAcceptanceStage(context.WithoutCancel(ctx), reviewID)
	}()
}

// enqueueSiblingRefreshes reverse-looks-up reviews whose linked_pr_refs
// contain this review's (owner, repo, number) and schedules a cross-PR
// refresh for each via OnReviewCompleted. Failures are non-fatal and
// logged at Warn — debounce + per-review cap + per-installation cap
// make a dropped sibling bounded, not silent (the next push webhook
// will re-trigger).
func (o *Orchestrator) enqueueSiblingRefreshes(ctx context.Context, reviewID uuid.UUID) {
	// See fanoutHopKey doc for the ping-pong rationale. Debug-log the
	// suppression so ops can grep-verify the guard is firing in prod;
	// without it, a suppressed cascade is indistinguishable from a
	// legitimate "no siblings found" result (both silent).
	if hop := fanoutHop(ctx); hop >= fanoutHopLimit {
		o.logger.Debug("[crosspr] sibling fanout suppressed",
			"review_id", reviewID, "hop", hop)
		return
	}
	st := o.crossPRStoreDep()
	review, err := st.GetReview(ctx, reviewID)
	if err != nil {
		o.logger.Warn("[crosspr] sibling fanout: load review failed",
			"review_id", reviewID, "error", err)
		return
	}
	if review.Status != "completed" {
		return
	}
	repo, err := st.GetRepo(ctx, review.RepoID)
	if err != nil {
		o.logger.Warn("[crosspr] sibling fanout: load repo failed",
			"review_id", reviewID, "repo_id", review.RepoID, "error", err)
		return
	}
	owner, repoName, err := splitRepoFullName(repo.FullName)
	if err != nil {
		o.logger.Warn("[crosspr] sibling fanout: split repo failed",
			"review_id", reviewID, "repo_full_name", repo.FullName, "error", err)
		return
	}
	siblings, err := st.FindReviewsLinkingToPR(ctx, db.FindReviewsLinkingToPRParams{
		ExcludeID: reviewID,
		Owner:     owner,
		Repo:      repoName,
		Number:    review.PRNumber,
	})
	if err != nil {
		o.logger.Warn("[crosspr] sibling fanout: lookup failed",
			"review_id", reviewID, "error", err)
		return
	}
	if len(siblings) == 0 {
		return
	}
	o.logger.Info("[crosspr] sibling fanout",
		"review_id", reviewID, "siblings", len(siblings))
	for _, sib := range siblings {
		sibID := sib.ID
		// Semaphore acquire is a blocking send on the bounded channel —
		// bounds process-wide goroutine fan-out from pathological
		// multi-linked PR families (N-squared risk when each sibling's
		// OnReviewCompleted re-triggers another fanout). Acquiring BEFORE
		// `go` prevents spawning a goroutine that'd just block anyway,
		// keeping the goroutine count at O(crossPRSiblingFanoutConcurrency)
		// instead of O(siblings).
		crossPRSiblingSem <- struct{}{}
		go func() {
			defer func() { <-crossPRSiblingSem }()
			defer func() {
				if r := recover(); r != nil {
					o.logger.Error("[crosspr] sibling refresh panic",
						"sibling_id", sibID,
						"recover", r,
						"stack", string(debug.Stack()))
					emitPipelinePanicEvent(ctx, o.logger, "sibling_refresh", r, obs.TraceID(ctx))
				}
			}()
			// Stamp the hop so the sibling's own re-entry into
			// enqueueSiblingRefreshes short-circuits at the guard above.
			// context.WithoutCancel preserves values, so the hop rides
			// through the debounce timer and reverse-lookup goroutine.
			nextCtx := withFanoutHop(context.WithoutCancel(ctx), fanoutHop(ctx)+1)
			// Re-enter OnReviewCompleted so the sibling's debounce,
			// per-review cap, and per-installation cap all apply for the
			// findings (runCrossPRStage) path.
			o.OnReviewCompleted(nextCtx, sibID)
			// Joint acceptance does not piggyback on OnReviewCompleted (the
			// bus subscriber is what dispatches both), so a sibling event
			// that arrives via reverse-lookup would otherwise never refresh
			// the joint "## Joint Issue Coverage" section. Run it
			// explicitly here, detached so the caller's ctx cancellation
			// can't orphan it mid-LLM call. The per-review mutex inside
			// runCrossPRAcceptanceStage serializes against any parallel
			// bus-driven dispatch.
			o.runCrossPRAcceptanceStage(nextCtx, sibID)
		}()
	}
}

// runCrossPRStage is the actual work. Called directly by the debounce
// timer; OnReviewCompleted must not invoke it synchronously. Safe to call
// for a reviewID that never had LinkedPRs — it early-exits cleanly.
//
// Gated by the per-review mutex, the per-review refresh cap, and the
// per-installation rate limit (the last two hold a quota peek; actual
// commit happens only when the stage is about to issue the LLM call).
// Re-extracts LinkedPRs, hydrates diffs + prior findings, and hashes the
// bundle — an unchanged hash skips the LLM entirely. Records quota only
// at LLM-commit so all early returns (flag off, hash stable, no provider,
// all-inaccessible) don't count against the installation's 30/hour cap.
// Persists the hash LAST so a crash between sticky upsert and hash write
// leaves a retryable state (next refresh recomputes, finds the bundle
// unchanged, and skips).
func (o *Orchestrator) runCrossPRStage(ctx context.Context, reviewID uuid.UUID) {
	mu := acquireCrossPRMutex(reviewID)
	mu.Lock()
	defer mu.Unlock()

	st := o.crossPRStoreDep()
	stateDep := o.crossPRStateDep()

	if !crossPRRefreshAllowed(reviewID, o.crossPRNow()) {
		// Per-review cap is a harder block than per-install — no footer
		// surface here, just log and drop. Per-install limit has its own
		// footer path below.
		o.logger.Info("[crosspr-stage] refresh cap hit, skipping", "review_id", reviewID)
		o.emitCrossPRSkipped(ctx, reviewID, "rate_limited_per_review", "")
		o.emitRateLimitHit(ctx, "crosspr_per_pr", reviewID.String(), 0)
		return
	}

	crossPRStageStart := time.Now()

	// 1. Load the review + its latest pipeline run.
	review, err := st.GetReview(ctx, reviewID)
	if err != nil {
		o.logger.Warn("[crosspr-stage] load review failed", "review_id", reviewID, "error", err)
		return
	}
	if review.Status != "completed" {
		o.logger.Info("[crosspr-stage] review not completed, skipping", "review_id", reviewID, "status", review.Status)
		return
	}

	// Hydrate the persisted PipelineRun for PREvent.PRBody + DBInstallationID.
	// If the run state is unavailable we can't proceed — the PR body lives
	// only inside PREvent on the persisted run, not on the reviews row.
	runID, err := st.GetLatestRunForReview(ctx, reviewID)
	if err != nil {
		o.logger.Warn("[crosspr-stage] no pipeline run for review", "review_id", reviewID, "error", err)
		return
	}
	run, err := stateDep.LoadRun(ctx, runID)
	if err != nil {
		o.logger.Warn("[crosspr-stage] load pipeline state failed", "run_id", runID, "review_id", reviewID, "error", err)
		return
	}

	// Rehydrate attribution onto the detached ctx. The bus dispatched us
	// from context.Background() (orchestrator.go:238), so every downstream
	// structured event — cross_pr.stage.*, llm.call.completed with
	// stage=crosspr — would otherwise resolve to empty DistinctId and be
	// dropped by the PostHog handler as unattributed. Sourced from the
	// persisted run which survives the async boundary.
	ctx = obs.SetInstallationID(ctx, run.DBInstallationID)
	if run.PREvent.PRAuthor != "" {
		ctx = obs.SetGithubLogin(ctx, run.PREvent.PRAuthor)
	}
	if run.TraceID != "" {
		ctx = obs.SetTraceID(ctx, run.TraceID)
	}

	// 2a. Per-installation rate limit (orthogonal to the per-review cap
	// above). This is a PEEK only — quota is not consumed until we've
	// committed to LLM work below (via crossPRInstallTryAcquire). Over-
	// limit runs surface a short footer in the sticky so users see the
	// backpressure instead of a silent drop.
	if allowed, resetAt := crossPRInstallPeek(run.PREvent.InstallationID, o.crossPRNow()); !allowed {
		o.logger.Warn("[crosspr-stage] installation rate limit hit, skipping",
			"installation_id", run.PREvent.InstallationID,
			"review_id", reviewID,
			"reset_at", resetAt)
		o.surfaceCrossPRRateLimitFooter(ctx, reviewID, review, run, resetAt)
		o.emitCrossPRSkipped(ctx, reviewID, "rate_limited", run.TraceID)
		retryAfterMs := max(int64(time.Until(resetAt).Milliseconds()), 0)
		o.emitRateLimitHit(ctx, "crosspr_global", reviewID.String(), retryAfterMs)
		return
	}

	// 2. Feature flag — load fresh so a toggle since review-completion
	// takes effect without a restart.
	flags := st.LoadFeatureFlags(ctx, run.DBInstallationID)
	if !flags.CrossPRChecks {
		o.logger.Info("[crosspr-stage] feature flag off, skipping", "review_id", reviewID)
		o.emitCrossPRSkipped(ctx, reviewID, "feature_flag_off", run.TraceID)
		return
	}
	run.FeatureFlags = flags

	// 3. Re-extract LinkedPRs from the persisted PR body (LinkedPRs itself
	// is not serialized to pipeline_states, see types.go `json:"-"`).
	maxLinks := flags.MaxLinkedPRs
	if maxLinks <= 0 {
		maxLinks = 5
	}
	run.LinkedPRs = ExtractLinkedPRs(run.PREvent.PRBody, run.PREvent.RepoFullName, run.PREvent.PRNumber, maxLinks)
	if len(run.LinkedPRs) == 0 {
		o.logger.Info("[crosspr-stage] no linked PRs, nothing to do", "review_id", reviewID)
		o.emitCrossPRSkipped(ctx, reviewID, "no_linked_prs", run.TraceID)
		return
	}

	// 4. Hydrate each linked PR: diff + prior-review findings.
	//
	// Parallelized with a bounded errgroup so a max_linked_prs=5 fan-out
	// completes in ~one round-trip budget instead of ~five. Each goroutine
	// writes to its own index in the pre-sized slice — no append race.
	// Returning nil from each goroutine is load-bearing: per-link failures
	// are encoded on link.FetchError / link.Accessible and MUST NOT cancel
	// sibling hydrations (errgroup.WithContext would do exactly that on a
	// non-nil return). The shared hydrateCtx still enforces the overall
	// 30s hydration budget.
	hydrateCtx, cancel := context.WithTimeout(ctx, crossPRHydrateTimeout)
	defer cancel()
	hydrated := make([]PRLink, len(run.LinkedPRs))
	g, gctx := errgroup.WithContext(hydrateCtx)
	g.SetLimit(crossPRHydrationConcurrency)
	for i, link := range run.LinkedPRs {
		i, link := i, link
		g.Go(func() error {
			fetched := hydratePRLink(gctx, o, run, link)
			fetched = hydratePriorFindings(gctx, o, fetched)
			hydrated[i] = fetched
			return nil
		})
	}
	_ = g.Wait() // always nil — errors encoded on per-link fields
	run.LinkedPRs = hydrated

	accessibleCount := 0
	inaccessibleCount := 0
	for _, l := range hydrated {
		if l.Accessible {
			accessibleCount++
		} else {
			inaccessibleCount++
		}
	}

	// 5. Hash the bundle and compare to the stored value. Empty (nil)
	// stored hash means "never run before" — proceed unconditionally.
	hash, err := computeCrossPRHash(run.PREvent.HeadSHA, hydrated)
	if err != nil {
		// Never fall through on a hash failure: a phantom "hash of empty
		// bytes" would collide across runs and silently wedge idempotency.
		o.logger.Error("[crosspr-stage] hash marshal failed — skipping stage",
			"review_id", reviewID, "error", err)
		return
	}
	if review.CrossPRHash != nil && *review.CrossPRHash == hash {
		o.logger.Info("[crosspr-stage] unchanged bundle, skipping LLM", "review_id", reviewID)
		o.emitCrossPRSkipped(ctx, reviewID, "idempotent_hash", run.TraceID)
		return
	}

	// 6. LLM call. crossPRCombinationRiskPrompt consumes PriorFindings +
	// diffs via writeLinkedPRFindings.
	if accessibleCount == 0 {
		run.CrossPRCoverage = &CrossPRCoverage{
			LinkedPRs:         hydrated,
			AccessibleCount:   0,
			InaccessibleCount: inaccessibleCount,
		}
		// IsCompatible() returns true here because CombinationRisks is nil:
		// we can't disprove compatibility when every linked PR is unreachable.
		//
		// Do NOT persist the hash. The linked PRs may be inaccessible only
		// because Argus hasn't been installed on the sibling repo yet; if
		// we persist, the next debounce tick sees an unchanged bundle and
		// skips the LLM, wedging the recovery path forever. Leaving the
		// hash empty forces a re-evaluation on the next event.
		o.logger.Info("[crosspr-stage] all linked PRs inaccessible; leaving hash empty for retry",
			"review_id", reviewID,
			"accessible", 0,
			"inaccessible", inaccessibleCount)
		return
	}

	provider, cfg, ok := o.crossPRResolveProvider(ctx, run, "crossPR")
	if !ok {
		o.logger.Warn("[crosspr-stage] no LLM provider resolved", "review_id", reviewID)
		run.CrossPRCoverage = &CrossPRCoverage{
			LinkedPRs:         hydrated,
			AccessibleCount:   accessibleCount,
			InaccessibleCount: inaccessibleCount,
		}
		return
	}

	var prompt strings.Builder
	prompt.WriteString("Primary PR diff:\n")
	writeDiffSummary(&prompt, run.Diff.Files, 500)

	for _, link := range hydrated {
		if !link.Accessible {
			prompt.WriteString(fmt.Sprintf("\nLinked PR %s/%s#%d — NOT ACCESSIBLE (%s)\n",
				link.Owner, link.Repo, link.Number, link.FetchError))
			continue
		}
		prompt.WriteString(fmt.Sprintf("\nLinked PR %s/%s#%d — %s\n",
			link.Owner, link.Repo, link.Number,
			util.Truncate(link.Title, 200, true)))
		prompt.WriteString(util.Truncate(link.Diff, 3000, false))
		prompt.WriteString("\n")
		writeLinkedPRFindings(&prompt, link)
	}

	// Record quota consumption only at the threshold where LLM work actually
	// starts. All early returns above (flag off, hash stable, no provider,
	// all-inaccessible) must not count against the installation's 30/hour.
	// TryAcquire folds check+record under the same mutex hold to close the
	// TOCTOU window between the peek at step 2a and this commit: a burst of
	// goroutines could otherwise all observe allowed=true at peek time and
	// all record at commit time, overshooting cap.
	if allowed, resetAt := crossPRInstallTryAcquire(run.PREvent.InstallationID, o.crossPRNow()); !allowed {
		o.logger.Warn("[crosspr-stage] installation rate limit hit at commit, skipping",
			"installation_id", run.PREvent.InstallationID,
			"review_id", reviewID,
			"reset_at", resetAt)
		o.surfaceCrossPRRateLimitFooter(ctx, reviewID, review, run, resetAt)
		o.emitCrossPRSkipped(ctx, reviewID, "rate_limited", run.TraceID)
		retryAfterMs := max(int64(time.Until(resetAt).Milliseconds()), 0)
		o.emitRateLimitHit(ctx, "crosspr_global", reviewID.String(), retryAfterMs)
		return
	}

	resp, err := provider.Complete(ctx, llm.CompletionRequest{
		Model:       cfg.Model,
		System:      crossPRCombinationRiskPrompt,
		Messages:    []llm.Message{{Role: "user", Content: prompt.String()}},
		MaxTokens:   crossPRMaxTokens,
		Temperature: 0.1,
		JSONMode:    true,
		Stage:       "crosspr",
	})
	if err != nil {
		// Include error_type / model / provider so a single log line is
		// enough to triage drift between provider-layer error shapes.
		o.logger.Warn("[crosspr-stage] LLM call failed",
			"review_id", reviewID,
			"error", err,
			"error_type", fmt.Sprintf("%T", err),
			"model", cfg.Model,
			"provider", cfg.Provider)
		run.CrossPRCoverage = &CrossPRCoverage{
			LinkedPRs:         hydrated,
			AccessibleCount:   accessibleCount,
			InaccessibleCount: inaccessibleCount,
		}
		return
	}

	o.persistAsyncStageTokens(ctx, reviewID, stageKeyCrossPR, StageTokens{
		PromptTokens:     resp.TokensUsed.PromptTokens,
		CompletionTokens: resp.TokensUsed.CompletionTokens,
		TotalTokens:      resp.TokensUsed.TotalTokens,
		Cost:             resp.Cost,
		Model:            cfg.Model,
		Provider:         cfg.Provider,
	}, run)

	var judged crossPRJudgeResponse
	cleaned := stripCodeFences(resp.Content)
	if err := json.Unmarshal([]byte(cleaned), &judged); err != nil {
		o.logger.Warn("[crosspr-stage] LLM parse failed",
			"review_id", reviewID,
			"error", err,
			"error_type", fmt.Sprintf("%T", err),
			"model", cfg.Model,
			"provider", cfg.Provider,
			"tokens_used", resp.TokensUsed.TotalTokens,
			"cost", resp.Cost,
			"response_prefix", util.Truncate(resp.Content, 200, true))
		run.CrossPRCoverage = &CrossPRCoverage{
			LinkedPRs:         hydrated,
			AccessibleCount:   accessibleCount,
			InaccessibleCount: inaccessibleCount,
		}
		return
	}

	// Schema version guard: unknown version → empty risks (safe default).
	// Never panic, never attempt fallback parsing — a silent shape drift is
	// preferable to speculative interpretation.
	risks := judged.CombinationRisks
	if judged.SchemaVersion != CrossPRJudgeSchemaV1 {
		o.logger.Warn("[crosspr-stage] unknown schema_version, treating as empty",
			"review_id", reviewID,
			"schema_version", judged.SchemaVersion,
			"model", cfg.Model,
			"provider", cfg.Provider,
			"tokens_used", resp.TokensUsed.TotalTokens,
			"cost", resp.Cost)
		risks = nil
	}

	// Normalize + validate each risk. An unknown category means the LLM
	// emitted a bucket outside our nine-value vocabulary — drop the entry
	// (with Warn) so downstream renderers can't encounter a category they
	// don't know how to map. Unknown severity normalizes to Medium.
	validated := risks[:0]
	for _, r := range risks {
		r.Severity = normalizeRiskSeverity(string(r.Severity), o.logger)
		if !r.Category.Valid() {
			o.logger.Warn("[crosspr-stage] unknown risk category, dropping entry",
				"review_id", reviewID,
				"category", string(r.Category),
				"description", util.Truncate(r.Description, 200, true),
				"linked_pr", r.LinkedPR)
			continue
		}
		validated = append(validated, r)
	}
	risks = validated

	incompatibilities := make([]string, 0, len(risks))
	for _, r := range risks {
		if r.Description == "" {
			continue
		}
		entry := r.Description
		if r.LinkedPR != "" {
			entry += " (" + r.LinkedPR + ")"
		}
		incompatibilities = append(incompatibilities, entry)
	}

	run.CrossPRCoverage = &CrossPRCoverage{
		LinkedPRs:         hydrated,
		Incompatibilities: incompatibilities,
		CombinationRisks:  risks,
		AccessibleCount:   accessibleCount,
		InaccessibleCount: inaccessibleCount,
	}
	compatible := run.CrossPRCoverage.IsCompatible()

	o.logger.Info("[crosspr-stage] done",
		"review_id", reviewID,
		"accessible", accessibleCount,
		"inaccessible", inaccessibleCount,
		"combination_risks", len(risks),
		"compatible", compatible)

	if pub := o.crossPRPublisherDep(); pub != nil {
		pub.Publish(reviewID, EventCrossPRChecked, map[string]any{
			"accessible":        accessibleCount,
			"inaccessible":      inaccessibleCount,
			"compatible":        compatible,
			"combination_risks": len(risks),
		})
	}

	// 7. Upsert the cross-PR section into the sticky PR-review comment.
	// Branch on error kind:
	//   - ErrStickyNotFound: review row exists but was never posted (or was
	//     deleted). There's nothing on GitHub to edit and nothing a retry
	//     will fix — persisting the hash is safe and prevents a retry loop.
	//   - ErrMarkersCorrupt: the PR-review body has a torn/duplicate Argus
	//     section. This needs a human look; log Error with enough context
	//     and bail without persisting, so a human can clear the body and
	//     re-trigger.
	//   - Any other error (transient GH 5xx, rate limit, network): retry on
	//     the next debounce tick — skip the hash persist so the idempotency
	//     check doesn't swallow the retry.
	if err := o.upsertCrossPRSticky(ctx, reviewID, review, run); err != nil {
		switch {
		case errors.Is(err, ghpkg.ErrStickyNotFound):
			o.logger.Warn("[crosspr-stage] sticky not found; persisting hash to avoid retry loop",
				"review_id", reviewID, "error", err)
		case errors.Is(err, ghpkg.ErrMarkersCorrupt):
			githubReviewID := int64(0)
			if review.GithubReviewID != nil {
				githubReviewID = *review.GithubReviewID
			}
			o.logger.Error("[crosspr-stage] sticky markers corrupt; skipping persist, needs manual intervention",
				"review_id", reviewID,
				"github_review_id", githubReviewID,
				"section", "crosspr",
				"error", err)
			return
		default:
			o.logger.Warn("[crosspr-stage] sticky update failed; will retry on next tick",
				"review_id", reviewID, "error", err)
			// Don't persist the hash on transient failures. Otherwise the
			// next debounce tick sees bundle unchanged and skips, wedging
			// the PR with the outdated (or missing) section.
			return
		}
	}

	// 8. Persist the hash last. A crash between upsertCrossPRSticky and
	// here means the sticky is fresh but the hash lags — the next refresh
	// recomputes, finds the bundle unchanged, and skips. Safe.
	o.persistCrossPRHash(ctx, reviewID, hash)

	// cross_pr.stage.completed fires only on the happy path — every early
	// return above emits a `cross_pr.stage.skipped` with a reason so the
	// funnel lines up (every invocation ends in either .completed or
	// .skipped; no silent "dropped" state).
	o.logger.InfoContext(ctx, "cross-PR stage completed",
		slog.String("event", "cross_pr.stage.completed"),
		slog.String("primary_review_id", reviewID.String()),
		slog.Int("linked_count", accessibleCount),
		slog.Int("risks_found", len(risks)),
		slog.Int64("duration_ms", time.Since(crossPRStageStart).Milliseconds()),
		slog.String("trace_id", run.TraceID),
	)
}

// upsertCrossPRSticky edits the "argus:crosspr" section of the primary
// PR review body with the rendered coverage markdown. Caller guarantees
// run.CrossPRCoverage is non-nil. Returns nil (no error, no-op) when
// the formatted section is empty — nothing useful to post.
func (o *Orchestrator) upsertCrossPRSticky(ctx context.Context, reviewID uuid.UUID, review *store.Review, run *PipelineRun) error {
	_ = reviewID
	sectionMD := formatCrossPRCoverageSection(run.CrossPRCoverage)
	if sectionMD == "" {
		return nil
	}
	if review.GithubReviewID == nil || *review.GithubReviewID <= 0 {
		// Review row exists but was never posted (e.g. failed post, stale
		// recovery). Nothing to edit — the main post path will include
		// this section next time it runs.
		return nil
	}
	owner, repo, err := splitRepoFullName(run.PREvent.RepoFullName)
	if err != nil {
		return fmt.Errorf("split repo %q: %w", run.PREvent.RepoFullName, err)
	}
	return o.crossPRGithubDep().UpdateStickySection(
		ctx,
		run.PREvent.InstallationID,
		owner, repo,
		run.PREvent.PRNumber,
		*review.GithubReviewID,
		"crosspr",
		sectionMD,
	)
}

// surfaceCrossPRRateLimitFooter writes a short rate-limit notice into the
// "argus:crosspr" sticky section so the user sees the backpressure instead
// of a silent drop. Best-effort: if the GitHub edit itself fails we log and
// return — do NOT let a sticky error cascade back to the caller.
//
// The section body deliberately replaces whatever cross-PR coverage was last
// posted: per spec, the rate-limit notice is the current truth until the
// quota window rolls over and the next trigger succeeds.
func (o *Orchestrator) surfaceCrossPRRateLimitFooter(ctx context.Context, reviewID uuid.UUID, review *store.Review, run *PipelineRun, resetAt time.Time) {
	if review == nil || review.GithubReviewID == nil || *review.GithubReviewID <= 0 {
		return
	}
	owner, repo, err := splitRepoFullName(run.PREvent.RepoFullName)
	if err != nil {
		o.logger.Warn("[crosspr-stage] rate-limit footer: split repo failed",
			"review_id", reviewID, "repo_full_name", run.PREvent.RepoFullName, "error", err)
		return
	}
	body := fmt.Sprintf("_⏱ cross-PR refresh rate-limited until %s UTC — check back shortly._",
		resetAt.UTC().Format("15:04"))
	if err := o.crossPRGithubDep().UpdateStickySection(
		ctx,
		run.PREvent.InstallationID,
		owner, repo,
		run.PREvent.PRNumber,
		*review.GithubReviewID,
		"crosspr",
		body,
	); err != nil {
		o.logger.Warn("[crosspr-stage] rate-limit footer update failed",
			"review_id", reviewID, "error", err)
	}
}

// persistReviewLinkedPRRefs writes the primary review's LinkedPRs as a
// JSONB array of {owner, repo, number} into reviews.linked_pr_refs. Called
// from synthesis BEFORE EventReviewCompleted publishes, so sibling reverse
// lookups (FindReviewsLinkingToPR) see up-to-date containment data.
//
// Non-fatal: any error is logged at Warn. Missing refs in the DB mean a
// sibling-PR completion won't fan out a refresh to this review — the next
// push to either PR re-triggers, so the only user-visible impact is a
// delayed cross-PR section update, not a data loss.
//
// Empty LinkedPRs is still persisted as '[]'::jsonb (the column default)
// so we never leave stale values from a prior run's refs.
func (o *Orchestrator) persistReviewLinkedPRRefs(ctx context.Context, run *PipelineRun) {
	type ref struct {
		Owner  string `json:"owner"`
		Repo   string `json:"repo"`
		Number int    `json:"number"`
	}
	refs := make([]ref, 0, len(run.LinkedPRs))
	for _, l := range run.LinkedPRs {
		if l.Owner == "" || l.Repo == "" || l.Number <= 0 {
			continue
		}
		refs = append(refs, ref{Owner: l.Owner, Repo: l.Repo, Number: l.Number})
	}
	raw, err := json.Marshal(refs)
	if err != nil {
		o.logger.Warn("[synthesis] marshal linked_pr_refs failed",
			"review_id", run.ReviewID, "error", err)
		return
	}
	if err := o.crossPRStoreDep().SetReviewLinkedPRRefs(ctx, db.SetReviewLinkedPRRefsParams{
		ID:           run.ReviewID,
		LinkedPRRefs: raw,
	}); err != nil {
		o.logger.Warn("[synthesis] persist linked_pr_refs failed",
			"review_id", run.ReviewID, "error", err)
	}
}

// persistCrossPRHash writes the hash via UpdateReviewCrossPRHash, wrapping
// the hex string in a *string as required by the sqlc-generated params type.
// Non-fatal: logs and returns on failure — the next refresh will just recompute.
func (o *Orchestrator) persistCrossPRHash(ctx context.Context, reviewID uuid.UUID, hash string) {
	h := hash
	if err := o.crossPRStoreDep().UpdateReviewCrossPRHash(ctx, db.UpdateReviewCrossPRHashParams{
		ID:          reviewID,
		CrossPRHash: &h,
	}); err != nil {
		o.logger.Warn("[crosspr-stage] persist hash failed", "review_id", reviewID, "error", err)
	}
}

// hydratePriorFindings enriches a PRLink with a PriorReview snapshot by
// consulting GetLatestCompletedReviewByPR. The primary Accessible/Diff
// fields must already be populated by hydratePRLink; we only touch the
// PriorReview pointer here.
//
// Auto-resolved findings filtering: migration 041 added resolved_thread_keys
// to auto_resolve_events — one "<path>:<line>" key per thread the auto-resolve
// goroutine closed on a post-review push. The sqlc row flattens those into
// AutoResolvedThreadKeys; filterAutoResolvedFindings drops Findings whose
// Path:Line matches so closed threads don't leak back into the cross-PR
// prompt.
//
// pgx.ErrNoRows simply means the linked PR was never reviewed — leave
// link.PriorReview nil. Callers check nil-ness to distinguish "no review"
// from "reviewed, no open findings" (non-nil, empty Findings).
func hydratePriorFindings(ctx context.Context, o *Orchestrator, link PRLink) PRLink {
	// Look up the repo row so we can feed repoID to GetLatestCompletedReviewByPR.
	// The linked PR may live in a repo Argus is not installed on — in that case
	// repo lookup fails; we leave Reviewed=false and return.
	st := o.crossPRStoreDep()
	fullName := link.Owner + "/" + link.Repo
	repo, err := st.GetRepoByFullName(ctx, fullName)
	if err != nil {
		// pgx.ErrNoRows is the documented "Argus not installed on this
		// repo" shape — graceful, not an error. Any OTHER error is an
		// infra problem (DB down, schema drift, timeout) that we do NOT
		// want to silently mask as "linked PR not reviewed".
		if !errors.Is(err, pgx.ErrNoRows) {
			o.logger.Warn("[crosspr] repo lookup failed — linked PR degraded to diff-only",
				"repo", fullName, "error", err)
		}
		return link
	}

	row, err := st.GetLatestCompletedReviewByPR(ctx, db.GetLatestCompletedReviewByPRParams{
		RepoID:   repo.ID,
		PRNumber: link.Number,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return link
		}
		o.logger.Warn("[crosspr-stage] prior-review lookup failed",
			"repo", fullName, "pr", link.Number, "error", err)
		return link
	}

	var files []FileReview
	if len(row.AllFileReviews) > 0 {
		if err := json.Unmarshal(row.AllFileReviews, &files); err != nil {
			o.logger.Warn("[crosspr-stage] prior findings unmarshal failed",
				"repo", fullName, "pr", link.Number, "error", err)
			return link
		}
	}

	findings := findingsFromFileReviews(files, row.ID.String())

	// Drop findings whose thread was auto-resolved since the review completed.
	// row.AutoResolvedThreadKeys is the flattened "<path>:<line>" set built by
	// GetLatestCompletedReviewByPR's UNNEST over auto_resolve_events rows that
	// fired after rv.completed_at (see migration 041).
	total := len(findings)
	kept := filterAutoResolvedFindings(findings, row.AutoResolvedThreadKeys)
	if filtered := total - len(kept); filtered > 0 {
		o.logger.Info("[crosspr] filtered auto-resolved findings",
			"review_id", row.ID,
			"filtered_count", filtered,
			"kept_count", len(kept),
			"total", total)
	}

	snap := &PriorReviewSnapshot{
		Findings: kept,
		HeadSHA:  row.HeadSHA,
	}
	if row.CompletedAt != nil {
		snap.ReviewedAt = *row.CompletedAt
	}
	link.PriorReview = snap
	return link
}

// filterAutoResolvedFindings drops any Finding whose "<path>:<line>" key
// appears in resolvedKeys — the set of thread locations that auto_resolve
// closed on pushes AFTER the linked PR's review completed. Returns the
// surviving Findings; the caller computes the filtered count via len-diff.
//
// Join-key shape: auto_resolve_events.resolved_thread_keys (migration 041)
// stores the resolved GitHub thread's Path+Line in the same format used
// below via findingKey, so the match is exact and O(1) per finding after
// the set is built.
//
// Semantics:
//   - Empty findings or empty resolvedKeys: pass-through (no allocation).
//   - Empty-string entries in resolvedKeys: ignored (defensive guard against
//     sqlc array scanning quirks).
//   - A zero-line Finding (Line == 0) can never match a migration-041 key
//     because the writer skips Line == 0 threads — so line-0 findings
//     always pass through, which is the right answer.
//
// The returned slice is a freshly allocated copy even when nothing is
// dropped, so callers cannot accidentally mutate the caller's backing
// array.
func filterAutoResolvedFindings(findings []Finding, resolvedKeys []string) []Finding {
	if len(findings) == 0 || len(resolvedKeys) == 0 {
		return findings
	}
	resolved := make(map[string]struct{}, len(resolvedKeys))
	for _, k := range resolvedKeys {
		if k == "" {
			continue
		}
		resolved[k] = struct{}{}
	}
	if len(resolved) == 0 {
		return findings
	}
	kept := make([]Finding, 0, len(findings))
	for _, f := range findings {
		if _, drop := resolved[findingKey(f)]; drop {
			continue
		}
		kept = append(kept, f)
	}
	return kept
}

// findingKey formats a Finding into the "<path>:<line>" shape used as the
// join key against auto_resolve_events.resolved_thread_keys. Centralised so
// writer (autoResolveStaleComments) and reader (filterAutoResolvedFindings)
// can't drift.
func findingKey(f Finding) string {
	return fmt.Sprintf("%s:%d", f.Path, f.Line)
}

// writeLinkedPRFindings emits the prior-findings block for one linked PR.
//
// Emission rules:
//   - link.PriorReview == nil → single "not reviewed by Argus — diff context only"
//     line, no findings list.
//   - PriorReview.HeadSHA mismatch with link.HeadSHA → the header carries a
//     staleness marker (prior SHA → current SHA).
//   - Findings truncate at crossPRFindingsPerLink with an explicit "…and N
//     more (truncated)" marker. Silent truncation would be a bug.
//   - PriorReview.ReviewedAt, when non-zero, is rendered as "reviewed Xh ago"
//     so the judge knows how fresh the context is. Age is derived at format
//     time via time.Since so a snapshot sitting in memory doesn't drift.
//
// Uses short-sha (7 chars) to match GitHub UI conventions; full sha is
// never needed for prompt readability.
func writeLinkedPRFindings(sb *strings.Builder, link PRLink) {
	if link.PriorReview == nil {
		sb.WriteString("(not reviewed by Argus — diff context only)\n")
		return
	}
	pr := link.PriorReview

	key := fmt.Sprintf("%s/%s#%d", link.Owner, link.Repo, link.Number)
	header := fmt.Sprintf("Prior findings in %s", key)
	var age time.Duration
	if !pr.ReviewedAt.IsZero() {
		age = time.Since(pr.ReviewedAt)
	}
	if age > 0 {
		header += fmt.Sprintf(" (reviewed %s ago", humanDuration(age))
		if pr.HeadSHA != "" {
			header += fmt.Sprintf(" at head_sha %s", shortSHA(pr.HeadSHA))
		}
		header += ")"
	} else if pr.HeadSHA != "" {
		header += fmt.Sprintf(" (reviewed at head_sha %s)", shortSHA(pr.HeadSHA))
	}
	if pr.HeadSHA != "" && link.HeadSHA != "" && pr.HeadSHA != link.HeadSHA {
		header += fmt.Sprintf(" (findings are stale — reviewed at %s, now at %s)",
			shortSHA(pr.HeadSHA), shortSHA(link.HeadSHA))
	}
	sb.WriteString(header)
	sb.WriteString(":\n")

	if len(pr.Findings) == 0 {
		sb.WriteString("  (no open findings)\n")
		return
	}

	for i, f := range pr.Findings {
		if i >= crossPRFindingsPerLink {
			sb.WriteString(fmt.Sprintf("  …and %d more (truncated)\n",
				len(pr.Findings)-crossPRFindingsPerLink))
			break
		}
		sb.WriteString(fmt.Sprintf("- [%s] %s:%d — %s\n",
			f.Severity, f.Path, f.Line, util.Truncate(f.Summary, 160, true)))
	}
}

// humanDuration renders a coarse "Xh ago" / "Xd ago" for prompt headers.
// Precision above hour is deliberate: minute-level age in a judge prompt
// is noise, and day-level is sufficient for staleness signal.
func humanDuration(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// computeCrossPRHash produces a stable sha256 over the inputs that, if
// unchanged, guarantee the LLM output will also be unchanged. We include
// primary head_sha (so a force-push invalidates), and for each linked PR:
// sorted owner/repo/number key, head_sha, accessibility flag, diff, and
// a compact projection of prior findings. Sorting is by owner/repo/number
// so a reorder of the LinkedPRs slice doesn't cause a spurious miss.
//
// Returns an error on marshal failure so the caller can bail rather than
// persisting a phantom "hash of nil" that would match every subsequent
// failed-marshal call and silently wedge the idempotency guard.
func computeCrossPRHash(primaryHeadSHA string, links []PRLink) (string, error) {
	type bucket struct {
		Key        string
		HeadSHA    string
		Accessible bool
		Diff       string
		Findings   []string
	}
	bs := make([]bucket, 0, len(links))
	for _, l := range links {
		key := fmt.Sprintf("%s/%s#%d", l.Owner, l.Repo, l.Number)
		var priorFindings []Finding
		if l.PriorReview != nil {
			priorFindings = l.PriorReview.Findings
		}
		findings := make([]string, 0, len(priorFindings))
		for _, f := range priorFindings {
			findings = append(findings, fmt.Sprintf("%s|%d|%s|%s|%s",
				f.Path, f.Line, f.Severity, f.Category, f.Summary))
		}
		sort.Strings(findings)
		bs = append(bs, bucket{
			Key:        key,
			HeadSHA:    l.HeadSHA,
			Accessible: l.Accessible,
			Diff:       l.Diff,
			Findings:   findings,
		})
	}
	sort.Slice(bs, func(i, j int) bool { return bs[i].Key < bs[j].Key })

	payload := struct {
		PrimaryHeadSHA string   `json:"primary_head_sha"`
		Links          []bucket `json:"links"`
	}{PrimaryHeadSHA: primaryHeadSHA, Links: bs}
	raw, err := json.Marshal(&payload)
	if err != nil {
		return "", fmt.Errorf("marshal cross-PR hash payload: %w", err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// --- Joint acceptance stage ---

// persistReviewLinkedIssueRefs writes run.LinkedIssues as a JSONB array
// of {owner, repo, number} into reviews.linked_issue_refs. Mirrors
// persistReviewLinkedPRRefs: called from synthesis BEFORE the
// EventReviewCompleted publish so FindSharedLinkedIssues sees this
// review's refs the instant a sibling fires.
//
// Non-fatal on error (logged at Warn). Missing refs delay joint
// acceptance by one push cycle, never lose data permanently.
//
// Empty LinkedIssues is still persisted as '[]'::jsonb (column default)
// so a prior run's refs don't linger after the PR body is edited to
// remove the issue links.
func (o *Orchestrator) persistReviewLinkedIssueRefs(ctx context.Context, run *PipelineRun) {
	type ref struct {
		Owner  string `json:"owner"`
		Repo   string `json:"repo"`
		Number int    `json:"number"`
	}
	refs := make([]ref, 0, len(run.LinkedIssues))
	for _, l := range run.LinkedIssues {
		if l.Owner == "" || l.Repo == "" || l.Number <= 0 {
			continue
		}
		refs = append(refs, ref{Owner: l.Owner, Repo: l.Repo, Number: l.Number})
	}
	raw, err := json.Marshal(refs)
	if err != nil {
		o.logger.Warn("[synthesis] marshal linked_issue_refs failed",
			"review_id", run.ReviewID, "error", err)
		return
	}
	if err := o.crossPRStoreDep().SetReviewLinkedIssueRefs(ctx, db.SetReviewLinkedIssueRefsParams{
		ID:              run.ReviewID,
		LinkedIssueRefs: raw,
	}); err != nil {
		o.logger.Warn("[synthesis] persist linked_issue_refs failed",
			"review_id", run.ReviewID, "error", err)
	}
}

// jointAcceptanceMutexes serializes runCrossPRAcceptanceStage per review
// id, mirroring crossPRMutexes. A separate map so findings-stage and
// acceptance-stage can progress independently — locking on the same key
// would force serialization between two stages that do disjoint work.
var jointAcceptanceMutexes = newMutexMap()

func acquireJointAcceptanceMutex(reviewID uuid.UUID) *sync.Mutex {
	return jointAcceptanceMutexes.acquire(reviewID)
}

// jointLinkedReviewSummary is the per-sibling bundle emitted into the
// joint-judge prompt: owner/repo#N key + head_sha + unified diff +
// flattened prior findings. Kept small and prompt-ready.
type jointLinkedReviewSummary struct {
	Key        string // owner/repo#N
	Owner      string
	Repo       string
	Number     int
	HeadSHA    string
	ReviewID   uuid.UUID
	Title      string
	Diff       string
	Findings   []Finding
	Accessible bool
	FetchError string
}

// runCrossPRAcceptanceStage judges whether the COMBINED change across
// linked PRs addresses each criterion of a shared linked issue. Mirrors
// runCrossPRStage's event-driven lifecycle but operates at the issue
// granularity: one judge call per shared issue (not per linked PR).
//
// Posts a new "## Joint Issue Coverage" section on the sticky, alongside
// the existing "## Issue Coverage" (per-PR) section. Per-PR acceptance is
// untouched — this runs additively.
//
// Gated by the joint-acceptance per-review mutex and the same per-install
// rate limit as the findings stage. FindSharedLinkedIssues short-circuits
// when fewer than 2 linked PRs reference the same issue. For each shared
// issue the stage fetches body + criteria, hydrates sibling reviews
// (diff + prior findings) in parallel, and issues one LLM verdict per
// issue — aggregated output goes out as one sticky section write.
func (o *Orchestrator) runCrossPRAcceptanceStage(ctx context.Context, reviewID uuid.UUID) {
	mu := acquireJointAcceptanceMutex(reviewID)
	mu.Lock()
	defer mu.Unlock()

	st := o.crossPRStoreDep()
	stateDep := o.crossPRStateDep()

	review, err := st.GetReview(ctx, reviewID)
	if err != nil {
		o.logger.Warn("[joint-accept] load review failed", "review_id", reviewID, "error", err)
		return
	}
	if review.Status != "completed" {
		return
	}

	runID, err := st.GetLatestRunForReview(ctx, reviewID)
	if err != nil {
		o.logger.Warn("[joint-accept] no pipeline run for review", "review_id", reviewID, "error", err)
		return
	}
	run, err := stateDep.LoadRun(ctx, runID)
	if err != nil {
		o.logger.Warn("[joint-accept] load pipeline state failed", "run_id", runID, "review_id", reviewID, "error", err)
		return
	}

	// Rehydrate attribution — same rationale as runCrossPRStage. Bus
	// dispatch path gave us context.Background(); PostHog handler would
	// drop cross_pr.acceptance.completed + llm.call.completed
	// (stage=joint_accept) without this.
	ctx = obs.SetInstallationID(ctx, run.DBInstallationID)
	if run.PREvent.PRAuthor != "" {
		ctx = obs.SetGithubLogin(ctx, run.PREvent.PRAuthor)
	}
	if run.TraceID != "" {
		ctx = obs.SetTraceID(ctx, run.TraceID)
	}

	// Per-install rate limit: shared counter with findings stage so an
	// installation can't evade the 30/hour cap by triggering both stages.
	// PEEK here — reserve quota later via crossPRInstallTryAcquire once we
	// know there's actual LLM work to do.
	if allowed, _ := crossPRInstallPeek(run.PREvent.InstallationID, o.crossPRNow()); !allowed {
		o.logger.Warn("[joint-accept] installation rate limit hit, skipping",
			"installation_id", run.PREvent.InstallationID, "review_id", reviewID)
		return
	}

	flags := st.LoadFeatureFlags(ctx, run.DBInstallationID)
	if !flags.CrossPRChecks {
		return
	}

	shared, err := st.FindSharedLinkedIssues(ctx, reviewID)
	if err != nil {
		o.logger.Warn("[joint-accept] find shared issues failed", "review_id", reviewID, "error", err)
		return
	}
	if len(shared) == 0 {
		return
	}

	provider, cfg, ok := o.crossPRResolveProvider(ctx, run, "jointAcceptance")
	if !ok {
		o.logger.Warn("[joint-accept] no LLM provider resolved", "review_id", reviewID)
		return
	}

	// Commit quota atomically: we have shared issues AND a provider, so LLM
	// work is imminent. One consumption per stage run, not per issue — the
	// cap is about stage dispatches, not per-issue costs. TryAcquire folds
	// check+record under one mutex hold to close the TOCTOU window between
	// the peek earlier in this function and here.
	if allowed, _ := crossPRInstallTryAcquire(run.PREvent.InstallationID, o.crossPRNow()); !allowed {
		o.logger.Warn("[joint-accept] installation rate limit hit at commit, skipping",
			"installation_id", run.PREvent.InstallationID, "review_id", reviewID)
		return
	}

	// Per-issue hydrate + judge loop. Each goroutine derives its OWN
	// per-issue timeout budget — a single shared timeout across all issues
	// would let one slow issue starve the rest.
	// Parallelized with a bounded errgroup: each shared issue is an
	// independent LLM call, saving ~10-30s on multi-issue cases.
	// Concurrency is deliberately lower than hydration/sibling fan-outs
	// because each unit is an LLM round trip (spending + QPS limit).
	//
	// Cap applied upfront — break is not available across goroutines.
	capped := shared
	if len(capped) > jointMaxSharedIssues {
		o.logger.Info("[joint-accept] shared-issue cap hit",
			"review_id", reviewID, "total", len(shared), "cap", jointMaxSharedIssues)
		capped = capped[:jointMaxSharedIssues]
	}

	slots := make([]*JointAcceptanceResult, len(capped))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(crossPRJointIssueConcurrency)
	for i, row := range capped {
		i, row := i, row
		g.Go(func() error {
			issueCtx, cancel := context.WithTimeout(gctx, crossPRHydrateTimeout)
			defer cancel()
			slots[i] = o.judgeSharedIssue(issueCtx, run, provider, cfg, row)
			return nil // per-issue failures already logged inside judgeSharedIssue
		})
	}
	_ = g.Wait()

	results := make([]JointAcceptanceResult, 0, len(slots))
	for _, res := range slots {
		if res != nil {
			results = append(results, *res)
		}
	}

	if len(results) == 0 {
		o.logger.Info("[joint-accept] no results produced", "review_id", reviewID, "shared", len(shared))
		return
	}

	run.JointAcceptance = results
	o.logger.Info("[joint-accept] done",
		"review_id", reviewID,
		"shared_issues", len(shared),
		"judged", len(results))

	// cross_pr.acceptance.completed is the joint (multi-PR) variant — the
	// per-PR acceptance event is emitted under stage.completed for
	// StateValidating. issues_evaluated counts the LLM-judged rows, not the
	// full shared-issue candidate set (capped via jointMaxSharedIssues).
	o.logger.InfoContext(ctx, "cross-PR joint acceptance completed",
		slog.String("event", "cross_pr.acceptance.completed"),
		slog.String("primary_review_id", reviewID.String()),
		slog.Int("issues_evaluated", len(results)),
		slog.String("trace_id", run.TraceID),
	)

	if pub := o.crossPRPublisherDep(); pub != nil {
		pub.Publish(reviewID, EventAcceptanceChecked, map[string]any{
			"joint":  true,
			"judged": len(results),
			"shared": len(shared),
		})
	}

	if err := o.upsertJointAcceptanceSticky(ctx, review, run); err != nil {
		o.logger.Warn("[joint-accept] sticky update failed",
			"review_id", reviewID, "error", err)
	}
}

// judgeSharedIssue hydrates the issue body + sibling reviews for one
// shared-issue row and runs the joint LLM judge. Returns nil on any
// unrecoverable error (logged) — callers simply skip absent results.
func (o *Orchestrator) judgeSharedIssue(
	ctx context.Context,
	run *PipelineRun,
	provider llm.Provider,
	cfg llm.ModelConfig,
	row db.FindSharedLinkedIssuesRow,
) *JointAcceptanceResult {
	issue, err := o.crossPRGithubDep().GetIssue(ctx, run.PREvent.InstallationID, row.Owner, row.Repo, row.Number)
	if err != nil {
		o.logger.Warn("[joint-accept] fetch issue failed",
			"issue", fmt.Sprintf("%s/%s#%d", row.Owner, row.Repo, row.Number), "error", err)
		return nil
	}
	criteria := extractCriteria(issue.Body)
	if len(criteria) == 0 {
		o.logger.Info("[joint-accept] issue has no criteria, skipping",
			"issue", fmt.Sprintf("%s/%s#%d", row.Owner, row.Repo, row.Number))
		return nil
	}

	siblings := o.hydrateLinkedReviews(ctx, run, row.ReviewIds)
	if len(siblings) < 2 {
		// Race: a sibling's status flipped or a row vanished between
		// FindSharedLinkedIssues and hydration. Skip — joint acceptance
		// requires >=2 reviews by definition.
		o.logger.Info("[joint-accept] fewer than 2 siblings after hydrate, skipping",
			"issue", fmt.Sprintf("%s/%s#%d", row.Owner, row.Repo, row.Number),
			"hydrated", len(siblings))
		return nil
	}

	var prompt strings.Builder
	prompt.WriteString(fmt.Sprintf("Issue %s/%s#%d — %s\n",
		row.Owner, row.Repo, row.Number, util.Truncate(issue.Title, 200, true)))
	prompt.WriteString("Criteria:\n")
	for i, c := range criteria {
		prompt.WriteString(fmt.Sprintf("%d. %s\n", i+1, c))
	}

	for _, s := range siblings {
		prompt.WriteString(fmt.Sprintf("\nLinked PR %s — %s\n",
			s.Key, util.Truncate(s.Title, 200, true)))
		if !s.Accessible {
			prompt.WriteString(fmt.Sprintf("(NOT ACCESSIBLE: %s)\n", s.FetchError))
			continue
		}
		prompt.WriteString(util.Truncate(s.Diff, 3000, false))
		prompt.WriteString("\n")
		if len(s.Findings) > 0 {
			prompt.WriteString(fmt.Sprintf("Prior findings in %s:\n", s.Key))
			for i, f := range s.Findings {
				if i >= crossPRFindingsPerLink {
					prompt.WriteString(fmt.Sprintf("  …and %d more (truncated)\n",
						len(s.Findings)-crossPRFindingsPerLink))
					break
				}
				prompt.WriteString(fmt.Sprintf("- [%s] %s:%d — %s\n",
					f.Severity, f.Path, f.Line, util.Truncate(f.Summary, 160, true)))
			}
		}
	}

	resp, err := provider.Complete(ctx, llm.CompletionRequest{
		Model:       cfg.Model,
		System:      jointAcceptanceJudgePrompt,
		Messages:    []llm.Message{{Role: "user", Content: prompt.String()}},
		MaxTokens:   acceptanceMaxTokens,
		Temperature: 0.1,
		JSONMode:    true,
		Stage:       "crosspr_acceptance",
	})
	if err != nil {
		o.logger.Warn("[joint-accept] LLM call failed",
			"issue", fmt.Sprintf("%s/%s#%d", row.Owner, row.Repo, row.Number),
			"error", err,
			"error_type", fmt.Sprintf("%T", err),
			"model", cfg.Model,
			"provider", cfg.Provider)
		return nil
	}

	// Joint acceptance reuses the Acceptance bucket — it's an issue
	// judgment, not a cross-PR risk judgment. Keeps stats pages aligned
	// with user-visible groupings ("acceptance" = everything issue-shaped).
	// run.ReviewID (not a local reviewID) — judgeSharedIssue is called
	// per shared-issue row from runCrossPRAcceptanceStage, which loaded
	// the run via loadState; the primary review id rides on the run.
	o.persistAsyncStageTokens(ctx, run.ReviewID, stageKeyAcceptance, StageTokens{
		PromptTokens:     resp.TokensUsed.PromptTokens,
		CompletionTokens: resp.TokensUsed.CompletionTokens,
		TotalTokens:      resp.TokensUsed.TotalTokens,
		Cost:             resp.Cost,
		Model:            cfg.Model,
		Provider:         cfg.Provider,
	}, run)

	var judged jointAcceptanceJudgeResponse
	cleaned := stripCodeFences(resp.Content)
	if err := json.Unmarshal([]byte(cleaned), &judged); err != nil {
		o.logger.Warn("[joint-accept] LLM parse failed",
			"issue", fmt.Sprintf("%s/%s#%d", row.Owner, row.Repo, row.Number),
			"error", err,
			"error_type", fmt.Sprintf("%T", err),
			"model", cfg.Model,
			"provider", cfg.Provider,
			"tokens_used", resp.TokensUsed.TotalTokens,
			"cost", resp.Cost,
			"response_prefix", util.Truncate(resp.Content, 200, true))
		return nil
	}

	if judged.SchemaVersion != JointAcceptanceSchemaV1 {
		o.logger.Warn("[joint-accept] unknown schema_version, treating as empty",
			"issue", fmt.Sprintf("%s/%s#%d", row.Owner, row.Repo, row.Number),
			"schema_version", judged.SchemaVersion,
			"model", cfg.Model,
			"provider", cfg.Provider,
			"tokens_used", resp.TokensUsed.TotalTokens,
			"cost", resp.Cost)
		return nil
	}

	// Normalize / fill fields the LLM may have omitted — owner/repo/number
	// are authoritatively ours; the URL comes from GitHub, not the LLM.
	judged.IssueOwner = row.Owner
	judged.IssueRepo = row.Repo
	judged.IssueNumber = row.Number
	if judged.IssueTitle == "" {
		judged.IssueTitle = issue.Title
	}
	judged.IssueURL = issue.URL
	for i := range judged.Criteria {
		judged.Criteria[i].Status = normalizeStatus(string(judged.Criteria[i].Status))
	}
	judged.Verdict = normalizeJointVerdict(string(judged.Verdict), judged.Criteria)

	return &judged
}

// normalizeJointVerdict enforces the rollup rules the prompt specifies,
// guarding against an LLM that emits a verdict inconsistent with its
// per-criterion statuses. Takes the raw LLM string (because the upstream
// JSON may have anything) and returns a canonical JointVerdict.
func normalizeJointVerdict(v string, criteria []JointAcceptanceCriterion) JointVerdict {
	candidate := JointVerdict(strings.ToLower(strings.TrimSpace(v)))
	if candidate.Valid() {
		// Trust the LLM's rollup when it's one of the three canonical
		// verdicts; the judge prompt is strict enough that a garbage
		// rollup is rare and a mis-agreeing rollup is informative.
		return candidate
	}
	// Fallback: derive from criteria.
	addr, part, unaddr := 0, 0, 0
	for _, c := range criteria {
		switch c.Status {
		case AcceptanceStatusAddressed:
			addr++
		case AcceptanceStatusPartial:
			part++
		case AcceptanceStatusUnaddressed:
			unaddr++
		}
	}
	switch {
	case addr == len(criteria) && len(criteria) > 0:
		return JointVerdictAddressed
	case addr == 0 && part == 0 && unaddr > 0:
		return JointVerdictUnaddressed
	default:
		return JointVerdictPartial
	}
}

// hydrateLinkedReviews loads each sibling review's PR diff + prior
// findings into a prompt-ready slice. Failures per-review are non-fatal
// (Accessible=false carries a reason); the caller drops entries below
// the >=2-sibling floor.
func (o *Orchestrator) hydrateLinkedReviews(
	ctx context.Context,
	run *PipelineRun,
	reviewIDs []uuid.UUID,
) []jointLinkedReviewSummary {
	// Cap upfront — the cap can't live inside g.Go (break is not available
	// across goroutines), and logging the cap hit once here matches the
	// prior observable surface.
	ids := reviewIDs
	if len(ids) > jointMaxSiblings {
		o.logger.Info("[joint-accept] sibling cap hit",
			"cap", jointMaxSiblings, "total", len(reviewIDs))
		ids = ids[:jointMaxSiblings]
	}

	// Parallel hydration: each sibling costs 4-5 sequential calls
	// (GetReview, GetRepo, GetAllFileReviewsForReview, GetPRDiff,
	// optionally GetPullRequest). Serial at jointMaxSiblings=5 was ~7.5s.
	// Each goroutine writes its own slot; zero-key entries are filtered
	// out after Wait so ordering matches the original (ids-order) output.
	slots := make([]jointLinkedReviewSummary, len(ids))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(crossPRJointSiblingConcurrency)
	for i, rid := range ids {
		i, rid := i, rid
		g.Go(func() error {
			slots[i] = o.hydrateLinkedReview(gctx, run, rid)
			return nil // per-review errors already logged + encoded inside hydrateLinkedReview
		})
	}
	_ = g.Wait()

	out := make([]jointLinkedReviewSummary, 0, len(slots))
	for _, s := range slots {
		if s.Key == "" {
			// Repo row missing / unrecoverable — skip silently, already
			// logged inside hydrateLinkedReview.
			continue
		}
		out = append(out, s)
	}
	return out
}

// hydrateLinkedReview loads one sibling review: its repo row (for
// owner/repo name), its PR diff (via GetPRDiff), and its prior findings
// (from pipeline_states.AllFileReviews). Any failure past repo-lookup
// flips Accessible=false and records a short reason in FetchError; the
// entry is still emitted so the prompt can mark the sibling as
// diff-free.
func (o *Orchestrator) hydrateLinkedReview(
	ctx context.Context,
	run *PipelineRun,
	reviewID uuid.UUID,
) jointLinkedReviewSummary {
	st := o.crossPRStoreDep()
	gh := o.crossPRGithubDep()
	rv, err := st.GetReview(ctx, reviewID)
	if err != nil {
		o.logger.Warn("[joint-accept] sibling review load failed",
			"review_id", reviewID, "error", err)
		return jointLinkedReviewSummary{}
	}
	repo, err := st.GetRepo(ctx, rv.RepoID)
	if err != nil {
		o.logger.Warn("[joint-accept] sibling repo load failed",
			"review_id", reviewID, "repo_id", rv.RepoID, "error", err)
		return jointLinkedReviewSummary{}
	}
	owner, repoName, err := splitRepoFullName(repo.FullName)
	if err != nil {
		o.logger.Warn("[joint-accept] sibling split repo failed",
			"review_id", reviewID, "full_name", repo.FullName, "error", err)
		return jointLinkedReviewSummary{}
	}

	sum := jointLinkedReviewSummary{
		Key:      fmt.Sprintf("%s/%s#%d", owner, repoName, rv.PRNumber),
		Owner:    owner,
		Repo:     repoName,
		Number:   rv.PRNumber,
		HeadSHA:  rv.HeadSHA,
		ReviewID: reviewID,
	}

	// Prior findings: reuse the AllFileReviews projection we already have
	// on the sibling's latest pipeline state. This is the same source the
	// async cross-PR stage uses (see hydratePriorFindings).
	rawFiles, err := st.GetAllFileReviewsForReview(ctx, reviewID)
	switch {
	case err == nil && len(rawFiles) > 0:
		var files []FileReview
		if err := json.Unmarshal(rawFiles, &files); err == nil {
			sum.Findings = findingsFromFileReviews(files, reviewID.String())
		} else {
			o.logger.Warn("[joint-accept] sibling findings unmarshal failed",
				"review_id", reviewID, "error", err)
		}
	case err != nil && !errors.Is(err, pgx.ErrNoRows):
		// Non-ErrNoRows: real DB problem (timeout, schema drift, pool
		// exhaustion). Previously was bare-returned which masked infra
		// faults as "sibling has no findings". Don't fail the joint stage
		// for one sibling — just record the degradation.
		o.logger.Warn("[joint-accept] sibling file-reviews lookup failed — findings empty for this sibling",
			"review_id", reviewID, "error", err)
	}

	// PR diff: we need the raw diff to feed the judge. Fetching it from
	// GitHub avoids depending on whether the sibling's pipeline state
	// still has RawDiff populated post-serialization.
	diffText, err := gh.GetPRDiff(ctx, run.PREvent.InstallationID, owner, repoName, rv.PRNumber)
	if err != nil {
		sum.Accessible = false
		sum.FetchError = summarizeErr(err)
		sum.Title = fmt.Sprintf("PR #%d", rv.PRNumber)
		return sum
	}

	// Title fetch is best-effort — falls back to "PR #N" on failure.
	if pr, err := gh.GetPullRequest(ctx, run.PREvent.InstallationID, owner, repoName, rv.PRNumber); err == nil {
		sum.Title = pr.PRTitle
	} else {
		sum.Title = fmt.Sprintf("PR #%d", rv.PRNumber)
	}
	sum.Diff = diffText
	sum.Accessible = true
	return sum
}

// upsertJointAcceptanceSticky posts the "joint_acceptance" sticky
// section alongside the existing "crosspr" + "issue" sections.
func (o *Orchestrator) upsertJointAcceptanceSticky(ctx context.Context, review *store.Review, run *PipelineRun) error {
	sectionMD := formatJointAcceptanceSection(run.JointAcceptance)
	if sectionMD == "" {
		return nil
	}
	if review.GithubReviewID == nil || *review.GithubReviewID <= 0 {
		return nil
	}
	owner, repo, err := splitRepoFullName(run.PREvent.RepoFullName)
	if err != nil {
		return fmt.Errorf("split repo %q: %w", run.PREvent.RepoFullName, err)
	}
	return o.crossPRGithubDep().UpdateStickySection(
		ctx,
		run.PREvent.InstallationID,
		owner, repo,
		run.PREvent.PRNumber,
		*review.GithubReviewID,
		"joint_acceptance",
		sectionMD,
	)
}

// formatJointAcceptanceSection builds the "## Joint Issue Coverage"
// markdown block. Returns "" for nil / empty input so the caller can
// short-circuit the sticky write.
//
// Rendering rules:
//   - Top-level heading is "## Joint Issue Coverage".
//   - One subsection per issue, linking to the issue URL and echoing the
//     verdict icon + word.
//   - One bullet per criterion, tagged with status icon; addressed /
//     partial bullets link to the sibling PR and evidence path; entries
//     missing an `addressed_by` fall back to "unaddressed" rendering.
func formatJointAcceptanceSection(results []JointAcceptanceResult) string {
	if len(results) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("\n## Joint Issue Coverage\n\n")
	for _, r := range results {
		issueRef := fmt.Sprintf("%s/%s#%d", r.IssueOwner, r.IssueRepo, r.IssueNumber)
		title := util.Truncate(r.IssueTitle, 120, true)
		if r.IssueURL != "" {
			sb.WriteString(fmt.Sprintf("### [%s](%s) — %s\n\n", issueRef, r.IssueURL, title))
		} else {
			sb.WriteString(fmt.Sprintf("### %s — %s\n\n", issueRef, title))
		}
		sb.WriteString(fmt.Sprintf("**Verdict:** %s %s\n\n", verdictIcon(string(r.Verdict)), r.Verdict))
		for _, c := range r.Criteria {
			icon := verdictIcon(string(c.Status))
			line := fmt.Sprintf("- %s %s", icon, util.Truncate(c.Text, 200, true))
			switch c.Status {
			case AcceptanceStatusAddressed, AcceptanceStatusPartial:
				if c.AddressedBy != "" {
					line += fmt.Sprintf(" — %s in %s", c.Status, c.AddressedBy)
				} else {
					line += fmt.Sprintf(" — %s", c.Status)
				}
				if c.Evidence != "" {
					line += fmt.Sprintf(" at `%s`", c.Evidence)
				}
			default:
				line += fmt.Sprintf(" — %s", c.Status)
			}
			sb.WriteString(line + "\n")
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// Stage keys used by persistAsyncStageTokens. Kept alongside labels.go's
// stageLabels map so drift is visible in one grep. labels_test.go asserts
// both are present in stageLabels.
const (
	stageKeyCrossPR    = "cross_pr"
	stageKeyAcceptance = "acceptance"
)

// persistAsyncStageTokens merges a StageTokens entry into BOTH the
// in-memory run.Tokens AND the reviews.token_usage JSONB column under
// the given stage key. Call AFTER the LLM returns — only real spend is
// persisted.
//
// Why both writes: async cross-PR and joint-acceptance stages load the
// run via o.sm.loadState (a fresh in-memory reconstruction), so
// mutating run.Tokens alone never reaches the DB. The synthesis-time
// token_usage commit is already past. A dedicated JSONB merge keeps
// display layers (/stats Cost-by-Stage, per-review TokenPill) accurate.
//
// Ordering: in-memory first so downstream code in the same stage that
// reads run.Tokens (sticky formatting, etc.) sees the update; then the
// DB write. A DB failure is logged at Warn but non-fatal — the LLM
// provider already billed us, losing the display-layer artifact isn't
// worth wedging the stage.
//
// Concurrency: MergeStageTokenEntry is a single atomic UPDATE. Per-
// review mutexes in the callers (crossPRMutexes, jointAcceptanceMutexes)
// prevent same-review overlap; different reviews hit different rows so
// no cross-row contention.
func (o *Orchestrator) persistAsyncStageTokens(
	ctx context.Context,
	reviewID uuid.UUID,
	stageKey string,
	entry StageTokens,
	run *PipelineRun,
) {
	// 1. In-memory merge — preserves the addCrossPR / addAcceptance
	// contract for callers that still read run.Tokens within the stage.
	switch stageKey {
	case stageKeyCrossPR:
		run.Tokens.addCrossPR(entry)
	case stageKeyAcceptance:
		run.Tokens.addAcceptance(entry)
	default:
		// Unknown key → programming error. Warn and still attempt the
		// DB write so telemetry isn't lost if a new stage is wired up
		// before the switch is extended.
		o.logger.Warn("[token-persist] unknown stage key, skipping in-memory merge",
			"stage_key", stageKey, "review_id", reviewID)
	}

	// 2. DB write — single UPDATE, atomic merge of bucket + total.
	entryJSON, err := json.Marshal(entry)
	if err != nil {
		o.logger.Warn("[token-persist] marshal entry failed",
			"stage_key", stageKey, "review_id", reviewID, "error", err)
		return
	}
	rows, err := o.crossPRStoreDep().MergeStageTokenEntry(ctx, db.MergeStageTokenEntryParams{
		ReviewID: reviewID,
		StageKey: stageKey,
		Entry:    entryJSON,
	})
	if err != nil {
		o.logger.Warn("[token-persist] DB merge failed",
			"stage_key", stageKey,
			"review_id", reviewID,
			"error", err,
			"total_tokens", entry.TotalTokens,
			"cost", entry.Cost,
			"model", entry.Model,
			"provider", entry.Provider)
		return
	}
	if rows == 0 {
		// A zero-row UPDATE means the reviews row the async stage is trying
		// to attribute spend to no longer exists (hard-delete, wrong UUID,
		// or a TRUNCATE between the stage launch and its LLM completion).
		// The provider already billed us; log with the exact bucket + cost
		// so operators can size the billing-vs-dashboard gap.
		o.logger.Warn("[token-persist] review row missing — LLM spend not attributed",
			"review_id", reviewID,
			"stage_key", stageKey,
			"total_tokens", entry.TotalTokens,
			"cost", entry.Cost,
			"model", entry.Model,
			"provider", entry.Provider)
	}
}

// emitCrossPRSkipped fires a structured `cross_pr.stage.skipped` event at
// every early-return path of runCrossPRStage. Keeping the emission centralised
// means `reason` values stay disciplined (see PostHog funnel: any new reason
// appears as a new bucket, so ad-hoc strings at the call site would fragment
// the dashboard). The trace_id arg falls back to obs.TraceID(ctx) when empty —
// async recovery paths that never saw the original trace still carry the
// pipeline-run-local trace.
func (o *Orchestrator) emitCrossPRSkipped(ctx context.Context, reviewID uuid.UUID, reason, traceID string) {
	if traceID == "" {
		traceID = obs.TraceID(ctx)
	}
	o.logger.InfoContext(ctx, "cross-PR stage skipped",
		slog.String("event", "cross_pr.stage.skipped"),
		slog.String("review_id", reviewID.String()),
		slog.String("reason", reason),
		slog.String("trace_id", traceID),
	)
}

// emitRateLimitHit fires the `rate_limit.hit` event for any rate-limit trip
// inside the pipeline. `kind` is a narrow vocabulary — "crosspr_per_pr",
// "crosspr_global", "github_secondary" — keeping the PostHog breakdown
// actionable. retryAfterMs=0 is legal (no precise window, e.g. per-review
// caps); the retry_after_ms attr is still emitted so the breakdown can show
// "0 = none" vs "5000 = 5s".
func (o *Orchestrator) emitRateLimitHit(ctx context.Context, kind, scope string, retryAfterMs int64) {
	o.logger.WarnContext(ctx, "rate limit hit",
		slog.String("event", "rate_limit.hit"),
		slog.String("kind", kind),
		slog.String("scope", scope),
		slog.Int64("retry_after_ms", retryAfterMs),
		slog.String("trace_id", obs.TraceID(ctx)),
	)
}

