-- Structured near-miss findings rendered in the review's Minor Notes block.
CREATE TABLE review_minor_notes (
    id                  UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    review_id           UUID NOT NULL REFERENCES reviews(id) ON DELETE CASCADE,
    attempt_generation  INT NOT NULL CHECK (attempt_generation > 0),
    file_path           TEXT NOT NULL,
    line                INT NOT NULL CHECK (line >= 0),
    severity            TEXT NOT NULL CHECK (severity IN ('critical','warning','suggestion','praise')),
    title               TEXT NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_review_minor_notes_current_attempt
    ON review_minor_notes(review_id, attempt_generation, file_path, line);
