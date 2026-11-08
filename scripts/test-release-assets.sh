#!/usr/bin/env bash
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT
for platform in linux-x64 macos-x64 macos-arm64 windows-x64; do
  mkdir -p "$TMP_DIR/binaries/$platform"
  ext=''; [[ "$platform" == windows-x64 ]] && ext='.exe'
  printf 'synthetic packaging fixture\n' > "$TMP_DIR/binaries/$platform/check$ext"
done
bash "$ROOT_DIR/scripts/build-release-assets.sh" --version v2.0.0 --bin-name check --dist "$TMP_DIR/binaries" --out "$TMP_DIR/packages" --skip-build
(cd "$TMP_DIR/packages" && sha256sum -c SHA256SUMS.txt)
test "$(find "$TMP_DIR/packages" -maxdepth 1 -type f | wc -l)" -eq 5
tar -tzf "$TMP_DIR/packages/check-v2.0.0-linux-x64.tar.gz" > "$TMP_DIR/contents"
for expected in check README.md README.ja.md LICENSE THIRD_PARTY_NOTICES.md CHANGELOG.md docs/migration-v2.md skills/xstash-bookmark-intel/SKILL.md; do
  grep -Fx "check-v2.0.0-linux-x64/$expected" "$TMP_DIR/contents"
done
if bash "$ROOT_DIR/scripts/build-release-assets.sh" --version v2.0.0 --bin-name check --dist "$TMP_DIR/binaries" --out "$TMP_DIR/packages" --skip-build; then
  echo 'error: repeated packaging should refuse to overwrite artifacts' >&2
  exit 1
fi
if bash "$ROOT_DIR/scripts/build-release-assets.sh" --version ../unsafe --dist "$TMP_DIR/binaries" --out "$TMP_DIR/invalid" --skip-build; then
  echo 'error: unsafe version should fail' >&2
  exit 1
fi
echo 'Release packaging checks passed.'
