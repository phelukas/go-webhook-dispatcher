ALTER TABLE webhook_submissions
    ADD COLUMN attempt_count INTEGER NOT NULL DEFAULT 0
        CHECK (attempt_count >= 0),
    ADD COLUMN locked_by TEXT,
    ADD COLUMN locked_at TIMESTAMPTZ,
    ADD COLUMN lease_token UUID,
    ADD COLUMN last_error TEXT,
    ADD COLUMN completed_at TIMESTAMPTZ;

UPDATE webhook_submissions
SET status = 'pending'
WHERE status = 'processing';

ALTER TABLE webhook_submissions
    ADD CONSTRAINT webhook_submissions_processing_lease_check
    CHECK (
        (
            status = 'processing'
            AND locked_by IS NOT NULL
            AND locked_at IS NOT NULL
            AND lease_token IS NOT NULL
        )
        OR
        (
            status <> 'processing'
            AND locked_by IS NULL
            AND locked_at IS NULL
            AND lease_token IS NULL
        )
    );

CREATE INDEX webhook_submissions_pending_idx
    ON webhook_submissions (created_at)
    WHERE status = 'pending';

CREATE INDEX webhook_submissions_processing_lease_idx
    ON webhook_submissions (locked_at)
    WHERE status = 'processing';
