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

if [[ ! "$VERSION" =~ ^v?[0-9]+\.[0-9]+\.[0-9]+([.-][A-Za-z0-9.-]+)?$ ]] || [[ ! "$BIN_NAME" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]]; then
  echo "error: invalid version or binary name" >&2
  exit 1
fi

mkdir -p "$DIST_DIR" "$OUT_DIR"
DIST_DIR="$(cd "$DIST_DIR" && pwd)"
OUT_DIR="$(cd "$OUT_DIR" && pwd)"
if [[ -e "$OUT_DIR/SHA256SUMS.txt" ]] || compgen -G "$OUT_DIR/$BIN_NAME-$VERSION-*" >/dev/null; then
  echo "error: release assets already exist; choose a fresh --out directory" >&2
  exit 1
fi

if [[ "$SKIP_BUILD" != "true" ]]; then
  VERSION="$VERSION" BIN_NAME="$BIN_NAME" bash "$ROOT_DIR/scripts/build-binaries.sh" "$DIST_DIR"
fi

TARGETS=(
  "linux-x64:linux-amd64:"
  "macos-x64:darwin-amd64:"
  "macos-arm64:darwin-arm64:"
  "windows-x64:windows-amd64:.exe"
)

for entry in "${TARGETS[@]}"; do
  platform="${entry%%:*}"
  ext="${entry##*:}"
  if [[ ! -f "$DIST_DIR/$platform/${BIN_NAME}${ext}" ]]; then
    echo "error: binary not found for $platform" >&2
    exit 1
  fi
done
for doc in README.md README.ja.md LICENSE CHANGELOG.md docs/migration-v2.md docs/releases/v2.0.0.md; do
  if [[ ! -f "$ROOT_DIR/$doc" ]]; then
    echo "error: required release document missing: $doc" >&2
    exit 1
  fi
done

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
  cp "$ROOT_DIR/README.md" "$ROOT_DIR/README.ja.md" "$ROOT_DIR/LICENSE" "$ROOT_DIR/CHANGELOG.md" "$package_dir/"
  mkdir -p "$package_dir/docs/releases"
  cp "$ROOT_DIR/docs/"*.md "$package_dir/docs/"
  cp "$ROOT_DIR/docs/releases/v2.0.0.md" "$package_dir/docs/releases/"
  cp -R "$ROOT_DIR/skills" "$package_dir/skills"

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
