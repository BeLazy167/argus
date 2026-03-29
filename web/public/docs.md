# Argus Documentation

> Code that understands itself.

Argus is an AI code review platform that doesn't just read your diffs — it understands your system. Every review builds a living model of your codebase that gets smarter over time.

---

## Getting Started

1. Install the [Argus GitHub App](https://github.com/apps/argus-eye)
2. Select which repos to monitor
3. Configure your LLM API key (BYOK — bring your own key via OpenRouter)
4. Push a PR — Argus reviews automatically

---

## The Review Pipeline

Every PR goes through 9 stages:

### 01 — Triage
A fast LLM pass classifies every changed file: **skip** (generated/vendored), **skim** (low-risk), **security_skim**, or **deep** (complex changes needing specialist review).

### 02 — Lead Brief
A lead agent gathers cross-file context before reviewing: traces callers, imports, shared types, dependency graph, scenario matches, and decision traces. This brief is shared with all specialists.

### 03 — Deep Review
Per-file parallel review with full codebase awareness. For files triaged as "deep," 4 specialist reviewers activate:
- **Bug Hunter** — logic errors, edge cases, off-by-one
- **Security** — injection, auth bypass, data exposure
- **Architecture** — coupling, abstraction leaks, design issues
- **Edge Case Hunter** — boundary conditions, unusual inputs, race conditions

### 04 — Deduplication
Cross-specialist dedup removes duplicate findings. When bug_hunter and security both flag the same line, the best explanation wins. Uses word-overlap clustering to catch near-duplicates.

### 05 — Validation & Simulation
Each comment is validated against the diff. Blast radius analysis checks downstream impact. Code simulations test known scenarios against the changed code.

### 06 — Scoring
A separate model scores each finding (0–100). Comments below the threshold are dropped. Severity is rebalanced — if >50% are critical, lowest-confidence criticals are downgraded.

### 07 — Pass 2
Hot files (3+ high-scoring findings) get a second review from the Architecture specialist for deeper structural analysis.

### 08 — Synthesis
An LLM generates a compact summary with severity counts, fix ordering, and root-cause analysis. Conditional diagrams (sequence + data flow) are generated for complex PRs.

### 09 — Post & Learn
The review is posted as one atomic GitHub review with emoji severity prefixes:
- 🔴 **Blockers** — must fix before merge
- 🟡 **Should fix** — important but not blocking
- 💡 **Suggestions** — nice to have
- ✅ **Praise** — good code acknowledged

Developers react: 👍 to confirm, 👎 to dismiss. Argus learns from both.

---

## What Argus Sees

Most review tools see the diff. Argus sees the system.

### Cross-File Context
When you change a function, Argus traces who calls it, what tests cover it, and what types it shares. The review isn't blind to the rest of the codebase.

### Blast Radius
A persistent dependency graph maps every function, class, and import. On each PR, Argus shows which downstream code is affected by the change.

### Scenario Memory
Past bugs, incidents, and edge cases are remembered across team turnover. When someone touches a module that broke before, Argus surfaces the relevant history.

### Decision Traces
Every review finding, every developer reply, every fix builds a living knowledge graph. The system accumulates institutional memory that no wiki or runbook can replicate.

---

## Code Simulation

Before you merge, Argus imagines what happens.

Given a PR diff and known scenarios, Argus simulates execution paths through the codebase. Each simulation reports:
- **Confidence** — how certain the prediction is (0–100%)
- **Root cause** — what specifically breaks
- **Impact** — who and what is affected
- **Suggestion** — how to fix it

Simulations run on the most critical scenarios first. Only findings above the confidence threshold are surfaced.

---

## The Conversational Review

### Summary
The review summary gives a senior dev's quick take with severity counts, fix ordering, and root cause:

> **Verdict:** This PR adds auth middleware and session handling. Not ready to merge due to 2 critical blockers.
>
> 🔴 2 blockers · 🟡 3 should fix · 5 files reviewed
>
> **Top priority:** Token verification in auth.ts doesn't check expiry — expired sessions pass through.
>
> **Fix order:** auth.ts → middleware.ts → session.ts
>
> Score: **4/10** · [Full review →](https://argusai.vercel.app/reviews/...)

### Inline Comments
Every comment follows: emoji severity + category title, then why it matters:

```
🔴 **Security:** Token expiry not validated

verifyToken() checks the signature but skips the exp claim.
An expired token passes validation, allowing stale sessions
to access protected endpoints indefinitely.
```

---

## Severities

| | Severity | Description |
|---|----------|-------------|
| 🔴 | **Blocker** | Will crash, corrupt data, or create a security vulnerability in production. Blocks merge. |
| 🟡 | **Should fix** | Should fix before merge but won't cause immediate harm. Design smells, missing edge cases, silent failures. |
| 💡 | **Suggestion** | Nice to have — could improve later. Readability, minor refactors. |
| ✅ | **Praise** | Good code worth acknowledging. Clean patterns, clever solutions. |

## Categories

| Category | Description |
|----------|-------------|
| **security** | Injection, leaked credentials, SSRF, path traversal |
| **bug** | Off-by-one, nil dereferences, broken invariants, missing edge cases |
| **performance** | N+1 queries, unnecessary allocations, missing caching |
| **error_handling** | Swallowed errors, empty catch blocks, silent fallbacks |
| **readability** | Unclear naming, complex nesting, dead code |
| **style** | Formatting, convention violations, import ordering |
| **type_design** | Weak type invariants, stringly-typed APIs, poor encapsulation |
| **testing** | Missing edge case tests, brittle assertions, untested error paths |

---

## PR Diagrams

Argus auto-generates Mermaid diagrams in the PR description for complex PRs:

| Diagram | Triggers when | Shows |
|---------|---------------|-------|
| **Sequence** | 3+ changed files | Call flow between modules with bug annotations (⚠️) |
| **Data Flow** | Security findings or auth/API/input files touched | Untrusted input traced through the system, tainted paths marked |
| **Dependency** | 10+ changed files | Import relationships between changed files |

Diagrams appear as collapsible `<details>` blocks in the PR description — click to expand. Max 2 diagrams per review.

---

## Review Rules

Create custom rules that Argus checks on every review:

- **Org-wide**: Apply to all repos in your workspace
- **Repo-specific**: Override or extend org rules for individual repos
- **Categories**: security, performance, style, convention, testing, architecture

Example: "All API endpoints must validate authentication tokens before processing requests."

---

## API Key Security

Your API keys are encrypted at rest using **AES-256-GCM** — the same standard used by banks and government systems. Here's how it works:

- **Encryption**: When you save an API key, it's immediately encrypted with a 256-bit key before touching the database. The plaintext never persists.
- **Storage**: Only the encrypted ciphertext is stored. Each key has a unique random nonce — even identical keys produce different ciphertext.
- **Access**: Keys are decrypted in-memory only when making an LLM API call, then immediately discarded. They're never logged, cached, or sent anywhere except the provider you configured.
- **Isolation**: Keys are scoped to your installation. No other user or workspace can access them.
- **Display**: The dashboard shows only a masked placeholder (`sk-...****`). The full key is never sent back to the frontend after saving.

We never see your code. We never see your API keys. Argus runs on your keys, in your repos, behind your auth.

---

## Model Configuration

Argus uses BYOK (Bring Your Own Key) — you provide your LLM API key. 7 providers supported:

| Provider | Auth | Notes |
|----------|------|-------|
| **OpenRouter** | API key | Routes to any model (default) |
| **OpenAI** | API key | Direct access |
| **Anthropic** | API key | Direct access |
| **Azure OpenAI** | API key + endpoint URL | Custom deployment endpoint required |
| **GCP Vertex AI** | Bearer token + endpoint URL | OpenAI-compatible endpoint |
| **AWS Bedrock** | API key + endpoint URL | Bedrock runtime endpoint |
| **Zhipu AI** | API key | GLM models |

Configure different models per pipeline stage. Type any model name — not limited to presets:

| Stage | Recommended | Purpose |
|-------|-------------|---------|
| Triage | gpt-4o-mini | Fast classification |
| Review | claude-sonnet-4 | Deep analysis |
| Scoring | gpt-4o-mini | Validation |
| Synthesis | gpt-4o-mini | Summary generation |

---

## Review Personas

Choose how Argus communicates:

- **Default** — balanced, professional feedback
- **Security Auditor** — prioritizes injection, auth, secrets
- **Performance Engineer** — focuses on N+1 queries, allocations, caching
- **Mentor** — educational tone, explains why, suggests learning paths
- **Architect** — design patterns, coupling, API contracts
- **Strict** — comments on everything, no issue too small
- **Custom** — write your own system prompt

Override per-PR with `@argus-eye review --persona strict`.

---

## Bot Commands

Comment on any PR to trigger actions:

| Command | Description |
|---------|-------------|
| `@argus-eye review` | Trigger a code review |
| `@argus-eye review --force` | Re-review even if already reviewed |
| `@argus-eye review --persona <name>` | Review with a specific persona |
| `@argus-eye test` | Generate a test plan from review findings |
| `@argus-eye test --code` | Draft executable test code for findings |
| `@argus-eye remember <pattern>` | Teach Argus a pattern for this repo |
| `@argus-eye remember --org <pattern>` | Teach an org-wide pattern |
| `@argus-eye resolve` | Resolve threads whose referenced file appears in the latest diff |
| `@argus-eye fix` | Apply suggestion blocks as a commit |
| `@argus-eye help` | Show available commands |

---

## Memory & Learning

Argus has 4 types of memory:

### Patterns
Code conventions auto-learned from reviews: "use `errors.Is()` not `==`", "always handle `context.Canceled`". Matched semantically on future PRs.

### Scenarios
Behavioral knowledge about what can go wrong. Three sources:
- **Auto-extracted** from critical/warning review findings
- **Auto-generated** from GitHub Issues labeled `argus` or `bug`
- **Manual** — create via the dashboard with steps, initial state, and expected outcome

Argus marks scenarios as **outdated** when their files change in a PR. React 👎 on a review comment to dismiss.

### Decision Traces
Every review finding + developer reply accumulated as institutional memory. Builds a risk profile per file — frequently flagged files get extra scrutiny.

### The Flywheel
Every review generates traces → traces inform future reviews → better reviews generate better traces → the system compounds. No retraining. The model stays fixed. The world model grows.

---

## Insights & Risk

The Insights dashboard shows codebase health:

- **Hot Files** — files with the most review findings, ranked by risk score
- **Decision Traces** — timeline of findings, approvals, and dismissals
- **Risk Scores** — weighted severity over time per file

---

## Settings & Controls

Toggle features per repo:

| Feature | Default | Description |
|---------|---------|-------------|
| Cross-file context | ON | Include related files in review context |
| Blast radius | ON | Show dependency impact analysis |
| Scenario memory | ON | Match known issues to PR files |
| Code simulation | OFF | Simulate execution paths (experimental) |

---

## Pricing

| | Free | Pro |
|---|---|---|
| Price | $0 | $19/mo per workspace |
| Repos | 3 | Unlimited |
| Reviews/mo | 50 | 500 |
| Deep review | Basic | 4 specialists |
| Memory | Patterns only | Full (scenarios, traces, simulation) |

BYOK — you bring your own LLM API key. We never see your code.

---

*Built by [Argus](https://argusai.vercel.app). Code that understands itself.*
