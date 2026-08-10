package admission

import "context"

// PermissionChecker answers whether an actor may spend this repo's review
// budget. The GitHub adapter asks GitHub for push permission; the dashboard
// adapter reads the organisation role already carried in the token.
//
// The error is returned, not folded into false, because the two mean different
// things to the caller and only one of them is the actor's fault.
type PermissionChecker interface {
	CanTriggerReview(ctx context.Context, actor Actor, repoFullName string) (bool, error)
}

// RateLimiter reserves a review from the per-repo and per-org buckets. It
// reports whether the reservation was granted, and cancels its own reservations
// when any bucket denies.
type RateLimiter interface {
	AllowReview(repoFullName, orgLogin string, force bool) bool
}

// Request is everything Admission needs to decide. Every field is a resolved
// fact: no payloads, no tokens, no database handles. That is what makes the
// decision table-testable without a webhook, a JWT, or a pipeline.
type Request struct {
	Actor        Actor
	RepoFullName string
	OrgLogin     string

	// Force draws from the tighter hourly bucket. It never widens a limit.
	Force bool

	Size          Size
	TokenEstimate TokenEstimate
	Limits        Limits
}

// Admission decides whether a review may run.
//
// Decide owns the gates that can be answered at the launch site: the actor's
// permission and the rate limit.
//
// Two more rules live in this package but are evaluated later by their caller,
// because each needs data that has not arrived yet — AutoRun needs the repo
// settings, Budget needs the fetched diff. They are exported functions rather
// than fields on Request, so no caller ever passes a flag whose job is to
// disable a gate.
//
// The package owns no locks. The in-flight slot and the concurrency token have
// lifetimes, and mixing a lock into a decision means every test of the decision
// acquires and releases real state.
type Admission struct {
	perms PermissionChecker
	rate  RateLimiter
}

// New builds an Admission. A nil permission checker refuses every actor that
// can be authorized, rather than admitting them: an unwired dependency must not
// read as permission.
func New(perms PermissionChecker, rate RateLimiter) *Admission {
	return &Admission{perms: perms, rate: rate}
}

// Decide returns the verdict for one review request.
//
// Order is deliberate and each step is cheaper than the next is expensive.
// Authorization first, because a refused actor must not consume a rate-limit
// token — otherwise anyone can exhaust a repo's hourly budget without ever
// being allowed to run anything. The Budget is last, because it is the only
// gate that needs the fetched diff.
func (a *Admission) Decide(ctx context.Context, req Request) Verdict {
	// 1. Who authorized this.
	if req.Actor.CanBeAuthorized() {
		if a.perms == nil {
			return Refuse("cannot verify permission to trigger a review")
		}
		allowed, err := a.perms.CanTriggerReview(ctx, req.Actor, req.RepoFullName)
		if err != nil {
			// Fail closed. A denied maintainer costs one retry. Admitting on
			// error hands the budget to anyone for as long as the outage lasts,
			// which is the failure this gate exists to prevent.
			return Refuse("could not verify whether %s may trigger a review — try again", req.Actor.Describe())
		}
		if !allowed {
			return Refuse("%s does not have write access to %s", req.Actor.Describe(), req.RepoFullName)
		}
	}
	// ActorSystem falls straight through: nobody asked, and auto_run — decided
	// upstream — is the authority. The actor's Login is the PR author and is
	// never consulted, because asking whether they may push would let a fork
	// pull request authorize its own review.

	// 2. Rate limit. After authorization, before spend.
	if a.rate != nil {
		if !a.rate.AllowReview(req.RepoFullName, req.OrgLogin, req.Force) {
			return Refuse("this repo has reached its review rate limit — try again later")
		}
	}

	// 3. Budget.
	return Budget(req.Size, req.TokenEstimate, req.Limits)
}
