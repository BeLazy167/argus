# Fix GitHub 502 Phantom Success + Duplicate Reviews

## Objective

Prevent duplicate PR reviews caused by GitHub returning 502 errors on `CreateReview` calls that actually succeeded server-side. When the retry fires, it creates a second identical review (e.g., PR #71 ended up with 78 comments — 39 x 2). The fix adds a pre-retry check that queries GitHub for an existing bot review before blindly retrying.

## Root Cause

1. `PostReview` calls `client.PullRequests.CreateReview` — GitHub processes the request and creates the review (39 comments)
2. GitHub returns a 502 instead of the review ID
3. `isRetryable(err)` matches 502 → retry fires after 2s sleep
4. Second `CreateReview` call succeeds → duplicate review with another 39 comments
5. The DB-level duplicate guard (`github_review_id`) cannot help because the 502 prevented receiving the ID from the first call

## Implementation Plan

### File: `internal/github/client.go`

- [ ] **1. Replace the 5xx retry block in `PostReview` (lines 172-178).** Currently the retry fires immediately after the 2s sleep with no verification. Change the `isRetryable` branch to first call `findBotReview` to check if the review was actually created despite the 502. If `findBotReview` returns a valid ID, return it immediately without retrying. If no review is found, proceed with the existing retry. Log at WARN level when checking and at INFO level when a phantom success is detected.

  **Current code (`client.go:172-178`):**
  ```go
  ghReview, _, err := client.PullRequests.CreateReview(ctx, owner, repo, prNumber, req)
  if err != nil && isRetryable(err) {
      slog.Warn("review post failed (5xx), retrying", "comments", len(comments), "error", err)
      time.Sleep(2 * time.Second)
      ghReview, _, err = client.PullRequests.CreateReview(ctx, owner, repo, prNumber, req)
  }
  ```

  **Replace with:**
  ```go
  ghReview, _, err := client.PullRequests.CreateReview(ctx, owner, repo, prNumber, req)
  if err != nil && isRetryable(err) {
      slog.Warn("review post failed (5xx), checking if review was created anyway",
          "comments", len(comments), "error", err)
      time.Sleep(2 * time.Second)

      existingID, checkErr := findBotReview(ctx, client, owner, repo, prNumber)
      if checkErr == nil && existingID > 0 {
          slog.Info("review was created despite 5xx, skipping retry",
              "github_review_id", existingID)
          return existingID, nil
      }

      slog.Warn("no existing review found, retrying", "check_error", checkErr)
      ghReview, _, err = client.PullRequests.CreateReview(ctx, owner, repo, prNumber, req)
  }
  ```

- [ ] **2. Add `findBotReview` helper function after `is422` (after line 214).** This function lists recent reviews on the PR via `client.PullRequests.ListReviews`, iterates backwards (newest first), and looks for a review authored by `argus-eye[bot]` or `argus-eye` submitted within the last 5 minutes. Returns the review ID if found, or `(0, nil)` if no matching review exists (indicating safe to retry).

  ```go
  func findBotReview(ctx context.Context, client *gh.Client, owner, repo string, prNumber int) (int64, error) {
      reviews, _, err := client.PullRequests.ListReviews(ctx, owner, repo, prNumber, &gh.ListOptions{PerPage: 30})
      if err != nil {
          return 0, fmt.Errorf("listing reviews: %w", err)
      }
      cutoff := time.Now().Add(-5 * time.Minute)
      for i := len(reviews) - 1; i >= 0; i-- {
          r := reviews[i]
          login := r.GetUser().GetLogin()
          if (login == "argus-eye[bot]" || login == "argus-eye") && r.GetSubmittedAt().GetTime().After(cutoff) {
              return r.GetID(), nil
          }
      }
      return 0, nil
  }
  ```

- [ ] **3. Verify build compiles and passes vet.** Run `go build ./...` and `go vet ./...` from the project root to confirm no regressions.

## Verification Criteria

- `go build ./...` succeeds with zero errors
- `go vet ./...` passes cleanly
- On a real 502, logs show `"review was created despite 5xx, skipping retry"` and the review ID is returned without a second `CreateReview` call
- On a genuine failure (review not created), logs show `"no existing review found, retrying"` and the retry proceeds as before
- Only 1 Argus review appears per pipeline run on any given PR

## Potential Risks and Mitigations

1. **`ListReviews` itself fails (rate limit, network error)**
   Mitigation: `findBotReview` returns `(0, error)`. The caller logs the `check_error` and proceeds with the retry — worst case is the same duplicate behavior as today, not worse.

2. **Race condition: review created by a concurrent pipeline run within 5 minutes**
   Mitigation: The 5-minute cutoff is conservative enough to catch phantom 502s (which are seconds old) but narrow enough to avoid matching reviews from prior runs. Additionally, the orchestrator already has a per-PR lock preventing concurrent runs for the same PR.

3. **Bot login name changes or differs across environments**
   Mitigation: The function checks both `argus-eye[bot]` (REST API login format) and `argus-eye` (GraphQL/app slug format), matching the same pattern used elsewhere in the codebase (`orchestrator.go:519-520`, `commands.go:244`).

4. **`ListReviews` pagination — PR has >30 reviews**
   Mitigation: `PerPage: 30` fetches the most recent page. Since we iterate backwards and only care about reviews from the last 5 minutes, the most recent page is sufficient. PRs with 30+ reviews in 5 minutes are not a realistic scenario.

## Alternative Approaches

1. **Idempotency key via PR comment**: Post a unique marker comment before `CreateReview`, then check for it before retrying. Trade-off: adds an extra API call on every review, not just on 502s, and leaves marker comments that need cleanup.

2. **DB-level dedup with timestamp**: Record the attempt timestamp in the DB before calling `CreateReview`, then check on retry. Trade-off: requires a DB migration and doesn't actually prevent the duplicate on GitHub — only detects it after the fact.

3. **Skip retry entirely on 502**: Since 502s from GitHub often mean "succeeded but response lost", just don't retry. Trade-off: genuine failures (review not created) would be silently dropped with no review posted.
