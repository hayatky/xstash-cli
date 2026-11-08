#!/usr/bin/env bash
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="${1:-$ROOT_DIR/dist}"
BIN_NAME="${BIN_NAME:-xstash}"
mkdir -p "$OUT_DIR"
OUT_DIR="$(cd "$OUT_DIR" && pwd)"
VERSION="${VERSION:-2.0.0}"
COMMIT="${COMMIT:-$(git -C "$ROOT_DIR" rev-parse HEAD)}"
if [[ ! "$VERSION" =~ ^v?[0-9]+\.[0-9]+\.[0-9]+([.-][A-Za-z0-9.-]+)?$ ]] || [[ ! "$COMMIT" =~ ^[0-9a-f]{40}$ ]]; then
  echo "error: invalid version or source commit" >&2
  exit 1
fi
if [[ ! "$BIN_NAME" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]]; then
  echo "error: invalid binary name" >&2
  exit 1
fi
for target in "linux:amd64:linux-x64" "darwin:amd64:macos-x64" "darwin:arm64:macos-arm64" "windows:amd64:windows-x64"; do
  IFS=: read -r goos goarch dir <<< "$target"
  ext=""; [[ "$goos" == windows ]] && ext=".exe"
  mkdir -p "$OUT_DIR/$dir"
  (cd "$ROOT_DIR" && GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION#v} -X main.commit=$COMMIT" -o "$OUT_DIR/$dir/$BIN_NAME$ext" ./cmd/xstash)
done
