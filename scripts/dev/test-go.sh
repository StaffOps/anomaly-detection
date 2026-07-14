#!/usr/bin/env bash
# Run the Go test suite. Mirrors .github/workflows/test.yml `test-go` job exactly
# (same package scope, same coverage invocation) so "green locally" predicts
# "green in CI".
#
# Usage: test-go.sh [--coverage]
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"
source ./lib.sh

coverage="${1:-}"

if [ "$coverage" = "--coverage" ]; then
  log_gate "test-go: ./internal/... with coverage"
  go_container controller '
    go test ./internal/... -coverprofile=cov.out -covermode=atomic -timeout 120s
    go tool cover -func=cov.out | tail -1
  '
else
  log_gate "test-go: ./..."
  go_container controller 'go test ./... -timeout 120s'
fi
