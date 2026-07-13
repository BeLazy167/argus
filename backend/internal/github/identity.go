package github

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
