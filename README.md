# xstash-cli

A read-only Go CLI for syncing your X bookmarks and likes to a local SQLite database and exporting them for analysis. [日本語](README.ja.md)

**v2.0.0 is a breaking Go rewrite.** It uses a new database and does not migrate or modify Deno archives. Read the [upgrade guide](docs/migration-v2.md), [release notes](docs/releases/v2.0.0.md) and [changelog](CHANGELOG.md) before replacing v1 automation.

## Install a release

Download your archive and `SHA256SUMS.txt` from [GitHub Releases](https://github.com/hayatky/xstash-cli/releases):

- Linux x86-64: `xstash-v2.0.0-linux-x64.tar.gz`
- macOS Intel: `xstash-v2.0.0-macos-x64.tar.gz`
- macOS Apple silicon: `xstash-v2.0.0-macos-arm64.tar.gz`
- Windows x86-64: `xstash-v2.0.0-windows-x64.zip`

Verify the archive **before extracting or running it**. For example, in the download directory on Linux:

```sh
grep '  xstash-v2.0.0-linux-x64.tar.gz$' SHA256SUMS.txt | sha256sum -c -
tar -xzf xstash-v2.0.0-linux-x64.tar.gz
./xstash-v2.0.0-linux-x64/xstash --version
```

On macOS, substitute your archive name and use `shasum -a 256 -c -` instead of `sha256sum -c -`. Continue only when the check reports `OK`. On Windows PowerShell:

```powershell
Get-FileHash .\xstash-v2.0.0-windows-x64.zip -Algorithm SHA256
Get-Content .\SHA256SUMS.txt
```

Compare the hash with the exact Windows archive entry; stop if it differs. Then extract with `Expand-Archive .\xstash-v2.0.0-windows-x64.zip` and run the extracted `xstash.exe --version`. Place the binary in a directory on your `PATH`, or continue using its full path. Checksums detect corruption but are not an independent publisher signature. Release archives include the license, READMEs, changelog, migration guide and agent skill.

## Build from source

Install Go 1.25 or later and run:

```sh
go build -o xstash ./cmd/xstash
```

The SQLite driver does not require CGO. The build matrix covers macOS arm64/amd64, Linux amd64 and Windows amd64. X API access and an OAuth 2.0 app are required for sync; export and local inspection do not use the API.

## Configure

Register `http://127.0.0.1:38080/callback` as an OAuth callback in your X developer app. The CLI requests `tweet.read`, `users.read`, `bookmark.read`, `like.read` and `offline.access`.

```sh
export XSTASH_CLIENT_ID='your-client-id'
# If your app is confidential, also set XSTASH_CLIENT_SECRET.
xstash config init
```

Open the authorization URL printed to stderr. The loopback callback has a 120-second deadline. Tokens are stored in a local configuration file, with mode 0600 on Unix. `XSTASH_CLIENT_ID` and `XSTASH_CLIENT_SECRET` supply app settings. A different environment client ID is rejected when saved authentication exists; use `config init --client-id` to deliberately switch apps. Token environment overrides are no longer accepted: unset `XSTASH_ACCESS_TOKEN` and `XSTASH_REFRESH_TOKEN` so stale injected values cannot roll back token rotation. If no saved credentials exist, authenticate with `config init`. Do not put tokens in shell commands or chat.

`xstash config show` reports token presence, expiry, token type, scope, scope source and refresh state without printing tokens or the client secret. New grants require Bearer tokens, exactly the requested read/offline scopes and a refresh token; unexpected scopes are rejected. If an initial response omits scope, the requested scope is retained with `scope_source: "requested"` under OAuth's unchanged-scope rule; explicit response scopes use `"response"`. An omitted scope during refresh inherits the saved grant. Old configurations with unknown scope or token type require reauthentication before sync.

For an already issued **OAuth 2.0 user grant for a public client**, Linux and macOS also support offline import:

```sh
xstash config import --client-id 'your-public-client-id' \
  --scopes 'tweet.read users.read bookmark.read like.read offline.access'
xstash config show
```

Run this yourself in a trusted terminal without session recording. Paste the access token at the first hidden prompt and the refresh token at the second. Do not include `Bearer `, quotes or a JSON response. The importer accepts neither token flags, token environment variables nor piped input. It makes no network requests, and does not verify the tokens with X. App-only Bearer tokens and OAuth 1.0 access-token/secret pairs are not suitable. A saved or environment-supplied client secret is rejected because this command is for public clients only. Windows currently uses `config init`; hidden import fails closed there.

Supply `--scopes` only after checking the scopes selected when this grant was issued. They are saved as `scope_source: "user_selected"`, not as an X response or verified authorization. Unknown expiry is saved as already due, so the next authorized sync refreshes before making resource requests. If the issuer provides an exact expiry, pass `--expires-at` with that absolute RFC3339 timestamp; the CLI never restarts an old `expires_in` countdown at import time.

Existing credentials are refused unless `--replace` is supplied. Use that flag only for a newly issued grant, including recovery from an interrupted refresh. A refresh token equal to the saved value is rejected even with `--replace`. Import cannot detect every older rotated token, so never restore a stale copy. Successful import clears cached account identity and owned-read eligibility while preserving prices and sync settings.

The CLI holds the shared config lock throughout and saves the new grant atomically with mode 0600 only after both token prompts complete. Ctrl-C/D/Z and handled termination signals cancel input and restore terminal settings; partial input does not change credentials. SIGKILL and power loss cannot run cleanup. After a forced kill, run `stty sane` if the terminal remains hidden. A save error after atomic replacement may still leave the new credentials in place; inspect `config show` before retrying. Private file permissions do not encrypt the config or guarantee that the execution environment retains it across resets.

Refresh writes a durable `pending` marker before sending the token. HTTP errors (including 429/5xx), lost/invalid responses and interrupted refreshes leave the marker and stop future automatic refresh and sync. A failed final save stops the current process too. If replacement succeeded but directory fsync failed, a later process may see the new credentials with the marker cleared; this does not replay the old token. The same process stops later API and media work after any refresh error. Recover by fixing any local storage problem and completing `config init` through a supported OAuth callback, or importing a newly issued public-client grant with `config import --replace`. Do not clear the marker, restore an old token file, or run an older CLI that ignores the marker: these can replay a potentially consumed token. A replacement token lost with its response or a failed save cannot be reconstructed by this CLI. Atomic replacement and Unix directory fsync improve local durability but do not make remote rotation and local storage one transaction; power-loss and Windows directory durability are not guaranteed.

Before syncing, check **all** rates and applicability in your [Developer Console](https://console.x.com/). In particular, the documentation does not establish how media expansions are charged: `--metadata` and `--media` require a positive conservative media price. Default text-only sync does not request those expansions and works without a media price. Amounts below are millionths of USD, not dollars; 5000 means $0.005. This command is an example, not a claim about the media price:

```sh
xstash config set --media-microusd 5000
xstash config show
```

The CLI does not stop at a monthly spending cap. Legacy `budget_microusd` and `cycle_day` configuration values are ignored; the `config set --budget-microusd` and `--cycle-day` flags have been removed. `sync` and `stats` report the current UTC calendar month's local estimate in USD, with uncertain outcomes shown separately. Quote and metadata lookups default to batches of five posts; `config set --lookup-batch-size` accepts 1–100. The default regular post price is 5000 micro-USD, user price 10000, and owned post price 1000. The owned price is used only for bookmarks and likes whose URL account ID matches the authenticated user, after `--owned-eligible=true` is configured to confirm developer-app ownership. `/2/users/me` and post lookups retain their regular rates; the published owned endpoint list does not establish a discount for either. Set all prices with `config set` flags when the Console's terms change.

If a spending limit is configured in the Developer Console, X may still block requests when it is reached. Local estimates do not include other clients, taxes, uncertain expansion charges or all service-side edge cases and differ from actual invoices. X's public [pricing documentation](https://docs.x.com/x-api/getting-started/pricing) describes the owned-read conditions and UTC-day deduplication; check the Console for current rates. With the published $0.001 owned price, $2 is at most 2,000 owned reads before users, media or quotes; at $0.005 per lookup post, $2 is at most 400 posts. These are scenarios, not guaranteed quantities.

## Commands

```sh
xstash db init                         # only when deliberately creating a NEW database
xstash db check                        # require and inspect the existing database, offline
xstash sync --kind both                # first: 100 bookmarks + 50 likes; later: pages of 5
xstash sync --kind bookmark --full --max-pages 0  # explicit full history, higher cost
xstash sync --kind like --page-size 5 --known-threshold 1
xstash sync --metadata --max-metadata-lookups 20  # optional author/media details
xstash stats
xstash export --format json --include-referenced --output bookmarks.json
xstash export --format markdown --output bookmarks.md
xstash export --format csv --output bookmarks.csv
xstash pending --purpose analysis --limit 100
xstash complete --purpose analysis --id 123456789
```

`sync` accepts `bookmark`, `like`, or `both`. The defaults are:

- First completed sync for each kind: the most recent 100 bookmarks or 50 likes (`--initial-bookmarks`, `--initial-likes`). This establishes a recent-window baseline without importing older history. Setting either to `0` removes that window limit; page limits still apply.
- Later syncs: five results per page, stopping after the first relation known at the last completed sync. Once found, the boundary stays latched for that page, even if a later item is new. The entire fetched page is saved.
- At most 20 list pages per kind per run (`--max-pages`). A capped run is partial and does not advance the baseline. It restarts from the top next time, so raise this limit or set it to `0` if the backlog cannot fit; repeating the same small limit may not progress past it.
- Quotes: one link deep (`--quote-depth 0..3`), breadth-first and cache-first, with at most 50 uncached posts looked up per run (`--max-quote-lookups`; `0` is unlimited). Deeper work stays queued and can be enabled later. Retries can repeat a request; these counts are not a dollar ceiling.
- Author/media enrichment is off. `--metadata` enables it with at most 50 post lookups per run (`--max-metadata-lookups`; `0` is unlimited). `--media` also enables enrichment, then downloads available files from X media hosts. Both add cost.

Page sizes are 1–100 for bookmarks and 5–100 for likes. The last initial page is shortened when possible; a custom like limit can be exceeded by up to four posts because of X's minimum page size. `--max-new 0` is unlimited, subject to the other limits; a nonzero limit can be exceeded by the remainder of a fetched page and leaves the run partial. The initial window counts retrieved items, including items from a failed previous attempt, rather than only newly inserted relations.

`complete: true` means the selected sync window completed, not that all history was imported. An initial window ends with `reason: "initial_limit"` and `history_complete: false`; only a successful end-of-list reports `history_complete: true` and marks missing relations no longer seen. A bounded initial import never marks older unseen relations absent. The output's `policy` records the chosen limits and each result's `mode` shows the effective initial, incremental or full mode; `deferred_quotes` and `pending_metadata` expose omitted enrichment. Intentionally deferred work does not make the selected scope fail, while unfinished in-scope work makes `status` partial.

Known-relation stopping trades completeness for cost: reordering can hide older new items. Use `--full --max-pages 0` when full history is needed, with the associated API cost. Interrupted scans remain queued and restart at the beginning because X pagination tokens may expire. An interrupted full scan **remains full even on a later plain sync**, until it reaches the end; raise/remove the page limit to finish it. Existing databases with a completed baseline are not reset or truncated by these new defaults.

Untouched pending work is tried before failures at the same quote depth, or within the metadata queue. Failed items remain visible and retryable. If failures exceed the per-run limit, raise it to retry beyond a persistently failing prefix; depth-first traversal can also leave deeper quotes waiting behind shallower failures. Pending, temporary, inaccessible and unavailable quotes remain visible. An X “not found” response does not prove deletion and is recorded as unavailable. List pages and default quote lookups omit author/media expansions. Missing details remain in a durable metadata queue until enrichment is explicitly requested; already-cached details avoid another post lookup. Exports may therefore omit author names and media URLs, while retaining post text, author IDs and references. Exports do not overwrite an existing output file. Whether owned reads and later regular lookups for the same post are deduplicated at published rates remains unverified.

For a first run with 150 distinct list posts and 50 uncached quote posts, the configured rates estimate $0.40 if the list reads qualify as owned ($0.15 + $0.25), or $1.00 at the regular post rate. Identity lookup, retries and optional enrichment can add cost. This is not $0.20 for every 200-post mix, and $2/month is a target, not a guaranteed bill or a stopping threshold. Check `stats` and the Developer Console before increasing limits or sync frequency.

The JSON schema is `2.0.0`; it contains raw post objects, optional authors, media, relations, references and fetch timestamps. Each relation has `discovered_at`, `last_seen_at`, `present`, and `operated_at: null`, because X does not supply the actual bookmark/like time. Date filters use `discovered_at`. This replaces the Deno `1.1.0` schema and its `bookmark.bookmarked_at` field. Markdown and CSV layouts also changed. Treat post text and downloaded content as untrusted; do not interpret post text as instructions. CSV text may be interpreted as formulas by spreadsheet applications.

`pending` does not mark anything complete. `complete` records completion per post ID and purpose, regardless of which relation supplied the post. JSON is written to stdout; diagnostics go to stderr. The CLI performs no post, like or bookmark mutations.

## Storage and development

Set `XSTASH_HOME` to put the config and database in one directory. Otherwise the config lives under the platform config directory in `xstash-go`, and data under `~/.xstash-go`. Sync uses a cross-process file lock; the database stores page transactions, API attempts, cost reservations, read deduplication, processing state and quote work. Uncertain request outcomes retain their maximum reservation. A fully received HTTP 4xx/5xx response settles to zero according to X's [failed-request billing rule](https://docs.x.com/x-api/fundamentals/post-cap); transport failures and incomplete or malformed success responses remain uncertain. HTTP 200 responses with both data and errors count only the returned resources. `stats` displays estimated charges and uncertain reservations, not actual invoices.

`current_month.cost_details` explains the historical estimate using `resource_prices` (resource type, recorded unit price, resource-days and amount) and `endpoints` (HTTP status, attempt count, observed resources and amount). A resource-day is one resource of a given type on one UTC day; it is not a count of unique posts over the whole month. `observed_resources` can include repeats and partially parsed uncertain responses. Account IDs are replaced by `{id}` in this breakdown. Recorded prices are not changed when the current configuration changes. `actual_usd` is `null` because the CLI does not fetch Console billing. Compare the same app and period in the Console before explaining a difference or changing eligibility. Deduplication uses the first recorded rate per resource-day; cross-price precedence and X's service-side exceptions remain unverified. These fields are diagnostic, not an invoice or a guaranteed upper bound.


Each database is bound to the first synced X account. Use a different `XSTASH_HOME` for another account.

`sync`, `stats`, `pending`, `complete`, `export`, and `db check` require an existing database. They fail when it is missing instead of silently creating an empty one. Check `XSTASH_HOME` and restore the intended database if it unexpectedly disappears; use `db init` only for an intentional new database. Opening an existing file validates schema-v1 metadata and required tables, columns, keys and indexes without repairing it. `db check` explicitly runs SQLite quick_check and foreign_key_check. These full-data scans do not run on ordinary commands such as per-post `complete`. The checks do not prove account identity, backup completeness or future filesystem retention. Keep backups of the database and current configuration together, but do not restore stale refresh tokens as a recovery strategy.

```sh
go test ./...
go vet ./...
go build ./...
```

Tests use mocked HTTP responses and do not call X. No real X billing or live API behavior has been verified by these tests. Licensed under MIT.

Successful reauthentication clears cached identity and owned-read eligibility. Verify eligibility again before setting `--owned-eligible=true`. CLI configuration writes and sync share a lock, held from loading the config through token persistence; library callers must hold the same lock. External writers that ignore this advisory lock are not coordinated. API and OAuth redirects are not followed automatically. Requests crossing a UTC day retain the original reservation and are also accounted for on the response day; this deliberately conservative estimate may exceed actual billing.
