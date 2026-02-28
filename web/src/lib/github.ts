/** Builds a GitHub PR URL, optionally anchoring to a review comment. */
export function githubPrUrl(
  repoFullName: string,
  prNumber: number,
  reviewId?: number,
): string {
  const base = `https://github.com/${repoFullName}/pull/${prNumber}`;
  return reviewId ? `${base}#pullrequestreview-${reviewId}` : base;
}
