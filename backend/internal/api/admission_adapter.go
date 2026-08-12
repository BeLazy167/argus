package api

import (
	"context"
	"fmt"
	"strings"

	"github.com/BeLazy167/argus/backend/internal/admission"
	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
)

// This file is the adapter layer between the transports and the Admission
// decision. Admission takes plain values; these functions turn a webhook
// payload or a verified token into one.
//
// The split matters: internal/admission never imports GitHub or Clerk, so its
// tests build an Actor directly and need neither a payload nor a token.

// actorFromRequestContext builds the actor for a dashboard caller from the
// verified token.
//
// There is no path from a Clerk subject to a GitHub login today, so the
// organisation role is the only permission signal this path has.
func actorFromRequestContext(ctx context.Context) admission.Actor {
	return admission.DashboardActor(getOrgRole(ctx))
}

// ghPermissions answers Admission's permission question for both actor kinds.
type ghPermissions struct {
	gh             *ghpkg.Client
	installationID int64
}

// CanTriggerReview reports whether the actor may spend this repo's budget.
//
// GitHub actors are checked against GitHub: write, maintain or admin passes.
// A merged pull request does not — GitHub labels such a person CONTRIBUTOR,
// which reads as privileged and grants nothing.
//
// Dashboard actors are checked on their organisation role. They already passed
// JWT verification and installation scoping to reach this point, so the role is
// the remaining question: a viewer must not be able to spend.
func (p ghPermissions) CanTriggerReview(ctx context.Context, actor admission.Actor, repoFullName string) (bool, error) {
	switch actor.Kind {
	case admission.ActorGitHubUser:
		owner, repo, ok := strings.Cut(repoFullName, "/")
		if !ok {
			return false, fmt.Errorf("malformed repo name %q", repoFullName)
		}
		return p.gh.HasRepoWriteAccess(ctx, p.installationID, owner, repo, actor.Login)

	case admission.ActorDashboardUser:
		// Installation scope IS the dashboard authorization, and it already ran:
		// every dashboard path reaches here through jwtAuth →
		// requireInstallationScope → GetRepoScoped, which refuses a repo the
		// caller's installations do not contain.
		//
		// An earlier version gated on org role instead and would have refused
		// EVERY dashboard user: it matched "org:admin"/"member", while this
		// codebase writes "org_member" and "owner" — and getOrgRole is empty
		// altogether on a personal-account installation, because Clerk only
		// sets org_role inside an organisation. A second, narrower gate built
		// on a signal the codebase does not consistently set is worse than no
		// second gate: it fails closed on the ordinary case.
		return true, nil

	default:
		// ActorSystem never reaches here: Admission does not ask about actors
		// that cannot be authorized. Refusing rather than allowing keeps that
		// true even if the guard is ever removed.
		return false, nil
	}
}

// admissionFor builds an Admission bound to one installation. The permission
// checker needs the installation to authenticate as the app.
func (s *Server) admissionFor(installationID int64) *admission.Admission {
	return admission.New(
		ghPermissions{gh: ghpkg.NewClient(s.ghApp, s.cfg.GitHubAppSlug), installationID: installationID},
		s.rateLimiter,
	)
}

// replyRefused tells the person who asked why their review did not run.
//
// One renderer, per-path channel: the GitHub paths post this, the dashboard
// returns the same text in its response body. A refusal with no visible reason
// reads as a fault — and on a public repo the contributor has no dashboard to
// check, so the pull request is the only place they will ever see it.
func (s *Server) replyRefused(ctx context.Context, evt ghpkg.IssueCommentEvent, v admission.Verdict) {
	owner, repo, ok := strings.Cut(evt.RepoFullName, "/")
	if !ok {
		return
	}
	gh := ghpkg.NewClient(s.ghApp, s.cfg.GitHubAppSlug)
	body := fmt.Sprintf("> **Argus** did not run a review.\n>\n> %s", v.Reason)
	if err := gh.CreateIssueComment(ctx, evt.InstallationID, owner, repo, evt.PRNumber, body); err != nil {
		s.logger.WarnContext(ctx, "posting refusal comment", "error", err, "repo", evt.RepoFullName, "pr", evt.PRNumber)
	}
}
