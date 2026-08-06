#!/usr/bin/env bash
# =============================================================================
# scripts/smoke-test.sh — lightweight post-deploy checks against a running host.
# Verifies the public health endpoint answers over HTTPS (or HTTP fallback).
#
# Usage:  DEPLOY_HOST=1.2.3.4 scripts/smoke-test.sh [scheme]
#   scheme  https (default) | http
#
# Kept intentionally minimal: it proves the front door (Caddy) is up and the app
# reports healthy. Expand with API contract checks once /v1/runs is live.
# =============================================================================
set -euo pipefail

HOST="${DEPLOY_HOST:?DEPLOY_HOST is required}"
SCHEME="${1:-https}"
RETRIES="${SMOKE_RETRIES:-10}"
SLEEP="${SMOKE_SLEEP:-6}"

url="${SCHEME}://${HOST}/healthz"
echo "==> Smoke testing ${url}"

for i in $(seq 1 "$RETRIES"); do
  # -k tolerates the not-yet-warm ACME cert on a brand-new host; on staging Caddy
  # may still be provisioning. Prefer strict verification once DNS is stable.
  code=$(curl -ksS -o /dev/null -w '%{http_code}' --max-time 10 "$url" || echo 000)
  if [[ "$code" == "200" ]]; then
    echo "==> Healthy (HTTP $code)"
    exit 0
  fi
  echo "  attempt ${i}/${RETRIES} — got HTTP ${code}"
  sleep "$SLEEP"
done

echo "!! Smoke test failed: ${url} never returned 200." >&2
exit 1
