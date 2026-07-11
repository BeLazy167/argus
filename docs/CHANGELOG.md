# Changelog

All notable changes to Argus are documented here. Format follows [Keep a Changelog](https://keepachangelog.com/).

## [0.2.0] - 2026-07-11

Review doctrine release (#112-#118). Reviews are now calibrated per PR by a computed contract instead of one-size-fits-all.

### Added
- Per-PR ReviewContract `{change_class, evidence_bar, depth, signals}` — deterministic signals first (draft → skim, wip/hotfix labels, branch prefixes for migration/one-time-script/revert, path-glob majority for migrations/scripts/tests/docs/generated, `refactor|rename|cleanup` title scrutiny bump that never lowers depth); LLM intent stage fills change_class only when metadata is silent, at confidence ≥ 0.6, else production (#112)
- "Unreviewable" handling for PRs over 1500 changed LOC or 60 files — still reviewed, with a reduced-confidence note and split recommendation (#112)
- Review Laws: one severity rubric injected once into every prompt (specialists/personas are focus lenses) — approve-with-findings default, silence is a valid review, no praise comments, style/formatting never flagged, evidence law (concrete failure scenario + file:line), fix law, diff/pattern/YAGNI scope laws (#113)
- Permanent checks in the rubric: destructive SQL WHERE/rollback, secrets/PII in log diffs, unit-ambiguous constants, refactor behavior-equivalence, unchecked errors; security category and marker-matched checks exempt from memory suppression (#113, #115)
- Cross-specialist corroboration recorded in dedup; bounded +10 scoring boost (#113)
- Glass Box footer on every review: contract class/depth, what was checked, findings suppressed by team feedback, review duration (#114)
- Gauge: on PR close, a detector diffs commits after each comment (±3-line proximity) into `addressed_human` / `addressed_agent` (bot-pattern authors weighted 0.5) / `ignored` (merged, lines untouched) / `deferred` (closed without merging); `vw_review_gauge` view with address-rate per category per change_class, dismiss rate, median time-to-merge; internal `GET /api/v1/stats/gauge` (#114)
- Suppression memory v2: dismissals stored by category+content (semantic, file-path-free) with change_kind + reason; suppression on a single ≥0.85 match, ≥3 similar ≥0.60, or category auto-suppression after 3 consecutive negative outcomes (#115)
- "Resolved by `<sha>`" reply + thread resolution when a re-review finds a prior finding fixed by new commits (#115)
- Reply outcome `not_applicable_change_kind` (#115)
- Migrations 048-051: `reviews.review_contract` jsonb, comment outcome vocabulary + `addressed_at`, `vw_review_gauge`, `review_comments.state` ledger (posted/addressed/dismissed/deferred/suppressed) (#112, #114-#116)
- Adversarial-verify workflow as the standing merge gate for every PR, documented in CLAUDE.md (#118)

### Changed
- `one_time_script` PRs reviewed by a single balanced reviewer (correctness + data safety) instead of the 4-specialist squad; `one_time_script`/`docs`/`generated` skip Pass 2 (#112)
- Security-relevant files and migration class max the evidence bar and pin depth at single or above — the floor never relaxes (#112)
- LLM judge now runs for every review (deep enrichments Pass2/validate stay pro+deep) and sees the PR body + contract (#113)
- Class-aware judge thresholds: throwaway/docs/generated need near-certain findings (suggestion +15, warning +10); migration/security judged more sensitively on critical (−5) (#113)
- Deterministic category caps bind over the judge (style 30, error_handling 45 unless security file); judge-omitted findings default to the threshold, not auto-pass (#113)
- Posting: max 10 inline findings severity-first with "plus N similar" overflow; near-threshold findings collapse into a "Minor notes" section; nits demoted off files carrying a critical; summaries use "needs work" language, never "blocked"/"rejected" (#114)
- Security findings and permanent checks exempt from suppression (downgrade only, never muted); prototype/one-off-era dismissals no longer silence production/migration reviews (#115)

### Removed
- Minimum-comment floor (minSurvivors) in scoring — silence is a valid review (#113)

## [0.1.0] - 2026-04-07

Initial open source release.

### Added
- 9-stage review pipeline (Triage, Briefing, Review, Dedup, Validate, Scoring, Pass2, Synthesis, Post)
- 4 specialist reviewers (bug hunter, security, architecture, regression)
- SmartDedup with 4 layers (canonical type, TF-IDF cosine, line proximity, LLM judge)
- Cross-file dedup (max 2 per vulnerability type)
- Go AST parser using go/ast stdlib
- Universal ctags parser (40+ languages)
- Regex parsers for Java, Rust, C#, Ruby, PHP, Swift, Kotlin, C/C++, Terraform
- SAST integration (staticcheck, eslint, semgrep)
- SAST-driven review hints (findings injected into LLM prompt)
- Language-specific security checklists (9 language families)
- Pattern learning from user feedback (reactions, replies)
- Positive and negative pattern indexing
- Pattern health monitoring and 90-day decay
- GitHub reaction webhook handler
- Incremental reviews with score progression (1-10)
- Simulation mode with scenario testing
- PR description enrichment with Mermaid diagrams
- Blast radius analysis via code graph
- P0/P1/P2 priority with confidence scores
- Variable scoring thresholds (critical=35, warning=45, suggestion=55)
- Deterministic FP scoring caps (await, race condition, attacker framing)
- 40-comment GitHub cap with file-diversity round-robin
- Post-selection Jaccard dedup
- Cold file second pass (Security specialist on under-reviewed files)
- XML structured output for AI agents
- Type-aware context injection
- Confidence-based comment display (medium collapsed)
- Custom rules API
- Next.js frontend dashboard
