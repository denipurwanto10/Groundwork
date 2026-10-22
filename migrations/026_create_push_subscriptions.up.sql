-- Migration 026 (up): Web Push subscriptions.
--
-- The desktop notifications of the app are raised by the service worker of
-- the browser. As long as the tab stays open that is enough — but the moment
-- the tab is closed, only the push service of the browser can still reach
-- the operator. To do that the server has to know where to send the
-- message: this table holds the address of every browser that asked for it.
--
-- One row is one browser. The endpoint of a push subscription is unique, so
-- a browser that subscribes twice keeps one row (the second call refreshes
-- it). One account may hold many rows: every browser and every device of
-- that account has its own subscription.
--
-- `user_id` points at `users` with ON DELETE CASCADE: a deleted account
-- takes its subscriptions with it, so a push is never sent to somebody
-- who no longer exists.
--
-- The two key columns hold the secrets of the subscription (the public key
-- of the browser and the shared secret). They are never written to the log
-- and never leave the server.

CREATE TABLE IF NOT EXISTS push_subscriptions (
    id           BIGSERIAL PRIMARY KEY,
    user_id      BIGINT      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    endpoint     TEXT        NOT NULL UNIQUE,
    p256dh       TEXT        NOT NULL,
    auth         TEXT        NOT NULL,
    user_agent   TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The subscriptions of one account are asked for by name: the account that
-- signed in is the one that receives its own messages.
CREATE INDEX IF NOT EXISTS idx_push_subscriptions_user ON push_subscriptions (user_id);

-- Keep updated_at correct even if a future UPDATE forgets to set it.
CREATE OR REPLACE FUNCTION set_push_subscriptions_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_push_subscriptions_updated_at ON push_subscriptions;
CREATE TRIGGER trg_push_subscriptions_updated_at
    BEFORE UPDATE ON push_subscriptions
    FOR EACH ROW
    EXECUTE FUNCTION set_push_subscriptions_updated_at();
