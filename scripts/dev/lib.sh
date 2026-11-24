#!/usr/bin/env bash
# Shared helpers for scripts/dev/*. Source, don't execute.
#
# Extracted from AGENTS.md's docker one-liners so there is exactly one place
# that knows how to run a container against controller/ or ml/. No local
# Go/Python — every operation runs in a container (repo convention).
# staffops-otel-libs is public, but its history was rewritten (git filter-repo,
# 2026-10-28): the go/v0.2.x tags no longer match sum.golang.org and the Go proxy
# does not serve it. GOPRIVATE/GONOSUMDB fetch it directly from GitHub (the Debian
# golang image ships git) and skip the checksum DB; go.sum pins the current hash.
# Remove once we move to otel-libs 0.3.0 (module path github.com/staffops/otel-libs/go).
GO_IMAGE="golang:1.26"
GO_MODULE_ENV=(-e "GOPRIVATE=github.com/staffops/*" -e "GONOSUMDB=github.com/staffops/*")

set -euo pipefail

# repo_root: absolute path to the repo root, regardless of cwd.
repo_root() {
  cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd
}

# go_container <mount-subdir> <shell-command>
# Runs `$GO_IMAGE` against the mounted subdir. Mirrors AGENTS.md's Go
# build/test blocks and .github/workflows/test.yml (same Go version).
go_container() {
  local subdir="$1" cmd="$2"
  local root
  root="$(repo_root)"
  docker run --rm \
    -v "${root}/${subdir}:/src" \
    -w /src \
    "${GO_MODULE_ENV[@]}" \
    "$GO_IMAGE" sh -c "$cmd"
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
