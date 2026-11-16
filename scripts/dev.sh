#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

if [[ ! -f deploy/.env ]]; then
  secret="$(openssl rand -base64 32 | tr -d '\n')"
  password="${TACK_DEV_PASSWORD:-tack-dev-$(openssl rand -hex 8)}"
  updater_token="$(openssl rand -hex 32)"
  cat > deploy/.env <<EOF
COMPOSE_PROJECT_NAME=tack
TACK_GHCR_OWNER=today
TACK_IMAGE_TAG=dev
TACK_UPDATER_TAG=dev
TACK_SITE_ADDRESS=:80
TACK_PUBLIC_URL=http://localhost
TACK_ADDR=:8080
TACK_DATA_DIR=/data
TACK_ADMIN_USERNAME=admin
TACK_ADMIN_PASSWORD=${password}
TACK_SESSION_SECRET=${secret}
TACK_GITHUB_REPO=
TACK_GITHUB_TOKEN=
TACK_UPDATER_URL=http://updater:8081
TACK_UPDATER_TOKEN=${updater_token}
TACK_COMPOSE_FILE=/compose/docker-compose.yml
TACK_ENV_FILE=/compose/.env
TACK_SERVICE=app
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
