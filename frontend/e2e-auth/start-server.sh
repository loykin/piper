#!/bin/sh
# Starts `piper server` with built-in authentication on a brand-new SQLite
# database, so every run begins at the "create the admin account" screen.
set -eu
dir=$(mktemp -d "${TMPDIR:-/tmp}/piper-auth-e2e.XXXXXX")
trap 'rm -rf "$dir"' EXIT INT TERM
cat > "$dir/piper.yaml" <<YAML
version: 4
server:
  http_addr: "127.0.0.1:18081"
  allow_insecure_dev_key: true
  data_dir: $dir/data
  db:
    driver: sqlite
    path: $dir/piper.db
runtime:
  type: baremetal
YAML
cd "$(dirname "$0")/../.."
go run ./cmd/piper server --config "$dir/piper.yaml"
