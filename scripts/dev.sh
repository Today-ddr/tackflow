#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

if [[ ! -f deploy/.env ]]; then
  secret="$(openssl rand -base64 32 | tr -d '\n')"
  password="${TACK_DEV_PASSWORD:-tack-dev-$(openssl rand -hex 8)}"
  cat > deploy/.env <<EOF
COMPOSE_PROJECT_NAME=tack
TACK_GHCR_OWNER=today
TACK_IMAGE_TAG=dev
TACK_SITE_ADDRESS=:80
TACK_PUBLIC_URL=http://localhost
TACK_ADDR=:8080
TACK_DATA_DIR=/data
TACK_ADMIN_USERNAME=admin
TACK_ADMIN_PASSWORD=${password}
TACK_SESSION_SECRET=${secret}
TACK_UID=$(id -u)
TACK_GID=$(id -g)
EOF
  chmod 600 deploy/.env
fi

set -a
# shellcheck disable=SC1091
. deploy/.env
set +a

export TACK_PUBLIC_URL="http://localhost:5173"
export TACK_ADDR=":8080"
export TACK_DATA_DIR="./data"

mkdir -p data

pnpm -C web dev &
web_pid=$!
go run ./cmd/tack &
app_pid=$!

cleanup() {
  kill "$web_pid" "$app_pid" 2>/dev/null || true
  wait "$web_pid" "$app_pid" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

wait "$web_pid" "$app_pid"
