-- +goose Up
ALTER TABLE runs ADD COLUMN IF NOT EXISTS worker_id TEXT NOT NULL DEFAULT '';
ALTER TABLE steps ADD COLUMN IF NOT EXISTS worker_id TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_steps_worker_nonterminal ON steps(worker_id, status);

-- +goose Down
DROP INDEX IF EXISTS idx_steps_worker_nonterminal;
ALTER TABLE runs DROP COLUMN IF EXISTS worker_id;
ALTER TABLE steps DROP COLUMN IF EXISTS worker_id;
