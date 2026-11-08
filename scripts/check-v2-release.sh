#!/usr/bin/env bash
set -euo pipefail

# Read-only preflight. Any API failure stops publication, including a 403.
: "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"
: "${GITHUB_SHA:?GITHUB_SHA is required}"
: "${GITHUB_REF:?GITHUB_REF is required}"
[[ "$GITHUB_REPOSITORY" == hayatky/xstash-cli ]]
[[ "$GITHUB_REF" == refs/heads/release/v2.0.0 ]]
[[ "$GITHUB_SHA" =~ ^[0-9a-f]{40}$ ]]
master_sha="$(gh api "repos/$GITHUB_REPOSITORY/git/ref/heads/master" --jq '.object.sha')"
if [[ "$master_sha" != "$GITHUB_SHA" ]]; then
  echo 'error: release commit must be exactly the current master commit' >&2
  exit 1
fi
tag_count="$(gh api "repos/$GITHUB_REPOSITORY/git/matching-refs/tags/v2.0.0" --jq 'map(select(.ref == "refs/tags/v2.0.0")) | length')"
release_ids="$(gh api --paginate "repos/$GITHUB_REPOSITORY/releases?per_page=100" --jq '.[] | select(.tag_name == "v2.0.0") | .id')"
if [[ "$tag_count" != 0 || -n "$release_ids" ]]; then
  echo 'error: v2.0.0 tag or release already exists; refusing to overwrite it' >&2
  exit 1
fi
echo "Release target verified: $GITHUB_SHA"
