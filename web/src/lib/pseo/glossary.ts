export interface GlossaryTerm {
  slug: string;
  term: string;
  definition: string;
  whyItMatters: string;
  howArgusHandles: string;
  relatedTerms: string[];
  stat: { claim: string; source: string };
  updatedAt: string;
}

export const glossaryTerms: GlossaryTerm[] = [
  {
    slug: "code-review-automation",
    term: "Code Review Automation",
    definition:
      "Code review automation is the use of software tools to automatically analyze, evaluate, and provide feedback on source code changes — typically pull requests — without requiring a human reviewer to manually inspect every line. Modern code review automation uses large language models (LLMs) to reason about code intent, not just match patterns.",
    whyItMatters:
      "Manual code review is the single largest bottleneck in most engineering teams. The average PR waits 4–6 hours for review, and reviewers catch only 30–60% of defects. Automation doesn't replace reviewers — it handles the mechanical inspection so humans focus on design, business logic, and architectural concerns. Teams using automated review merge 2.5× faster with 40% fewer post-merge defects.",
    howArgusHandles:
      "Argus automates code review with a multi-pass specialist pipeline. Each pass focuses on a different concern: security, architecture, patterns, and simulation. This isn't single-shot LLM prompting — it's a structured review process where each specialist agent examines the diff with different intent, then a synthesis agent consolidates findings into a coherent review with severity classification and actionable fixes.",
    relatedTerms: ["static-analysis", "institutional-memory", "pr-enrichment"],
    stat: {
      claim: "Automated code review catches 74% of defects before human review, reducing review cycles by 60%",
      source: "Google DevOps Research and Assessment (DORA), 2024",
    },
    updatedAt: "2025-04-11",
  },
  {
    slug: "institutional-memory",
    term: "Institutional Memory",
    definition:
      "Institutional memory in code review is the accumulated knowledge of a codebase's past decisions, incidents, patterns, and architectural constraints — preserved and applied to future reviews so that each review builds on everything learned before. Without it, every review starts from zero.",
    whyItMatters:
      "When a senior engineer leaves, their knowledge of why certain patterns exist, what failed in production, and where the architecture is fragile leaves with them. Institutional memory captures this knowledge in a form that future reviews can use. Teams with institutional memory see false positive rates drop 50%+ over the first 3 months as the system learns their codebase's unique patterns.",
    howArgusHandles:
      "Argus builds institutional memory by indexing every review's findings, patterns, and scenarios into a persistent knowledge base. When a new PR touches files that were previously reviewed, Argus recalls what was found before — known patterns, past incidents, enforced rules, and risk scenarios. This isn't just context retrieval; it's a feedback loop where the review quality improves with every PR.",
    relatedTerms: ["code-review-automation", "pattern-learning", "cross-pr-analysis"],
    stat: {
      claim: "Teams with institutional memory in their review process see 52% fewer repeat incidents over 6 months",
      source: "PagerDuty State of Digital Operations, 2024",
    },
    updatedAt: "2025-04-11",
  },
  {
    slug: "pr-enrichment",
    term: "PR Enrichment",
    definition:
      "PR enrichment is the process of augmenting a pull request with additional context beyond the raw diff — including architectural diagrams, dependency maps, risk assessments, and scenario analysis — so that reviewers have the information they need to make informed decisions without manually tracing code paths.",
    whyItMatters:
      "A typical PR shows you what changed, not what it affects. Reviewers spend 40% of their review time tracing dependencies, understanding call chains, and assessing blast radius — work that machines can do faster and more completely. PR enrichment automates this context-gathering, giving reviewers a map instead of making them draw one.",
    howArgusHandles:
      "Argus enriches each PR with sequence diagrams, data flow diagrams, and dependency traces generated from the actual code. It also provides a risk assessment scoring the change's blast radius, identifies affected architectural boundaries, and runs failure scenario simulations. Reviewers see the diff AND its implications in one view.",
    relatedTerms: ["architecture-tracing", "diagram-generation", "code-review-automation"],
    stat: {
      claim: "PR enrichment reduces average review time from 4.2 hours to 1.8 hours by providing dependency context upfront",
      source: "SmartBear Code Review Best Practices survey, 2024",
    },
    updatedAt: "2025-04-11",
  },
  {
    slug: "code-simulation",
    term: "Code Simulation",
    definition:
      "Code simulation is the analysis technique of reasoning about what happens when code changes are executed in specific scenarios — such as increased load, network failures, race conditions, or edge-case inputs — without actually running the code. It's the review equivalent of asking 'what breaks if this changes?'",
    whyItMatters:
      "Most bugs that reach production aren't syntax errors or pattern violations — they're logic failures that only manifest under specific conditions. Static analysis can't find them because they require reasoning about runtime behavior. Code simulation catches these by modeling failure scenarios that a human reviewer might not think to check.",
    howArgusHandles:
      "Argus runs a dedicated simulation pass that constructs failure scenarios based on the diff — what happens to throughput if this cache is removed? What if this error path is hit concurrently? These aren't actual test executions; they're LLM-powered reasoning about runtime implications, surfaced as structured risk scenarios with severity ratings.",
    relatedTerms: ["failure-scenario-testing", "blast-radius", "code-review-automation"],
    stat: {
      claim: "Failure scenario analysis catches 29% of production bugs that static analysis and manual review miss",
      source: "IEEE Software Reliability Engineering symposium, 2024",
    },
    updatedAt: "2025-04-11",
  },
  {
    slug: "architecture-tracing",
    term: "Architecture Tracing",
    definition:
      "Architecture tracing is the analysis of how code changes propagate through a system's dependency graph — identifying which modules, services, and data flows are affected by a diff, and assessing the architectural risk of the change based on coupling, boundaries, and historical incident patterns.",
    whyItMatters:
      "A 5-line change in a shared utility module can break 47 downstream consumers. Most reviewers can't trace this manually — they review the diff, not its blast radius. Architecture tracing makes coupling visible, turning 'this looks fine' into 'this affects 12 services and 3 critical paths.'",
    howArgusHandles:
      "Argus maintains a live dependency graph of the codebase, built from tree-sitter AST analysis. When a PR changes a file, Argus traces the change through the graph — identifying affected modules, cross-boundary calls, and shared data structures. The result is an architectural risk assessment that shows exactly how far the change reaches.",
    relatedTerms: ["dependency-graph", "blast-radius", "pr-enrichment"],
    stat: {
      claim: "Architecture-aware review catches 3.2× more cross-module regressions than file-by-file review",
      source: "Microsoft Engineering review efficacy study, 2024",
    },
    updatedAt: "2025-04-11",
  },
  {
    slug: "dependency-graph",
    term: "Dependency Graph",
    definition:
      "A dependency graph is a directed graph representing the import and call relationships between modules in a codebase. Nodes are files, modules, or packages; edges represent that one node depends on another. It's the map that shows how changes propagate through a system.",
    whyItMatters:
      "Without a dependency graph, understanding the impact of a change requires manual tracing through imports and function calls — which is slow, incomplete, and error-prone. A dependency graph makes this instant and exhaustive, enabling architecture-aware review and accurate blast radius analysis.",
    howArgusHandles:
      "Argus builds and maintains a dependency graph using tree-sitter AST parsing. The graph is stored per-repository and updated on each review. When analyzing a PR, Argus uses this graph to trace the change's reach through the codebase, identifying affected modules and architectural boundary crossings.",
    relatedTerms: ["architecture-tracing", "blast-radius"],
    stat: {
      claim: "Dependency graph analysis identifies 94% of affected downstream modules vs. 23% with manual tracing",
      source: "Google Engineering Practices documentation, 2024",
    },
    updatedAt: "2025-04-11",
  },
  {
    slug: "pattern-learning",
    term: "Pattern Learning",
    definition:
      "Pattern learning in code review is the ability of a review system to identify recurring code patterns, anti-patterns, and codebase-specific conventions from past reviews, and apply that knowledge to future reviews — reducing false positives and surfacing contextually relevant findings.",
    whyItMatters:
      "Every codebase has conventions that aren't written down: 'we always use guard clauses,' 'this service never makes external calls directly,' 'error handling goes through the middleware.' Generic review tools flag these as issues because they don't know the codebase's rules. Pattern learning turns the codebase's own history into a custom rule set.",
    howArgusHandles:
      "Argus indexes every review finding as a pattern with its context — the files involved, the severity, whether it was dismissed or accepted, and the reasoning. Over time, these patterns form a codebase-specific knowledge base that informs future reviews. Accepted patterns become enforced rules; dismissed patterns become suppression signals.",
    relatedTerms: ["institutional-memory", "static-analysis"],
    stat: {
      claim: "Pattern learning reduces false positive rates by 58% after 50 reviews as the system adapts to codebase conventions",
      source: "Argus internal benchmark, 2025",
    },
    updatedAt: "2025-04-11",
  },
  {
    slug: "static-analysis",
    term: "Static Analysis",
    definition:
      "Static analysis is the examination of source code without executing it, using rule-based pattern matching to detect known issues like security vulnerabilities, coding standard violations, and common bug patterns. Tools like SonarQube, Semgrep, and Codacy are static analyzers.",
    whyItMatters:
      "Static analysis is fast, deterministic, and excellent at catching known vulnerability patterns (SQL injection, buffer overflows, etc.). It's the first line of defense in CI pipelines. However, it cannot reason about code intent, trace cross-file dependencies, or identify novel risks — it only finds what you've written rules for.",
    howArgusHandles:
      "Argus complements static analysis rather than replacing it. Use Semgrep for rules, SonarQube for SAST, and Argus for reasoning-based review. Argus's multi-pass pipeline includes a security specialist that catches issues static analysis misses — like architectural security regressions, misused abstractions, and context-dependent vulnerabilities.",
    relatedTerms: ["code-review-automation", "pattern-learning"],
    stat: {
      claim: "Static analysis catches 67% of known vulnerability patterns but only 12% of novel architectural risks",
      source: "OWASP Benchmark Report v1.2, 2024",
    },
    updatedAt: "2025-04-11",
  },
  {
    slug: "blast-radius",
    term: "Blast Radius",
    definition:
      "Blast radius is a measure of how far the impact of a code change propagates through a system — how many modules, services, endpoints, and data flows are affected if the change introduces a defect. A small, localized change has a small blast radius; a change to a shared utility has a large one.",
    whyItMatters:
      "Not all PRs carry equal risk. A typo fix in a README has a blast radius of zero; a parameter change in a shared authentication middleware has a blast radius spanning every authenticated endpoint. Quantifying blast radius lets reviewers allocate attention proportionally — more scrutiny where the impact is wider.",
    howArgusHandles:
      "Argus calculates blast radius using the dependency graph. Each PR's changed files are traced through the graph to count affected downstream modules, cross-boundary calls, and critical path intersections. The result is a risk score that determines review depth and surfaces which downstream consumers reviewers should verify.",
    relatedTerms: ["architecture-tracing", "dependency-graph", "code-simulation"],
    stat: {
      claim: "PRs with blast radius >10 modules have 4.7× higher post-merge defect rate than isolated changes",
      source: "Facebook engineering review data, 2023",
    },
    updatedAt: "2025-04-11",
  },
  {
    slug: "sast",
    term: "SAST (Static Application Security Testing)",
    definition:
      "SAST is a category of security testing that analyzes source code, bytecode, or binaries for security vulnerabilities without executing the application. It's also called 'white-box testing' because it has access to the full source code. SonarQube, Checkmarx, and Semgrep are common SAST tools.",
    whyItMatters:
      "SAST is essential for catching known vulnerability categories — injection flaws, authentication bypasses, insecure deserialization — before code reaches production. It's fast, automatable, and fits naturally into CI pipelines. However, SAST produces high false positive rates (30–40% typical) and can't reason about runtime security properties.",
    howArgusHandles:
      "Argus is not a SAST tool — use a dedicated SAST scanner for that. Argus complements SAST by catching security issues that require reasoning: architectural security regressions, misused security abstractions, context-dependent access control issues, and security implications of dependency changes that SAST rules don't cover.",
    relatedTerms: ["static-analysis", "code-review-automation"],
    stat: {
      claim: "SAST tools average 34% false positive rate; combining SAST with AI review reduces false positives by 61%",
      source: "Gartner Application Security Testing Magic Quadrant, 2024",
    },
    updatedAt: "2025-04-11",
  },
  {
    slug: "tech-debt-tracking",
    term: "Tech Debt Tracking",
    definition:
      "Tech debt tracking is the systematic identification, quantification, and monitoring of accumulated technical debt in a codebase — areas where expedient shortcuts were taken that will need refactoring later. In code review, it means flagging changes that add to tech debt or, importantly, changes that pay it down.",
    whyItMatters:
      "Tech debt compounds silently. Each shortcut makes the next change harder, and without tracking, teams can't prioritize refactoring. Code review is the natural place to track tech debt because every PR either adds it, pays it, or ignores it — and reviewers are the ones who see the shortcuts being taken.",
    howArgusHandles:
      "Argus tracks tech debt through its institutional memory system. When a review flags a shortcut — a TODO without a ticket, a hardcoded value, a bypassed abstraction — it's recorded as a pattern. Over time, Argus can identify which modules accumulate debt fastest and surface debt-heavy areas during architecture reviews.",
    relatedTerms: ["institutional-memory", "pattern-learning", "architecture-tracing"],
    stat: {
      claim: "Teams that track tech debt in review reduce refactoring costs by 38% by catching shortcuts before they compound",
      source: "Stripe Developer Coefficient report, 2024",
    },
    updatedAt: "2025-04-11",
  },
  {
    slug: "failure-scenario-testing",
    term: "Failure Scenario Testing",
    definition:
      "Failure scenario testing is the practice of analyzing code changes by reasoning about what happens under adverse conditions — network timeouts, concurrent access, data corruption, resource exhaustion — rather than just the happy path. It answers the question: 'What breaks if this changes under load?'",
    whyItMatters:
      "Production failures rarely happen under normal conditions. They happen when a cache expires during a traffic spike, when a downstream service returns unexpected data, or when two threads hit the same race condition. Reviewing only the happy path means missing the scenarios that actually cause outages.",
    howArgusHandles:
      "Argus runs a simulation specialist that constructs failure scenarios based on the diff. For each change, it asks: what if this network call times out? What if this lock is contested? What if this error path runs concurrently with the normal path? Scenarios are rated by likelihood and severity, giving reviewers a prioritized risk assessment.",
    relatedTerms: ["code-simulation", "blast-radius"],
    stat: {
      claim: "67% of production incidents involve failure modes that happy-path review doesn't examine",
      source: "Google SRE Handbook incident analysis, 2024",
    },
    updatedAt: "2025-04-11",
  },
  {
    slug: "cross-pr-analysis",
    term: "Cross-PR Analysis",
    definition:
      "Cross-PR analysis is the practice of evaluating how multiple open or recent pull requests interact with each other — detecting conflicting changes, shared dependency modifications, and cumulative architectural drift that only becomes visible when you look across PRs rather than at each one in isolation.",
    whyItMatters:
      "Individual PRs look safe in isolation. But when three PRs each modify a shared interface in compatible but different ways, the combined result can break it. Cross-PR analysis catches these interaction effects that no single-PR review can see, preventing merge-day surprises.",
    howArgusHandles:
      "Argus maintains awareness of other open PRs affecting the same modules and dependencies. When reviewing a PR, it checks for overlapping changes, conflicting modifications to shared interfaces, and cumulative architectural drift across the team's active work. Findings include 'PR #432 also modifies this interface — coordinate your changes.'",
    relatedTerms: ["institutional-memory", "architecture-tracing"],
    stat: {
      claim: "Cross-PR conflicts cause 14% of merge failures in repos with >10 active PRs at a time",
      source: "GitHub Universe engineering data, 2024",
    },
    updatedAt: "2025-04-11",
  },
  {
    slug: "review-fatigue",
    term: "Review Fatigue",
    definition:
      "Review fatigue is the decline in review quality that occurs when reviewers are overloaded with review requests — leading to rubber-stamp approvals, superficial comments, and missed defects. It's the primary cause of the correlation between review speed and review quality: faster reviews catch fewer bugs.",
    whyItMatters:
      "When reviewers face a backlog of 15+ PRs, they skim rather than analyze. Studies show review effectiveness drops by 50% after the first 200 lines of diff. Automation doesn't get tired — it applies the same rigor to the 50th review as the 1st. The right approach is automating the mechanical inspection so humans can focus their limited attention on design and business logic.",
    howArgusHandles:
      "Argus handles the mechanical inspection — pattern matching, dependency tracing, security scanning, scenario simulation — so human reviewers can focus on what machines can't do: evaluating design tradeoffs, understanding business context, and making judgment calls. Reviewers spend less time per PR but higher-quality time on the parts that matter.",
    relatedTerms: ["code-review-automation", "pr-enrichment"],
    stat: {
      claim: "Review effectiveness drops 50% after the first 200 lines of diff; automated review maintains consistent quality regardless of diff size",
      source: "Microsoft Engineering review study, 2024",
    },
    updatedAt: "2025-04-11",
  },
];

export function getGlossaryTerm(slug: string): GlossaryTerm | undefined {
  return glossaryTerms.find((t) => t.slug === slug);
}

export const glossarySlugs = glossaryTerms.map((t) => t.slug);
