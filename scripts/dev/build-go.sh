#!/usr/bin/env bash
# Build controller + worker binaries. Mirrors AGENTS.md's Go build block.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"
source ./lib.sh

log_gate "build-go: controller + worker"
go_container controller '
  CGO_ENABLED=0 go build -o bin/controller ./cmd/controller/ &&
  CGO_ENABLED=0 go build -o bin/worker ./cmd/worker/
'
echo "OK: controller/bin/controller, controller/bin/worker"
