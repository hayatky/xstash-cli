# Changelog

## v2.0.0

Breaking Go rewrite of the Deno CLI. See the [upgrade guide](docs/migration-v2.md) before replacing a v1 installation.

### Added

- Read-only bookmark and like synchronization into local SQLite, without CGO.
- Separate posts and relations, durable quote/metadata queues, page transactions and account-bound archives.
- Bounded recent-window defaults, incremental synchronization and explicit full-history scans.
- Optional author/media enrichment and media downloads.
- Durable per-post, per-purpose processing through `pending` and `complete`.
- API-attempt records, UTC resource-day estimates, uncertain reservations and diagnostic cost breakdowns.
- OAuth PKCE, validated read/offline grants, shared config locking and conservative refresh-token rotation handling.
- Offline public-client token import through hidden prompts on Linux/macOS.
- Explicit `db init`, offline `db check`, version reporting and packaged binaries for Linux x86-64, macOS Intel/Apple silicon and Windows x86-64.

### Breaking changes

- New Go database and configuration paths. No automatic Deno database migration; existing Deno archives remain separate.
- JSON schema `2.0.0` replaces `1.1.0`; Markdown and CSV layouts also change. Discovery/confirmation timestamps replace assumed bookmark-operation times.
- CLI commands and processing workflow change. Update scripts and agents before switching binaries.
- Token environment overrides are rejected. Unknown legacy authentication metadata requires reauthentication; successful reauthentication clears owned-read eligibility.
- Monthly budget/cycle flags are removed and old values ignored. Spending estimates are informational and never stop sync.

### Limitations

Cross-platform builds and offline tests do not verify live X behavior or invoices. Token import is unavailable on Windows. Estimates depend on configured rates and eligibility, and enrichment can add charges. See the README for refresh recovery, bounded-sync semantics and platform durability limitations.

## v1.0.0

Previous Deno release. Its source and the final Deno commit remain available for existing archives; see the [upgrade guide](docs/migration-v2.md#preserve-the-old-archive).
