-- Shadow metrics are operational aggregates; private answers remain in evidence_json.
ALTER TABLE typesafe_shadow_verifications ADD COLUMN latency_ms INTEGER CHECK (latency_ms >= 0);
ALTER TABLE typesafe_shadow_verifications ADD COLUMN input_tokens INTEGER CHECK (input_tokens >= 0);
