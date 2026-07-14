#!/usr/bin/env bash
# Run the ML test suite. Mirrors .github/workflows/test.yml `test-ml` job
# (PH.10 done — real tests, ~98% coverage). Same install + pytest invocation
# so "green here" predicts "green in CI".
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"
source ./lib.sh

log_gate "test-ml: pytest"
python_container ml "
  pip install -e '.[dev]' -q
  pytest tests/ -q
"
