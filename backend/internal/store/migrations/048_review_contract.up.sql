-- ReviewContract per review: change class, evidence bar, depth, signals.
-- Written by the orchestrator's pre-post persist; read by the dashboard.
ALTER TABLE reviews ADD COLUMN IF NOT EXISTS review_contract JSONB;
