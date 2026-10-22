-- Migration 026 (down): drop the Web Push subscriptions again.
--
-- The table goes back entirely: a subscription only means something to the
-- push service, and without the VAPID keys the server sends nothing anyway.

DROP TRIGGER IF EXISTS trg_push_subscriptions_updated_at ON push_subscriptions;
DROP FUNCTION IF EXISTS set_push_subscriptions_updated_at();
DROP TABLE IF EXISTS push_subscriptions;
