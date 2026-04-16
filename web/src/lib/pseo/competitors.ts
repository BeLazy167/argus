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
    selfHosted: boolean;
  };
}

export const featureLabels: Record<keyof Competitor["features"], string> = {
  memory: "Institutional memory across reviews",
  multiPass: "Multi-pass review pipeline",
  diagramGeneration: "PR diagram generation (sequence + data flow)",
  codeSimulation: "Failure scenario simulation",
  architectureAnalysis: "Architecture & dependency tracing",
  byok: "Bring your own LLM key",
  patternLearning: "Pattern learning from codebase history",
  selfHosted: "Self-hosted deployment",
};

export const argusFeatures: Competitor["features"] = {
  memory: true,
  multiPass: true,
  diagramGeneration: true,
  codeSimulation: true,
  architectureAnalysis: true,
  byok: true,
  patternLearning: true,
  selfHosted: false,
};

export const competitors: Competitor[] = [
  {
    slug: "coderabbit",
    name: "CodeRabbit",
    tagline: "AI code review bot for GitHub, GitLab, Azure DevOps & Bitbucket",
    pricing: "$12–$25/mo per user",
    strengths: [
      "Most-installed AI review app on GitHub — 2M+ repos, 9000+ orgs",
      "Agentic workflows: generates tests, docs, and Jira/Linear issues from reviews",
      "Integrated static analysis (ESLint, Ruff, golangci-lint, TruffleHog, Trivy)",
      "Learnings engine — team feedback on past reviews persists and tailors future ones",
      "Issue Planner generates coding plans from tickets before code is written",
      "SOC 2 Type II certified, code never used for training",
    ],
    weaknesses: [
      "No failure scenario simulation",
      "No BYOK — locked to CodeRabbit's managed LLM infrastructure",
      "No self-hosted deployment",
      "Architecture tracing is partial (diagrams exist; no first-class dependency graph)",
    ],
    argusAdvantage:
      "CodeRabbit is broadly integrated with a large install base and its Learnings engine learns from team feedback — similar in spirit to Argus's memory. Argus's distinct bet is failure scenario simulation (predicting what breaks against known scenarios before merge) plus BYOK so you control cost and data path. Argus is newer; CodeRabbit wins on scale and polish today.",
    summary:
      "CodeRabbit is the most widely adopted AI code review tool, processing 13M+ PRs across GitHub, GitLab, Azure DevOps, and Bitbucket. Its Learnings engine gives it genuine memory across reviews — when you correct or dismiss a finding, that preference persists. Strong on breadth, speed, and ecosystem integrations; doesn't offer BYOK, scenario simulation, or self-hosting.",
    stat: {
      claim: "CodeRabbit has processed over 13 million pull requests across 2 million repositories",
      source: "CodeRabbit official website, 2026",
    },
    updatedAt: "2026-04-16",
    features: {
      memory: true,
      multiPass: false,
      diagramGeneration: true,
      codeSimulation: false,
      architectureAnalysis: false,
      byok: false,
      patternLearning: true,
      selfHosted: false,
    },
  },
  {
    slug: "codacy",
    name: "Codacy",
    tagline: "AI code governance and quality platform",
    pricing: "Free (2 committers) – $15–$25/mo per committer",
    strengths: [
      "49 languages supported with static analysis",
      "AI Guardrails IDE extension scans AI-generated code in real-time",
      "AI Reviewer combines rule-based + AI reasoning on PRs",
      "AI Risk Hub dashboard for tracking AI code risk across teams",
      "Quality gates and CI/CD enforcement",
    ],
    weaknesses: [
      "Primarily rule-based foundation — AI features are recent additions",
      "No institutional memory or cross-review learning",
      "No failure scenario simulation",
      "No multi-stage review pipeline",
    ],
    argusAdvantage:
      "Codacy applies static rules augmented with recent AI features. Argus is AI-native — multi-stage review, institutional memory that learns your codebase's unique patterns, and failure simulation. Rules can't ask 'why did this change break that module?' Argus can.",
    summary:
      "Codacy is a mature code quality platform that has pivoted toward AI code governance in 2026. It now offers AI Guardrails (IDE scanning), AI Reviewer (hybrid static + AI PR review), and an AI Risk Hub for organizational oversight. Strong for teams managing AI-generated code quality at scale, but lacks the multi-angle depth and institutional memory of purpose-built AI review tools.",
    stat: {
      claim: "Development teams now generate 30-70% of code through AI assistants, driving Codacy's pivot to AI governance",
      source: "Codacy product announcement, 2026",
    },
    updatedAt: "2026-04-16",
    features: {
      memory: false,
      multiPass: false,
      diagramGeneration: false,
      codeSimulation: false,
      architectureAnalysis: false,
      byok: false,
      patternLearning: false,
      selfHosted: true,
    },
  },
  {
    slug: "sonarqube",
    name: "SonarQube",
    tagline: "Code quality and security analysis platform (SAST + SCA)",
    pricing: "Free (Community) – $2.5k–$100k+/yr (self-hosted)",
    strengths: [
      "Industry-standard SAST with deep security vulnerability detection",
      "AI Code Assurance detects AI-generated code and applies specialized taint analysis",
      "AI CodeFix generates suggested fixes using Claude Sonnet or GPT-5.1",
      "Architecture-as-Code: blueprint visualization + deviation detection (2026)",
      "Quality gate enforcement in CI/CD pipelines",
      "Self-hosted deployment option for compliance requirements",
    ],
    weaknesses: [
      "Static analysis core — AI layer is supplementary",
      "Heavy infrastructure for self-hosted deployments",
      "No institutional memory or learning from past reviews",
      "No BYOK — AI features use Sonar's managed Anthropic/OpenAI access",
    ],
    argusAdvantage:
      "SonarQube is a compliance-grade SAST platform with real Architecture-as-Code visualization in 2026. Argus is AI-native: it learns from your team's review feedback, runs failure scenario simulations, and lets you bring your own LLM key. Best used together — SonarQube for deterministic gates, Argus for contextual intelligent review.",
    summary:
      "SonarQube is the industry standard for static application security testing (SAST) and code quality gates. In 2026, it added AI Code Assurance, AI CodeFix, and Architecture-as-Code visualization. Strong on compliance and on-prem deployment; doesn't learn from review history or offer BYOK.",
    stat: {
      claim: "SonarQube Cloud now includes AI-generated code detection, AI CodeFix, and Architecture-as-Code blueprint visualization",
      source: "Sonar product page, 2026",
    },
    updatedAt: "2026-04-16",
    features: {
      memory: false,
      multiPass: false,
      diagramGeneration: false,
      codeSimulation: false,
      architectureAnalysis: true,
      byok: false,
      patternLearning: false,
      selfHosted: true,
    },
  },
  {
    slug: "github-copilot",
    name: "GitHub Copilot",
    tagline: "AI pair programmer with built-in code review",
    pricing: "Free – $10–$39/mo per user",
    strengths: [
      "Native GitHub integration — code review runs on every PR, no install",
      "60M+ reviews processed, actionable feedback in 71% of cases",
      "Agentic architecture (March 2026) traces cross-file dependencies before commenting",
      "Copilot Memory (public preview) persists repo-learned details across reviews",
      "Mermaid diagram generation via Ask/Plan/Agent modes",
      "Custom instructions via `copilot-instructions.md` (org + repo scopes)",
      "Free tier available for individual developers",
    ],
    weaknesses: [
      "Code review is one feature among many — not the primary focus",
      "No BYOK — tied to GitHub's managed Copilot LLM infrastructure",
      "No failure scenario simulation",
      "No self-hosted option",
      "Review depth throttled by premium request credits on lower tiers",
    ],
    argusAdvantage:
      "Copilot is deeply integrated into GitHub and genuinely improved in 2026 — agentic cross-file analysis, memory, diagrams. Argus's remaining edges: failure scenario simulation, BYOK so you control model and cost, and first-class scenario memory (not just static repo knowledge). Worth using together — Copilot for its GitHub-native surface, Argus when you need deeper review on critical paths.",
    summary:
      "GitHub Copilot expanded from code completion to code review, then to an agentic multi-angle reviewer (March 2026) with cross-file dependency tracing and persistent memory. 60M+ reviews processed, actionable feedback in 71% of cases. Strong on integration and context; lacks BYOK, self-hosting, and failure scenario simulation.",
    stat: {
      claim: "Copilot code review reached 60M reviews by March 2026, with actionable feedback in 71% of cases",
      source: "GitHub Copilot official stats, March 2026",
    },
    updatedAt: "2026-04-16",
    features: {
      memory: true,
      multiPass: false,
      diagramGeneration: true,
      codeSimulation: false,
      architectureAnalysis: true,
      byok: false,
      patternLearning: false,
      selfHosted: false,
    },
  },
  {
    slug: "sourcery",
    name: "Sourcery",
    tagline: "AI code review with PR summaries, diagrams & inline feedback",
    pricing: "Free (OSS) – $10–$24/mo per user",
    strengths: [
      "Instant PR reviews with summaries and sequence diagrams",
      "Multi-angle review via a series of AI reviewers with different specialities",
      "BYOK — bring your own LLM endpoints (enterprise-friendly)",
      "Real-time IDE scanning alongside PR review",
      "Used by 300,000+ developers at HelloFresh, Cisco, Red Hat",
      "Interactive PR commands (`@sourcery-ai guide`, `@sourcery-ai resolve`)",
    ],
    weaknesses: [
      "No institutional memory — doesn't learn from past review feedback",
      "No pattern learning from review history",
      "No failure scenario simulation",
      "No explicit architecture / dependency graph analysis",
      "No self-hosted option",
    ],
    argusAdvantage:
      "Sourcery is the closest match on several features — multi-angle review, diagrams, BYOK. Argus differentiates on institutional memory that actually learns from your team's 👍/👎 and replies, failure scenario simulation, and first-class pattern learning. Sourcery is faster today; Argus gets sharper the more your team uses it.",
    summary:
      "Sourcery delivers instant PR summaries, diagrams, and multi-angle review with bring-your-own-LLM support. Used by 300k+ developers at HelloFresh, Cisco, Red Hat. Competitive on review depth and BYOK; doesn't yet learn from review history or run failure scenario simulations.",
    stat: {
      claim: "Sourcery is used by over 300,000 developers at companies including HelloFresh, Cisco, and Red Hat",
      source: "Sourcery official website, 2026",
    },
    updatedAt: "2026-04-16",
    features: {
      memory: false,
      multiPass: true,
      diagramGeneration: true,
      codeSimulation: false,
      architectureAnalysis: false,
      byok: true,
      patternLearning: false,
      selfHosted: false,
    },
  },
  {
    slug: "qodo",
    name: "Qodo (formerly CodiumAI)",
    tagline: "AI code quality platform — review, testing, and CLI agents",
    pricing: "Free (30 reviews/mo) – $30–$38/mo per user",
    strengths: [
      "Multi-agent review architecture — highest F1 score (60.1%) in benchmarks",
      "PR-Agent is open-source — self-host + own OpenAI/Anthropic key supported",
      "Catches architecture-level issues, multi-repo dependency conflicts, breaking changes",
      "Combined platform: PR review (Merge), test generation (Gen), CLI agents (Command)",
      "IDE integration for VS Code and JetBrains with AI-powered test generation",
      "Credit-based system supporting Claude Opus, Grok, and other models",
      "30 free PR reviews per month on free tier",
    ],
    weaknesses: [
      "Credit system can get expensive with premium models (5 credits for Opus)",
      "BYOK is via self-hosted PR-Agent, not the managed tier",
      "No first-class institutional memory across reviews",
      "No diagrams or failure scenario simulation",
    ],
    argusAdvantage:
      "Qodo is one of the strongest AI review tools today — multi-agent, architecture-aware, open-source core. Argus's distinct bets: first-class memory system (learns from every 👍/👎 and reply, not just codebase context), failure scenario simulation, and BYOK in the managed tier (Qodo requires self-hosting PR-Agent for BYOK).",
    summary:
      "Qodo (CodiumAI) is a multi-product AI code quality platform combining PR review (Merge), test generation (Gen), and CLI agents (Command). Its 2026 multi-agent architecture led benchmarks at 60.1% F1. PR-Agent is open-source with full self-hosted + BYOK support. Strong all-rounder; doesn't yet do scenario simulation or persistent review memory.",
    stat: {
      claim: "Qodo 2.0's multi-agent review achieved the highest F1 score (60.1%) benchmarked against 7 leading tools",
      source: "Qodo 2.0 release announcement, February 2026",
    },
    updatedAt: "2026-04-16",
    features: {
      memory: false,
      multiPass: true,
      diagramGeneration: false,
      codeSimulation: false,
      architectureAnalysis: true,
      byok: true,
      patternLearning: false,
      selfHosted: true,
    },
  },
  {
    slug: "semgrep",
    name: "Semgrep",
    tagline: "SAST, SCA, and secrets detection platform with custom rules",
    pricing: "Free (10 contributors) – $35–$80/mo per contributor",
    strengths: [
      "20,000+ Pro rules with cross-file analysis",
      "Organizational Memory — learns from triage decisions, applies to future scans",
      "Custom rule authoring in familiar syntax",
      "Custom Workflows (2026) — multi-step pipelines mixing deterministic + AI steps",
      "Bundled SAST + SCA + secrets detection in one platform",
      "AI Assistant (Multimodal) for business-logic flaw triage",
      "Open-source engine self-hostable (Pro / AI features are cloud-only)",
    ],
    weaknesses: [
      "Fundamentally pattern-matching — rules can't reason about intent like a reviewer",
      "No PR diagrams or architecture visualization",
      "No BYOK — AI features locked to Semgrep's managed models",
      "No failure scenario simulation",
      "Fully self-hosted (AI + dashboard) requires Enterprise custom pricing",
    ],
    argusAdvantage:
      "Semgrep is a best-in-class SAST platform with genuine organizational memory that learns from triage. Argus's edge: failure scenario simulation, BYOK, and review-native workflow (inline PR comments with replies, reactions, diagrams) versus Semgrep's scan/finding-triage model. Use Semgrep for SAST gates, Argus for reviewer-style depth.",
    summary:
      "Semgrep is a SAST, SCA, and secrets detection platform with 20k+ rules, an open-source engine, Organizational Memory that learns from triage, and Custom Workflows for multi-step pipelines. Strong on compliance scanning and on-prem deployment. The AI features (Multimodal/Assistant) run only in the managed cloud, and there's no BYOK.",
    stat: {
      claim: "Semgrep's Team plan is free for up to 10 contributors, with 20,000+ Pro rules and cross-file analysis",
      source: "Semgrep pricing page, 2026",
    },
    updatedAt: "2026-04-16",
    features: {
      memory: true,
      multiPass: false,
      diagramGeneration: false,
      codeSimulation: false,
      architectureAnalysis: false,
      byok: false,
      patternLearning: true,
      selfHosted: true,
    },
  },
  {
    slug: "greptile",
    name: "Greptile",
    tagline: "AI code review with full codebase context",
    pricing: "$30/mo per seat (50 reviews included, $1/extra)",
    strengths: [
      "Full codebase context — graph index of functions, dependencies, ripple effects",
      "Memory & Learning — auto-learns from PR replies, 👍/👎, and custom rules",
      "Auto-generates custom rules from observed team patterns",
      "Auto sequence diagrams and call-flow visualization in PR reviews",
      "Docker/Kubernetes self-hosted deployment option",
      "Agent v4 with 74% improvement in actionable comments vs v3",
      "Slack bot for natural language codebase Q&A",
      "Raised $180M valuation — well-funded and rapidly improving",
    ],
    weaknesses: [
      "No failure scenario simulation",
      "No BYOK — locked to Greptile's managed models",
      "Usage-based pricing adds up ($1/review beyond 50/seat)",
    ],
    argusAdvantage:
      "Greptile is the closest peer on architecture + memory + diagrams — genuine, well-executed features. Argus's remaining differentiators: failure scenario simulation, BYOK so you control the LLM path, and an open pipeline model configurable per stage. Greptile is more mature; Argus gives you more control over cost and model choice.",
    summary:
      "Greptile delivers one of the strongest context-aware code reviews on the market — full-codebase graph index, auto-sequence diagrams, Memory & Learning from team feedback, and self-hosted deployment via Docker/Kubernetes. Agent v4 raised actionable comments per PR by 74%. Doesn't offer BYOK or failure scenario simulation.",
    stat: {
      claim: "Greptile Agent v4 increased actionable comments per PR from 0.92 to 1.60, a 74% improvement",
      source: "Greptile v4 blog post, 2026",
    },
    updatedAt: "2026-04-16",
    features: {
      memory: true,
      multiPass: false,
      diagramGeneration: true,
      codeSimulation: false,
      architectureAnalysis: true,
      byok: false,
      patternLearning: true,
      selfHosted: true,
    },
  },
  {
    slug: "cubic",
    name: "Cubic",
    tagline: "AI code review with architecture diagrams and custom rules",
    pricing: "$30/mo per developer (free for public repos)",
    strengths: [
      "Micro-agent architecture: Planner, Security, Duplication, Editorial agents (2026)",
      "51% false-positive reduction via specialized micro-agents",
      "Repository-wide analysis + continuous codebase scanning (beyond PR diffs)",
      "Self-learning from maintainer feedback, adapts to project conventions",
      "Auto-generated Mermaid architecture-change diagrams",
      "Custom policies in natural language, no complex rule syntax",
      "Jira, Linear, Asana integration for ticket verification",
      "Free for public repos",
    ],
    weaknesses: [
      "No failure scenario simulation",
      "No BYOK — locked to Cubic's managed LLM infrastructure",
      "No self-hosted option",
    ],
    argusAdvantage:
      "Cubic is a genuinely deep peer — micro-agent pipeline, continuous scanning, self-learning, architecture diagrams. Argus's remaining edges: failure scenario simulation (scenario-against-diff reasoning is a distinct bet), BYOK, and a free tier for private repos (Cubic is free only for public). Both are strong choices; pick based on deployment constraints and whether scenario simulation matters to your workflow.",
    summary:
      "Cubic is one of the most feature-complete AI reviewers in 2026 — micro-agent architecture (Planner, Security, Duplication, Editorial) with 51% FP reduction, continuous codebase scanning beyond PR diffs, self-learning from maintainer feedback, and auto Mermaid architecture diagrams. Free for public repos. Doesn't offer BYOK, self-hosting, or scenario simulation.",
    stat: {
      claim: "Cubic's micro-agent architecture achieved a 51% reduction in false positives without sacrificing recall",
      source: "Cubic architecture refinement case study, 2026",
    },
    updatedAt: "2026-04-16",
    features: {
      memory: true,
      multiPass: true,
      diagramGeneration: true,
      codeSimulation: false,
      architectureAnalysis: true,
      byok: false,
      patternLearning: true,
      selfHosted: false,
    },
  },
];

export function getCompetitor(slug: string): Competitor | undefined {
  return competitors.find((c) => c.slug === slug);
}

export const competitorSlugs = competitors.map((c) => c.slug);
