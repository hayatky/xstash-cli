#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST_DIR="$ROOT_DIR/dist"
OUT_DIR="$ROOT_DIR/dist/release"
BIN_NAME="${BIN_NAME:-xstash}"
VERSION=""
SKIP_BUILD="false"

usage() {
  cat <<'EOF'
Usage:
  bash scripts/build-release-assets.sh --version <tag-or-version> [options]

Options:
  --version <value>    Required. Version label used in artifact names (e.g. v0.1.0)
  --dist <path>        Binary source directory (default: ./dist)
  --out <path>         Release artifact output directory (default: ./dist/release)
  --bin-name <name>    Binary name without extension (default: xstash)
  --skip-build         Skip binary build step
  -h, --help           Show this help
EOF
}

hash_file() {
  local file="$1"
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$file" | awk '{print $1}'
    return
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$file" | awk '{print $1}'
    return
  fi
  if command -v openssl >/dev/null 2>&1; then
    openssl dgst -sha256 "$file" | awk '{print $2}'
    return
  fi

  echo "error: no SHA-256 command found (expected one of: shasum, sha256sum, openssl)" >&2
  exit 1
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version)
      VERSION="${2:-}"
      shift 2
      ;;
    --dist)
      DIST_DIR="${2:-}"
      shift 2
      ;;
    --out)
      OUT_DIR="${2:-}"
      shift 2
      ;;
    --bin-name)
      BIN_NAME="${2:-}"
      shift 2
      ;;
    --skip-build)
      SKIP_BUILD="true"
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "error: unknown option: $1" >&2
      usage
      exit 1
      ;;
  esac
done

if [[ -z "$VERSION" ]]; then
  echo "error: --version is required" >&2
  usage
  exit 1
fi

if [[ "$SKIP_BUILD" != "true" ]]; then
  bash "$ROOT_DIR/scripts/build-binaries.sh" "$DIST_DIR"
fi

mkdir -p "$OUT_DIR"

TARGETS=(
  "linux-x64:linux-amd64:"
  "macos-x64:darwin-amd64:"
  "macos-arm64:darwin-arm64:"
  "windows-x64:windows-amd64:.exe"
)

TMP_DIR="$(mktemp -d)"
cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

shasums_file="$OUT_DIR/SHA256SUMS.txt"
: > "$shasums_file"

echo "Packaging release assets"
echo "Version: $VERSION"
echo "Binary source: $DIST_DIR"
echo "Output: $OUT_DIR"

for entry in "${TARGETS[@]}"; do
  platform="${entry%%:*}"
  rest="${entry#*:}"
  target="${rest%%:*}"
  ext="${entry##*:}"

  src_binary="$DIST_DIR/$platform/${BIN_NAME}${ext}"
  if [[ ! -f "$src_binary" ]]; then
    echo "error: binary not found for $platform ($target): $src_binary" >&2
    exit 1
  fi

  package_base="${BIN_NAME}-${VERSION}-${platform}"
  package_dir="$TMP_DIR/$package_base"
  mkdir -p "$package_dir"
  cp "$src_binary" "$package_dir/${BIN_NAME}${ext}"

  if [[ "$platform" == "windows-x64" ]]; then
    asset_path="$OUT_DIR/${package_base}.zip"
    (cd "$TMP_DIR" && zip -rq "$asset_path" "$package_base")
  else
    asset_path="$OUT_DIR/${package_base}.tar.gz"
    tar -C "$TMP_DIR" -czf "$asset_path" "$package_base"
  fi

  sha256="$(hash_file "$asset_path")"
  asset_name="$(basename "$asset_path")"
  echo "$sha256  $asset_name" >> "$shasums_file"
  echo "  - $asset_name"
done

echo "  - $(basename "$shasums_file")"
echo "Done."
