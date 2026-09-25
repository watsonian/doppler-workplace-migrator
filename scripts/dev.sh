#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"

FRONTEND_PID=""
BACKEND_PID=""

cleanup() {
    echo "Stopping services..."
    kill "$FRONTEND_PID" "$BACKEND_PID" 2>/dev/null || true
    wait "$FRONTEND_PID" "$BACKEND_PID" 2>/dev/null || true
    echo "Done."
}
trap cleanup EXIT

# Install dependencies up front, in the foreground. `pnpm run` otherwise does this
# implicitly from the backgrounded dev server, where a failure is easy to miss and
# any prompt has no terminal to read from.
echo "Checking frontend dependencies..."
cd "$ROOT_DIR/frontend"
pnpm install

# Start frontend dev server
echo "Starting frontend dev server on :5173..."
pnpm run dev &
FRONTEND_PID=$!

# Start backend
echo "Starting Go backend on :8080..."
cd "$ROOT_DIR/backend"
DEV_MODE=true go run ./cmd/server &
BACKEND_PID=$!

echo ""
echo "Frontend: http://localhost:5173"
echo "Backend:  http://localhost:8080"
echo "Press Ctrl+C to stop."
echo ""

# Exit as soon as either side dies, so a crashed dev server doesn't leave a
# half-running environment that looks healthy. `wait -n` would be tidier but is
# unavailable on the bash 3.2 that ships with macOS.
while kill -0 "$FRONTEND_PID" 2>/dev/null && kill -0 "$BACKEND_PID" 2>/dev/null; do
    sleep 1
done

if ! kill -0 "$FRONTEND_PID" 2>/dev/null; then
    echo "Frontend dev server exited." >&2
else
    echo "Backend exited." >&2
fi
exit 1
