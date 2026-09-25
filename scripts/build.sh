#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"

echo "==> Building frontend..."
cd "$ROOT_DIR/frontend"
# Non-interactive safety (confirmModulesPurge) comes from frontend/pnpm-workspace.yaml,
# so it applies here, in Docker, and to the implicit install that `pnpm run` performs.
pnpm install --frozen-lockfile
pnpm run build

echo "==> Copying frontend assets into backend embed directory..."
EMBED_DIR="$ROOT_DIR/backend/webui"
mkdir -p "$EMBED_DIR"
# Clear the previous build but keep .gitkeep, which is committed so the
# //go:embed directive resolves on a clean checkout.
find "$EMBED_DIR" -mindepth 1 -not -name '.gitkeep' -delete
cp -R "$ROOT_DIR/frontend/dist/." "$EMBED_DIR/"

echo "==> Building backend..."
cd "$ROOT_DIR/backend"
CGO_ENABLED=0 go build -o "$ROOT_DIR/backend/server" ./cmd/server

echo "==> Build complete."
echo "Binary: $ROOT_DIR/backend/server"
