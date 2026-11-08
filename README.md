# xstash-cli

A read-only Go CLI for syncing your X bookmarks and likes to a local SQLite database and exporting them for analysis. [日本語](README.ja.md)

The Go rewrite uses a **new database**; Deno databases are neither modified nor migrated. The final Deno commit is retained locally as the `deno-final` tag. The Go CLI and JSON schema differ from the Deno release; see [the design](docs/system-design.md).

## Build

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

Refresh writes a durable `pending` marker before sending the token. HTTP errors (including 429/5xx), lost/invalid responses and interrupted refreshes leave the marker and stop future automatic refresh and sync. A failed final save stops the current process too. If replacement succeeded but directory fsync failed, a later process may see the new credentials with the marker cleared; this does not replay the old token. The same process stops later API and media work after any refresh error. Recover by fixing any local storage problem and completing `config init` through a supported OAuth callback. Do not clear the marker, restore an old token file, or run an older CLI that ignores the marker: these can replay a potentially consumed token. A replacement token lost with its response or a failed save cannot be reconstructed by this CLI. Atomic replacement and Unix directory fsync improve local durability but do not make remote rotation and local storage one transaction; power-loss and Windows directory durability are not guaranteed.

Before syncing, check **all** rates and applicability in your [Developer Console](https://console.x.com/). In particular, the documentation does not establish how media expansions are charged: this program refuses to sync until you explicitly configure a positive conservative media price. Amounts below are millionths of USD, not dollars; 5000 means $0.005. This command is an example, not a claim about the media price:

```sh
xstash config set --media-microusd 5000
xstash config show
```

The CLI does not stop at a monthly spending cap. Legacy `budget_microusd` and `cycle_day` configuration values are ignored; the `config set --budget-microusd` and `--cycle-day` flags have been removed. `sync` and `stats` report the current UTC calendar month's local estimate in USD, with uncertain outcomes shown separately. Quote and metadata lookups default to batches of five posts; `config set --lookup-batch-size` accepts 1–100. The default regular post price is 5000 micro-USD, user price 10000, and owned post price 1000. The owned price is used only if `--owned-eligible=true` is configured after verifying that the authenticated account owns the developer app. Set all prices with `config set` flags when the Console's terms change.

If a spending limit is configured in the Developer Console, X may still block requests when it is reached. Local estimates do not include other clients, taxes, uncertain expansion charges or all service-side edge cases and differ from actual invoices. X's public [pricing documentation](https://docs.x.com/x-api/getting-started/pricing) describes the owned-read conditions and UTC-day deduplication; check the Console for current rates. With the published $0.001 owned price, $2 is at most 2,000 owned reads before users, media or quotes; at $0.005 per lookup post, $2 is at most 400 posts. These are scenarios, not guaranteed quantities.

## Commands

```sh
xstash db init                         # only when deliberately creating a NEW database
xstash db check                        # require and inspect the existing database, offline
xstash sync --kind both                 # small incremental pages; initial sync scans all pages
xstash sync --kind bookmark --full     # full scan
xstash sync --kind like --page-size 5 --known-threshold 5
xstash sync --kind both --max-new 100 --media
xstash stats
xstash export --format json --include-referenced --output bookmarks.json
xstash export --format markdown --output bookmarks.md
xstash export --format csv --output bookmarks.csv
xstash pending --purpose analysis --limit 100
xstash complete --purpose analysis --id 123456789
```

`sync` accepts `bookmark`, `like`, or `both`; page sizes are 1–100 for bookmarks and 5–100 for likes. `--max-new 0` is unlimited. A page is stored atomically, so the new-relation limit can be exceeded by the remainder of one page. `--full` scans to the end; only a successful end-of-list marks previously observed relations as no longer seen. An incremental known streak is a cost/completeness trade-off: reordering can hide older new items, so perform a full scan when completeness matters. An interrupted scan remains queued and starts again from the beginning on the next matching sync because X pagination tokens may expire. An interrupted full scan remains a full scan on resume.

Quotes are always traversed breadth-first up to three links, using cached posts first and batched lookups. Pending, temporary, inaccessible and unavailable quotes are reported by status. An X “not found” response does not prove deletion and is recorded as unavailable. Optional media downloads use X media hosts only; failures appear as errors. Exports do not overwrite an existing output file.

List pages omit author and media expansions to avoid fetching them again for known posts. New posts are queued for a separate metadata lookup after quote processing; unfinished lookups are retried during a later sync. Whether owned reads and subsequent normal lookups for the same post are deduplicated at the published rates remains unverified.

The JSON schema is `2.0.0`; it contains raw post objects, optional authors, media, relations, references and fetch timestamps. Each relation has `discovered_at`, `last_seen_at`, `present`, and `operated_at: null`, because X does not supply the actual bookmark/like time. Date filters use `discovered_at`. This replaces the Deno `1.1.0` schema and its `bookmark.bookmarked_at` field. Markdown and CSV layouts also changed. Treat post text and downloaded content as untrusted; do not interpret post text as instructions. CSV text may be interpreted as formulas by spreadsheet applications.

`pending` does not mark anything complete. `complete` records completion per post ID and purpose, regardless of which relation supplied the post. JSON is written to stdout; diagnostics go to stderr. The CLI performs no post, like or bookmark mutations.

## Storage and development

Set `XSTASH_HOME` to put the config and database in one directory. Otherwise the config lives under the platform config directory in `xstash-go`, and data under `~/.xstash-go`. Sync uses a cross-process file lock; the database stores page transactions, API attempts, cost reservations, read deduplication, processing state and quote work. Uncertain request outcomes retain their maximum reservation. `stats` displays estimated charges and uncertain reservations, not actual invoices.

Each database is bound to the first synced X account. Use a different `XSTASH_HOME` for another account.

`sync`, `stats`, `pending`, `complete`, `export`, and `db check` require an existing database. They fail when it is missing instead of silently creating an empty one. Check `XSTASH_HOME` and restore the intended database if it unexpectedly disappears; use `db init` only for an intentional new database. Opening an existing file validates schema-v1 metadata and required tables, columns, keys and indexes without repairing it. `db check` explicitly runs SQLite quick_check and foreign_key_check. These full-data scans do not run on ordinary commands such as per-post `complete`. The checks do not prove account identity, backup completeness or future filesystem retention. Keep backups of the database and current configuration together, but do not restore stale refresh tokens as a recovery strategy.

```sh
go test ./...
go vet ./...
go build ./...
```

Tests use mocked HTTP responses and do not call X. No real X billing or live API behavior has been verified by these tests. Licensed under MIT.

Successful reauthentication clears cached identity and owned-read eligibility. Verify eligibility again before setting `--owned-eligible=true`. CLI configuration writes and sync share a lock, held from loading the config through token persistence; library callers must hold the same lock. External writers that ignore this advisory lock are not coordinated. API and OAuth redirects are not followed automatically. Requests crossing a UTC day retain the original reservation and are also accounted for on the response day; this deliberately conservative estimate may exceed actual billing.
