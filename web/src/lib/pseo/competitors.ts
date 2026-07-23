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
    pricing: "Free (public repos); Pro $24/mo/user; Pro Plus $48/mo/user; Enterprise custom (self-hosted)",
    strengths: [
      "Reviews on GitHub, GitLab, Azure DevOps, and Bitbucket (Cloud + Data Center)",
      "Bundles 40+ linters and SAST/security scanners into every review",
      "Unit-test generation and test-coverage gap detection",
      "Learnings engine — feedback on past reviews persists per org and tailors future ones",
      "Creates and validates Jira / Linear / GitHub issues against a PR's acceptance criteria",
      "First-class VS Code / Cursor IDE extension plus CLI; SOC 2 Type II certified",
    ],
    weaknesses: [
      "No failure-scenario simulation (its pipeline ranks context and verifies comments, but doesn't reason about runtime failure modes)",
      "No computed per-PR review contract — depth is set through config/profiles, not auto-routed per PR",
      "BYOK and self-hosting are gated to the Enterprise tier",
    ],
    argusAdvantage:
      "CodeRabbit is the more mature, broadly integrated product — more platforms, bundled static analysis, test generation, ticketing, and a SOC 2 certification Argus does not have. Argus's narrower bets: a computed per-PR review contract that routes review depth automatically (no config or profiles to hand-tune), failure-scenario simulation, judge-scored findings with a hard comment cap on every tier (no minimum-comment noise), BYOK in the standard managed tier rather than Enterprise-only, and a Glass Box footer that shows the contract, what was checked, and what team feedback suppressed. Pick CodeRabbit for breadth and ecosystem today; pick Argus if depth-routing and cost/model control matter more than integrations.",
    summary:
      "CodeRabbit is the most widely adopted AI code reviewer, running across GitHub, GitLab, Azure DevOps, and Bitbucket with 40+ bundled scanners, test generation, ticket integration, and a Learnings engine that remembers team feedback. It's the breadth leader. It doesn't do failure-scenario simulation or automatic per-PR depth routing, and reserves BYOK/self-hosting for Enterprise.",
    stat: {
      claim: "CodeRabbit bundles 40+ linters and security scanners into each review and is SOC 2 Type II certified",
      source: "coderabbit.ai, 2026",
    },
    updatedAt: "2026-07-22",
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
    pricing: "$30/seat/mo (50 reviews/seat, then $1/review); Enterprise / self-hosted custom",
    strengths: [
      "TREX spins up a disposable sandbox, runs the PR, and catches bugs that only surface at runtime",
      "Full-repo graph of functions, classes, and dependencies for cross-file impact analysis",
      "Learns from team comments, replies, and reactions; auto-indexes existing rule files",
      "Auto sequence diagrams on every non-trivial PR",
      "Reviews GitHub and GitLab (Cloud + Self-Managed); genuine air-gapped self-hosting",
      "BYO-LLM (own base URL + keys), Jira/Linear ticket context, SOC 2 Type II",
    ],
    weaknesses: [
      "No bundled static-analysis/SAST engine — positions against linters and pairs with Semgrep instead",
      "No first-class IDE extension",
      "No computed per-PR review contract (uses directory-scoped config + TREX heuristics)",
      "Usage-based pricing adds up beyond 50 reviews/seat",
    ],
    argusAdvantage:
      "Greptile is the closest peer — and on failure simulation it arguably goes further: TREX executes the PR in a sandbox, where Argus reasons about failure scenarios rather than running them. Greptile also matches Argus on memory, architecture tracing, diagrams, BYOK, and self-hosting, and adds GitLab support and SOC 2. Argus's remaining distinct bets are the computed per-PR review contract (auto depth routing — Greptile has no equivalent), judge-scored findings with a hard comment cap, and the Glass Box footer. This is a genuine toss-up; choose on whether you want execution-based simulation (Greptile) or contract-driven depth routing and cost control (Argus).",
    summary:
      "Greptile delivers one of the strongest context-aware reviews on the market: a full-repo dependency graph, auto sequence diagrams, memory from team feedback, GitLab support, air-gapped self-hosting, and TREX — a sandbox that actually runs your PR to catch runtime bugs. It doesn't bundle static analysis or ship an IDE extension, and has no automatic per-PR depth-routing contract.",
    stat: {
      claim: "Greptile's TREX runs the PR in a sandbox and catches ~20% more bugs than review alone",
      source: "greptile.com/trex, 2026",
    },
    updatedAt: "2026-07-22",
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
      ticketing: false,
      complianceCert: true,
    },
  },
  {
    slug: "cubic",
    name: "Cubic",
    tagline: "Micro-agent code review with continuous whole-codebase scanning",
    pricing: "Free (20 reviews/mo, free for OSS); Team $30/dev/mo; Pro $79/dev/mo; Enterprise custom",
    strengths: [
      "Micro-agent pipeline (Planner, Security, Duplication, Editorial) with a sub-1% false-positive rate",
      "Continuous whole-codebase scanning beyond open PR diffs",
      "Self-learns from maintainer feedback and adapts to project conventions",
      "Cross-file architecture and dependency reasoning",
      "MCP server exposes reviews to Cursor / VS Code / Claude Code / CLI agents",
      "BYO LLM keys (Enterprise); SOC 2 Type I; free for open source",
    ],
    weaknesses: [
      "GitHub-only — no GitLab, Bitbucket, or Azure DevOps",
      "No self-hosted / on-prem deployment (SaaS only)",
      "No failure-scenario simulation, no bundled SAST, no test generation",
      "Integrations pull ticket context into reviews but don't create or verify tickets; no native IDE extension",
    ],
    argusAdvantage:
      "Cubic and Argus are close on philosophy — specialized agents, low false positives, self-learning, architecture reasoning, BYOK. Argus's edges: failure-scenario simulation, a computed per-PR review contract that routes depth automatically, self-hosting (Cubic is SaaS-only), and the Glass Box footer. Cubic's edges over Argus: continuous whole-codebase scanning beyond the diff, an MCP server for agent access, and a SOC 2 Type I report. Both are strong, GitHub-centric choices; deployment constraints (self-host vs SaaS) and whether you want scenario simulation are the deciding factors.",
    summary:
      "Cubic is a feature-dense, GitHub-only reviewer: a micro-agent pipeline with a sub-1% false-positive rate, continuous codebase scanning beyond PR diffs, self-learning from maintainer feedback, BYOK on Enterprise, and an MCP server for coding agents. It's SaaS-only and doesn't do failure simulation, static analysis, test generation, or ticket creation.",
    stat: {
      claim: "Teams merge pull requests ~28% faster with Cubic, at a sub-1% false-positive rate",
      source: "cubic.dev, 2026",
    },
    updatedAt: "2026-07-22",
    features: {
      memory: true,
      multiPass: true,
      diagramGeneration: false,
      codeSimulation: false,
      architectureAnalysis: true,
      byok: true,
      patternLearning: true,
      reviewContract: false,
      selfHosted: false,
      multiPlatform: false,
      staticAnalysis: false,
      testGeneration: false,
      ideIntegration: false,
      ticketing: false,
      complianceCert: true,
    },
  },
  {
    slug: "sourcery",
    name: "Sourcery",
    tagline: "Instant PR reviews with diagrams, IDE scanning, and BYOK",
    pricing: "Free (OSS); Pro $12/seat/mo; Team $24/seat/mo (BYO LLM); Enterprise (self-hosted)",
    strengths: [
      "Instant PR summaries, sequence diagrams, and line-by-line feedback",
      "First-class IDE extensions — VS Code, JetBrains, and Cursor",
      "Reviews GitHub and GitLab",
      "Bundled continuous security / vulnerability scanning",
      "Bring-your-own-LLM endpoints and self-hosted Enterprise deployment",
      "Jira ticket integration with PR-addresses-ticket checks; SOC 2 Type I",
    ],
    weaknesses: [
      "No institutional memory or pattern learning — doesn't learn from past review feedback",
      "Single-pass review; no multi-agent pipeline",
      "No architecture / dependency-graph analysis beyond the diff",
      "No failure-scenario simulation, no test generation, no per-PR review contract",
    ],
    argusAdvantage:
      "Sourcery is fast and IDE-native, with diagrams, BYOK, GitLab support, bundled security scanning, and an IDE extension Argus lacks. But it's largely stateless: no memory, no pattern learning, no architecture graph, no scenario simulation. Argus's differentiators are exactly those — memory that learns from your team's 👍/👎 and replies (dismissed patterns suppressed semantically, security findings never), architecture tracing, failure-scenario simulation, and a computed per-PR review contract. Sourcery is quicker to feel useful; Argus gets sharper the more your team uses it and resolves its own fixed comments on re-review instead of re-flagging.",
    summary:
      "Sourcery delivers instant PR summaries, diagrams, and inline feedback with strong IDE extensions, GitLab support, bundled security scanning, and BYOK. It's a capable, fast reviewer — but a largely stateless one: no institutional memory, pattern learning, architecture graph, or scenario simulation.",
    stat: {
      claim: "Sourcery ships first-class IDE extensions (VS Code, JetBrains, Cursor) alongside PR review, from $12/seat/mo",
      source: "sourcery.ai, 2026",
    },
    updatedAt: "2026-07-22",
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
    tagline: "Quality-first AI platform spanning review, tests, and IDE",
    pricing: "Pro Team $30/user/mo; Enterprise custom (on-prem / air-gapped); open-source PR-Agent free",
    strengths: [
      "Generates unit tests for changed code, alongside review",
      "Creates and verifies Jira / Linear tickets from PRs (PR-to-Ticket + compliance checks)",
      "First-class VS Code and JetBrains IDE extensions",
      "Reviews GitHub, GitLab, Bitbucket, and Azure DevOps",
      "Self-hosted / air-gapped deployment; BYO model via LiteLLM / Bedrock / Vertex",
      "Auto best-practices memory; open-source PR-Agent core; SOC 2 compliant",
    ],
    weaknesses: [
      "No failure-scenario simulation",
      "No bundled SAST engine (security review is LLM + compliance checks, not third-party scanners)",
      "No computed per-PR review contract (rules + agents, not automatic depth routing)",
    ],
    argusAdvantage:
      "Qodo is a broad quality platform — review plus test generation, ticketing, IDE extensions, four Git platforms, and self-hosting, much of which Argus doesn't offer. Argus is narrower and review-focused: its edges are failure-scenario simulation, the computed per-PR review contract (auto depth routing), judge-scored findings with a hard comment cap, and the Glass Box footer. If you want one tool that also writes tests and files tickets, Qodo is broader; if you want the deepest single-PR review with depth routing and cost control, Argus is the tighter pick.",
    summary:
      "Qodo is a quality-first platform that spans review, unit-test generation, and IDE assistance across GitHub, GitLab, Bitbucket, and Azure DevOps, with self-hosting, BYOK, ticketing, and an open-source core. It's broad. It doesn't do failure-scenario simulation, bundle a SAST engine, or auto-route review depth per PR.",
    stat: {
      claim: "Qodo's auto best-practices raised suggestion acceptance rates by nearly 50% in pre-release testing",
      source: "qodo.ai, 2026",
    },
    updatedAt: "2026-07-22",
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
    pricing: "Free (≤10 contributors); Teams from $30/contributor/mo per module; Enterprise (on-prem)",
    strengths: [
      "Bundled proprietary SAST, supply-chain (SCA), and secrets scanning — deterministic plus AI detection",
      "Reviews GitHub, GitLab, Bitbucket, and Azure DevOps",
      "Assistant layer adds AI triage, autofix, and business-logic (IDOR/authz) detection",
      "Auto-generates custom rules from natural language + code examples",
      "Self-hosted / on-prem (CLI runs locally; Enterprise on-prem SCM)",
      "First-class VS Code and JetBrains extensions; Jira/Linear/Asana tickets; SOC 2 Type II",
    ],
    weaknesses: [
      "Not a general LLM code reviewer — no multi-pass review pipeline, diagrams, or architecture graph",
      "No institutional memory of team review decisions, no failure-scenario simulation",
      "No BYOK — the Assistant layer uses Semgrep-hosted models",
      "No test generation, no per-PR review contract",
    ],
    argusAdvantage:
      "Semgrep and Argus solve adjacent problems: Semgrep is a best-in-class static-analysis/security platform with an AI triage layer; Argus is a general senior-engineer reviewer. Semgrep wins decisively on SAST/SCA/secrets, platform breadth, IDE, ticketing, and compliance — none of which Argus does. Argus wins on the review itself: institutional memory, multi-pass reasoning, architecture tracing, failure-scenario simulation, diagrams, BYOK, and the computed review contract. Many teams run both — Semgrep for deterministic security scanning, Argus for judgment-heavy review.",
    summary:
      "Semgrep is fundamentally a SAST/SCA/secrets platform — bundled scanners across four Git platforms with an AI Assistant that triages findings, autofixes, and flags business-logic flaws. It's the security-scanning leader here, but it isn't a general reasoning-based reviewer: no memory, multi-pass pipeline, architecture graph, simulation, or BYOK.",
    stat: {
      claim: "Semgrep Assistant cuts findings needing manual triage by ~20% on day one, up to ~40% after a week",
      source: "docs.semgrep.dev, 2026",
    },
    updatedAt: "2026-07-22",
    features: {
      memory: true,
      multiPass: false,
      diagramGeneration: false,
      codeSimulation: false,
      architectureAnalysis: false,
      byok: false,
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
    pricing: "Free Developer tier; Team from $18/dev/mo (free for OSS); Business/Enterprise (self-hosted)",
    strengths: [
      "Bundled SAST / SCA / IaC + secrets scanning integrated into PR reviews",
      "Reviews GitHub, GitLab, and Bitbucket",
      "First-class VS Code and IntelliJ IDE extensions",
      "Self-hosted / on-prem via Kubernetes / Helm",
      "SOC 2 Type II and ISO 27001 certified; free tier with IDE plugin",
    ],
    weaknesses: [
      "AI reviewer is steered by a manual review.md file, not learned team feedback — no institutional memory",
      "No multi-agent pipeline, architecture graph, diagrams, or failure-scenario simulation",
      "No BYOK, no pattern learning, no test generation, no per-PR review contract, no ticket creation",
    ],
    argusAdvantage:
      "Codacy is a quality/security-gate platform (SAST/SCA/IaC/secrets) that added an AI reviewer for low-noise PR feedback. It leads Argus on bundled scanning, platform breadth, IDE extensions, self-hosting, and compliance. Argus leads on the review's intelligence: institutional memory that learns from feedback, multi-pass reasoning, architecture tracing, failure-scenario simulation, BYOK, and the computed review contract. Codacy is the pick for a compliance-grade quality gate; Argus for a reviewer that reasons and remembers.",
    summary:
      "Codacy is primarily a code-quality and security platform — bundled SAST/SCA/IaC/secrets scanning across GitHub, GitLab, and Bitbucket, with IDE plugins, self-hosting, and SOC 2 + ISO 27001. Its newer AI reviewer adds low-noise PR feedback but is configuration-steered, not learned: no memory, multi-pass reasoning, architecture graph, simulation, or BYOK.",
    stat: {
      claim: "Codacy is SOC 2 Type II compliant, verified by an independent third-party audit",
      source: "blog.codacy.com/codacy-soc2-type-ii, 2026",
    },
    updatedAt: "2026-07-22",
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
      ticketing: false,
      complianceCert: true,
    },
  },
  {
    slug: "sonarqube",
    name: "SonarQube",
    tagline: "Enterprise SAST & code-quality platform, now with AI review",
    pricing: "Cloud: Free (≤50k LOC); Team from $34/mo (by LOC). Server (self-managed): Community free + paid editions",
    strengths: [
      "Proprietary bundled SAST across 40+ languages with taint / data-flow analysis",
      "Self-hosted SonarQube Server (Docker / Kubernetes) plus Cloud",
      "Reviews GitHub, GitLab, Bitbucket, and Azure DevOps",
      "Architecture & dependency analysis with drift detection (via the Gitar acquisition)",
      "Self-hosted / BYO LLM (Azure OpenAI, AWS Bedrock); VS Code + JetBrains extensions",
      "SOC 2 Type II and ISO 27001:2022; used across 75% of the Fortune 100",
    ],
    weaknesses: [
      "AI review is newer (Sonar Review is alpha) — depth of LLM reasoning trails review-first tools",
      "No institutional memory that learns from team review decisions across PRs",
      "No failure-scenario simulation, no pattern learning from history, no test generation, no ticket creation",
      "No computed per-PR review contract",
    ],
    argusAdvantage:
      "SonarQube is the enterprise SAST/quality incumbent — deep static analysis, self-hosting, four platforms, IDE, and compliance at Fortune-100 scale, all beyond Argus's current reach. Its AI review layer is newer and rule/analysis-driven rather than memory-driven. Argus's edges are the review's judgment: institutional memory, failure-scenario simulation, a computed per-PR review contract, judge-scored findings with a hard comment cap, and the Glass Box. SonarQube is the pick for enterprise static analysis and governance; Argus for a reasoning reviewer that compounds over time.",
    summary:
      "SonarQube (Sonar) is the enterprise SAST and code-quality standard — 40+ languages with taint analysis, self-hosted Server, four Git platforms, IDE extensions, and top-tier compliance. Its AI review (Sonar Review, alpha, plus the Gitar acquisition) is promising but young, and it lacks learned institutional memory, failure simulation, and per-PR depth routing.",
    stat: {
      claim: "7 million+ developers use Sonar; it's trusted across 75% of the Fortune 100",
      source: "sonarsource.com, 2026",
    },
    updatedAt: "2026-07-22",
    features: {
      memory: false,
      multiPass: true,
      diagramGeneration: true,
      codeSimulation: false,
      architectureAnalysis: true,
      byok: true,
      patternLearning: false,
      reviewContract: false,
      selfHosted: true,
      multiPlatform: true,
      staticAnalysis: true,
      testGeneration: false,
      ideIntegration: true,
      ticketing: false,
      complianceCert: true,
    },
  },
  {
    slug: "github-copilot",
    name: "GitHub Copilot (code review)",
    tagline: "Native GitHub review from inside the tool developers already use",
    pricing: "Included in Copilot: Pro $10/mo, Business $19/user/mo, Enterprise $39/user/mo",
    strengths: [
      "Native to GitHub and the IDEs developers already use (VS Code, Visual Studio, JetBrains, Xcode)",
      "Zero-friction adoption for teams already on Copilot",
      "Configurable/customizable review guidance (2026 updates)",
      "SOC 2 Type I & II and ISO/IEC 27001 certification via the Copilot Trust Center",
    ],
    weaknesses: [
      "Single agentic pass — no multi-agent pipeline, diagrams, or architecture-graph analysis",
      "No institutional memory of team decisions (only thumbs up/down to improve the product)",
      "No failure-scenario simulation, pattern learning, BYOK, self-hosting, or per-PR review contract",
      "GitHub-only; no test generation or ticket creation in review",
    ],
    argusAdvantage:
      "Copilot's code review is the convenient default if you're already in the GitHub/Copilot ecosystem, and it carries enterprise compliance Argus doesn't. But as a reviewer it's comparatively thin: a single pass, no memory, no architecture graph, no simulation, no BYOK. Argus is purpose-built for the review itself — memory that learns, multi-pass reasoning, architecture tracing, failure-scenario simulation, a computed review contract, and a Glass Box that shows its work. Choose Copilot for zero-friction convenience; choose Argus when the quality and accountability of the review is the point.",
    summary:
      "GitHub Copilot's code review is the frictionless, native option for teams already on Copilot, with strong IDE reach and enterprise compliance. As a reviewer it's a single agentic pass, though — no institutional memory, multi-agent pipeline, architecture graph, failure simulation, BYOK, or self-hosting.",
    stat: {
      claim: "Copilot Business/Enterprise hold SOC 2 (Type I & II) and ISO/IEC 27001 certification",
      source: "github.blog, 2026",
    },
    updatedAt: "2026-07-22",
    features: {
      memory: false,
      multiPass: false,
      diagramGeneration: false,
      codeSimulation: false,
      architectureAnalysis: false,
      byok: false,
      patternLearning: false,
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
