# Argus Documentation

> Your codebase has bugs you haven't found yet. Argus finds them before your users do.

Argus is an AI code reviewer that catches real bugs — not lint warnings. It understands your system across files, remembers what broke before, and gets smarter with every review.

**Zero configuration. Works with your existing GitHub flow.**

---

## Getting Started

Setup takes 2 minutes:

1. Install the [Argus GitHub App](https://github.com/apps/argus-eye)
2. Select repos to monitor
3. Add your LLM API key (BYOK — bring your own key)
4. Open a PR — Argus reviews automatically

No config files. No CI setup. No YAML.

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
> 🔴 2 blockers · 🟡 3 should fix · 5 files reviewed
>
> **Top priority:** Token verification in auth.ts doesn't check expiry — expired sessions pass through.
>
> **Fix order:** auth.ts → middleware.ts → session.ts
>
> Score: **4/10** · [Full review →](https://argus.reviews/reviews/...)

Severity counts. Fix ordering. Root cause. One glance tells you where to start.

### Inline Comments

Each finding explains *why* it matters, not just *what's wrong*:

```
🔴 **Security:** Token expiry not validated

verifyToken() checks the signature but skips the exp claim.
An expired token passes validation, allowing stale sessions
to access protected endpoints indefinitely.
```

Every comment with a fix includes a GitHub "Apply suggestion" button — one click to patch.

### Severity Levels

| | Level | Meaning |
|---|---|---|
| 🔴 | **Blocker** | Will crash, corrupt data, or create a security hole. Blocks merge. |
| 🟡 | **Should fix** | Won't cause immediate harm but should fix before merge. |
| 💡 | **Suggestion** | Nice to have. Improve later. |
| ✅ | **Praise** | Good code acknowledged. |

When everything is critical, nothing is. Argus calibrates severity so blockers mean something.

### PR Diagrams

For complex PRs, Argus generates Mermaid diagrams directly in your PR description:

| Diagram | When | What it shows |
|---------|------|---------------|
| **Sequence** | 3+ files changed | Call flow between modules, bugs annotated with warning signs |
| **Data Flow** | Security findings detected | Untrusted input traced through the system |
| **Dependency** | 10+ files changed | Import relationships between changed files |

Collapsed by default — click to expand. Max 2 per review.

---

## How It Works

Argus runs a multi-stage review pipeline: it gathers cross-file context, performs focused analysis from multiple angles, deduplicates findings, validates them against the diff, and synthesizes a summary with fix ordering and diagrams.

Most PRs complete in a couple of minutes.

---

## Memory & Learning

Argus doesn't start from zero on every review. It remembers.

### Patterns
Conventions your team cares about, automatically picked up from past reviews and developer feedback. Matched semantically on future PRs — no config needed.

### Scenarios
Known failure modes and edge cases for your codebase. Created from reviews, from GitHub Issues, or manually from the dashboard. When someone touches a file with known scenarios, Argus checks whether the change is safe against them.

### The Flywheel
Every review, reply, and fix becomes institutional memory. Reviews get sharper over time as Argus learns what your team cares about — without any model retraining.

---

## Code Simulation

For enabled scenarios, Argus reasons about whether your change is safe: what could break, who's affected, and how to fix it. Low-confidence predictions are filtered out to keep noise down.

---

## Review Personas

Choose how Argus communicates:

| Persona | Focus |
|---------|-------|
| **Default** | Balanced, professional |
| **Security Auditor** | Injection, auth, secrets |
| **Performance Engineer** | N+1 queries, allocations, caching |
| **Mentor** | Educational tone, explains why |
| **Architect** | Design patterns, coupling, API contracts |
| **Strict** | Comments on everything |
| **Adversarial** | Assumes worst-case inputs |
| **Fresh Eyes** | Reviews as if seeing the codebase for the first time |
| **Custom** | Write your own system prompt |

Override per-PR: `@argus-eye review --persona strict`

---

## Bot Commands

Comment on any PR:

| Command | What it does |
|---------|-------------|
| `@argus-eye review` | Trigger a review |
| `@argus-eye review --force` | Re-review even if already reviewed |
| `@argus-eye review --persona <name>` | Review with a specific persona |
| `@argus-eye test` | Generate a test plan from findings |
| `@argus-eye test --code` | Draft executable test code |
| `@argus-eye remember <pattern>` | Teach a pattern for this repo |
| `@argus-eye remember --org <pattern>` | Teach an org-wide pattern |
| `@argus-eye resolve` | Resolve all open Argus threads on this PR |
| `@argus-eye fix` | Apply suggestion blocks as a commit |
| `@argus-eye help` | Show available commands |

---

## Review Rules

Create rules Argus enforces on every review:

- **Org-wide** — apply to all repos
- **Repo-specific** — override or extend
- **Categories** — security, performance, style, convention, testing, architecture

Example: *"All API endpoints must validate authentication tokens before processing requests."*

---

## Security

### Your API Keys
Encrypted at rest with industry-standard symmetric encryption. Decrypted in-memory only during LLM calls, then discarded. Never logged, never cached, never sent anywhere except your configured provider. The dashboard shows only masked placeholders.

### Your Code
Argus reads diffs and file content to review. Your code is never stored, never used for training, never shared. Each workspace is fully isolated.

**We never see your code. We never see your API keys.**

---

## Model Configuration

BYOK — bring your own key. 7 providers supported:

| Provider | Notes |
|----------|-------|
| **OpenRouter** | Routes to any model (default) |
| **OpenAI** | Direct access |
| **Anthropic** | Direct access |
| **Azure OpenAI** | Custom deployment endpoint |
| **GCP Vertex AI** | OpenAI-compatible endpoint |
| **AWS Bedrock** | Bedrock runtime endpoint |
| **Zhipu AI** | GLM models |

Configure different models per review stage to tune cost/quality. Type any model name.

---

## Settings

Toggle per repo:

| Feature | Default | Why it matters |
|---------|---------|----------------|
| Cross-file context | ON | Reviews understand callers, imports, and shared types |
| Blast radius | ON | Shows what downstream code your change affects |
| Scenario memory | ON | Surfaces past bugs when you touch the same files |
| Code simulation | OFF | Tests known failure scenarios against your diff |

---

## Pricing

|  | Free | Pro |
|---|---|---|
| **Price** | $0 | **$19/mo** per workspace |
| Repos | 3 | Unlimited |
| Reviews/mo | 50 | 500 |
| Deep review | Basic | Full multi-angle analysis |
| Memory | Patterns only | Full — scenarios, simulation, decision history |
| Diagrams | — | Sequence + data flow |

BYOK — you bring your own LLM API key. We charge for the platform, not the AI.

---

*Built by [Argus](https://argus.reviews). Stop shipping bugs.*
