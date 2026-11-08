# Upgrading from Deno v1 to Go v2

v2.0.0 is a breaking rewrite, not an in-place database upgrade. It reads X bookmarks and likes without adding, removing or changing them on X.

## Preserve the old archive

1. Stop scheduled Deno syncs before changing commands or credentials.
2. Keep the Deno database, exports and the v1 program together in a separate location. Back up the database while the old process is stopped. Treat any saved credentials as secrets.
3. Keep using the [v1.0.0 source](https://github.com/hayatky/xstash-cli/tree/v1.0.0) for the released Deno version, or the [final Deno commit, 93ddc73](https://github.com/hayatky/xstash-cli/tree/93ddc73ad4d3c58e2eae597e32e888fe744ae1b5), for its final source state. The repository's `deno-final` tag identifies that commit. Go does not read, modify or automatically migrate those archives.

Do not copy a Deno DB into the Go data directory. Keeping the old archive is important: re-fetching from X cannot recover posts that are no longer available or reconstruct their previous local discovery times.

## Start a separate Go archive

Install and checksum-verify the [v2 binary](../README.md#install-a-release), or build with Go 1.25 or later. Confirm `xstash --version` before changing automation.

Use a new directory for v2. On macOS or Linux, for example:

```sh
export XSTASH_HOME="$HOME/.xstash-go-v2"
xstash db init
xstash db check
export XSTASH_CLIENT_ID='your-client-id'
xstash config init
xstash config show
```

On PowerShell, set `$env:XSTASH_HOME` and `$env:XSTASH_CLIENT_ID` instead. Register `http://127.0.0.1:38080/callback` with your X OAuth 2.0 app first. See [authentication instructions](../README.md#configure) for confidential apps and the exact read/offline scopes. `db init` is for intentional new databases; if an established archive goes missing, check the path or restore the intended DB rather than creating an empty replacement.

Authenticate afresh. Do not restore old token files or inject `XSTASH_ACCESS_TOKEN` / `XSTASH_REFRESH_TOKEN`; v2 rejects those environment variables. Old configurations without scope and token-type metadata also require reauthentication. Successful authentication resets cached identity and owned-read eligibility, so confirm eligibility again before enabling the discounted estimate.

For an already issued **public-client OAuth 2.0 user grant**, Linux and macOS offer `config import` with hidden terminal prompts. It performs no network verification and requires you to check the grant's scopes. Windows uses `config init`. Never put tokens in command arguments, pipes, chat or logs. When refresh is interrupted or its outcome is uncertain, obtain a new grant through `config init` or `config import --replace`; do not clear the pending marker or replay a saved old token. See the README for the full import and refresh recovery requirements.

## Review cost and synchronization scope

Check rates and access in the [X Developer Console](https://console.x.com/), then choose the first sync deliberately:

```sh
xstash sync --kind both
xstash stats
```

The defaults select the most recent 100 bookmarks and 50 likes, with 20 list pages per kind, one quote level and at most 50 uncached quote lookups per run. Later syncs use five-item pages and stop at a known relation. This is a recent-window baseline, not a full historical import. `complete: true` means the chosen window completed; inspect `history_complete`, `mode`, `policy` and deferred work separately.

Author/media enrichment is off by default. `--metadata` enables it; `--media` also downloads files. Both require a configured positive media price and can add cost. An interrupted full scan continues as full even without `--full` on the next run. Page-capped scans restart at the top; repeatedly using an insufficient limit may not advance them.

There is **no monthly hard stop**. Removed `--budget-microusd` / `--cycle-day` flags and legacy settings do not enforce a limit. Local estimates and uncertain reservations are not invoices, guaranteed ceilings or proof of owned-read eligibility. Check the Console before raising limits or scheduling recurring API calls.

## Update scripts and consumers

- Replace Deno commands with the Go command interface in the [README](../README.md#commands). Review existing cron jobs and agent prompts rather than simply replacing the executable.
- JSON export schema `2.0.0` replaces Deno schema `1.1.0`. Posts, bookmark/like relations, references, authors and media are separate concepts. Markdown and CSV layouts also changed.
- Replace assumptions about `bookmark.bookmarked_at`: `discovered_at` is first local discovery, `last_seen_at` is last confirmation and `operated_at` is `null`. Date filters use discovery time, not the X action time.
- Durable processing state is keyed by post ID and purpose. `pending --purpose analysis` reads without completing; call `complete --purpose analysis --id ID` only after processing succeeds. Separate purposes keep different workflows independent.
- Export files are created exclusively and are not overwritten. Treat posts, links and media as untrusted data; spreadsheet programs may interpret CSV cells as formulas.
- CLI version `2.0.0`, JSON schema `2.0.0` and Go internal DB schema `1` are independent version numbers. A DB schema of `1` does not identify a Deno database.

Keep v1 and v2 storage separate if you need to return to the old archive. Do not point an old executable at v2 credentials, especially after refresh-token rotation.
