#!/usr/bin/env bash
# gofmt + go vet (Go) and ruff (ML). Mirrors .github/workflows/test.yml's
# `lint-go` and `lint-ml` jobs exactly — same commands, same exclusions.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"
source ./lib.sh

log_gate "lint: gofmt (controller/)"
go_container controller '
  unformatted=$(gofmt -l .)
  if [ -n "$unformatted" ]; then
    echo "gofmt found unformatted files:"; echo "$unformatted"; exit 1
  fi
  echo "OK: gofmt clean"
'

log_gate "lint: go vet (controller/)"
go_container controller 'go vet ./...'
echo "OK: go vet clean"

log_gate "lint: ruff (ml/)"
docker run --rm \
  -v "$(repo_root)/ml:/app" \
  -w /app \
  python:3.11-slim sh -c "
    pip install -q ruff &&
    ruff check . --exclude 'server/generated/*'
  "
echo "OK: ruff clean"
