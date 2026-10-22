-- Migration 021 (up): work orders — the task a unit is sent out for.
--
-- A vehicle was driving without a job. This table holds the job: what has to
-- be done, who does it, where it goes and by when.
--
-- `place_id` points at `entities`: the places of the app are the destinations
-- of the field work (the "Places" tab).
--
-- `assigned_user_id` points at `users`: the account that has to do the work.
-- Both references are ON DELETE SET NULL: removing a driver or a place does
-- not throw away the history of the work orders, it only leaves them
-- unassigned.
--
-- The CHECK constraints mirror the validation in internal/task, so a value the
-- API rejects can never reach the database either.

CREATE TABLE IF NOT EXISTS tasks (
    id               BIGSERIAL PRIMARY KEY,
    title            VARCHAR(160)  NOT NULL CHECK (char_length(btrim(title)) > 0),
    description      VARCHAR(2000) NOT NULL DEFAULT '',
    status           VARCHAR(20)   NOT NULL DEFAULT 'pending'
                     CHECK (status IN ('pending', 'in_progress', 'completed')),
    assigned_user_id BIGINT        REFERENCES users (id)    ON DELETE SET NULL,
    place_id         BIGINT        REFERENCES entities (id) ON DELETE SET NULL,
    due_date         DATE,
    created_at       TIMESTAMPTZ   NOT NULL DEFAULT now()
);

-- The board reads the tasks of one status, newest first.
CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks (status, created_at DESC);

-- A driver only ever sees the tasks that are assigned to them.
CREATE INDEX IF NOT EXISTS idx_tasks_assigned_user ON tasks (assigned_user_id, created_at DESC);

-- The places that still have open work are asked for by name.
CREATE INDEX IF NOT EXISTS idx_tasks_place ON tasks (place_id);
