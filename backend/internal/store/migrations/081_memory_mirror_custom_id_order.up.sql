-- Mirror transitions from different relational aggregates can target the same
-- deterministic memory row. Support the pending-event predecessor check that
-- serializes those transitions by tenant and payload custom ID.
CREATE INDEX memory_mirror_outbox_custom_id_pending_idx
  ON memory_mirror_outbox (installation_id, (NULLIF(payload->>'custom_id', '')), id)
  WHERE processed_at IS NULL;
