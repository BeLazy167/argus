// Package admission answers one question: may this review run?
//
// Five entry points can start a review — the pull-request webhook, the trigger
// checkbox, the `@argus-eye review` comment, the dashboard button, and retry.
// Each one used to assemble its own gates in its own order, and no two
// assembled the same set. One path checked the actor's permission. One had no
// rate limit at all. None bounded what a review could cost.
//
// All five now go through Decide. The proof is negative: Server.allowReview,
// the wrapper every call site used to reach the limiter through, became unused
// and was deleted — nothing can reach the limiter except through this package.
//
// This package holds the decision and nothing else. It does not know that
// GitHub or Clerk exist: callers build an Actor with their own adapter and pass
// it in. It holds no locks — the in-flight slot and the concurrency token have
// lifetimes, and a decision does not.
package admission

// ActorKind names how a review was asked for. The kind, not the login, decides
// which rule authorizes the request.
type ActorKind string

const (
	// ActorSystem is a review nobody asked for: the pull-request webhook fired
	// and the repo's auto_run setting decided. The Actor still carries the PR
	// author, but only for attribution.
	ActorSystem ActorKind = "system"
	// ActorGitHubUser is a person acting through GitHub — ticking the trigger
	// checkbox, or writing the review command.
	ActorGitHubUser ActorKind = "github_user"
	// ActorDashboardUser is a person acting through the dashboard, identified
	// by the organisation role in their token.
	ActorDashboardUser ActorKind = "dashboard_user"
)

// Actor is who asked for a review.
//
// One value, built by two adapters — one for GitHub webhooks, one for the
// dashboard token. Before this existed the same question had four different
// shapes across four handlers, and one of them was nothing at all, which is why
// authorization could not be written once.
type Actor struct {
	Kind ActorKind

	// Login is the GitHub login, when there is one. On ActorSystem it holds the
	// pull-request author.
	//
	// ATTRIBUTION ONLY on ActorSystem. The author did not ask for the review,
	// and asking GitHub whether they may push would let a fork pull request
	// authorize its own review — the exact abuse this package exists to stop.
	// CanBeAuthorized reports false for that kind so the mistake cannot be made
	// by reaching for this field.
	Login string

	// OrgRole is the dashboard organisation role from the token. Empty for
	// GitHub actors. There is no mapping from a dashboard identity to a GitHub
	// login, so this is the only permission signal that path has.
	OrgRole string
}

// CanBeAuthorized reports whether it is meaningful to ask "may this actor
// trigger a review?".
//
// False for ActorSystem. That path is authorized by the auto_run setting, not
// by a person, and the guard is the reason Login can be carried safely there.
func (a Actor) CanBeAuthorized() bool {
	return a.Kind == ActorGitHubUser || a.Kind == ActorDashboardUser
}

// Describe names the actor for a reason string.
//
// It never invents a handle. A dashboard actor reads as its role, because the
// token carries a Clerk subject, which is not a name anyone would recognise on
// a pull request — and there is no mapping from one to a GitHub login.
//
// Only called for actors that CanBeAuthorized, so ActorSystem has no rung here.
func (a Actor) Describe() string {
	if a.Kind == ActorGitHubUser {
		if a.Login != "" {
			return "@" + a.Login
		}
		return "a GitHub user"
	}
	if a.OrgRole != "" {
		return "a dashboard user (" + a.OrgRole + ")"
	}
	return "a dashboard user"
}

// SystemActor builds the actor for a pull-request webhook. prAuthor is recorded
// for attribution and is never authorized.
func SystemActor(prAuthor string) Actor {
	return Actor{Kind: ActorSystem, Login: prAuthor}
}

// GitHubActor builds the actor for a person acting through GitHub.
func GitHubActor(login string) Actor {
	return Actor{Kind: ActorGitHubUser, Login: login}
}

// DashboardActor builds the actor for a person acting through the dashboard.
func DashboardActor(orgRole string) Actor {
	return Actor{Kind: ActorDashboardUser, OrgRole: orgRole}
}
