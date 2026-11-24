#!/usr/bin/env bash
# Preflight: validates the environment before an agent (or human) starts work,
# so failures surface immediately instead of mid-run. Exits non-zero with an
# actionable message per missing prerequisite.
set -uo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"
source ./lib.sh

problems=0

check() {
  local desc="$1"; shift
  printf '  %-45s' "$desc"
  if "$@" >/dev/null 2>&1; then
    echo "OK"
  else
    echo "MISSING"
    problems=$((problems + 1))
  fi
}

echo "== docker =="
check "docker daemon reachable" docker info

echo ""
echo "== git hooks (docs-with-code pre-commit gate) =="
want=".githooks"
have="$(git -C "$(repo_root)" config core.hooksPath || true)"
if [ "$have" = "$want" ]; then
  echo "  core.hooksPath = .githooks                   OK"
else
  git -C "$(repo_root)" config core.hooksPath "$want"
  echo "  core.hooksPath set to .githooks              FIXED"
fi

echo ""
echo "== go modules (staffops-otel-libs is public — no token needed) =="
# Sanity-check the pinned module resolves with the same direct-fetch settings the
# build uses (see GO_MODULE_ENV in lib.sh). A failure here is network, not auth.
if docker run --rm "${GO_MODULE_ENV[@]}" "$GO_IMAGE" sh -c \
    'go mod download github.com/staffops/staffops-otel-libs/go@v0.2.1 >/dev/null 2>&1'; then
  echo "  staffops-otel-libs fetchable (direct)        OK"
else
  echo "  staffops-otel-libs fetchable (direct)        MISSING"
  echo "    -> check network access to github.com (module is public)."
  problems=$((problems + 1))
fi

echo ""
echo "== local stack (.env) — only required for scripts/start.sh =="
env_file="$(repo_root)/.env"
if [ -f "$env_file" ]; then
  missing_vars=""
  for var in PROMETHEUS_URL LOKI_URL ALERTMANAGER_URL; do
    if ! grep -qE "^${var}=.+" "$env_file"; then
      missing_vars="${missing_vars} ${var}"
    fi
  done
  if [ -z "$missing_vars" ]; then
    echo "  .env present with required vars               OK"
  else
    echo "  .env present but missing:${missing_vars}"
    echo "    -> only blocks scripts/start.sh / synthetic-injection Phase 5, not verify.sh"
  fi
else
  echo "  .env not found (copy .env.example)             SKIPPED"
  echo "    -> only needed for scripts/start.sh or running replay against real endpoints"
fi

echo ""
if [ "$problems" -eq 0 ]; then
  echo "=== doctor: ready ==="
  exit 0
else
  echo "=== doctor: ${problems} problem(s) found — see MISSING lines above ==="
  exit 1
fi
