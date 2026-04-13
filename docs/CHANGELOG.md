# Changelog

All notable changes to Argus are documented here. Format follows [Keep a Changelog](https://keepachangelog.com/).

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
