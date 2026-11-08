#!/usr/bin/env bash
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="${1:-$ROOT_DIR/dist}"
BIN_NAME="${BIN_NAME:-xstash}"
mkdir -p "$OUT_DIR"
for target in "linux:amd64:linux-x64" "darwin:amd64:macos-x64" "darwin:arm64:macos-arm64" "windows:amd64:windows-x64"; do
  IFS=: read -r goos goarch dir <<< "$target"
  ext=""; [[ "$goos" == windows ]] && ext=".exe"
  mkdir -p "$OUT_DIR/$dir"
  (cd "$ROOT_DIR" && GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 go build -o "$OUT_DIR/$dir/$BIN_NAME$ext" ./cmd/xstash)
done
