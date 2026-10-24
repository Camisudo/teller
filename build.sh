#!/usr/bin/env bash
# Builds the React frontend, embeds it into the Go binary, and produces
# dist/teller (Linux) and dist/teller.exe (Windows).
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
FRONT="${FRONTEND_DIR:-/home/guiness/crm-frontend}"
export PATH="$HOME/.local/share/mise/shims:$PATH"

echo "==> Building frontend from $FRONT"
(
  cd "$FRONT"
  # Same-origin API: the binary serves both the UI and /v1.
  VITE_API_BASE_URL=/v1 npm run build
)

echo "==> Embedding frontend"
rm -rf "$HERE/web/dist"
mkdir -p "$HERE/web/dist"
cp -r "$FRONT/dist/." "$HERE/web/dist/"

echo "==> Running tests"
(cd "$HERE" && go vet ./...)

mkdir -p "$HERE/dist"
echo "==> Building linux/amd64"
(cd "$HERE" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o dist/teller .)
echo "==> Building windows/amd64"
(cd "$HERE" && CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-s -w -H windowsgui" -o dist/teller.exe .)

echo "==> Done"
ls -lh "$HERE/dist/"
