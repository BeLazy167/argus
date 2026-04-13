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
  multiPass: "Multi-pass specialist pipeline",
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
      "Issue Planner generates coding plans from tickets before code is written",
      "SOC 2 Type II certified, code never used for training",
    ],
    weaknesses: [
      "No institutional memory — reviews don't improve from past incidents",
      "No failure scenario simulation or code simulation",
      "No cross-PR dependency tracking between related changes",
      "Architecture diagrams limited compared to multi-specialist analysis",
    ],
    argusAdvantage:
      "CodeRabbit is fast and broadly integrated. Argus goes deeper — multi-pass specialist pipeline (bug hunter, security, architecture, regression), institutional memory that learns from every review, and failure simulation that predicts what breaks before it ships.",
    summary:
      "CodeRabbit is the most widely adopted AI code review tool, processing 13M+ PRs across GitHub, GitLab, Azure DevOps, and Bitbucket. It offers agentic workflows, integrated static analysis, and an Issue Planner for pre-coding specs. Strong for breadth and speed, but lacks the multi-specialist depth, institutional memory, and failure simulation of purpose-built review tools.",
    stat: {
      claim: "CodeRabbit has processed over 13 million pull requests across 2 million repositories",
      source: "CodeRabbit official website, 2026",
    },
    updatedAt: "2026-04-12",
    features: {
      memory: false,
      multiPass: false,
      diagramGeneration: true,
      codeSimulation: false,
      architectureAnalysis: false,
      byok: true,
      patternLearning: false,
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
      "No multi-pass specialist pipeline",
    ],
    argusAdvantage:
      "Codacy applies static rules augmented with recent AI features. Argus is AI-native — multi-pass specialist review, institutional memory that learns your codebase's unique patterns, and failure simulation. Rules can't ask 'why did this change break that module?' Argus can.",
    summary:
      "Codacy is a mature code quality platform that has pivoted toward AI code governance in 2026. It now offers AI Guardrails (IDE scanning), AI Reviewer (hybrid static + AI PR review), and an AI Risk Hub for organizational oversight. Strong for teams managing AI-generated code quality at scale, but lacks the multi-specialist depth and institutional memory of purpose-built AI review tools.",
    stat: {
      claim: "Development teams now generate 30-70% of code through AI assistants, driving Codacy's pivot to AI governance",
      source: "Codacy product announcement, 2026",
    },
    updatedAt: "2026-04-12",
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
      "Quality gate enforcement in CI/CD pipelines",
      "Self-hosted deployment option for compliance requirements",
      "Cloud plans starting at $32/mo with SCA integration",
    ],
    weaknesses: [
      "Static analysis core — AI features are supplementary, not primary",
      "Heavy infrastructure for self-hosted deployments",
      "No PR-contextual reasoning about code intent",
      "No institutional memory or learning from past reviews",
    ],
    argusAdvantage:
      "SonarQube finds known vulnerability patterns via static analysis. Argus finds novel risks by reasoning about code changes in context — tracing dependencies, simulating failures, and remembering past incidents. Complementary tools: use SonarQube for SAST gates, Argus for intelligent review.",
    summary:
      "SonarQube is the industry standard for static application security testing (SAST) and code quality gates. In 2026, it added AI Code Assurance to detect AI-generated code and apply specialized analysis. Excellent for enforcing quality standards and security compliance, but fundamentally a static analysis tool — it doesn't reason about intent, trace architectural dependencies, or learn from review history.",
    stat: {
      claim: "SonarQube Cloud now includes AI-generated code detection and specialized taint analysis for AI-written snippets",
      source: "Sonar product page, 2026",
    },
    updatedAt: "2026-04-12",
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
    slug: "github-copilot",
    name: "GitHub Copilot",
    tagline: "AI pair programmer with built-in code review",
    pricing: "Free – $10–$39/mo per user",
    strengths: [
      "Native GitHub integration — code review runs on every PR",
      "60M+ reviews processed, actionable feedback in 71% of cases",
      "Agentic code review architecture launched March 2026",
      "Code completion, chat, and agent mode in one tool",
      "Free tier available for individual developers",
    ],
    weaknesses: [
      "Code review is secondary to code generation — not its primary focus",
      "No institutional memory or cross-review learning",
      "No failure scenario simulation or architecture tracing",
      "Review depth limited by premium request credits",
    ],
    argusAdvantage:
      "Copilot now includes code review, but it's one feature among many. Argus is purpose-built for review depth — multi-pass specialist pipeline, institutional memory, failure simulation, and cross-PR dependency tracking. Copilot reviews at breadth; Argus reviews at depth. Use both together.",
    summary:
      "GitHub Copilot expanded from AI code completion to include code review in 2025-2026, processing 60M+ reviews with its agentic architecture. In 71% of reviews it surfaces actionable feedback. However, code review is one feature in a broader tool — it lacks the multi-specialist depth, institutional memory, and failure simulation of dedicated review tools.",
    stat: {
      claim: "Copilot code review reached 60M reviews by March 2026, with actionable feedback in 71% of cases",
      source: "GitHub Copilot official stats, March 2026",
    },
    updatedAt: "2026-04-12",
    features: {
      memory: false,
      multiPass: false,
      diagramGeneration: false,
      codeSimulation: false,
      architectureAnalysis: false,
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
      "Real-time IDE scanning alongside PR review",
      "Used by 300,000+ developers at HelloFresh, Cisco, Red Hat",
      "Daily security scans (Team plan) covering 200+ repos",
      "20% discount for annual billing",
    ],
    weaknesses: [
      "No institutional memory — reviews don't learn from past incidents",
      "Single-pass review without specialist agents",
      "No failure scenario simulation or architecture tracing",
      "No cross-PR dependency tracking",
    ],
    argusAdvantage:
      "Sourcery provides fast summaries and diagrams. Argus provides depth — multi-pass specialist review (bug hunter, security, architecture, regression), institutional memory that learns your patterns, and failure simulation. Sourcery cleans up code; Argus catches systemic risks.",
    summary:
      "Sourcery is an AI review tool used by 300,000+ developers, offering instant PR summaries, sequence diagrams, and inline feedback. It includes real-time IDE scanning and security scanning for Team plans. Strong for speed and code quality improvements, but lacks the multi-specialist depth, institutional memory, and failure simulation of purpose-built review tools.",
    stat: {
      claim: "Sourcery is used by over 300,000 developers at companies including HelloFresh, Cisco, and Red Hat",
      source: "Sourcery official website, 2026",
    },
    updatedAt: "2026-04-12",
    features: {
      memory: false,
      multiPass: false,
      diagramGeneration: true,
      codeSimulation: false,
      architectureAnalysis: false,
      byok: false,
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
      "Combined platform: PR review (Merge), test generation (Gen), CLI agents (Command)",
      "IDE integration for VS Code and JetBrains with AI-powered test generation",
      "Credit-based system supporting Claude Opus, Grok, and other models",
      "30 free PR reviews per month on free tier",
    ],
    weaknesses: [
      "Credit system can get expensive with premium models (5 credits for Opus)",
      "No institutional memory across reviews",
      "No failure scenario simulation or architecture tracing",
      "Testing focus may dilute review depth",
    ],
    argusAdvantage:
      "Qodo combines testing and review. Argus is purpose-built for review depth — 4-specialist pipeline, institutional memory that improves every review, and failure simulation. Qodo's multi-agent review is strong (60.1% F1), but Argus adds memory, simulation, and cross-PR tracking on top.",
    summary:
      "Qodo (CodiumAI) is a multi-product AI code quality platform combining PR review (Merge), test generation (Gen), and CLI agents (Command). Its 2026 multi-agent architecture achieved the highest F1 score in benchmark testing. Strong for teams wanting an all-in-one code quality tool, but lacks institutional memory, failure simulation, and the cross-PR tracking of specialized review tools.",
    stat: {
      claim: "Qodo 2.0's multi-agent review achieved the highest F1 score (60.1%) benchmarked against 7 leading tools",
      source: "Qodo 2.0 release announcement, February 2026",
    },
    updatedAt: "2026-04-12",
    features: {
      memory: false,
      multiPass: true,
      diagramGeneration: false,
      codeSimulation: false,
      architectureAnalysis: false,
      byok: true,
      patternLearning: false,
      selfHosted: false,
    },
  },
  {
    slug: "semgrep",
    name: "Semgrep",
    tagline: "SAST, SCA, and secrets detection platform with custom rules",
    pricing: "Free (10 contributors) – $35–$80/mo per contributor",
    strengths: [
      "20,000+ Pro rules with cross-file analysis",
      "Custom rule authoring in familiar syntax",
      "Bundled SCA and secrets detection in one platform",
      "AI-powered triage for business logic flaws (IDOR, broken auth)",
      "Available as Cursor and Claude Code plugin for IDE scanning",
    ],
    weaknesses: [
      "Fundamentally pattern-matching — rules can't reason about intent",
      "Custom rules require manual authoring and ongoing maintenance",
      "No institutional memory or learning from past reviews",
      "Enterprise pricing can reach mid-five figures annually",
    ],
    argusAdvantage:
      "Semgrep applies rules you write. Argus applies reasoning you don't have to author — understanding intent, tracing dependencies, learning from incidents. Complementary: Semgrep for deterministic rules, Argus for AI-powered reasoning.",
    summary:
      "Semgrep is a SAST, SCA, and secrets detection platform with 20,000+ rules and an open-source core. In 2026, it added AI-powered triage for complex business logic flaws and IDE plugins for Cursor and Claude Code. Excellent for deterministic security scanning at scale, but fundamentally rule-based — it can't reason about intent, learn from past reviews, or simulate failures.",
    stat: {
      claim: "Semgrep's Team plan is free for up to 10 contributors, with 20,000+ Pro rules and cross-file analysis",
      source: "Semgrep pricing page, 2026",
    },
    updatedAt: "2026-04-12",
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
    slug: "greptile",
    name: "Greptile",
    tagline: "AI code review with full codebase context",
    pricing: "$30/mo per seat (50 reviews included, $1/extra)",
    strengths: [
      "Full codebase context — reviews entire repo, not just the diff",
      "Agent v4 with 74% improvement in actionable comments vs v3",
      "Slack bot for natural language codebase Q&A",
      "Supports Python, TypeScript, Go, Java, Rust, C/C++ and more",
      "Raised $180M valuation — well-funded and rapidly improving",
    ],
    weaknesses: [
      "No institutional memory — reviews don't learn from past incidents",
      "Single-agent review without specialist pipeline",
      "No failure scenario simulation or architecture tracing",
      "Usage-based pricing adds up ($1/review beyond 50/seat)",
      "No BYOK — can't bring your own LLM provider",
    ],
    argusAdvantage:
      "Greptile reviews with full codebase context, which is strong. Argus adds depth on top — 4-specialist multi-pass pipeline, institutional memory that improves every review, failure simulation, and cross-PR dependency tracking. Greptile sees the codebase; Argus remembers its history.",
    summary:
      "Greptile is a fast-growing AI code review tool that analyzes the entire codebase (not just the diff) for context-aware reviews. Its Agent v4 significantly improved actionable feedback rates. Also offers Slack integration for codebase Q&A. Strong on breadth and context, but lacks multi-specialist depth, institutional memory, failure simulation, and BYOK flexibility.",
    stat: {
      claim: "Greptile Agent v4 increased actionable comments per PR from 0.92 to 1.60, a 74% improvement",
      source: "Greptile v4 blog post, 2026",
    },
    updatedAt: "2026-04-12",
    features: {
      memory: false,
      multiPass: false,
      diagramGeneration: false,
      codeSimulation: false,
      architectureAnalysis: false,
      byok: false,
      patternLearning: false,
      selfHosted: false,
    },
  },
  {
    slug: "cubic",
    name: "Cubic",
    tagline: "AI code review with architecture diagrams and custom rules",
    pricing: "$30/mo per developer (free for public repos)",
    strengths: [
      "Auto-generated Mermaid architecture diagrams in PR descriptions",
      "Full codebase context analysis, not just diff review",
      "Custom policies defined in natural language",
      "Jira, Linear, and Asana integration for ticket verification",
      "Analytics dashboard for PR review times and bug detection rates",
    ],
    weaknesses: [
      "No institutional memory — reviews don't learn from past incidents",
      "No multi-specialist pipeline (single-pass review)",
      "No failure scenario simulation or scenario testing",
      "No BYOK — can't bring your own LLM provider",
      "No cross-PR dependency tracking between related changes",
    ],
    argusAdvantage:
      "Cubic generates architecture diagrams and verifies tickets. Argus goes deeper — 4-specialist multi-pass review, institutional memory, failure simulation, cross-PR tracking, and BYOK flexibility. Cubic shows structure; Argus catches risks.",
    summary:
      "Cubic is an AI code review tool that combines PR analysis with auto-generated Mermaid architecture diagrams and project management integration (Jira, Linear, Asana). It verifies PRs fulfill ticket requirements and tracks team productivity metrics. Strong for visibility and project alignment, but lacks multi-specialist depth, institutional memory, failure simulation, and BYOK support.",
    stat: {
      claim: "Cubic reports fewer false positives than the industry average and proposes concrete code changes via background agents",
      source: "Cubic official website, 2026",
    },
    updatedAt: "2026-04-12",
    features: {
      memory: false,
      multiPass: false,
      diagramGeneration: true,
      codeSimulation: false,
      architectureAnalysis: false,
      byok: false,
      patternLearning: false,
      selfHosted: false,
    },
  },
];

export function getCompetitor(slug: string): Competitor | undefined {
  return competitors.find((c) => c.slug === slug);
}

export const competitorSlugs = competitors.map((c) => c.slug);
