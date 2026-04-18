# UCB1 scenario selection

**Date**: 2026-04-17
**Status**: Shipped
**Touches**:
- `backend/internal/store/sqlc/query/scenarios.sql` — `ListScenariosForFiles`
- `backend/internal/store/scenarios.go` — `UCBScore` helper + wrapper doc
- `backend/internal/store/scenarios_ucb_test.go` — table-driven test
- `backend/internal/pipeline/simulation.go` — doc comment at `RunSimulations`

## Why

Before this change the scenario selector used:

```sql
ORDER BY trigger_count DESC, created_at DESC LIMIT 20
```

Combined with the in-code `limit := min(5, len(req.Scenarios))` in `RunSimulations`, this produced a **cold-start starvation bug**: scenarios with `trigger_count = 0` always lost to incumbents with higher counts, so on any file with 6+ active scenarios the newest ones never got their first simulation → stayed at count 0 → lost forever.

Diagnostic query against prod (2026-04-17):

```
total_pairs:   156   (repo × file pairs with ≥1 active scenario)
pairs_over_5:   13   (files where the bug bites)
pairs_over_10:   0
worst_case:      9 scenarios competing for 5 slots on a single file
```

13 starved (repo, file) pairs → roughly 20–25 scenarios in permanent cold-start. Enough to fix.

## Algorithm

Classical [UCB1](https://en.wikipedia.org/wiki/Multi-armed_bandit) (Auer, Cesa-Bianchi, Fischer — 2002). Each scenario is one arm of a multi-armed bandit:

```
score = μ + √(2 · ln(T) / n)
        │                    │
        └─ exploit: fraction  └─ explore: bonus that shrinks as n grows
           of recent runs
           that produced a
           real finding
```

- `μ` = mean reward = `wins / n`, where `win = 1 when scenario_runs.verdict IN ('broken','partial')`, else 0. `fixed` and `unclear` don't count as wins (they're either "no problem here" or noise).
- `n` = number of runs for this scenario in the last 90 days.
- `T` = total runs across all scenarios in this repo in the last 90 days.
- `n = 0` → score encoded as `1e9` so newcomers are guaranteed their first run before any incumbent is re-run. Tiebreak: `created_at DESC` (newest first).

The SQL lives in `ListScenariosForFiles`; it returns top 20 by UCB. The caller (`simulation.go:RunSimulations`) takes the first 5 from that slice.

## Why these properties matter

| Property | Old behavior (trigger_count DESC) | New behavior (UCB1) |
|---|---|---|
| Cold start | Newcomers never picked when incumbents saturate the top 5 | Newcomers always picked first (score = ∞) |
| Noise suppression | Noisy scenarios (always `unclear`) keep winning while their count is high | Their `μ` stays near 0, so after enough data they sink |
| Exploration dries up | Never — always picks the same top 5 | As incumbents accumulate runs, the √ term shrinks, giving lower-n scenarios room to climb |
| Tuning knobs | None | `totalSlots` (5), reward definition (`broken | partial`), window (90d) |
| Regret bound | None | O(ln N) — [UCB1 regret proof](https://lilianweng.github.io/posts/2018-01-23-multi-armed-bandit/) |

## Trade-offs we accepted

1. **Stationarity assumption**: UCB1 assumes each arm's reward distribution doesn't drift. In practice scenarios become stale as the codebase evolves — a scenario that caught 3 bugs 6 months ago might be irrelevant today. Mitigation: 90-day rolling window means old runs decay out. Future improvement: sliding-window UCB or exponential decay.

2. **Convergence time**: a freshly-learned scenario needs ~5–10 runs before `μ` is meaningful. Until then UCB behaves like "always pick the newcomer" because the √ term dominates. Fine for Argus — we *want* new scenarios tested aggressively.

3. **Repo-local T**: we scope `T` to the repo so exploration is locality-aware. A scenario in a quiet repo with 3 total runs gets a bigger exploration bonus than the same scenario in a busy repo with 1000 total runs — that matches intuition.

## Rejected alternatives

- **Newcomer quota** (reserve 2 of 5 slots for `trigger_count = 0`) — simpler but doesn't address the long-tail shelving case where a scenario ran twice 6 months ago then never again. UCB handles both in one score.
- **Three-segment rotation** (least-recently-run + newest + top by count) — three independent heuristics, harder to reason about than one score. Also doesn't suppress noisy scenarios.
- **Thompson sampling** — Bayesian Beta posterior per scenario. Slightly better empirical performance on some benchmarks, more code, not worth the delta for Argus.

## Verification

- **Query smoke test**: ran the UCB query against prod on `BeLazy167/agentmd-scorer / infra/docker/Dockerfile.prod` (the 9-scenario worst case). All 9 scenarios correctly tied at `1e9` since none have runs yet. The next 5 PRs touching that file will give each scenario at least one turn.
- **Unit test**: `scenarios_ucb_test.go` covers the formula — reward-edge cases (all wins / all losses / mixed), zero-run sentinel, exploration-bonus shape.
- **Post-deploy check**: within a week, pick any scenario id from the prior starved set (e.g. 170 in `BeLazy167/agentmd-scorer`) and verify `scenario_runs` has ≥1 row for it.

## Follow-ups (not this ship)

1. **Decay the reward**: exponentially-decayed mean reward would better reflect recency than a hard 90-day cutoff.
2. **Pool severity into reward**: treat `broken-critical` as a bigger win than `broken-low`.
3. **Feed `is_outdated` into the score**: multiply score by 0.3 when outdated, letting fresh scenarios outrank stale ones.
4. **Expose per-repo T on the dashboard** so operators can sanity-check which scenarios are winning the top-5.
