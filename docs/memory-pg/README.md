# Memory-in-Postgres: removing Supermemory

Research program (2026-08-01) for replacing Supermemory — the last external SaaS
dependency in the review pipeline — with a Postgres-native memory store behind
the existing `memory.Indexer` seam.

## Decision summary

| Question | Answer |
|---|---|
| Remove Supermemory? | **Yes.** It is the last external seam in an otherwise self-hostable product; its failure mode silently degrades review quality behind 5s timeouts; ~90% of its corpus is re-derivable from our own Postgres. |
| Replace with Polygres (pgContext/pgGraph)? | **Not now.** PG 17/18-only (we run 16), superuser + custom access method makes it structurally impossible on every managed Postgres (RDS/Supabase/Neon/Cloud SQL) — locking out self-hosters; 11 days old, single maintainer; ANN + fusion features are self-labeled Experimental; its hybrid RRF score breaks the 0.85-similarity suppression contract. **Hedge:** our schema is deliberately shaped like a pgContext collection so it stays a drop-on-top upgrade if it matures. |
| Replace with what? | **pgvector + core Postgres FTS** in the existing database. Available on Fly MPG, RDS, Supabase, Neon, and via a one-line docker-compose change. Preserves the [0,1] similarity score contract exactly. Net-negative complexity: deletes the rate limiter, backoff, BYOK client registry, batch collision-merge workarounds, and the cron decay phase. |
| Migrate how? | **Hybrid, re-derive-first** (docs/memory-pg/02-migration-plan.md): re-derive PG-backed classes with tooling cloned from `cmd/migrate-memory`; one-shot export archive rescues the four SM-only classes; 7-phase cutover with a shadow-read window. |
| Postgres version? | **Two-track:** dev+CI on 19beta2 (spike-verified: pgvector 0.8.6 from master builds and the full design SQL passes); prod cutover takes 19-GA-if-released-else-18; self-host compose default `pgvector/pgvector:pg16` now (volume-safe), major bump at prod cutover. See 01 §Postgres version policy. |
| Biggest risk? | **Threshold recalibration.** All seven `thresholds.go` floors (incl. SuppressionDrop 0.85) were tuned on Supermemory's embedding space. New embeddings shift the similarity distribution; suppression fails *silent-open* if 0.85 becomes unreachable. Mitigation: shadow-read score-pair logging, quantile-matched re-tune, cutover gated on ≥95% suppression-decision agreement. |

## Documents

1. [01-replacement-architecture.md](01-replacement-architecture.md) — the engine decision (pgvector vs Polygres, with full feasibility evidence), schema, hybrid-search design, embedding pipeline, effort estimate.
2. [02-migration-plan.md](02-migration-plan.md) — memory-class inventory, re-derivability per class, the 7-phase cutover, data-loss risk register.
3. [03-improvements.md](03-improvements.md) — current-module weaknesses (including three latent bugs found during research) and eight prioritized improvements the move unlocks.

## Ground rules carried through all documents

- Everything sits behind `memory.Indexer` (backend/internal/memory/indexer.go:47-83). `memorytest/fake.go` proves the seam is substitutable; the pgvector backend is the third adapter. Pipeline call sites do not change.
- Semantics preserved exactly: write floor (`review_floor.go`), dismissal suppression with security/Law-12 exemption (`suppression.go`), change-kind lifecycle filter, error-honest novelty gating (never novel-on-error), briefing render shapes and char caps, deterministic customId contract, live pattern_stats.
- `Score` stays an absolute [0,1] similarity. Hybrid fusion (RRF + recency) reorders candidates; it never manufactures a score.
