CREATE TABLE IF NOT EXISTS model_pricing (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    model_pattern TEXT NOT NULL UNIQUE,
    input_per_million NUMERIC(10,4) NOT NULL,
    output_per_million NUMERIC(10,4) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO model_pricing (model_pattern, input_per_million, output_per_million) VALUES
    ('gpt-5.4', 2.0000, 8.0000),
    ('gpt-4o', 2.5000, 10.0000),
    ('gpt-4o-mini', 0.1500, 0.6000),
    ('gpt-4.1', 2.0000, 8.0000),
    ('gpt-4.1-mini', 0.4000, 1.6000),
    ('gpt-4.1-nano', 0.1000, 0.4000),
    ('o3', 2.0000, 8.0000),
    ('o3-mini', 1.1000, 4.4000),
    ('o4-mini', 1.1000, 4.4000),
    ('claude-sonnet-4-5', 3.0000, 15.0000),
    ('claude-3-5-haiku', 0.8000, 4.0000),
    ('claude-opus-4-5', 15.0000, 75.0000),
    ('claude-sonnet-4-6', 3.0000, 15.0000),
    ('claude-opus-4-6', 15.0000, 75.0000),
    ('claude-haiku-4-5', 0.8000, 4.0000),
    ('deepseek-chat', 0.1400, 0.2800),
    ('deepseek-reasoner', 0.5500, 2.1900),
    ('deepseek-v3', 0.1400, 0.2800),
    ('accounts/fireworks/models/glm-5p1', 0.1000, 0.1000),
    ('qwen/qwen3', 0.2000, 0.6000),
    ('anthropic/claude-sonnet-4.6', 3.0000, 15.0000),
    ('anthropic/claude-opus-4.6', 15.0000, 75.0000),
    ('inception/mercury-2', 0.2500, 1.0000)
ON CONFLICT (model_pattern) DO NOTHING;
