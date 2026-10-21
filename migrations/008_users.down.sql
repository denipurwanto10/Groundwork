-- Migration 008 (down): drop users table.
--
-- Destroys every account: nobody can log in afterwards.

DROP TABLE IF EXISTS users;
