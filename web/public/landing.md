# ARGUS — AI Code Review That Builds Institutional Memory

> Nothing merges unseen. AI code review that gets smarter with every PR.

**Argus is an AI code reviewer that catches real bugs — not lint warnings.** It traces dependencies across files, remembers what broke before, and simulates failures before they ship.

Install the [Argus GitHub App](https://github.com/apps/argus-eye) — zero configuration, 60 seconds.

---

## What Argus Does

- **Catches the bugs humans miss.** Argus reads the full diff, understands your system across files, and flags regressions before they hit production.
- **Remembers what broke before.** Every review feeds institutional memory. When a similar bug pattern reappears, Argus cites the past incident.
- **Simulates failures.** Scenario-based checking: "Does this break the old OAuth flow? The cold-start path? The incremental migration?"
- **Verifies PR intent.** Argus reads the PR description and linked issues, then checks whether the diff actually delivers what the author claimed.

## What Argus Is Not

- Not a linter. Argus ignores style, prefers real-world impact.
- Not a training data vacuum. We don't train on your code.
- Not a bot that posts 40 nitpicks. Argus scores findings and only posts high-signal ones inline.

## Pricing

- **Free** — personal accounts, public repos
- **Pro** — $19/month per organization, unlimited private repos

No credit card to start. [Install on GitHub →](https://github.com/apps/argus-eye)

## Links

- [Documentation](https://argus.reviews/docs)
- [Pricing](https://argus.reviews/pricing)
- [Changelog](https://argus.reviews/changelog)
- [Compare to CodeRabbit, Greptile, Cubic](https://argus.reviews/compare)
- [Dashboard](https://argus.reviews/dashboard)

## For AI Agents

This markdown file is returned when `Accept: text/markdown` is requested for `/`. For structured machine-readable metadata:

- `/robots.txt` — crawler rules + Content-Signal preferences (ai-train=no, search=yes, ai-input=yes)
- `/sitemap.xml` — site map
- `/llms.txt` — LLM context file (overview + key routes)
- `/docs.md` — full documentation in markdown

## Contact

- GitHub: [BeLazy167/argus](https://github.com/BeLazy167/argus) — source code + issue tracker
- Email: support@argus.reviews
