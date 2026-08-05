#!/usr/bin/env bash
# =============================================================================
# scripts/deploy.sh — roll a droplet forward to a pinned image tag over SSH,
# health-gate the result, and auto-rollback to the previous tag on failure.
#
# Usage:   scripts/deploy.sh <target> <environment>
#   target       do            (only DigitalOcean droplet is implemented today)
#   environment  staging | production
#
# Required environment (exported by the deploy workflow / your shell):
#   DEPLOY_HOST        droplet IP or hostname
#   DEPLOY_USER        SSH user (default: deploy)
#   IMAGE_NAMESPACE    Docker Hub namespace the images live under
#   IMAGE_TAG          the tag to roll to (e.g. sha-abc1234 or v1.2.3)
#
# The compose file for <target> is copied to the host and run there. The host
# keeps its own secrets in /opt/nexus/.env (seeded once by bootstrap-droplet.sh).
# =============================================================================
set -euo pipefail

TARGET="${1:?usage: deploy.sh <do> <staging|production>}"
ENVIRONMENT="${2:?usage: deploy.sh <do> <staging|production>}"

DEPLOY_USER="${DEPLOY_USER:-deploy}"
DEPLOY_HOST="${DEPLOY_HOST:?DEPLOY_HOST is required}"
IMAGE_NAMESPACE="${IMAGE_NAMESPACE:?IMAGE_NAMESPACE is required}"
IMAGE_TAG="${IMAGE_TAG:?IMAGE_TAG is required}"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

case "$TARGET" in
  do) COMPOSE_SRC="$REPO_ROOT/deploy/do/docker-compose.prod.yml"
      CADDY_SRC="$REPO_ROOT/deploy/do/Caddyfile" ;;
  *)  echo "Unsupported target '$TARGET' (only 'do' is implemented)." >&2; exit 2 ;;
esac

REMOTE_DIR="/opt/nexus"
SSH_OPTS=(-o StrictHostKeyChecking=accept-new -o ConnectTimeout=15)
SSH="ssh ${SSH_OPTS[*]} ${DEPLOY_USER}@${DEPLOY_HOST}"

echo "==> Deploying ${IMAGE_NAMESPACE}/*:${IMAGE_TAG} to ${ENVIRONMENT} (${DEPLOY_HOST})"

# 1. Ship the compose file + Caddyfile (config is versioned in git, not on host).
echo "==> Syncing compose config"
# shellcheck disable=SC2086
scp ${SSH_OPTS[*]} "$COMPOSE_SRC" "${DEPLOY_USER}@${DEPLOY_HOST}:${REMOTE_DIR}/docker-compose.prod.yml"
# shellcheck disable=SC2086
scp ${SSH_OPTS[*]} "$CADDY_SRC" "${DEPLOY_USER}@${DEPLOY_HOST}:${REMOTE_DIR}/Caddyfile"

# 2. Run the health-gated cutover on the host. The whole flow is a single remote
#    script so the previous tag (for rollback) is captured atomically.
# shellcheck disable=SC2086
$SSH IMAGE_NAMESPACE="$IMAGE_NAMESPACE" IMAGE_TAG="$IMAGE_TAG" ENVIRONMENT="$ENVIRONMENT" 'bash -s' <<'REMOTE'
set -euo pipefail
cd /opt/nexus

COMPOSE="docker compose -f docker-compose.prod.yml --env-file .env"

if [[ ! -f .env ]]; then
  echo "FATAL: /opt/nexus/.env missing on host — run bootstrap-droplet.sh first." >&2
  exit 1
fi

# Record the currently-running control image tag so we can roll back to it.
PREV_TAG="$(docker inspect --format '{{ index .Config.Labels "nexus.image_tag" }}' \
             "$(${COMPOSE} ps -q nexus-control 2>/dev/null || true)" 2>/dev/null || true)"
# Fall back to the tag persisted from the last successful deploy.
if [[ -z "${PREV_TAG:-}" && -f .last_good_tag ]]; then PREV_TAG="$(cat .last_good_tag)"; fi
echo "Previous good tag: ${PREV_TAG:-<none>}"

# Persist the target tag into .env (IMAGE_TAG/IMAGE_NAMESPACE lines managed here).
grep -v -E '^(IMAGE_TAG|IMAGE_NAMESPACE)=' .env > .env.next || true
{ echo "IMAGE_NAMESPACE=${IMAGE_NAMESPACE}"; echo "IMAGE_TAG=${IMAGE_TAG}"; } >> .env.next
mv .env.next .env

roll_to() {
  local tag="$1"
  sed -i -E "s/^IMAGE_TAG=.*/IMAGE_TAG=${tag}/" .env
  ${COMPOSE} pull
  ${COMPOSE} up -d --remove-orphans
}

echo "==> Pulling + starting ${IMAGE_TAG}"
roll_to "${IMAGE_TAG}"

# Health gate: poll the app health endpoint through the running container.
echo "==> Health gate"
ok=0
for i in $(seq 1 30); do
  if docker run --rm --network container:"$(${COMPOSE} ps -q nexus-control)" \
        curlimages/curl:latest -fsS http://localhost:8080/healthz >/dev/null 2>&1; then
    ok=1; break
  fi
  # Fallback: exec wget inside the control container (no extra image needed).
  if ${COMPOSE} exec -T nexus-control wget -qO- http://localhost:8080/healthz >/dev/null 2>&1; then
    ok=1; break
  fi
  echo "  attempt ${i}/30 — not healthy yet"; sleep 5
done

if [[ "$ok" == "1" ]]; then
  echo "==> Healthy. Marking ${IMAGE_TAG} as last-good."
  echo "${IMAGE_TAG}" > .last_good_tag
  docker image prune -f >/dev/null 2>&1 || true
  exit 0
fi

echo "!! Health gate FAILED for ${IMAGE_TAG}."
if [[ -n "${PREV_TAG:-}" && "${PREV_TAG}" != "${IMAGE_TAG}" ]]; then
  echo "==> Rolling back to ${PREV_TAG}"
  roll_to "${PREV_TAG}"
  echo "Rolled back to ${PREV_TAG}."
else
  echo "No previous good tag to roll back to — leaving failed release up for inspection."
fi
exit 1
REMOTE

rc=$?
if [[ $rc -ne 0 ]]; then
  echo "==> Deploy failed (rc=$rc)."
  exit $rc
fi

# 3. External smoke test from the runner's perspective (through Caddy/public).
if [[ -x "$REPO_ROOT/scripts/smoke-test.sh" ]]; then
  echo "==> External smoke test"
  DEPLOY_HOST="$DEPLOY_HOST" "$REPO_ROOT/scripts/smoke-test.sh" || {
    echo "==> Smoke test failed after a healthy cutover — investigate." >&2
    exit 1
  }
fi

echo "==> Deploy to ${ENVIRONMENT} complete: ${IMAGE_TAG}"
