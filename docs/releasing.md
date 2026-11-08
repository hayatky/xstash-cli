# Releasing xstash

The v2.0.0 release is published by `.github/workflows/release-v2.yml`, triggered only by a push to `release/v2.0.0`. Pushing to `master` or pushing a tag does not trigger this release workflow. Never include local config, databases, tokens, downloaded media or private exports in an asset.

1. Review and merge the approved changes into `master`. Update both READMEs, `CHANGELOG.md`, the migration guide and `docs/releases/<version>.md` for user-facing changes. Keep CLI, export and internal DB schema versions distinct.
2. From the intended clean release commit, run the offline checks and wait for the merged master CI checks to pass:

   ```sh
   go test ./...
   go vet ./...
   go build ./...
   ```

   Tests must not call the real X API. Report target platforms that were cross-compiled but not runtime-tested.
3. Build all release archives with the approved version:

   ```sh
   bash scripts/build-release-assets.sh --version v2.0.0
   ```

   The script builds the supported binaries and writes archives plus `SHA256SUMS.txt` under `dist/release`. Use fresh build outputs. `--skip-build` is for explicitly verified matching binaries, not for reusing an unknown previous build.
4. Verify every checksum, inspect archive contents and run the host binary's `--version`. Expect `xstash 2.0.0 (<40-character commit>)` for the release binary and check that both the version and commit match the intended release. A direct source build without release linker flags reports an unknown commit. Confirm each archive contains the correct platform binary, both READMEs, license, changelog, migration guide and skills. Run an offline smoke test using an isolated temporary `XSTASH_HOME`: `db init`, `db check`, `stats`, `pending` and `export`. Do not use real credentials or run sync as a packaging test.
5. Review `.github/workflows/release-v2.yml` before triggering it. It must require that its exact commit equals remote `master` and refuse an existing `v2.0.0` tag or release. Its validation job runs tests, vet and builds, then packages the four archives and checksum file. Only the publishing job receives `contents: write`; publication uses the job-scoped GitHub token, not a persistent credential.
6. With release authorization, create and push `release/v2.0.0` at the validated merged `master` commit. This branch push is the publication trigger. The workflow creates the `v2.0.0` tag atomically, creates a draft release, attaches all four platform archives plus `SHA256SUMS.txt`, verifies the asset count and master/tag commits, then publishes it. Its body comes from `docs/releases/v2.0.0.md`. GitHub's generated source archives are not executable packages. Do not move an already published tag. If a run fails or its outcome is uncertain, inspect the tag, release and assets before deciding how to recover; a tag or draft may already exist, and the guard will intentionally refuse a blind rerun.
7. After publication, verify the tag's commit, release text and five attached files on GitHub. Download the assets and verify their hashes against the published checksum file. Report the release URL and any checks that could not be completed. Preserve the v1 source history and final Deno reference documented in the migration guide.

Checksums detect corrupted or mismatched assets. They are not independent signatures or a guarantee of reproducible builds. Do not advertise code signing, notarization, live-API validation or runtime coverage unless separately performed and verified.
