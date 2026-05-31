-- +goose Up
ALTER TABLE notebook_servers ADD COLUMN env TEXT NOT NULL DEFAULT '';
ALTER TABLE notebook_servers DROP COLUMN image;
ALTER TABLE notebook_servers DROP COLUMN namespace;

-- +goose Down
ALTER TABLE notebook_servers DROP COLUMN env;
ALTER TABLE notebook_servers ADD COLUMN image     TEXT NOT NULL DEFAULT '';
ALTER TABLE notebook_servers ADD COLUMN namespace TEXT NOT NULL DEFAULT '';
