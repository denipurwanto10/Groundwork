-- Migration 017 (up): audit log — one row per successful write.
--
-- The trail answers "who changed what, and when". It is written by the
-- audit middleware (internal/audit), so a handler cannot forget to log: the
-- middleware sees every successful POST/PUT/DELETE.
--
-- The account is not the owner of the row: user_id only points at the
-- account, and it is emptied when the account is deleted. The trail has to
-- outlive the account it talks about, so the identity is copied into
-- user_email as well.
--
-- Re-runnable: the table and its indexes are guarded.

CREATE TABLE IF NOT EXISTS audit_logs (
    id          BIGSERIAL PRIMARY KEY,
    user_id     BIGINT      REFERENCES users (id) ON DELETE SET NULL,
    user_email  VARCHAR(255),
    action      VARCHAR(32)  NOT NULL,
    entity_type VARCHAR(32)  NOT NULL,
    entity_id   BIGINT,
    details     TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The log is read newest first, and usually narrowed to one action or one
-- kind of object.
CREATE INDEX IF NOT EXISTS idx_audit_logs_created ON audit_logs (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_logs_action  ON audit_logs (action, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_logs_entity  ON audit_logs (entity_type, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_logs_user    ON audit_logs (user_id, created_at DESC);
