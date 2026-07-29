-- +goose Up
ALTER TABLE users RENAME COLUMN email TO username;
ALTER TABLE login_history RENAME COLUMN email TO username;

-- +goose Down
ALTER TABLE login_history RENAME COLUMN username TO email;
ALTER TABLE users RENAME COLUMN username TO email;
