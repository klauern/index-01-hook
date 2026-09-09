ALTER TABLE recordings ADD COLUMN evaluation_expires_at_ms INTEGER;

-- Preserve known deadlines. Do not recapture older rows whose evidence is absent.
UPDATE recordings SET evaluation_expires_at_ms = COALESCE(
    (SELECT expires_at_ms FROM evaluation_evidence
     WHERE recording_fingerprint = recordings.payload_fingerprint), 0);
