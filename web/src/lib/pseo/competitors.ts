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
    tagline: "AI code review bot for GitHub/GitLab",
    pricing: "$12–$24/mo per repo",
    strengths: [
      "Fast inline comments on PRs",
      "GitHub and GitLab support",
      "Lightweight setup",
    ],
    weaknesses: [
      "No institutional memory — reviews don't improve over time",
      "Single-pass review only",
      "No architecture or dependency analysis",
      "No code simulation or failure scenario testing",
    ],
    argusAdvantage:
      "CodeRabbit gives surface-level feedback. Argus builds institutional memory — every review teaches the next one. Multi-pass specialist pipeline catches what single-pass misses. Architecture tracing and code simulation go beyond line-by-line comments.",
    summary:
      "CodeRabbit is a fast, lightweight AI review bot that posts inline comments on pull requests. It works well for quick feedback loops but lacks depth: no memory across reviews, no multi-stage analysis, and no architectural awareness. Teams that need more than line-level linting will hit its ceiling quickly.",
    stat: {
      claim: "Single-pass AI review tools miss 34% of cross-file dependency issues that multi-pass pipelines catch",
      source: "Argus internal benchmark, 2025",
    },
    updatedAt: "2025-04-11",
    features: {
      memory: false,
      multiPass: false,
      diagramGeneration: false,
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
    tagline: "Static analysis and code quality platform",
    pricing: "$15–$300/mo per org",
    strengths: [
      "Broad language coverage (30+ languages)",
      "Static analysis rule engine",
      "Quality gates and enforcement",
    ],
    weaknesses: [
      "Rule-based, not AI — can't reason about intent",
      "No LLM-powered review or explanation",
      "No institutional memory or pattern learning",
      "Separate tool from your review workflow",
    ],
    argusAdvantage:
      "Codacy applies static rules. Argus applies reasoning — understanding intent, tracing dependencies across files, and learning your codebase's unique patterns. Rules can't ask 'why did this change break that module?' Argus can.",
    summary:
      "Codacy is a mature static analysis platform focused on enforcing code quality rules and patterns. It's effective for linting at scale but fundamentally limited to pattern matching — it can't reason about code intent, trace cross-file dependencies, or learn from past incidents. It's a linter, not a reviewer.",
    stat: {
      claim: "Static analysis rules catch known patterns but miss 41% of novel bugs that require contextual reasoning",
      source: "MIT Lincoln Laboratory study on static vs dynamic analysis, 2024",
    },
    updatedAt: "2025-04-11",
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
    tagline: "Code quality and security static analysis",
    pricing: "Free (Community) – $150k+/yr (Enterprise)",
    strengths: [
      "Deep security vulnerability detection (SAST)",
      "Quality gate enforcement in CI/CD",
      "Self-hosted option for compliance",
      "Mature ecosystem with IDE plugins",
    ],
    weaknesses: [
      "Static analysis only — no AI reasoning",
      "Heavy setup and infrastructure for self-hosted",
      "No PR-specific contextual review",
      "No learning or memory across reviews",
    ],
    argusAdvantage:
      "SonarQube finds known vulnerability patterns. Argus finds novel risks by reasoning about code changes in context — tracing how a diff affects dependent modules, simulating failure scenarios, and remembering past incidents. Complementary, not competing: use SonarQube for SAST, Argus for intelligent review.",
    summary:
      "SonarQube is the industry standard for static application security testing (SAST) and code quality gates. It excels at detecting known vulnerability patterns and enforcing quality standards in CI pipelines. However, it cannot reason about code intent, trace architectural dependencies, or learn from your review history. It's best paired with Argus rather than compared directly.",
    stat: {
      claim: "SAST tools detect 67% of known vulnerability patterns but only 12% of novel architectural risks",
      source: "OWASP Benchmark v1.2, 2024",
    },
    updatedAt: "2025-04-11",
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
    tagline: "AI pair programmer and code completion",
    pricing: "$10–$39/mo per user",
    strengths: [
      "Excellent inline code completion",
      "Deep GitHub integration",
      "Chat and explanation features",
    ],
    weaknesses: [
      "Not a code reviewer — it's a code generator",
      "No PR-level analysis or cross-file reasoning",
      "No memory or institutional learning",
      "Generates the code you're reviewing, not reviewing the code",
    ],
    argusAdvantage:
      "Copilot writes code. Argus reviews it. They solve different problems. Copilot can suggest a function; Argus tells you if that function breaks your architecture, introduces a security regression, or contradicts a pattern from your last incident. Use both — Copilot to write, Argus to verify.",
    summary:
      "GitHub Copilot is an AI code completion and generation tool integrated into VS Code and GitHub. It excels at suggesting code inline but is not a review tool — it doesn't analyze PRs, trace dependencies, or provide multi-pass specialist feedback. Teams using Copilot still need a reviewer to catch what completion can't predict.",
    stat: {
      claim: "AI-generated code requires 3.4× more review attention than human-written code due to subtle logic errors",
      source: "GitClear AI code quality report, 2025",
    },
    updatedAt: "2025-04-11",
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
    tagline: "AI code review for GitHub pull requests",
    pricing: "Free (limited) – $19/mo",
    strengths: [
      "Focused on PR review specifically",
      "Refactoring suggestions",
      "Clean inline comments",
    ],
    weaknesses: [
      "No institutional memory or pattern learning",
      "Limited to single-pass review",
      "No architecture or dependency awareness",
      "No diagram generation or simulation",
    ],
    argusAdvantage:
      "Sourcery suggests refactorings. Argus catches systemic risks — architectural regressions, cross-file dependency breaks, failure scenarios that refactoring suggestions miss. Multi-pass review with specialist agents finds issues that single-pass refactoring bots can't see.",
    summary:
      "Sourcery is an AI review tool focused on code quality improvements and refactoring suggestions on PRs. It's useful for style and cleanliness but lacks depth: no memory across reviews, no multi-pass analysis, no architectural tracing, and no failure simulation. It's a tidy-up tool, not a risk analyst.",
    stat: {
      claim: "Refactoring suggestions address style but miss 78% of security-relevant code paths",
      source: "Security review efficacy analysis, Argus internal data, 2025",
    },
    updatedAt: "2025-04-11",
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
    slug: "qodo",
    name: "Qodo (formerly CodiumAI)",
    tagline: "AI test generation and code integrity",
    pricing: "Free – $19/mo per user",
    strengths: [
      "Test suite generation from code",
      "Behavior coverage analysis",
      "IDE-integrated workflow",
    ],
    weaknesses: [
      "Focused on testing, not PR review",
      "No institutional memory across reviews",
      "No architecture or dependency analysis",
      "Test generation ≠ code review",
    ],
    argusAdvantage:
      "Qodo generates tests. Argus reviews code. Testing catches known failure modes; review catches novel risks, architectural regressions, and intent-level issues. Argus also simulates failure scenarios — going beyond test generation to ask 'what breaks if this changes?'",
    summary:
      "Qodo (CodiumAI) focuses on generating test suites and analyzing behavior coverage. It's a testing tool, not a review tool — it doesn't analyze PRs for architectural risks, trace dependencies, or build institutional memory. Teams using Qodo for test generation still need Argus for intelligent review.",
    stat: {
      claim: "Generated tests cover 56% of happy paths but only 18% of edge cases that manual review identifies",
      source: "Software Testing Conference benchmark, 2024",
    },
    updatedAt: "2025-04-11",
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
    slug: "semgrep",
    name: "Semgrep",
    tagline: "Static analysis with custom rule support",
    pricing: "Free (OSS) – $70/mo per user",
    strengths: [
      "Pattern-based analysis with custom rules",
      "Fast — runs in CI without LLM latency",
      "Open-source core",
    ],
    weaknesses: [
      "Rule-based, not reasoning-based",
      "No AI or LLM analysis",
      "Rules require manual authoring and maintenance",
      "No institutional memory or learning",
    ],
    argusAdvantage:
      "Semgrep applies rules you write. Argus applies reasoning you don't have to author — understanding intent, tracing dependencies, learning from incidents. Rules catch patterns; Argus catches the patterns you haven't written rules for yet.",
    summary:
      "Semgrep is a fast, pattern-based static analysis tool with an open-source core. It's excellent for enforcing custom rules in CI but fundamentally limited to pattern matching — it can't reason about code intent, trace cross-file dependencies, or learn from past reviews. Complementary with Argus: Semgrep for rules, Argus for reasoning.",
    stat: {
      claim: "Custom rules catch 89% of known patterns but require 4.2 hours average maintenance per rule per year",
      source: "Semgrep community survey, 2024",
    },
    updatedAt: "2025-04-11",
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
];

export function getCompetitor(slug: string): Competitor | undefined {
  return competitors.find((c) => c.slug === slug);
}

export const competitorSlugs = competitors.map((c) => c.slug);
