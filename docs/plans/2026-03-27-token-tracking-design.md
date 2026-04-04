# Per-Stage Token + Model Tracking

## Decisions
- **B**: per-stage granularity (model, provider, tokens, cost per stage)
- **A**: track post-review LLM ops (enrichment, conventions, patterns, file_synthesis, graph)
- **A**: persist tokens on failure
- **A**: trust OpenRouter's cost field
- Settings toggles for all 5 post-review ops (default: enabled)

## Changes

### types.go
- `StageTokens`: add `Model`, `Provider` fields
- `RunTokenUsage`: add `Enrichment`, `Conventions`, `Patterns`, `FileSynthesis []StageTokens`, `Graph` stages
- `PipelineRun`: add `PREnrichment`, `LearnPatterns`, `LearnConventions`, `FileSynthesis`, `ArchitectureGraph` bool flags

### persona.go
- `repoSettings`: add 5 `*bool` fields (default true)
- 5 `is*Enabled` helpers

### orchestrator.go
- Gate each post-review op with setting check
- Track tokens for enrichment, conventions, patterns, file synthesis, graph
- Pass `cfg.Model`/`cfg.Provider` into StageTokens at every call site
- Persist tokens on failure: add `token_usage` to `UpdateReviewStatus` path

### triage.go, scoring.go, review.go
- Pass `cfg.Model`/`cfg.Provider` into StageTokens construction

### store/queries.go
- `UpdateReviewStatus`: accept optional `tokenUsage []byte` param, persist on failure

### No DB migration needed
- `token_usage` is JSONB, new fields appear automatically
- Settings live in existing `settings_json` JSONB
