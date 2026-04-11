ALTER TABLE reviews ADD COLUMN IF NOT EXISTS diagrams JSONB DEFAULT '[]'::jsonb;
ALTER TABLE reviews ADD COLUMN IF NOT EXISTS truncated_files JSONB DEFAULT '[]'::jsonb;

UPDATE reviews
SET diagrams = jsonb_build_array(
  jsonb_build_object('type', 'dependency', 'title', COALESCE(diagram_title, 'Architecture'), 'mermaid', diagram)
)
WHERE diagram IS NOT NULL AND diagram != '';
