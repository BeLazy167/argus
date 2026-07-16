package github

import "strings"

// IsArgusThread reports whether a GitHub review thread or comment was
// authored by this deployment's Argus App (identified by its slug, e.g.
// "argus-eye"). Matches only the App's own login variants — crucially, it
// does NOT treat every "*[bot]" login as Argus. Without this tightness,
// our auto-resolve heuristic (diff overlap ±3 lines) would also close
// threads from Dependabot, Codecov, Renovate, Cubic, etc. sharing the PR.
//
// Both variants are returned by GitHub depending on the API:
//
//   - REST endpoints typically return the `[bot]`-suffixed login.
//   - GraphQL endpoints (e.g. reviewThreads) return the bare app slug.
//
// Keep both forever — they're both canonical for different call paths.
func IsArgusThread(authorLogin, appSlug string) bool {
	return authorLogin == appSlug || authorLogin == appSlug+"[bot]"
}

// IsPrivilegedAssociation reports whether a GitHub author_association grants
// maintainer-level trust on the repo — the gate for privileged actions like
// `@argus resolve` and the reply-path thread-resolution / terminal-state
// shortcut. OWNER / MEMBER / COLLABORATOR can write and resolve conversations;
// CONTRIBUTOR / FIRST_TIME_CONTRIBUTOR / MANNEQUIN / NONE (a fork contributor on
// someone else's repo) cannot. Case-insensitive and whitespace-trimmed; an
// empty or unknown association denies (fail-closed). author_association is a
// coarse proxy for write permission — good enough here; a follow-up can tighten
// to the collaborator-permission API if ever needed.
func IsPrivilegedAssociation(authorAssociation string) bool {
	switch strings.ToUpper(strings.TrimSpace(authorAssociation)) {
	case "OWNER", "MEMBER", "COLLABORATOR":
		return true
	default:
		return false
	}
}
