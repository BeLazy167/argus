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

Every PR goes through 6 stages:

### 01 — Triage
A fast LLM pass classifies every changed file: **skip** (generated/vendored), **skim** (low-risk), **security_skim**, or **deep** (complex changes needing specialist review).

### 02 — Context Gathering
Before reviewing, Argus gathers full context:
- **Cross-file analysis**: traces callers, imports, tests, and shared types for every changed file
- **Blast radius**: queries the dependency graph to find downstream code affected by the change
- **Scenario matching**: searches known issues, past bugs, and edge cases relevant to the changed files
- **Decision traces**: looks up review history — what was flagged before, what developers agreed or dismissed

### 03 — Deep Review
Per-file parallel review with full codebase awareness. For files triaged as "deep," 4 specialist reviewers activate:
- **Bug Hunter** — logic errors, edge cases, off-by-one
- **Security** — injection, auth bypass, data exposure
- **Architecture** — coupling, abstraction leaks, design issues
- **Regression** — behavior changes that could break existing functionality

### 04 — Scoring & Validation
A separate model scores each finding (0–100). Comments below the threshold are dropped to reduce noise. Overlapping findings are deduplicated. This is why Argus doesn't spam your PRs.

### 05 — Synthesis
An LLM generates a conversational summary — like a senior dev giving a quick take on the PR. Not "found X issues," but natural language feedback.

### 06 — Post & Learn
The review is posted with structured inline comments (What happened + Why it matters). Developers react:
- 👍 to confirm a finding — Argus learns the pattern
- 👎 to dismiss — Argus learns to avoid similar false positives

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
Instead of "Argus found 3 issues," the review summary reads like a senior dev's assessment:

> Overall this looks solid — clean separation of concerns and good test coverage. Found 3 things worth addressing: The JWT validation in `auth/handler.go` doesn't check token expiry, which could allow expired sessions through. That's the main blocker.
>
> Score: **7/10** · [Full review →](https://argusai.vercel.app/reviews/...)

### Inline Comments
Every comment has structured What/Why sections:

```
[critical · bug] JWT expiry not validated

What: The verifyToken() function checks the signature but skips
the exp claim, accepting expired tokens as valid.

Why: An attacker with a stolen expired token could access protected
endpoints indefinitely. This bypasses session timeout controls.
```

---

## Severities

| Severity | Description |
|----------|-------------|
| **critical** | Bugs, security vulnerabilities, data loss risks that will cause production failures |
| **warning** | Performance issues, error handling gaps, fragile code that works but could break |
| **suggestion** | Readability improvements, style consistency, minor refactors |
| **praise** | Well-written code, good patterns, clever solutions worth highlighting |

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

Argus uses BYOK (Bring Your Own Key) — you provide your LLM API key. Supports 7 providers: OpenRouter, OpenAI, Anthropic, Azure OpenAI, GCP Vertex AI, AWS Bedrock, and Zhipu AI. Configure different models per pipeline stage:

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
- **Mentor** — encouraging, educational explanations
- **Strict** — terse, no-nonsense, high standards
- **Custom** — write your own system prompt

---

## Bot Commands

Comment on any PR to trigger actions:

| Command | Description |
|---------|-------------|
| `@argus-eye review` | Trigger a manual review |
| `@argus-eye review --force` | Re-review even if already reviewed |
| `@argus-eye remember <pattern>` | Teach Argus a pattern for this repo |
| `@argus-eye remember --org <pattern>` | Teach an org-wide pattern |
| `@argus-eye resolve` | Resolve review threads on files changed since review |
| `@argus-eye fix` | Apply suggestion blocks as a commit |
| `@argus-eye help` | Show available commands |

---

## Memory & Learning

Argus has 4 types of memory:

### Patterns
Code conventions auto-learned from reviews: "use `errors.Is()` not `==`", "always handle `context.Canceled`". Matched semantically on future PRs.

### Scenarios
Behavioral knowledge about what can go wrong: "EU FX rounding breaks during proration." Created from critical findings, activated only after developer 👍 approval.

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
