-- BYOK model override for the embeddings provider slot. TEI/Ollama-style
-- endpoints serve whatever model they were launched with regardless of the
-- request's model field, so a base_url override MUST be able to declare its
-- true model — memories.embedding_model is stamped from it, and every
-- similarity floor is calibrated per embedding space. NULL = use the platform
-- default model.
ALTER TABLE provider_keys ADD COLUMN model TEXT;
