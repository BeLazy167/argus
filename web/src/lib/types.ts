import type { FindingState } from "./generated/api-types";
import type { ReviewContract } from "./generated/contract-types";

// Wire types generated from Go structs by tygo (see backend/tygo.yaml).
// Re-exported here so consumers keep importing everything from "@/lib/types".
// Regenerate after changing a Go wire struct: `cd backend && make tygo`.
// FindingState and ReviewContract are also used internally below.
export type { FindingState, ReviewContract };
export type { GaugeRow, Installation, ModelConfig, Stats } from "./generated/api-types";

// The types below stay hand-written: the wire transforms them (masked/derived
// fields) or the frontend refines them (JSONB shapes, unions the Go side leaves
// as plain strings).
export type Repo = {
  id: number;
  installation_id: number;
  github_id: number;
  full_name: string;
  default_branch: string;
  enabled: boolean;
  settings_json: Record<string, unknown>;
  created_at: string;
  updated_at: string;
};

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

export type Review = {
  id: string;
  repo_id: number;
  pr_number: number;
  pr_title: string;
  pr_author: string;
  head_sha: string;
  base_sha: string;
  head_ref: string;
  github_review_id?: number;
  status: "pending" | "in_progress" | "completed" | "failed" | "cancelled";
  summary?: string;
  score?: number;
  trigger: string;
  triggered_by?: string;
  duration_ms?: number;
  token_usage?: TokenUsage;
  error?: string;
  file_count?: number;
  deep_review?: boolean;
  persona?: string;
  is_incremental?: boolean;
  simulation_results?: SimulationResult[];
  diagram?: string;
  diagram_title?: string;
  diagrams?: { type?: string; title?: string; mermaid: string }[];
  truncated_files?: string[];
  /**
   * LLM-generated conversational verdict from synthesis. Posted to the GitHub
   * PR comment body; also surfaced on the dashboard Summary card as the
   * primary readout (preferred over the raw per-file `summary`).
   */
  brief?: string;
  /** Routing contract the pipeline ran under. Absent on pre-contract reviews. */
  review_contract?: ReviewContract | null;
  created_at: string;
  completed_at?: string;
};

export type SimulationResult = {
  scenario: string;
  passes: boolean;
  confidence: number;
  root_cause: string;
  impact?: string;
  suggestion?: string;
};

export type ReviewComment = {
  id: string;
  review_id: string;
  file_path: string;
  start_line?: number;
  end_line?: number;
  side?: string;
  body: string;
  severity?: "critical" | "warning" | "suggestion" | "praise";
  category?: string;
  specialist?: string;
  confidence_score?: number;
  code_snippet?: string;
  github_comment_id?: number;
  matched_pattern_id?: number;
  matched_pattern_score?: number;
  enforced_rule_content?: string;
  is_new_finding?: boolean;
  /** Lifecycle state; "suppressed" findings were never posted to the PR. */
  state?: FindingState;
  /** Why the finding was suppressed (e.g. "team_feedback:3", "dismissed_match:0.91"). */
  suppressed_reason?: string;
  created_at: string;
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

export type Rule = {
  id: number;
  category: string;
  content: string;
  priority: number;
  enabled: boolean;
  created_at: string;
  updated_at: string;
};

export type ActivityLog = {
  id: number;
  action: string;
  actor?: string;
  resource?: string;
  metadata?: Record<string, unknown>;
  created_at: string;
};

export type Pattern = {
  id: number;
  installation_id: number;
  repo_id?: number;
  content: string;
  supermemory_id?: string;
  created_by?: string;
  source?: string;
  category?: string;
  pr_number?: number;
  created_at: string;
  updated_at: string;
};

export type PatternStat = {
  week: string;
  source: string;
  count: number;
};

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

export type ScenarioVerdict = "broken" | "fixed" | "partial" | "unclear";

export type Scenario = {
  id: number;
  installation_id: number;
  repo_id: number;
  description: string;
  source: string;
  source_ref: string;
  files: string[];
  modules: string[];
  severity: string;
  active: boolean;
  created_at: string;
  steps: { action: string; hint?: string }[];
  initial_state: string;
  expected_outcome: string;
  is_outdated: boolean;
  last_run_at?: string;
  last_verdict?: ScenarioVerdict;
  last_confidence?: number;
  last_why?: string;
  last_fix?: string;
  last_pr_number?: number;
  last_review_id?: string;
  trigger_count?: number;
};

export type ScenarioRun = {
  id: number;
  scenario_id: number;
  review_id: string;
  pr_number: number;
  verdict: ScenarioVerdict;
  confidence: number;
  why?: string;
  fix?: string;
  root_cause?: string;
  impact?: string;
  created_at: string;
};

export type ScenarioKPIs = {
  active: number;
  broken_this_week: number;
  fixed_this_week: number;
  outdated: number;
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
