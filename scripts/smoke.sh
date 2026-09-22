#!/usr/bin/env bash
# Smoke test the running service's probe and version contract.
# Usage: scripts/smoke.sh [base-url]   (default http://127.0.0.1:8080)
#
# Exits non-zero on the first failed check. This is the scripted counterpart of
# the manual curl checks in README.md; CI does not run it (it needs a live
# process), but a deploy pipeline can.
set -euo pipefail

base="${1:-http://127.0.0.1:8080}"

check() {
  local path="$1" want="$2"
  local got
  got="$(curl -sS -o /dev/null -w '%{http_code}' "${base}${path}")"
  if [[ "${got}" != "${want}" ]]; then
    echo "FAIL ${path}: status ${got}, want ${want}" >&2
    exit 1
  fi
  echo "ok   ${path} -> ${got}"
}

check /livez 200
check /readyz 200
check /api/v1/version 200
check /api/v1/does-not-exist 404

body="$(curl -sS "${base}/api/v1/version")"
case "${body}" in
  *'"name":"bloom"'*) echo "ok   /api/v1/version body: ${body}" ;;
  *) echo "FAIL /api/v1/version body: ${body}" >&2; exit 1 ;;
esac
