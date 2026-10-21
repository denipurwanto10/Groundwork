-- Migration 008 (up): create users table for JWT authentication.
--
-- Only bcrypt hashes are stored; the plaintext password never reaches the
-- database. Re-runnable: every statement is guarded (IF NOT EXISTS).

CREATE TABLE IF NOT EXISTS users (
    id            BIGSERIAL PRIMARY KEY,
    email         VARCHAR(255) NOT NULL CHECK (char_length(btrim(email)) > 0),
    password_hash VARCHAR(255) NOT NULL CHECK (char_length(password_hash) > 0),
    role          VARCHAR(50)  NOT NULL DEFAULT 'operator'
                  CHECK (role IN ('admin', 'operator', 'viewer')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One account per address, case-insensitive.
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_email_lower ON users (lower(email));
