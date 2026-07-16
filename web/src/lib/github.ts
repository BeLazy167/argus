/** Builds a GitHub PR URL, optionally anchoring to a review comment. */
export function githubPrUrl(
  repoFullName: string,
  prNumber: number,
  reviewId?: number,
): string {
  const base = `https://github.com/${repoFullName}/pull/${prNumber}`;
  return reviewId ? `${base}#pullrequestreview-${reviewId}` : base;
}

/** Builds a GitHub commit URL for a repo + full SHA. */
export function githubCommitUrl(repoFullName: string, sha: string): string {
  return `https://github.com/${repoFullName}/commit/${sha}`;
}
