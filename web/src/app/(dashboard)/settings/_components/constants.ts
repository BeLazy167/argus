export const PROVIDERS = [
  "openrouter",
  "openai",
  "anthropic",
  "fireworks",
  "groq",
  "together",
  "deepseek",
  "azure",
  "gcp_vertex",
  "aws_bedrock",
  "zhipu",
  "vercel",
] as const;
export type Provider = (typeof PROVIDERS)[number];

export const PROVIDER_LABELS: Record<Provider, string> = {
  openrouter: "OpenRouter",
  openai: "OpenAI",
  anthropic: "Anthropic",
  fireworks: "Fireworks AI",
  groq: "Groq",
  together: "Together AI",
  deepseek: "DeepSeek",
  azure: "Azure OpenAI",
  gcp_vertex: "GCP Vertex AI",
  aws_bedrock: "AWS Bedrock",
  zhipu: "Zhipu AI (GLM)",
  vercel: "Vercel AI Gateway",
};

export const MODEL_PICKS: Record<Provider, string[]> = {
  openrouter: ["anthropic/claude-sonnet-4", "openai/gpt-4o", "google/gemini-2.5-pro"],
  openai: ["gpt-4o", "gpt-4o-mini"],
  anthropic: ["claude-sonnet-4-20250514"],
  fireworks: [
    "accounts/fireworks/models/glm-5p1",
    "accounts/fireworks/models/deepseek-r1",
    "accounts/fireworks/models/llama-v3p3-70b-instruct",
  ],
  groq: ["llama-3.3-70b-versatile", "mixtral-8x7b-32768", "gemma2-9b-it"],
  together: [
    "deepseek-ai/DeepSeek-V3.1",
    "meta-llama/Llama-3.3-70B-Instruct-Turbo",
    "Qwen/Qwen2.5-72B-Instruct-Turbo",
  ],
  deepseek: ["deepseek-chat", "deepseek-reasoner"],
  azure: ["gpt-4o", "gpt-4o-mini"],
  gcp_vertex: ["gemini-2.5-pro", "gemini-2.5-flash"],
  aws_bedrock: ["anthropic.claude-sonnet-4", "anthropic.claude-haiku"],
  zhipu: ["glm-5", "glm-4-plus", "glm-4"],
  vercel: [
    "anthropic/claude-sonnet-4.6",
    "anthropic/claude-haiku-4.5",
    "openai/gpt-5.4",
    "zai/glm-5",
  ],
};

export const CORE_STAGES = ["triage", "review", "scoring", "synthesis"] as const;

export const STAGE_DESCRIPTIONS: Record<string, string> = {
  triage: "Decides which files need detailed review vs. can be skimmed",
  review: "Analyzes code changes and writes review comments",
  scoring: "Cross-model validation — scores and deduplicates specialist comments",
  synthesis: "Combines per-file reviews into a unified summary",
};

export const STAGE_LABELS: Record<string, string> = {
  triage_system: "Triage",
  review_system: "Review",
  scoring_system: "Scoring",
  specialist_bug_hunter: "Bug Hunter Specialist",
  specialist_security: "Security Specialist",
  specialist_architecture: "Architecture Specialist",
  specialist_regression: "Regression Specialist",
};

export const PROMPT_STAGES = Object.keys(STAGE_LABELS);

/* ── Auto-review Toggle ─────────────────────────────────────────────
 *
 * Controls whether PR open/push events automatically kick off a review.
 * Off by default at both org and repo level (nil in settings JSON => false).
 * When off, the backend posts a one-shot "Trigger Argus review" checkbox
 * comment on opened PRs, with a token/cost estimate from history + live
 * diff. Clicking the checkbox fires an edited webhook and runs the review
 * under the 3/hr force-cap path.
 *
 * A cost/behavior control, applied to every installation.
 */
export const AUTO_RUN_TOGGLE = {
  key: "auto_run",
  label: "Auto-review every PR",
  hint: "Run a review automatically on PR open and on every new commit",
  description:
    "When off, Argus posts a task-list checkbox on opened PRs with an estimated token / cost preview. Ticking the box runs the review on demand.",
  defaultValue: false,
} as const;

/* AUTO_RESOLVE_TOGGLE: separate from AUTO_RUN because it's diff-only and
 * therefore safe to run regardless of whether the review pipeline fires.
 * Default ON — users on manual-review repos still usually want stale
 * comments to clear when they push a fix.
 */
export const AUTO_RESOLVE_TOGGLE = {
  key: "auto_resolve_enabled",
  label: "Auto-resolve stale review threads",
  hint: "Close Argus comments when you push fixes to the flagged lines",
  description:
    "On every push to an open PR, Argus checks whether the new commit changed any of the lines it previously flagged (within ±3 lines) and marks those review threads resolved. Runs regardless of auto-review — it's diff-based, with no LLM call.",
  defaultValue: true,
} as const;

/* ── Pipeline Feature Toggles ── */

export const PIPELINE_FEATURES = [
  {
    key: "deep_review",
    label: "Deep Review",
    hint: "4 specialist agents review each file in parallel",
    description:
      "Run 4 specialist reviewers (Bug Hunter, Security, Architecture, Regression) with lead agent coordination",
    defaultValue: false,
  },
  {
    key: "cross_file_context",
    label: "Cross-File Context",
    hint: "Traces callers, imports, and shared types across files",
    description: "Fetch related files for richer review context",
    defaultValue: true,
  },
  {
    key: "blast_radius",
    label: "Blast Radius Analysis",
    hint: "Maps downstream dependents affected by changes",
    description:
      "Trace dependency impact via code graph. Finds how changes affect downstream callers.",
    defaultValue: true,
  },
  {
    key: "simulation",
    label: "Simulation & Scenarios",
    hint: "Tests known risk scenarios against the PR",
    description: "Run stored scenarios against the diff to predict breakage. Requires Deep Review.",
    defaultValue: false,
    requiresDeepReview: true,
  },
  {
    key: "pr_enrichment",
    label: "PR Enrichment",
    hint: "Auto-enriches PR descriptions with missing context and diagrams",
    description:
      "Auto-enriches PR descriptions with missing context and architecture diagrams (sequence, data flow, dependency).",
    defaultValue: true,
  },
  {
    key: "learn_patterns",
    label: "Pattern Learning",
    hint: "Learns reusable patterns from high-confidence findings",
    description: "Learn recurring code patterns from review feedback to improve future reviews.",
    defaultValue: true,
  },
  {
    key: "learn_conventions",
    label: "Convention Learning",
    hint: "Extracts codebase conventions from code diffs",
    description: "Learn team coding conventions from approved PRs and apply them in reviews.",
    defaultValue: true,
  },
  {
    key: "file_synthesis",
    label: "File Synthesis",
    hint: "Creates per-file institutional memory summaries",
    description: "Combine per-file reviews into a unified PR summary with cross-cutting insights.",
    defaultValue: true,
  },
  {
    key: "architecture_graph",
    label: "Architecture Graph",
    hint: "Extracts dependency graph from code changes",
    description:
      "Build and maintain a dependency graph from reviewed code for blast radius analysis.",
    defaultValue: true,
  },
] as const;
