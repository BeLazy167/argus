export type Installation = {
  id: number;
  installation_id: number;
  org_login: string;
  created_at: string;
  suspended_at?: string;
};

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
};

export type TokenUsage = {
  triage: StageTokens;
  review: StageTokens[];
  scoring?: StageTokens;
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
  github_review_id?: number;
  status: "pending" | "in_progress" | "completed" | "failed";
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
  created_at: string;
  completed_at?: string;
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

export type ModelConfig = {
  id: number;
  repo_id?: number;
  stage: string;
  provider: string;
  model: string;
  base_url?: string;
  max_tokens: number;
  temperature: number;
  created_at: string;
  updated_at: string;
};

export type Stats = {
  total_reviews: number;
  completed_today: number;
  avg_score: number;
  active_repos: number;
  critical_finds: number;
  pending_reviews: number;
  catch_rate: number;
  prs_this_week: number;
  high_risk_count: number;
  avg_review_time_ms: number;
  deep_review_count: number;
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

export type UserInstallation = {
  id: number;
  installation_id: number;
  role: string;
  created_at: string;
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
