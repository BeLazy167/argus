# Argus Documentation

> Your codebase has bugs you haven't found yet. Argus finds them before your users do.

Argus is an AI code reviewer that catches real bugs — not lint warnings. It understands your system across files, remembers what broke before, and gets sharper with every review.

**Open source. Bring your own LLM key. Works with your existing GitHub flow.**

---

## Getting Started

Setup takes a few minutes:

1. Install the [Argus GitHub App](https://github.com/apps/argus-eye)
2. Select repos to monitor
3. Add your LLM API key and pick a model per pipeline stage (BYOK — bring your own key)
4. Open a PR — on the hosted app, Argus posts a **Trigger Argus review** checkbox with a cost preview; tick it to run. Turn on Auto-review in Settings to review every PR automatically.

No config files. No CI setup. No YAML. Models are the one required configuration — Argus ships no default model, and an unconfigured stage stops the review with a guided setup error.

---

## What Argus Catches (That Other Tools Miss)

Most review tools see the diff. Argus sees the system.

**Cross-file bugs**: When you change a function, Argus traces who calls it, what tests cover it, and what types it shares. If your change breaks a caller three files away, Argus flags it.

**Repeated mistakes**: Past bugs, incidents, and edge cases are remembered across team turnover. When someone touches a module that broke before, Argus surfaces what happened last time.

**Silent failures**: Empty catch blocks, swallowed errors, functions that return success on failure. The bugs that pass code review and break production at 2am.

**Security paths**: Traces untrusted input through your system — from user input to database query — and flags injection, auth bypass, and data exposure along the way.

---

## How Reviews Look

### The Summary

Every review starts with a quick take — like a senior dev glancing at your PR:

> **Verdict:** This PR adds auth middleware and session handling. Not ready to merge due to 2 critical blockers.
>
> **Findings:** 2 blocking · 3 warning · 1 suggestion · 0 praise · 5 files reviewed
>
> **Top priority:** Token verification in auth.ts doesn't check expiry — expired sessions pass through.
>
> **Fix order:** auth.ts → middleware.ts → session.ts
>
> Score: **4/10** · [Full review →](https://argus.reviews/reviews/...)

Severity counts. Fix ordering. Root cause. One glance tells you where to start.

### Inline Comments

Each finding carries its severity, judge confidence, category, and the *why* — not just *what's wrong*:

```
🔴 **CRITICAL (9/10) · Security:** Token expiry not validated

verifyToken() checks the signature but skips the exp claim.
An expired token passes validation, allowing stale sessions
to access protected endpoints indefinitely.
```

When a fix is clear, the comment includes a GitHub-native `suggestion` block — one click to apply. Suggestions are labeled unverified: Argus does not compile or run them. Findings at medium confidence arrive collapsed; overflow past the 10-inline-comment cap lands in the summary's Minor notes section instead of the diff.

### Severity Levels

| | Level | Meaning |
|---|---|---|
| 🔴 | **Critical** | Bugs, security holes, data loss — will fail in production. |
| 🟡 | **Warning** | Works but fragile: races, error-handling gaps, perf traps. |
| 💡 | **Suggestion** | Readability and minor refactors. Unverified — Argus doesn't run the fix. |
| ✅ | **Praise** | At most one genuine line in the summary — never an inline comment. |

When everything is critical, nothing is. Argus calibrates severity so blockers mean something.

### PR Diagrams

For complex PRs, Argus generates Mermaid diagrams directly in your PR description:

| Diagram | When | What it shows |
|---------|------|---------------|
| **Sequence** | 3+ files changed | Call flow between modules, bugs annotated with warning signs |
| **Data Flow** | Security findings detected | Untrusted input traced through the system |
| **Dependency** | 10+ files changed | Import relationships between changed files |

Collapsed by default — click to expand. Max 2 per review, and only when grounded in the diff and code graph.

---

## How It Works

Argus runs a multi-stage review pipeline: it computes a review contract for the PR (production, migration, one-off script, test, config, docs, generated, revert), triages files, gathers cross-file context, performs focused analysis from multiple angles, deduplicates findings, judge-scores them against class-aware thresholds, and synthesizes a summary with fix ordering and diagrams.

Most PRs complete in a couple of minutes. Oversized PRs aren't skipped — past the soft limit (60 files or 1,500 changed lines by default) Argus reviews the highest-risk files at reduced depth; past the hard limit (400 files / 20,000 lines) it refuses and says why. Both limits are tunable in Settings → Limits.

**Incremental reviews:** on later pushes Argus reviews only the inter-diff since the last completed review. A force-push, changed base, or failed compare falls back to a full review; `@argus-eye review --force` forces one.

---

## Memory & Learning

Argus doesn't start from zero on every review. It remembers.

### Patterns
Conventions your team cares about, automatically picked up from past reviews and developer feedback. Matched semantically on future PRs — no config needed.

### Scenarios
Known failure modes and edge cases for your codebase. Created from reviews, from GitHub Issues labeled `argus` or `bug`, or manually from the dashboard. When someone touches a file with known scenarios, Argus checks whether the change is safe against them.

### The Flywheel
Every review, reply, and fix becomes institutional memory. Reviews get sharper over time as Argus learns what your team cares about — without any model retraining.

Memory lives in Argus's Postgres as ordinary rows tagged with their embedding space; retrieval fuses vector similarity and full-text search. Hosted, it uses the platform embedding model (voyage-4) out of the box — bring your own Voyage/OpenAI key or point at a self-hosted OpenAI-compatible endpoint from the Providers page to own the space.

---

## Code Simulation

For stored scenarios, Argus reasons about whether your change is safe: what could break, who's affected, and how to fix it.

- Runs up to 5 stored scenarios per PR, ranked by historical usefulness.
- Verdicts: **Broken**, **Partial fix**, or **Unclear** — each with a Why and a Fix.
- Only failures at 50%+ confidence are shown; a clean run collapses to one line.
- Requires Deep Review and Simulation & Scenarios — both under Settings → Org Defaults → Pipeline Features.

---

## Review Personas

Choose how Argus reviews:

| Persona | Focus |
|---------|-------|
| **Default** | Balanced, professional |
| **Security Auditor** | Injection, auth, secrets |
| **Performance Engineer** | N+1 queries, allocations, caching |
| **Mentor** | Educational tone, explains why |
| **Architect** | Design patterns, coupling, API contracts |
| **Strict** | Exhaustive analysis depth — traces every path and error branch |
| **Adversarial** | Assumes worst-case inputs |
| **Fresh Eyes** | Reviews as if seeing the codebase for the first time |
| **Custom** | Write your own system prompt |

Personas tune tone, focus, and depth — never the severity bar. Strict doesn't manufacture comments.

Override per-PR: `@argus-eye review --persona strict`

---

## Auto-review & Triggers

Two ways a review starts:

- **Auto-review** — reviews every PR on open, push, and reopen. Off by default on the hosted app (a cost control, since you pay your provider); on by default on self-hosted installs when unset. Repo settings override org defaults, and an explicit off is respected everywhere.
- **Manual triggers** — the trigger checkbox or a bot command. Both run regardless of the auto-review setting.

When auto-review is off, Argus posts a one-shot **Trigger Argus review** checkbox comment per PR, with a token/cost estimate when available. Ticking it requires repository write access — pasted or forged look-alike comments don't work. The trigger resets after a refusal or pipeline failure so you can retry.

Rate limits: 30 reviews/hour per repo, 200/day per org, and a tighter 10/hour bucket for checkbox and `--force` triggers.

---

## Bot Commands

Comment on any PR:

| Command | What it does |
|---------|-------------|
| `@argus-eye review` | Trigger a review |
| `@argus-eye review --force` | Re-review the full diff, ignoring incremental state |
| `@argus-eye review --persona <name>` | Review with a specific persona |
| `@argus-eye test` | Generate a test plan from findings (needs a completed review) |
| `@argus-eye test --code` | Draft executable test code (needs a completed review) |
| `@argus-eye remember <pattern>` | Teach a pattern for this repo — requires write access |
| `@argus-eye remember --org <pattern>` | Teach an org-wide pattern — requires org privileges |
| `@argus-eye resolve` | Resolve all open Argus threads on this PR — maintainer-only |
| `@argus-eye fix` | Apply suggestion blocks as a commit |
| `@argus-eye help` | Show available commands |

Commands run regardless of auto-review — they're explicit intent. Self-hosted installs answer to their own `GITHUB_APP_SLUG`, not necessarily `@argus-eye`.

---

## Review Rules

Create rules in the dashboard and Argus applies them to every review — team standards, not generic best practices.

- Rules are scoped to the installation and managed from the dashboard — no repo config file is read.
- Each rule has a category, free-form content, a priority (low / medium / high), and an enabled flag.
- Categories: security, performance, style, testing, documentation, error-handling, accessibility, other.

Example: *"All API endpoints must validate authentication tokens before processing requests."*

---

## Security

### Your API Keys
Encrypted at rest with industry-standard symmetric encryption. Decrypted in-memory only during LLM calls, then discarded. Never logged, never cached, never sent anywhere except your configured provider. The dashboard shows only masked placeholders.

### Your Code
Argus reads diffs and file content to review. Diffs are processed in memory and discarded after review; Argus stores PR metadata, review results, and memory — not your full source tree. Each workspace is fully isolated, and nothing is used for training.

---

## Model Configuration

BYOK — bring your own key. Set a provider, key, and model per pipeline stage (intent, triage, review, scoring, synthesis and friends), per repo or as org defaults — repo config wins. There is no platform default model: an unconfigured stage stops the review with a guided setup error.

Supported providers:

| Provider | Notes |
|----------|-------|
| **OpenRouter** | Routes to hundreds of models |
| **OpenAI** | Direct access |
| **Anthropic** | Direct access |
| **Fireworks AI** | Fast open-model inference |
| **Groq** | Low-latency inference |
| **Together AI** | Open-model hosting |
| **DeepSeek** | Direct access |
| **Azure OpenAI** | Custom deployment endpoint |
| **GCP Vertex AI** | OpenAI-compatible endpoint |
| **AWS Bedrock** | Bedrock runtime endpoint |
| **Zhipu AI** | GLM models |
| **Vercel AI Gateway** | Gateway endpoint |

Only providers with a saved key appear as options. Custom model identifiers are supported — type any model name your provider accepts, with optional endpoint, temperature, and max-token overrides.

You pay your provider directly. Every stage records input/output tokens, model, and cost — visible per review in the dashboard.

---

## Settings

Toggles live under **Settings → Org Defaults** with per-repo overrides on **Repo Overrides**:

| Feature | Default | Why it matters |
|---------|---------|----------------|
| Auto-review every PR | Off (hosted) / on (self-hosted unset) | Reviews run automatically vs. on-demand via checkbox |
| Auto-resolve stale threads | On | Pushes that fix flagged lines resolve the thread — no LLM call |
| Deep Review | Off | 4 specialist reviewers per file; required for simulation |
| Simulation & Scenarios | Off | Tests stored failure scenarios against the diff |
| Cross-File Context | On | Reviews understand callers, imports, and shared types |
| Blast Radius | On | Shows what downstream code your change affects |
| PR Enrichment | On | Adds context and Mermaid diagrams to the PR description |
| Pattern Learning | On | Learns reusable patterns from high-confidence findings |
| Convention Learning | On | Extracts team conventions from diffs |
| File Synthesis | On | Per-file institutional memory summaries |
| Architecture Graph | On | Builds the dependency graph behind blast radius |
| Issue Acceptance | On (org flag) | Verifies linked-issue acceptance criteria |
| Cross-Repo PR Checks | On (org flag) | Compatibility verification across linked PRs — max linked PRs tunable 1–20 (default 5) |

The **Limits** tab caps review size: soft limits reduce depth, hard limits refuse. Defaults: 60/400 files, 1.5k/20k changed lines, 3M/10M average tokens (the token measure engages only once the repo has review history).

---

## Pricing

| | Everything |
|---|---|
| **Price** | **$0** — open source |
| Repos | Unlimited |
| Reviews | Unlimited |
| Deep review | 4 specialists + lead agent |
| Memory | Full — patterns, scenarios, decision traces |
| Simulation | Included |
| Diagrams | Sequence + data flow + dependency |

BYOK — you bring your own LLM API key and pay your provider directly. Argus takes no cut. Self-host it (`SELF_HOSTED=true`) and there is nothing else to pay.

---

*Built by [Argus](https://argus.reviews). Stop shipping bugs.*
