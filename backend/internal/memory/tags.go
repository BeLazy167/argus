package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

// Container tags and customIDs are Argus's own naming scheme for memory rows:
// the tag names the scope a row belongs to (one repo, or the installation-wide
// `_shared` container), and the customID is the stable identity used to
// upsert a row rather than duplicate it.
//
// The character rules below are load-bearing: every customID already in the
// store follows them, and relaxing them would make the same logical memory
// hash to a different ID than its existing row, silently duplicating instead
// of updating it.

// tagSanitizer replaces characters that are not valid in a container tag.
var tagSanitizer = strings.NewReplacer(":", "-", "/", "-", "~", "-", ".", "-")

// idSanitizerRe matches any character outside the allowed customID set:
// alphanumerics plus `_`, `-`, and `:`. Everything else — notably `(`, `)`,
// `[`, `]`, `/`, `.`, `~`, and spaces — is replaced.
//
// tagSanitizer above is deliberately narrower and is NOT a substitute: it
// misses parens and brackets, which broke Next.js route-group paths like
// `src/app/(auth)/oauth/page.tsx` and dynamic segments like `[slug]/page.tsx`.
// Production reviews logged 7× HTTP 400 errors for exactly that gap.
var idSanitizerRe = regexp.MustCompile(`[^a-zA-Z0-9_:-]`)

// CustomIDSanitize makes an arbitrary string safe for use as a customID.
// Replaces any disallowed character with `-`. Idempotent on already-safe
// input. Callers assembling multi-segment IDs should join with `--` AFTER
// per-segment sanitization — sanitizing the joined string would also collapse
// the `--` separator (`--` is allowed, just redundant).
//
// Exported deliberately: the pipeline uses it to assemble its own customIDs
// (for example the arch-summary doc id in orchestrator.go) against the same
// character set.
func CustomIDSanitize(s string) string {
	return idSanitizerRe.ReplaceAllString(s, "-")
}

// SharedTag is the container tag for cross-repo patterns under one
// installation. Rows are scoped by installation_id, so there is no owner
// segment in the tag itself.
const SharedTag = "_shared"

// repoNameHash returns the first 6 hex chars of sha256(raw repo name). Used as
// a deterministic disambiguator so two repo names that sanitize to the same
// string never share a container or a customID.
func repoNameHash(repo string) string {
	sum := sha256.Sum256([]byte(repo))
	return hex.EncodeToString(sum[:3])
}

// repoNameIsLossy reports whether the repo name cannot be represented safely as
// a bare container tag / customID segment without risking a collision: either
// sanitization changes it (so "sdk.js" and "sdk-js" would map to the same
// token) or the sanitized tag form equals the reserved SharedTag (so a repo
// literally named "_shared" would land in the cross-repo container). When true,
// callers append repoNameHash to keep distinct repos distinct.
func repoNameIsLossy(repo string) bool {
	tag := tagSanitizer.Replace(repo)
	return tag != repo || CustomIDSanitize(repo) != repo || tag == SharedTag
}

// RepoTagNew returns the container tag for a single repo under an
// installation. Rows carry installation_id separately, so the repo name alone
// identifies the container.
//
// Collision guard: tagSanitizer maps '.', '/', ':', '~' to '-', so "sdk.js"
// and "sdk-js" would otherwise share one container, and a repo named "_shared"
// would collide with SharedTag. When the raw name is lossy under sanitization
// (or collides with SharedTag) a short deterministic hash of the RAW name is
// appended so distinct repos never merge. Safe names are returned unchanged so
// the common case keeps stable, human-readable tags.
func RepoTagNew(repo string) string {
	tag := tagSanitizer.Replace(repo)
	if repoNameIsLossy(repo) {
		return tag + "-" + repoNameHash(repo)
	}
	return tag
}

// repoIDSegment returns the collision-safe {repo} segment used inside customID
// builders. It mirrors RepoTagNew's disambiguation so a repo's container tag
// and its customIDs stay consistent: both append the same raw-name hash when
// the name is lossy under sanitization. Without this, "sdk.js" and "sdk-js"
// would produce identical customIDs and clobber each other's rows.
func repoIDSegment(repo string) string {
	seg := CustomIDSanitize(repo)
	if repoNameIsLossy(repo) {
		return seg + "-" + repoNameHash(repo)
	}
	return seg
}
