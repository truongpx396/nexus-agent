#!/usr/bin/env bash
# =============================================================================
# scripts/bootstrap-droplet.sh — one-time host prep for a fresh DO droplet.
# Run as root on the droplet (Terraform's cloud-init installs Docker; this adds
# the deploy user, the compose directory, and the nexus-sandboxd privilege
# boundary described in deploy/mini/README.md).
#
# Usage (on the droplet):
#   curl -fsSL <raw>/scripts/bootstrap-droplet.sh | sudo bash -s -- <deploy-pubkey>
# or copy the file over and:  sudo bash bootstrap-droplet.sh "ssh-ed25519 AAAA..."
# =============================================================================
set -euo pipefail

DEPLOY_USER="${DEPLOY_USER:-deploy}"
DEPLOY_PUBKEY="${1:-}"
REMOTE_DIR="/opt/nexus"

if [[ "$(id -u)" -ne 0 ]]; then
  echo "Run as root (sudo)." >&2; exit 1
fi

echo "==> Ensuring Docker is present"
if ! command -v docker >/dev/null 2>&1; then
  curl -fsSL https://get.docker.com | sh
fi
systemctl enable --now docker

echo "==> Creating deploy user '${DEPLOY_USER}'"
if ! id "$DEPLOY_USER" >/dev/null 2>&1; then
  useradd -m -s /bin/bash "$DEPLOY_USER"
fi
usermod -aG docker "$DEPLOY_USER"

if [[ -n "$DEPLOY_PUBKEY" ]]; then
  install -d -m 700 -o "$DEPLOY_USER" -g "$DEPLOY_USER" "/home/${DEPLOY_USER}/.ssh"
  echo "$DEPLOY_PUBKEY" > "/home/${DEPLOY_USER}/.ssh/authorized_keys"
  chown "$DEPLOY_USER:$DEPLOY_USER" "/home/${DEPLOY_USER}/.ssh/authorized_keys"
  chmod 600 "/home/${DEPLOY_USER}/.ssh/authorized_keys"
fi

echo "==> Preparing ${REMOTE_DIR}"
install -d -m 755 -o "$DEPLOY_USER" -g "$DEPLOY_USER" "$REMOTE_DIR"
install -d -m 755 /run/nexus

if [[ ! -f "${REMOTE_DIR}/.env" ]]; then
  cat > "${REMOTE_DIR}/.env" <<'ENV'
# Seed real secrets here, then `chmod 600` and `chown deploy:deploy`.
# IMAGE_NAMESPACE / IMAGE_TAG are overwritten by scripts/deploy.sh on each deploy.
IMAGE_NAMESPACE=your-dockerhub-namespace
IMAGE_TAG=latest
APP_DOMAIN=app.example.com
ACME_EMAIL=ops@example.com
POSTGRES_PASSWORD=change-me-strong
VAULT_TOKEN=change-me-strong
OIDC_ISSUER=https://app.example.com/auth
OIDC_CLIENT_ID=nexus-agent
OIDC_CLIENT_SECRET=change-me
SANDBOX_BROKER_SOCKET=/run/nexus/sandboxd.sock
ENV
  chmod 600 "${REMOTE_DIR}/.env"
  chown "$DEPLOY_USER:$DEPLOY_USER" "${REMOTE_DIR}/.env"
  echo "   -> Wrote ${REMOTE_DIR}/.env template. EDIT IT with real secrets before deploying."
fi

cat <<'NOTE'

==> Bootstrap complete.

NEXT STEPS (manual, on purpose):
  1. Edit /opt/nexus/.env with real secrets (POSTGRES_PASSWORD, VAULT_TOKEN,
     OIDC_*, APP_DOMAIN, ACME_EMAIL).
  2. Install the nexus-sandboxd host daemon as a systemd unit so the app never
     holds a container-runtime socket (see deploy/mini/README.md — FR-059).
     Do NOT bind-mount /var/run/docker.sock into the app as a shortcut.
  3. Push to main (staging) or tag v*.*.* (production) to let CI deploy here.
NOTE
