-- Migration 017 (down): drop the audit log.
DROP TABLE IF EXISTS audit_logs;
