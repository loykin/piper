-- +goose Up
ALTER TABLE projects ADD COLUMN owner_member_id TEXT NOT NULL DEFAULT 'member-local';
CREATE INDEX idx_projects_owner_member ON projects(owner_member_id);

-- +goose Down
DROP INDEX IF EXISTS idx_projects_owner_member;
ALTER TABLE projects DROP COLUMN owner_member_id;
