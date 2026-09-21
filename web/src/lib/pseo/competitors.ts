export interface Competitor {
  slug: string;
  name: string;
  tagline: string;
  pricing: string;
  strengths: string[];
  weaknesses: string[];
  argusAdvantage: string;
  summary: string;
  stat: { claim: string; source: string };
  updatedAt: string;
  features: {
    memory: boolean;
    multiPass: boolean;
    diagramGeneration: boolean;
    codeSimulation: boolean;
    architectureAnalysis: boolean;
    byok: boolean;
    patternLearning: boolean;
    reviewContract: boolean;
    selfHosted: boolean;
    multiPlatform: boolean;
    staticAnalysis: boolean;
    testGeneration: boolean;
    ideIntegration: boolean;
    ticketing: boolean;
    complianceCert: boolean;
  };
}

export const featureLabels: Record<keyof Competitor["features"], string> = {
  memory: "Institutional memory across reviews",
  multiPass: "Multi-pass / multi-agent pipeline",
  diagramGeneration: "PR diagram generation (sequence + data flow)",
  codeSimulation: "Failure scenario simulation",
  architectureAnalysis: "Architecture & dependency tracing",
  byok: "Bring your own LLM key",
  patternLearning: "Pattern learning from codebase history",
  reviewContract: "Computed per-PR review contract (auto depth routing)",
  selfHosted: "Self-hosted deployment",
  multiPlatform: "Reviews GitLab / Bitbucket / Azure DevOps",
  staticAnalysis: "Bundled static analysis / SAST",
  testGeneration: "Generates unit tests",
  ideIntegration: "IDE extension (VS Code / JetBrains)",
  ticketing: "Jira / Linear ticket creation & checks",
  complianceCert: "SOC 2 / ISO 27001 certified",
};

// Short noun-phrase labels for inline prose. The featureLabels above are table
// row headers and read awkwardly in a sentence; these are used by the detail
// page's "where the competitor leads" caveat. Only the six gap features (the
// ones Argus lacks) ever appear there.
export const featureShortLabels: Partial<Record<keyof Competitor["features"], string>> = {
  multiPlatform: "multi-platform support beyond GitHub",
  staticAnalysis: "bundled static analysis",
  testGeneration: "test generation",
  ideIntegration: "an IDE extension",
  ticketing: "issue-tracker ticket integration",
  complianceCert: "SOC 2 compliance",
};

// Argus's own row. The last six are honest gaps today: GitHub-only, no bundled
// SAST engine, no test generation, no IDE extension, no ticket creation, and no
// SOC 2 certification yet (young, open-source project).
export const argusFeatures: Competitor["features"] = {
  memory: true,
  multiPass: true,
  diagramGeneration: true,
  codeSimulation: true,
  architectureAnalysis: true,
  byok: true,
  patternLearning: true,
  reviewContract: true,
  selfHosted: true,
  multiPlatform: false,
  staticAnalysis: false,
  testGeneration: false,
  ideIntegration: false,
  ticketing: false,
  complianceCert: false,
};

export const competitors: Competitor[] = [
  {
    slug: "coderabbit",
    name: "CodeRabbit",
    tagline: "The most broadly adopted AI reviewer, across every major Git platform",
    pricing: "Free (public repos); Essentials $24/dev/mo; Team $48/dev/mo; Advanced $72/dev/mo; Enterprise custom — annual billing; self-hosted is Enterprise-only (500+ seats)",
    strengths: [
      "Reviews on GitHub, GitLab, Azure DevOps, and Bitbucket (Cloud + Data Center)",
      "Bundles 50+ linters and SAST/security scanners into every review; Advanced adds continuous security monitoring, per-PR security review, and blast-radius/architectural impact analysis",
      "Finishing Touches on Team+: unit-test generation, merge-conflict resolution, autofix, and code simplification",
      "Learnings engine — feedback on past reviews persists per org and tailors future ones",
      "Creates and validates Jira / Linear / GitHub / GitLab issues against a PR's acceptance criteria; Coding Plans generated from issues",
      "Triage cross-repo PR prioritization queue, Change Stack review workspace, and a metered Coding Agent for Slack/Discord ($0.40/agent-min)",
      "First-class VS Code extension (also runs in Cursor / Windsurf) plus CLI reviews; SOC 2 Type II and ISO 27001 certified",
    ],
    weaknesses: [
      "No failure-scenario simulation (its pipeline ranks context, verifies comments, and traces attack paths, but doesn't run or reason about runtime failure modes of a change)",
      "No computed per-PR review contract — Triage ranks the PR queue for human attention and depth is set through config/profiles, not auto-routed per PR",
      "BYOK (own LLM provider) and self-hosting are Enterprise-only; self-hosted requires a 500+ seat contract",
      "Per-developer hourly review caps (5/8/10/hr by tier); over-limit reviews bill usage-based at $0.25 per reviewed file",
    ],
    argusAdvantage:
      "CodeRabbit is the more mature, broadly integrated product — more platforms, bundled static analysis, test generation, ticketing, a Triage queue and Coding Agent, and SOC 2 / ISO 27001 certifications Argus does not have. Argus's narrower bets: a computed per-PR review contract that routes review depth automatically (no config or profiles to hand-tune), failure-scenario simulation, judge-scored findings with a hard comment cap on every tier (no minimum-comment noise), BYOK in the standard managed tier rather than a 500-seat Enterprise gate, and a Glass Box footer that shows the contract, what was checked, and what team feedback suppressed. Pick CodeRabbit for breadth and ecosystem today; pick Argus if depth-routing and cost/model control matter more than integrations.",
    summary:
      "CodeRabbit is the most widely adopted AI code reviewer, running across GitHub, GitLab, Azure DevOps, and Bitbucket with 50+ bundled scanners, test generation, ticket creation and validation, a Learnings engine that remembers team feedback, and newer platform pieces — a Triage prioritization queue, Change Stack workspace, continuous security monitoring, and a metered coding agent. It's the breadth leader. It doesn't do failure-scenario simulation or automatic per-PR depth routing, reserves BYOK/self-hosting for a 500-seat Enterprise tier, and caps reviews per developer per hour.",
    stat: {
      claim: "CodeRabbit bundles 50+ linters and security scanners into each review and is SOC 2 Type II and ISO 27001 certified",
      source: "coderabbit.ai, 2026",
    },
    updatedAt: "2026-09-20",
    features: {
      memory: true,
      multiPass: true,
      diagramGeneration: true,
      codeSimulation: false,
      architectureAnalysis: true,
      byok: true,
      patternLearning: true,
      reviewContract: false,
      selfHosted: true,
      multiPlatform: true,
      staticAnalysis: true,
      testGeneration: true,
      ideIntegration: true,
      ticketing: true,
      complianceCert: true,
    },
  },
  {
    slug: "greptile",
    name: "Greptile",
    tagline: "Full-codebase-context review that actually runs your PR in a sandbox",
    pricing: "Free Starter (1 dev, 50 credits/mo); Pro $30/seat/mo (50 credits, then $1/credit; a TREX review costs 3 credits); Enterprise custom (self-hosted); free for OSS",
    strengths: [
      "TREX runs the PR branch in a disposable sandbox — services, mocks, browser agents, written tests — and catches bugs that only surface at runtime",
      "Full-repo graph of functions, classes, and dependencies, plus Repo Clusters for cross-repo context",
      "Learns from team comments, replies, and reactions; plain-English custom rules via cascading greptile.json / .greptile config",
      "Auto diagrams on every non-trivial PR (sequence, entity-relation, class, flow) plus a 0–5 confidence score and P0–P2 severity badges",
      "Reviews GitHub and GitLab (Cloud + Self-Managed) and Perforce depots; genuine air-gapped self-hosting with BYO-LLM",
      "Auto-approves low-risk PRs after a clean 5/5 review; MCP server, Claude Code / Codex plugins, and one-click Fix-with-your-Agent handoffs",
      "Reviews code against linked Jira tickets, Linear issues, and Confluence pages (read-only — no ticket creation); SOC 2 Type II",
    ],
    weaknesses: [
      "No bundled static-analysis/SAST engine — positions against linters rather than shipping them",
      "No first-party IDE extension — IDE reach is via MCP server, coding-agent plugins, and fix handoffs",
      "No computed per-PR review contract — strictness and auto-approve are config/score-driven, not auto-routed depth",
      "Credit pricing adds up: 50 credits/seat included, then $1/credit, and TREX reviews burn 3 credits each",
    ],
    argusAdvantage:
      "Greptile is the closest peer — and on failure simulation it arguably goes further: TREX executes the PR in a sandbox, where Argus reasons about failure scenarios rather than running them. Greptile also matches Argus on memory, architecture tracing, diagrams, BYOK, and self-hosting, and adds GitLab and Perforce support, ticket-context checks, auto-approve, and SOC 2. Argus's remaining distinct bets are the computed per-PR review contract (auto depth routing — Greptile has no equivalent), judge-scored findings with a hard comment cap, and the Glass Box footer. This is a genuine toss-up; choose on whether you want execution-based simulation (Greptile) or contract-driven depth routing and cost control (Argus).",
    summary:
      "Greptile delivers one of the strongest context-aware reviews on the market: a full-repo dependency graph with cross-repo clusters, auto diagrams, memory from team feedback, GitLab and Perforce support, air-gapped self-hosting with BYO-LLM, and TREX — a sandbox that actually runs your PR and writes tests to catch runtime bugs. It adds Jira/Linear ticket context and auto-approve on clean reviews, but doesn't bundle static analysis, ship a native IDE extension, create tickets, or auto-route review depth per PR.",
    stat: {
      claim: "Greptile's TREX runs the PR in a sandbox and catches ~20% more bugs than review alone",
      source: "greptile.com/trex, 2026",
    },
    updatedAt: "2026-09-20",
    features: {
      memory: true,
      multiPass: true,
      diagramGeneration: true,
      codeSimulation: true,
      architectureAnalysis: true,
      byok: true,
      patternLearning: true,
      reviewContract: false,
      selfHosted: true,
      multiPlatform: true,
      staticAnalysis: false,
      testGeneration: true,
      ideIntegration: false,
      ticketing: true,
      complianceCert: true,
    },
  },
  {
    slug: "cubic",
    name: "Cubic",
    tagline: "Micro-agent code review with continuous whole-codebase scanning",
    pricing: "Free (20 PR reviews/mo; free for public repos); Team $30/dev/mo annual ($40 monthly); Pro $79/dev/mo annual ($99 monthly); Enterprise custom",
    strengths: [
      "#1-ranked AI reviewer on independent benchmarks; micro-agent pipeline plus custom plain-English review agents",
      "Continuous whole-codebase scans — thousands of agents plus static analysis (secrets, CVEs, IaC) beyond open PR diffs",
      "Auto-generated architecture diagrams on every PR, plus an AI wiki with on-demand diagrams",
      "Learns from senior developers' comment history and team feedback; merge-confidence score and auto-approval for clean PRs",
      "Background agents fix findings as commits or fix PRs; creates Jira/Linear tickets and checks PRs against linked-issue requirements",
      "MCP server, CLI, and desktop app reach Cursor / VS Code / Claude Code / CI; BYOK on Enterprise; SOC 2 Type I; free for OSS",
    ],
    weaknesses: [
      "GitHub-only — no GitLab, Bitbucket, or Azure DevOps",
      "No self-hosted / on-prem deployment (SaaS only)",
      "No failure-scenario simulation or test generation; static analysis runs in codebase scans, not the per-PR review",
      "No native IDE extension — IDE/agent access goes through MCP, the CLI, or the desktop app",
      "No computed per-PR review contract — deeper review is a manually requested Ultrareview; BYOK is Enterprise-only",
    ],
    argusAdvantage:
      "Cubic and Argus are close on philosophy — specialized agents, low false positives, self-learning, architecture reasoning — and Cubic has added breadth Argus lacks: continuous codebase scans with static analysis, auto-generated PR diagrams, auto-approval, background fix agents, Jira/Linear ticket creation and issue-requirement checks, an MCP server, CLI, desktop app, and SOC 2 Type I. Argus's remaining edges: failure-scenario simulation, a computed per-PR review contract that routes depth automatically (Cubic's deeper Ultrareview is manually requested), genuine self-hosting, BYOK in the standard tier rather than Enterprise-only, and the Glass Box footer. Pick Cubic for a polished, feature-dense GitHub SaaS; pick Argus for depth routing, scenario simulation, and deployment/cost control.",
    summary:
      "Cubic is a feature-dense, GitHub-only reviewer: a benchmark-topping micro-agent pipeline, continuous whole-codebase scans with static analysis, auto-generated PR architecture diagrams, auto-approval, background fix agents, Jira/Linear ticket creation and issue-requirement checks, an AI wiki, and MCP/CLI/desktop access for coding agents. It's SaaS-only, meters usage by reviewed lines, reserves BYOK for Enterprise, and doesn't do failure simulation, test generation, or automatic per-PR depth routing.",
    stat: {
      claim: "Cubic is ranked the #1 AI code reviewer on every independent benchmark",
      source: "cubic.dev, 2026",
    },
    updatedAt: "2026-09-20",
    features: {
      memory: true,
      multiPass: true,
      diagramGeneration: true,
      codeSimulation: false,
      architectureAnalysis: true,
      byok: true,
      patternLearning: true,
      reviewContract: false,
      selfHosted: false,
      multiPlatform: false,
      staticAnalysis: true,
      testGeneration: false,
      ideIntegration: false,
      ticketing: true,
      complianceCert: true,
    },
  },
  {
    slug: "macroscope",
    name: "Macroscope",
    tagline: "Low-noise AI review that auto-approves safe PRs and validates its own fixes",
    pricing: "Usage-based — code review $0.05/KB of diff (10 KB min; ~$0.025–$0.20/KB by Detection Mode); status $0.05/commit; Agent $0.01/credit (1,000 free/mo); $100 free credit; no per-seat fees; Enterprise via sales",
    strengths: [
      "Precision / low-noise focus — top detection rate (~48%) on its published 118-bug benchmark, with per-review and per-PR spend caps",
      "AST reference graph of the whole codebase for cross-file, cross-component bug detection",
      "Approvability auto-approves low-risk PRs (CODEOWNERS + eligibility + zero-issue correctness); Fix It For Me opens a fix PR and iterates until CI passes",
      "Detection Modes (Budget→Ultra) scale review depth with parallel agents; markdown-defined Check Run Agents add custom checks on every PR",
      "Context-aware: pulls Jira / Linear / Sentry / LaunchDarkly / BigQuery / PostHog context; the Agent creates Jira/Linear issues and draws architecture / sequence diagrams",
      "Learns from thumbs up/down feedback; CLI for local pre-push reviews; usage-based pricing, no per-seat fees; SOC 2 Type II",
    ],
    weaknesses: [
      "GitHub-only — no GitLab, Bitbucket, or Azure DevOps",
      "No self-hosted / on-prem deployment (SaaS only)",
      "No computed per-PR review contract — depth is user-set via Detection Modes (repo/dev/label), not auto-routed",
      "No failure-scenario simulation, bundled SAST, test generation, BYOK, or IDE extension (CLI only)",
      "Each push re-bills a review — spend stays predictable only through caps and budgets",
    ],
    argusAdvantage:
      "Macroscope and Argus both chase signal-to-noise and both learn from team feedback — and Macroscope now scales review depth too: Detection Modes add parallel-agent passes and markdown Check Run Agents run custom checks on every PR. It pushes harder on workflow automation: Approvability auto-approves low-risk PRs, Fix It For Me runs CI until its fix passes, and the Agent creates Jira/Linear tickets and draws diagrams — none of which Argus does today — plus a SOC 2 Type II certification Argus doesn't have. Argus's distinct edges: a computed per-PR review contract that routes depth automatically (Macroscope's modes are user-set), failure-scenario simulation, BYOK, self-hosting, the Glass Box footer, and open source. Pick Macroscope for hands-off automation on GitHub with usage pricing; pick Argus for a self-hostable reviewer with auto depth routing and cost control.",
    summary:
      "Macroscope is a precision AI reviewer for GitHub that leans into automation: Approvability auto-approves low-risk PRs, Fix It For Me opens a fix PR and iterates until CI passes, and Detection Modes scale review depth from Budget to a parallel-agent Ultra. It builds an AST reference graph of the codebase, learns from thumbs up/down feedback, pulls Jira / Linear / Sentry / LaunchDarkly context into reviews, and its Agent creates tickets and diagrams — all on usage-based pricing with no per-seat fees. It's GitHub-only and SaaS-only, with no computed per-PR depth routing, failure simulation, BYOK, self-hosting, or IDE extension.",
    stat: {
      claim: "Macroscope detected ~48% of 118 real runtime bugs — the highest in its published benchmark (CodeRabbit ~46%, Greptile ~24%)",
      source: "macroscope.com, 2026",
    },
    updatedAt: "2026-09-20",
    features: {
      memory: true,
      multiPass: true,
      diagramGeneration: true,
      codeSimulation: false,
      architectureAnalysis: true,
      byok: false,
      patternLearning: true,
      reviewContract: false,
      selfHosted: false,
      multiPlatform: false,
      staticAnalysis: false,
      testGeneration: false,
      ideIntegration: false,
      ticketing: true,
      complianceCert: true,
    },
  },
  {
    slug: "sourcery",
    name: "Sourcery",
    tagline: "Instant PR reviews with diagrams, IDE scanning, and BYOK",
    pricing: "Free (public repos); Pro $15/seat/mo ($12 annual); Team $30/seat/mo ($24 annual — BYO LLM, full security scanning, Jira); Enterprise custom (self-hosted)",
    strengths: [
      "Instant PR summaries, a reviewer's guide with sequence diagrams, and line-by-line feedback",
      "First-class IDE extensions — VS Code, Cursor, Windsurf, and JetBrains — running the same review engine pre-PR",
      "Reviews GitHub (incl. Enterprise Server) and GitLab (incl. self-managed)",
      "Bundled security scanning — SAST, secrets, IaC, dependencies, licenses — on every PR plus scheduled repo scans",
      "Bring-your-own-LLM (Team tier) and self-hosted Enterprise deployment",
      "Jira integration auto-creates tasks from security findings; can auto-approve clean PRs; SOC 2 Type II",
    ],
    weaknesses: [
      "No pattern learning — custom rules are hand-written, not learned from review history",
      "Single-pass review; no multi-agent pipeline",
      "No architecture / dependency-graph analysis beyond the diff",
      "No failure-scenario simulation, no test generation, no per-PR review contract",
      "Usage metered in diff characters — a per-PR cap plus a rolling seven-day budget skips large PRs",
    ],
    argusAdvantage:
      "Sourcery is fast and IDE-native, with diagrams, BYOK, GitLab support, a bundled security scanner, and an IDE extension Argus lacks. It tailors comments from your reactions and can auto-approve clean PRs, but stays lighter on depth: no multi-pass pipeline, pattern learning, architecture graph, or scenario simulation. Argus's differentiators are those — pattern learning, architecture tracing, failure-scenario simulation, and a computed per-PR review contract, plus memory that goes deeper: it learns from your team's 👍/👎 and replies, suppressing dismissed patterns semantically while never silencing security findings. Sourcery is quicker to feel useful; Argus goes deeper on review quality as your team uses it.",
    summary:
      "Sourcery delivers instant PR summaries, sequence diagrams, and inline feedback with strong IDE extensions (VS Code, Cursor, Windsurf, JetBrains), GitLab support, bundled security scanning (SAST, secrets, IaC, dependencies), BYOK, and Jira task creation. It's a capable, fast reviewer — now SOC 2 Type II — though lighter on depth: no multi-pass pipeline, pattern learning, architecture graph, or scenario simulation.",
    stat: {
      claim: "Sourcery pairs PR review with first-class IDE extensions (VS Code, Cursor, Windsurf, JetBrains) and is SOC 2 Type II certified",
      source: "sourcery.ai, 2026",
    },
    updatedAt: "2026-09-20",
    features: {
      memory: true,
      multiPass: false,
      diagramGeneration: true,
      codeSimulation: false,
      architectureAnalysis: false,
      byok: true,
      patternLearning: false,
      reviewContract: false,
      selfHosted: true,
      multiPlatform: true,
      staticAnalysis: true,
      testGeneration: false,
      ideIntegration: true,
      ticketing: true,
      complianceCert: true,
    },
  },
  {
    slug: "qodo",
    name: "Qodo (formerly CodiumAI)",
    tagline: "AI code quality and governance platform spanning review, tests, and IDE",
    pricing: "Pro Team from $30/mo (credit-based — 2,500 credits ≈ ~18 reviews/mo at $0.012/credit, pooled across the team); Enterprise custom (on-prem / air-gapped); 14-day trial, no permanent free tier; open-source PR-Agent free",
    strengths: [
      "Generates unit tests for changed code — Qodo Gen (IDE agent) and Qodo Cover (CI agent) — alongside review",
      "Pulls Jira, Linear, Azure DevOps, Monday.com, and GitHub/GitLab Issues into reviews for requirement-compliance checks; can auto-create tickets",
      "First-class VS Code and JetBrains IDE extensions",
      "Reviews GitHub, GitLab, Bitbucket, and Azure DevOps; Gerrit on Enterprise",
      "Single-tenant SaaS, on-prem, and air-gapped deployment; BYOK on Enterprise",
      "PR history memory and self-learning system (Enterprise); open-source PR-Agent core; SOC 2 compliant",
    ],
    weaknesses: [
      "No failure-scenario simulation",
      "No bundled SAST engine (security review is LLM + compliance checks, not third-party scanners)",
      "No computed per-PR review contract (rules + agents, not automatic depth routing)",
      "Credit-metered billing on every review; unused credits expire monthly",
      "No permanent free tier for private repos; BYOK and the self-learning system are Enterprise-only",
    ],
    argusAdvantage:
      "Qodo is a broad quality platform — review plus test generation, ticketing, IDE extensions, five Git platforms, and self-hosting, much of which Argus doesn't offer. Argus is narrower and review-focused: its edges are failure-scenario simulation, the computed per-PR review contract (auto depth routing), judge-scored findings with a hard comment cap, BYOK in the standard managed tier rather than Enterprise-only, and the Glass Box footer. If you want one tool that also writes tests and files tickets, Qodo is broader; if you want the deepest single-PR review with depth routing and cost control, Argus is the tighter pick.",
    summary:
      "Qodo is a quality and governance platform spanning agentic PR review, unit-test generation (Qodo Gen / Cover), and IDE assistance across GitHub, GitLab, Bitbucket, Azure DevOps, and Gerrit (Enterprise), with on-prem/air-gapped deployment, ticket-compliance checks, PR memory, and an open-source core — all metered by credits. It's broad. It doesn't do failure-scenario simulation, bundle a SAST engine, or auto-route review depth per PR.",
    stat: {
      claim: "Qodo publishes a public AI code review benchmark on real merged PRs and reports the strongest overall F1 score among evaluated tools",
      source: "qodo.ai/ai-code-review-benchmark, 2026",
    },
    updatedAt: "2026-09-20",
    features: {
      memory: true,
      multiPass: true,
      diagramGeneration: true,
      codeSimulation: false,
      architectureAnalysis: true,
      byok: true,
      patternLearning: true,
      reviewContract: false,
      selfHosted: true,
      multiPlatform: true,
      staticAnalysis: false,
      testGeneration: true,
      ideIntegration: true,
      ticketing: true,
      complianceCert: true,
    },
  },
  {
    slug: "semgrep",
    name: "Semgrep",
    tagline: "SAST / SCA / secrets platform with an AI triage layer",
    pricing: "Free (≤10 contributors, ≤10 repos); Teams from $30/contributor/mo per module (Secrets $15); Enterprise custom (on-prem SCM, dedicated infra)",
    strengths: [
      "Bundled proprietary SAST, supply-chain (SCA), and secrets scanning — deterministic plus AI detection",
      "Reviews GitHub, GitLab, Bitbucket, and Azure DevOps",
      "Multimodal AI layer (formerly Assistant) adds triage, autofix PRs, and business-logic (IDOR/authz) detection",
      "Memories learns org-specific remediation preferences from triage feedback, per project and per rule",
      "BYOK / custom model providers (OpenAI, Bedrock, Azure OpenAI, Gemini, xAI, Anthropic) on Enterprise",
      "Semgrep Guardian scans AI-generated code as it's written; MCP plugin feeds scans to coding agents",
      "Self-hosted / on-prem (CLI runs locally; Enterprise adds on-prem SCM + dedicated infrastructure)",
      "VS Code and JetBrains extensions; Jira ticketing; SOC 2 Type II certified",
    ],
    weaknesses: [
      "Not a general LLM code reviewer — no multi-pass review pipeline, diagrams, or architecture graph",
      "No failure-scenario simulation — the AI layer triages and autofixes findings rather than reasoning about a change end-to-end",
      "AI-powered detection (IDOR/authz) runs on full scans only — not diff-aware PR scans",
      "BYOK and custom model providers are Enterprise-only, and AI features consume monthly per-seat AI credits",
      "No test generation, no per-PR review contract",
    ],
    argusAdvantage:
      "Semgrep and Argus solve adjacent problems: Semgrep is a best-in-class static-analysis/security platform with an AI triage/remediation layer (Multimodal); Argus is a general senior-engineer reviewer. Semgrep wins decisively on SAST/SCA/secrets, platform breadth, IDE, ticketing, and compliance — none of which Argus does — and now offers BYOK on its Enterprise tier. Argus wins on the review itself: multi-pass reasoning, architecture tracing, failure-scenario simulation, diagrams, the computed review contract, and BYOK without an Enterprise contract. Many teams run both — Semgrep for deterministic security scanning, Argus for judgment-heavy review.",
    summary:
      "Semgrep is fundamentally a SAST/SCA/secrets platform — bundled scanners across four Git platforms with a Multimodal AI layer that triages findings, autofixes, flags business-logic flaws, and remembers org preferences via Memories. It's the security-scanning leader here, but it isn't a general reasoning-based reviewer: no multi-pass pipeline, architecture graph, or simulation, and BYOK is gated to Enterprise.",
    stat: {
      claim: "Semgrep Multimodal filters ~60% of SAST findings as noise on average, with a 96% human-agree rate",
      source: "docs.semgrep.dev, 2026",
    },
    updatedAt: "2026-09-20",
    features: {
      memory: true,
      multiPass: false,
      diagramGeneration: false,
      codeSimulation: false,
      architectureAnalysis: false,
      byok: true,
      patternLearning: true,
      reviewContract: false,
      selfHosted: true,
      multiPlatform: true,
      staticAnalysis: true,
      testGeneration: false,
      ideIntegration: true,
      ticketing: true,
      complianceCert: true,
    },
  },
  {
    slug: "codacy",
    name: "Codacy",
    tagline: "Code-quality & security platform with an AI reviewer layer",
    pricing: "Developer free (IDE plugin); Team $18/dev/mo annual or $21 monthly (≤30 devs, free for OSS); Business custom; self-hosted option (Kubernetes/Helm)",
    strengths: [
      "Bundled SAST / SCA / IaC / secrets scanning integrated into PR reviews; Business adds DAST, container scanning, and license scanning",
      "Reviews GitHub, GitLab, and Bitbucket",
      "AI Reviewer cross-checks the PR description and linked Jira ticket against the diff for logic gaps, missing tests, duplication, and complexity",
      "Free IDE plugin (VS Code, JetBrains, Cursor, Windsurf) with Guardrails MCP — scans AI-generated code as it's written and lets agents autofix issues",
      "Two-way Jira integration — creates tickets from findings and feeds ticket context into reviews",
      "Self-hosted / on-prem via Kubernetes / Helm",
      "SOC 2 Type II and ISO 27001 certified; free Developer tier",
    ],
    weaknesses: [
      "AI Reviewer is GitHub-only today (the scanning platform covers GitLab and Bitbucket)",
      "AI reviewer is steered by a manual review.md file, not learned team feedback — no institutional memory",
      "No multi-agent pipeline, architecture graph, diagrams, or failure-scenario simulation",
      "No BYOK (Codacy-managed Gemini), no pattern learning, no per-PR review contract",
      "No native test generation — it flags coverage gaps and hands context to your own AI agent (via MCP) to write them",
    ],
    argusAdvantage:
      "Codacy is a quality/security-gate platform (SAST/SCA/IaC/secrets) with a hybrid AI reviewer that checks the diff against PR/Jira intent. It leads Argus on bundled scanning, platform breadth, IDE extensions, self-hosting, Jira ticket creation, and compliance. Argus leads on the review's intelligence: institutional memory that learns from feedback (Codacy's reviewer is steered by a static review.md), multi-pass reasoning, architecture tracing, failure-scenario simulation, BYOK, and the computed review contract. Codacy is the pick for a compliance-grade quality gate; Argus for a reviewer that reasons and remembers.",
    summary:
      "Codacy is primarily a code-quality and security platform — bundled SAST/SCA/IaC/secrets scanning across GitHub, GitLab, and Bitbucket, with IDE plugins, self-hosting, and SOC 2 + ISO 27001. Its AI Reviewer (GitHub-only, Team/Business) posts low-noise PR reviews steered by a review.md file and Jira context, and the platform now creates Jira tickets from findings. Still no learned memory, multi-pass reasoning, architecture graph, simulation, or BYOK.",
    stat: {
      claim: "Codacy is SOC 2 Type II and ISO 27001 certified",
      source: "codacy.com, 2026",
    },
    updatedAt: "2026-09-20",
    features: {
      memory: false,
      multiPass: false,
      diagramGeneration: false,
      codeSimulation: false,
      architectureAnalysis: false,
      byok: false,
      patternLearning: false,
      reviewContract: false,
      selfHosted: true,
      multiPlatform: true,
      staticAnalysis: true,
      testGeneration: false,
      ideIntegration: true,
      ticketing: true,
      complianceCert: true,
    },
  },
  {
    slug: "sonarqube",
    name: "SonarQube",
    tagline: "Enterprise SAST & code-quality platform; AI review via the separate Gitar product",
    pricing:
      "Cloud: Free (≤50k LOC); Team from $34/mo; Enterprise custom (by LOC/yr). Server (self-hosted): Community Build free + Developer/Enterprise/Data Center (by LOC/yr). Gitar AI review: $20–$40/user/mo; Enterprise custom",
    strengths: [
      "Proprietary bundled SAST across 30+ languages (40+ on Enterprise) with taint / data-flow analysis, plus an Advanced Security add-on (SCA, malicious packages, SBOM)",
      "Self-hosted SonarQube Server (free Community Build; Docker / Kubernetes) alongside SonarQube Cloud",
      "Covers GitHub, GitLab, Bitbucket, and Azure DevOps — Gitar also supports self-managed Azure DevOps Server and Bitbucket Data Center",
      "Architecture management (GA): intended architecture defined as code with automated drift detection; Gitar posts walkthroughs and architecture diagrams on PRs",
      "Gitar AI Code Review learns team conventions from review feedback, commits fixes, and iterates until CI passes (separate SKU from $20/user/mo)",
      "AI CodeFix suggestions (Sonar-hosted or BYO Azure OpenAI), AI Code Assurance gates for AI-generated code, MCP server + CLI",
      "Gitar integrates Jira / Linear / Plane / YouTrack — creates follow-up issues and validates PRs against linked tickets; SOC 2 Type II + ISO 27001",
    ],
    weaknesses: [
      "The AI reviewer is a separate paid product (Gitar, from $20/user/mo), not part of SonarQube — full coverage means two products and two bills",
      "No failure-scenario simulation — Gitar diagnoses real CI failures rather than simulating a change's failure modes",
      "No computed per-PR review contract — Gitar's Focused/Thorough depth is an org/repo setting, not auto-routed per PR",
      "No unit-test generation",
    ],
    argusAdvantage:
      "Sonar is now a two-product stack: SonarQube for deterministic SAST, quality gates, and architecture-as-code drift detection, and Gitar (separate SKU, from $20/user/mo) for AI-native review that learns team conventions, posts diagrams, files Jira/Linear follow-ups, and iterates fixes until CI passes — plus self-hosting, BYOK, four platforms, and compliance Argus doesn't have. That stack now overlaps most of Argus's list. Argus's distinct bets: a computed per-PR review contract that auto-routes depth (Gitar's Focused/Thorough is a manual org/repo setting), failure-scenario simulation (Gitar diagnoses real CI failures rather than simulating a change), judge-scored findings with a hard comment cap, a Glass Box footer, and free open-source self-hosting versus enterprise-gated tiers. Pick the Sonar stack for enterprise SAST + governance + an AI reviewer that auto-fixes CI; pick Argus for depth-routed reasoning review in one self-hostable, open-source tool.",
    summary:
      "SonarQube remains the enterprise SAST and code-quality standard — 30+/40+ languages with taint analysis, a free self-hosted Community Build, four Git platforms, IDE extensions, architecture management with drift detection, and SOC 2 + ISO 27001. Its AI review story is now a separate product: Gitar (from $20/user/mo) learns team conventions from review feedback, posts walkthroughs and diagrams on PRs, files Jira/Linear follow-ups, and commits fixes until CI passes. Neither layer does failure-scenario simulation, test generation, or computed per-PR depth routing.",
    stat: {
      claim: "7 million+ developers use Sonar; it's trusted across 75% of the Fortune 100",
      source: "sonarsource.com, 2026",
    },
    updatedAt: "2026-09-20",
    features: {
      memory: true,
      multiPass: true,
      diagramGeneration: true,
      codeSimulation: false,
      architectureAnalysis: true,
      byok: true,
      patternLearning: true,
      reviewContract: false,
      selfHosted: true,
      multiPlatform: true,
      staticAnalysis: true,
      testGeneration: false,
      ideIntegration: true,
      ticketing: true,
      complianceCert: true,
    },
  },
  {
    slug: "github-copilot",
    name: "GitHub Copilot (code review)",
    tagline: "Native GitHub review from inside the tool developers already use",
    pricing:
      "Included in Copilot: Pro $10/mo, Pro+ $39/mo, Max $100/mo, Business $19/user/mo, Enterprise $39/user/mo; reviews also consume AI Credits (~$0.05–$5 each) + GitHub Actions minutes",
    strengths: [
      "Native to GitHub and the IDEs developers already use (VS Code, Visual Studio, JetBrains, Xcode)",
      "Zero-friction adoption for teams already on Copilot",
      "Ensemble-of-agents review with Lite / Balanced effort levels; auto-resolves its own comments once addressed",
      "Copilot Memory (public preview) persists repo-level facts across reviews, shared with the coding agent and CLI",
      "Deeply configurable — copilot-instructions.md, AGENTS.md, CLAUDE.md / REVIEW.md, path-specific rules, MCP servers, agent skills, custom runners + firewall",
      "SOC 2 Type I & II and ISO/IEC 27001 certification via the Copilot Trust Center",
    ],
    weaknesses: [
      "Metered per review — AI Credits plus GitHub Actions minutes on top of the seat price; the review model is auto-selected and undisclosed",
      "Memory stores repo facts with a 28-day expiry — it doesn't learn from your verdicts on past findings",
      "Depth is a manual Lite / Balanced dial — no computed per-PR review contract, diagrams, architecture graph, or failure-scenario simulation",
      "GitHub-only; no BYOK or self-hosting; no bundled SAST in the review itself (rules-based CodeQL lives in separate GitHub Code Quality), no test generation or ticket creation",
    ],
    argusAdvantage:
      "Copilot code review got meaningfully stronger this year: an ensemble of agents, repo-fact memory shared across Copilot agents, a Lite/Balanced depth dial, and comments that auto-resolve once addressed — plus zero-friction adoption and compliance Argus doesn't have. What's still missing is depth and control: the dial is a manual per-request or org/repo setting, not a computed per-PR contract; there's no failure-scenario simulation, diagrams, or architecture tracing; no BYOK or self-hosting (the model is auto-selected and undisclosed); and reviews are metered in AI Credits + Actions minutes on top of the seat. Argus is purpose-built for the review itself — auto depth routing, failure simulation, judge-scored findings with a hard cap, BYOK, self-hosting, and a Glass Box that shows its work. Choose Copilot for friction-free review inside GitHub; choose Argus when review depth, routing, and cost/model control are the point.",
    summary:
      "GitHub Copilot's code review is the frictionless, native option for teams already on Copilot — now an ensemble of agents with Lite/Balanced effort levels, repo-fact memory shared across Copilot agents (public preview), and comments that auto-resolve once addressed, on strong IDE reach and enterprise compliance. It's metered per review (AI Credits + Actions minutes), GitHub-only, and still lacks failure simulation, diagrams, architecture tracing, BYOK, self-hosting, and a computed per-PR depth contract.",
    stat: {
      claim: "Copilot code review's agent ensemble raised addressed high-severity comments ~47% while cutting review cost ~8%",
      source: "github.blog, 2026",
    },
    updatedAt: "2026-09-20",
    features: {
      memory: true,
      multiPass: true,
      diagramGeneration: false,
      codeSimulation: false,
      architectureAnalysis: false,
      byok: false,
      patternLearning: true,
      reviewContract: false,
      selfHosted: false,
      multiPlatform: false,
      staticAnalysis: false,
      testGeneration: false,
      ideIntegration: true,
      ticketing: false,
      complianceCert: true,
    },
  },
];

export function getCompetitor(slug: string): Competitor | undefined {
  return competitors.find((c) => c.slug === slug);
}

export const competitorSlugs = competitors.map((c) => c.slug);
