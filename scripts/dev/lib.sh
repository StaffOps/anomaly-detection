#!/usr/bin/env bash
# Shared helpers for scripts/dev/*. Source, don't execute.
#
# Extracted from AGENTS.md's docker one-liners so there is exactly one place
# that knows how to run a container against controller/ or ml/. No local
# Go/Python — every operation runs in a container (repo convention).
# staffops-otel-libs is a public org module, pulled via the default Go proxy —
# no token, no GOPRIVATE, no git credential wiring.

set -euo pipefail

# repo_root: absolute path to the repo root, regardless of cwd.
repo_root() {
  cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd
}

# go_container <mount-subdir> <shell-command>
# Runs `golang:1.25-alpine` against the mounted subdir. Mirrors AGENTS.md's Go
# build/test blocks and .github/workflows/test.yml (same Go version).
go_container() {
  local subdir="$1" cmd="$2"
  local root
  root="$(repo_root)"
  docker run --rm \
    -v "${root}/${subdir}:/src" \
    -w /src \
    golang:1.25-alpine sh -c "$cmd"
}

# python_container <mount-subdir> <shell-command>
# Runs `python:3.11-slim` — no private-module auth needed on the ML side.
python_container() {
  local subdir="$1" cmd="$2"
  local root
  root="$(repo_root)"
  docker run --rm \
    -v "${root}/${subdir}:/app" \
    -w /app \
    python:3.11-slim sh -c "$cmd"
}

log_gate() {
  echo ""
  echo "==> $1"
}
