#!/usr/bin/env bash
# One command that runs the same gates as .github/workflows/test.yml, in the
# same order (lint before test, matching the `needs:` dependency), so "green
# here" predicts "green in CI". Stops at the first failing gate.
#
# Usage: verify.sh [--coverage]
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"

coverage="${1:-}"
failed=""

run_gate() {
  local name="$1"; shift
  if ! "$@"; then
    failed="$name"
    return 1
  fi
}

run_gate "lint" ./lint.sh || { echo ""; echo "FAILED at: lint"; exit 1; }
run_gate "test-go" ./test-go.sh "$coverage" || { echo ""; echo "FAILED at: test-go"; exit 1; }
run_gate "test-ml" ./test-ml.sh || { echo ""; echo "FAILED at: test-ml"; exit 1; }

echo ""
echo "=== verify: all gates passed ==="
