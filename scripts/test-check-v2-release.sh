#!/usr/bin/env bash
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

# All gh calls are intercepted. No token, network or repository writes needed.
cat > "$TMP_DIR/gh" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$MOCK_CALLS"
case "$*" in
  'api repos/hayatky/xstash-cli/git/ref/heads/master --jq .object.sha')
    [[ "$MOCK_CASE" != api-failure ]] || exit 1
    if [[ "$MOCK_CASE" == wrong-master ]]; then
      printf '%040d\n' 2
    else
      printf '%s\n' "$GITHUB_SHA"
    fi
    ;;
  'api repos/hayatky/xstash-cli/git/matching-refs/tags/v2.0.0 --jq map(select(.ref == "refs/tags/v2.0.0")) | length')
    [[ "$MOCK_CASE" != tag-api-failure ]] || exit 1
    if [[ "$MOCK_CASE" == existing-tag ]]; then echo 1; else echo 0; fi
    ;;
  'api --paginate repos/hayatky/xstash-cli/releases?per_page=100 --jq .[] | select(.tag_name == "v2.0.0") | .id')
    [[ "$MOCK_CASE" != release-api-failure ]] || exit 1
    if [[ "$MOCK_CASE" == existing-release ]]; then echo 123; fi
    ;;
  *) echo 'Unexpected gh invocation in preflight' >&2; exit 90 ;;
esac
MOCK
chmod 700 "$TMP_DIR/gh"

for scenario in ok wrong-master existing-tag existing-release api-failure tag-api-failure release-api-failure wrong-ref wrong-repo bad-sha; do
  repository='hayatky/xstash-cli'
  ref='refs/heads/release/v2.0.0'
  sha='1111111111111111111111111111111111111111'
  case "$scenario" in
    wrong-ref) ref='refs/heads/master' ;;
    wrong-repo) repository='example/fork' ;;
    bad-sha) sha='bad' ;;
  esac
  : > "$TMP_DIR/calls"
  status=0
  PATH="$TMP_DIR:$PATH" GH_TOKEN='' GITHUB_TOKEN='' \
    MOCK_CASE="$scenario" MOCK_CALLS="$TMP_DIR/calls" \
    GITHUB_REPOSITORY="$repository" GITHUB_REF="$ref" GITHUB_SHA="$sha" \
    bash "$ROOT_DIR/scripts/check-v2-release.sh" > "$TMP_DIR/output" 2>&1 || status=$?
  if [[ "$scenario" == ok ]]; then
    if [[ "$status" -ne 0 ]] || [[ "$(wc -l < "$TMP_DIR/calls")" -ne 3 ]]; then
      cat "$TMP_DIR/output" >&2
      echo 'error: valid release target rejected or checks skipped' >&2
      exit 1
    fi
  elif [[ "$status" -eq 0 ]]; then
    echo "error: preflight accepted unsafe scenario: $scenario" >&2
    exit 1
  fi
  case "$scenario" in
    wrong-ref|wrong-repo|bad-sha)
      if [[ -s "$TMP_DIR/calls" ]]; then
        echo "error: invalid local context reached GitHub: $scenario" >&2
        exit 1
      fi
      ;;
  esac
  echo "Release preflight $scenario: passed"
done
echo 'Release preflight checks passed (mocked GitHub only).'
