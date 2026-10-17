-- +goose Up
-- reason: why a notebook reached its current status (the driver's failure
-- message), so it is visible in the record/API/UI instead of only in server
-- logs. image: dropped — no driver ever filled it, and the image already
-- lives in the stored manifest (yaml), which responses read it from.
ALTER TABLE notebook_servers ADD COLUMN reason TEXT NOT NULL DEFAULT '';
ALTER TABLE notebook_history ADD COLUMN reason TEXT NOT NULL DEFAULT '';
ALTER TABLE notebook_servers DROP COLUMN image;
ALTER TABLE notebook_history DROP COLUMN image;

-- +goose Down
ALTER TABLE notebook_history ADD COLUMN image TEXT NOT NULL DEFAULT '';
ALTER TABLE notebook_servers ADD COLUMN image TEXT NOT NULL DEFAULT '';
ALTER TABLE notebook_history DROP COLUMN reason;
ALTER TABLE notebook_servers DROP COLUMN reason;
