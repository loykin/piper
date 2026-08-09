-- +goose Up
ALTER TABLE runs ADD COLUMN worker_id TEXT NOT NULL DEFAULT '';
ALTER TABLE steps ADD COLUMN worker_id TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_steps_worker_nonterminal ON steps(worker_id, status);

-- +goose Down
DROP INDEX IF EXISTS idx_steps_worker_nonterminal;
-- modernc.org/sqlite (this project's driver) bundles SQLite 3.35.0+, which
-- supports ALTER TABLE DROP COLUMN — unlike 00013_worker_id.sql's Down
-- (an older, pre-existing migration in this repo), this one actually drops
-- the columns so a down-then-up cycle doesn't fail on "duplicate column
-- name" the second time Up runs.
ALTER TABLE runs DROP COLUMN worker_id;
ALTER TABLE steps DROP COLUMN worker_id;
