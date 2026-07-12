import type {
  FindingState,
  Repo as WireRepo,
  Review as WireReview,
  ReviewComment as WireReviewComment,
  Scenario as WireScenario,
} from "./generated/api-types";
import type { ReviewContract } from "./generated/contract-types";

// ─────────────────────────────────────────────────────────────────────────────
// Wire types generated from Go structs by tygo (see backend/tygo.yaml). The
// generated file is the single source of truth for every shape below, so a Go
// wire change breaks `tsc` here. Regenerate with `cd backend && make tygo`.
//
// Three dispositions:
//   • re-export    — generated shape is exactly the wire the frontend reads.
//   • extension    — generated wire + frontend refinements (typed JSONB,
//                    narrowed unions); the `Omit<Wire…>` base still tracks drift
//                    on every un-overridden field.
//   • hand-written — genuinely frontend-only, or a different endpoint's computed
//                    shape not backed by a single generated struct.
// ─────────────────────────────────────────────────────────────────────────────

// Re-exports — generated shape consumed as-is.
export type {
  ActivityLog,
  GaugeRow,
  Installation,
  ModelConfig,
  Pattern,
  PatternStat,
  Rule,
  ScenarioKPIs,
  ScenarioRun,
  ScenarioVerdict,
  Stats,
} from "./generated/api-types";
// FindingState and ReviewContract are re-exported and also used in extensions below.
export type { FindingState, ReviewContract };

// ── Extensions: generated wire + frontend refinements ────────────────────────

/** Wire Repo, but settings_json is typed as an indexable object (the dashboard
 *  spreads and indexes it) rather than the generated `unknown`. */
export type Repo = Omit<WireRepo, "settings_json"> & {
  settings_json: Record<string, unknown>;
};

/**
 * Wire Review, with the JSONB columns typed richly and the status narrowed to
 * its union. `file_count`/`simulation_results` are dashboard-only fields the
 * detail view derives client-side (not part of the serialized Review row).
 */
export type Review = Omit<
  WireReview,
  "status" | "token_usage" | "diagrams" | "truncated_files" | "review_contract"
> & {
  status: "pending" | "in_progress" | "completed" | "failed" | "cancelled";
  token_usage?: TokenUsage;
  diagrams?: { type?: string; title?: string; mermaid: string }[];
  truncated_files?: string[];
  /** Routing contract the pipeline ran under. Absent on pre-contract reviews. */
  review_contract?: ReviewContract | null;
  file_count?: number;
  simulation_results?: SimulationResult[];
};

/**
 * Wire ReviewComment, with severity narrowed to its union and state/is_new_finding
 * relaxed to optional so the live-stream reducer can build a partial comment.
 */
export type ReviewComment = Omit<WireReviewComment, "severity" | "state" | "is_new_finding"> & {
  severity?: "critical" | "warning" | "suggestion" | "praise";
  /** Lifecycle state; "suppressed" findings were never posted to the PR. */
  state?: FindingState;
  is_new_finding?: boolean;
};

/** Wire Scenario, but files/modules stay required — the scenarios UI maps over
 *  them directly. last_verdict keeps the generated ScenarioVerdict union. */
export type Scenario = Omit<WireScenario, "files" | "modules"> & {
  files: string[];
  modules: string[];
};

// ── Hand-written: frontend-only or non-generated wire ────────────────────────

export type StageTokens = {
  prompt_tokens: number;
  completion_tokens: number;
  total_tokens: number;
  cost?: number;
  model?: string;
  provider?: string;
  /** Set on entries inside the `review[]` array — the specialist that
   *  produced this review pass (bug_hunter | security | architecture |
   *  regression). Empty for skim single-pass reviews. */
  specialist?: string;
  /** Set on entries inside the `file_synthesis[]` and `simulation[]`
   *  arrays — the file path or scenario descriptor the entry refers to. */
  file?: string;
};

// MemoryIndexedKind is the closed set of values on the `kind` payload field of
// memory_indexed events. One per distinct Supermemory upsert kind.
export type MemoryIndexedKind =
  | "patterns"
  | "patterns_praise"
  | "conventions"
  | "file_synthesis"
  | "pr_summary"
  | "arch_summary"
  | "arch_graph";

export type TokenUsage = {
  intent?: StageTokens;
  triage: StageTokens;
  review: StageTokens[];
  scoring?: StageTokens;
  synthesis?: StageTokens;
  enrichment?: StageTokens;
  conventions?: StageTokens;
  patterns?: StageTokens;
  file_synthesis?: StageTokens[];
  graph?: StageTokens;
  lead_agent?: StageTokens;
  acceptance?: StageTokens;
  cross_pr?: StageTokens;
  simulation?: StageTokens[];
  reply?: StageTokens;
  total: StageTokens;
};

export type SimulationResult = {
  scenario: string;
  passes: boolean;
  confidence: number;
  root_cause: string;
  impact?: string;
  suggestion?: string;
};

export type ProviderKey = {
  id: number;
  installation_id: number;
  provider: string;
  api_key_masked: string;
  base_url?: string;
  repo_id?: number;
  created_at: string;
  updated_at: string;
};

// PromptTemplate is the /prompts endpoint's computed shape (default-vs-custom
// resolved server-side), not the store prompt_templates row — hence hand-written.
export type PromptTemplate = {
  stage: string;
  prompt_text: string;
  is_custom: boolean;
};

export type OpenRouterModel = {
  id: string;
  name: string;
  context_length: number;
  pricing: {
    prompt: string;
    completion: string;
  };
};

export type FileRisk = {
  file_path: string;
  trace_count: number;
  last_trace: string;
};

export type DecisionTrace = {
  id: number;
  repo_id: number;
  file_path: string;
  kind: string;
  summary: string;
  review_id?: string;
  pr_number?: number;
  author?: string;
  created_at: string;
};

export type GraphNode = {
  id: number;
  repo_id: number;
  kind: string;
  name: string;
  file_path: string;
  line_start: number;
  line_end: number;
  language: string;
  pr_number: number | null;
  is_merged: boolean;
};

export type GraphEdge = {
  id: number;
  repo_id: number;
  source_id: number;
  target_id: number;
  kind: string;
  source_name: string;
  target_name: string;
};
