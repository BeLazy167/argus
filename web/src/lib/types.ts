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
  error?: string;
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
  created_at: string;
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
};

export type ActivityLog = {
  id: number;
  action: string;
  actor?: string;
  resource?: string;
  metadata?: Record<string, unknown>;
  created_at: string;
};
