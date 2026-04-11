// Package pipeline — linking.go provides issue and PR link detection for the
// issue-acceptance and cross-PR verification workers.
package pipeline

import (
	"regexp"
	"strconv"
	"strings"
)

// closingKeywordsRe matches GitHub's "closing" keywords plus the non-closing
// "refs"/"references"/"related to" family. GraphQL closingIssuesReferences
// only returns the closing set, so regex is also needed to catch
// non-closing mentions that users still want verified.
var linkedIssueRe = regexp.MustCompile(
	`(?i)\b(close[sd]?|fix(?:es|ed)?|resolve[sd]?|refs?|references?|related\s+to)\s+(?:([\w.-]+)/([\w.-]+))?#(\d+)`,
)

// linkedPRURLRe matches full GitHub PR URLs.
var linkedPRURLRe = regexp.MustCompile(
	`https://github\.com/([\w.-]+)/([\w.-]+)/pull/(\d+)`,
)

// linkedPRShorthandRe matches `owner/repo#N` — ambiguous between issue and PR,
// resolved by trying PR fetch first.
var linkedPRShorthandRe = regexp.MustCompile(
	`\b([\w.-]+)/([\w.-]+)#(\d+)\b`,
)

// ExtractLinkedIssues scans the PR body for issue references and returns
// a deduped slice of IssueLink. currentRepoFullName is `owner/repo` of the
// primary PR and is used to attach same-repo references.
//
// This is the *fallback* path — the primary detection uses GraphQL
// closingIssuesReferences. Use this to merge in non-closing mentions
// (refs/references/related to) that GraphQL won't return.
func ExtractLinkedIssues(body, currentRepoFullName string) []IssueLink {
	if body == "" {
		return nil
	}
	currentOwner, currentRepo := splitOwnerRepo(currentRepoFullName)
	seen := make(map[string]bool)
	var out []IssueLink
	for _, m := range linkedIssueRe.FindAllStringSubmatch(body, -1) {
		// groups: [full, keyword, owner?, repo?, number]
		owner, repo := m[2], m[3]
		if owner == "" || repo == "" {
			owner, repo = currentOwner, currentRepo
		}
		num, err := strconv.Atoi(m[4])
		if err != nil || num <= 0 {
			continue
		}
		key := owner + "/" + repo + "#" + m[4]
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, IssueLink{
			Owner:  owner,
			Repo:   repo,
			Number: num,
			URL:    "https://github.com/" + owner + "/" + repo + "/issues/" + m[4],
		})
	}
	return out
}

// ExtractLinkedPRs scans the PR body for other-PR references. currentRepoFullName
// + currentPRNumber are used to exclude self-references. The result is capped
// at max entries (caller passes FeatureFlags.MaxLinkedPRs or env default).
func ExtractLinkedPRs(body, currentRepoFullName string, currentPRNumber, max int) []PRLink {
	if body == "" || max <= 0 {
		return nil
	}
	currentOwner, currentRepo := splitOwnerRepo(currentRepoFullName)
	seen := make(map[string]bool)
	var out []PRLink

	// Match full URLs first — unambiguous.
	for _, m := range linkedPRURLRe.FindAllStringSubmatch(body, -1) {
		owner, repo := m[1], m[2]
		num, err := strconv.Atoi(m[3])
		if err != nil || num <= 0 {
			continue
		}
		if owner == currentOwner && repo == currentRepo && num == currentPRNumber {
			continue // self-ref
		}
		key := owner + "/" + repo + "#" + m[3]
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, PRLink{
			Owner:  owner,
			Repo:   repo,
			Number: num,
			URL:    "https://github.com/" + owner + "/" + repo + "/pull/" + m[3],
		})
		if len(out) >= max {
			return out
		}
	}

	// Then try shorthand `owner/repo#N` — resolver can't tell issue from PR,
	// caller will attempt PR fetch and fall back.
	for _, m := range linkedPRShorthandRe.FindAllStringSubmatch(body, -1) {
		owner, repo := m[1], m[2]
		num, err := strconv.Atoi(m[3])
		if err != nil || num <= 0 {
			continue
		}
		if owner == currentOwner && repo == currentRepo && num == currentPRNumber {
			continue
		}
		key := owner + "/" + repo + "#" + m[3]
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, PRLink{
			Owner:  owner,
			Repo:   repo,
			Number: num,
			URL:    "https://github.com/" + owner + "/" + repo + "/pull/" + m[3],
		})
		if len(out) >= max {
			return out
		}
	}
	return out
}

// splitOwnerRepo parses "owner/repo" into its two halves. Returns empty
// strings on malformed input.
func splitOwnerRepo(full string) (string, string) {
	if i := strings.Index(full, "/"); i >= 0 {
		return full[:i], full[i+1:]
	}
	return "", ""
}

// MergeIssueLinks deduplicates two IssueLink slices by (owner, repo, number).
// Entries in primary take precedence over fallback when they collide.
func MergeIssueLinks(primary, fallback []IssueLink) []IssueLink {
	seen := make(map[string]bool, len(primary)+len(fallback))
	out := make([]IssueLink, 0, len(primary)+len(fallback))
	for _, l := range primary {
		key := l.Owner + "/" + l.Repo + "#" + strconv.Itoa(l.Number)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, l)
	}
	for _, l := range fallback {
		key := l.Owner + "/" + l.Repo + "#" + strconv.Itoa(l.Number)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, l)
	}
	return out
}
