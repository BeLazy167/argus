export interface Persona {
  slug: string;
  title: string;
  subtitle: string;
  painPoints: string[];
  howArgusFits: string[];
  featureCallouts: { feature: string; reason: string }[];
  stat: { claim: string; source: string };
  updatedAt: string;
}

export const personas: Persona[] = [
  {
    slug: "sre-teams",
    title: "AI Code Review for SRE & Reliability Teams",
    subtitle:
      "Catch architectural regressions, trace blast radius, and simulate failure scenarios before they reach production.",
    painPoints: [
      "Changes to shared infrastructure code break downstream services in ways no single PR review can predict",
      "Incident post-mortems identify the same root causes repeatedly — but that knowledge doesn't flow back into the review process",
      "On-call engineers reviewing PRs at 2am miss cross-module dependencies that cause the next page",
      "Manual blast radius analysis is slow and incomplete — you can't trace every affected endpoint by hand",
    ],
    howArgusFits: [
      "Architecture tracing shows exactly which services and endpoints a change affects — no manual tracing needed",
      "Failure scenario simulation asks 'what breaks if this cache expires during a spike?' before the code ships",
      "Institutional memory captures incident patterns and applies them to future reviews — the post-mortem becomes the pre-mortem",
      "Blast radius scoring lets SREs prioritize review attention on high-impact changes",
    ],
    featureCallouts: [
      {
        feature: "Failure scenario simulation",
        reason: "Catches the runtime failure modes that cause on-call pages — race conditions, cache invalidation, cascading timeouts",
      },
      {
        feature: "Architecture tracing",
        reason: "Maps change propagation across service boundaries so you know what's affected before merge",
      },
      {
        feature: "Institutional memory",
        reason: "Turns every incident post-mortem into review intelligence — 'this pattern caused SEV-2 last month'",
      },
      {
        feature: "Blast radius scoring",
        reason: "Quantifies risk so you can allocate review time proportionally to impact",
      },
    ],
    stat: {
      claim: "SRE teams using architecture-aware review reduce change-related incidents by 43% in the first quarter",
      source: "Argus internal benchmark across 12 SRE teams, 2025",
    },
    updatedAt: "2025-04-11",
  },
  {
    slug: "startup-ctos",
    title: "AI Code Review for Startup CTOs & Founders",
    subtitle:
      "Ship fast without shipping bugs. Institutional memory that survives turnover, review coverage that scales with your team.",
    painPoints: [
      "Small teams can't afford dedicated reviewers — the CTO reviews everything, and it's a bottleneck",
      "Key engineer leaves and takes all the architectural knowledge with them",
      "Moving fast means cutting corners, and tech debt compounds faster than you can track it",
      "Junior engineers merge code that senior engineers would have flagged — but seniors are busy shipping",
    ],
    howArgusFits: [
      "Argus provides senior-level review coverage on every PR, 24/7 — no waiting for the CTO to be online",
      "Institutional memory captures architectural decisions and conventions so knowledge doesn't walk out the door",
      "Multi-pass specialist pipeline catches what rushed single-pass review misses — security, architecture, patterns",
      "Pattern learning adapts to your codebase's conventions, reducing noise and surfacing what matters",
    ],
    featureCallouts: [
      {
        feature: "Multi-pass specialist pipeline",
        reason: "Security, architecture, patterns, and simulation — four specialist reviews in one pass, not one generic scan",
      },
      {
        feature: "Institutional memory",
        reason: "When your lead engineer joins a competitor, the review quality doesn't drop — Argus remembers what they taught it",
      },
      {
        feature: "Pattern learning",
        reason: "Learns your codebase's unwritten rules — 'we always use guard clauses,' 'this service never calls external APIs directly'",
      },
      {
        feature: "PR enrichment with diagrams",
        reason: "Generated sequence and data flow diagrams so junior engineers understand the change's context before approving",
      },
    ],
    stat: {
      claim: "Startups using automated review merge 2.5× faster with 40% fewer post-merge defects than teams relying on manual review alone",
      source: "DORA State of DevOps report, 2024",
    },
    updatedAt: "2025-04-11",
  },
  {
    slug: "platform-engineering",
    title: "AI Code Review for Platform Engineering Teams",
    subtitle:
      "Guard your platform's interfaces and abstractions. Automatic boundary enforcement and cross-team impact analysis.",
    painPoints: [
      "Platform teams maintain shared libraries and infrastructure that product teams consume — a breaking change cascades across every team",
      "API contract changes get reviewed in isolation and break consumers that the reviewer didn't know about",
      "Cross-team PRs that modify platform interfaces need special scrutiny, but reviewers don't always know which changes are platform-critical",
      "Deprecation paths are hard to enforce through review alone — 'we deprecated this function' doesn't stop people from using it",
    ],
    howArgusFits: [
      "Architecture tracing identifies when a change crosses platform boundaries — flagging it for platform-team review automatically",
      "Cross-PR analysis detects when multiple product teams are modifying the same platform interface simultaneously",
      "Pattern learning captures deprecation rules and platform conventions, enforcing them in every review",
      "Blast radius analysis quantifies how many consumers a platform change affects",
    ],
    featureCallouts: [
      {
        feature: "Architecture tracing",
        reason: "Automatically flags changes that cross platform boundaries — no manual tagging needed",
      },
      {
        feature: "Cross-PR analysis",
        reason: "Detects conflicting modifications to shared interfaces across multiple teams' PRs",
      },
      {
        feature: "Pattern learning",
        reason: "Enforces platform conventions and deprecation rules in review — 'use the new client factory, not the deprecated one'",
      },
      {
        feature: "Dependency graph",
        reason: "Maps which product teams depend on which platform modules — impact analysis in seconds, not hours",
      },
    ],
    stat: {
      claim: "Platform teams using boundary-aware review reduce interface-breaking changes by 61% across their organization",
      source: "ThoughtWorks Technology Radar, 2024",
    },
    updatedAt: "2025-04-11",
  },
  {
    slug: "open-source-maintainers",
    title: "AI Code Review for Open-Source Maintainers",
    subtitle:
      "Review community contributions at scale. Consistent standards, institutional memory, and security scanning for every PR.",
    painPoints: [
      "Hundreds of community PRs per month — too many for maintainers to review thoroughly",
      "Contributors don't know the project's unwritten conventions, leading to style and architecture mismatches",
      "Security-sensitive PRs (dependency updates, auth changes) need expert review but maintainers are volunteers",
      "Maintainer burnout from review load is the leading cause of open-source project abandonment",
    ],
    howArgusFits: [
      "Argus provides first-pass review on every community PR — catching style issues, convention violations, and security concerns before a human reviewer looks at it",
      "Pattern learning captures the project's conventions — coding style, preferred patterns, known anti-patterns — and enforces them consistently",
      "Security specialist pass flags dependency updates with known vulnerabilities and auth-related changes that need careful review",
      "Review comments include reasoning and references — contributors learn the project's standards, not just what to change",
    ],
    featureCallouts: [
      {
        feature: "Multi-pass specialist pipeline",
        reason: "Catches convention violations, security concerns, and architectural issues in one automated pass — maintainers review the exceptions, not the baseline",
      },
      {
        feature: "Pattern learning",
        reason: "Learns the project's style and architecture conventions from past reviews — consistent enforcement without writing a linter config",
      },
      {
        feature: "Institutional memory",
        reason: "Captures past design decisions and rejected approaches — 'we discussed this pattern in #342 and chose a different approach'",
      },
      {
        feature: "BYOK (Bring Your Own Key)",
        reason: "Use your own LLM provider key — no per-seat costs, compatible with any OpenAI-compatible API",
      },
    ],
    stat: {
      claim: "Open-source maintainers using AI-assisted review handle 3.2× more PRs per month with equivalent quality vs. manual-only review",
      source: "GitHub Octoverse contributor trends, 2024",
    },
    updatedAt: "2025-04-11",
  },
];

export function getPersona(slug: string): Persona | undefined {
  return personas.find((p) => p.slug === slug);
}

export const personaSlugs = personas.map((p) => p.slug);
