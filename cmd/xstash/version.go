package main

// Release builds stamp these values with -ldflags. Source builds identify the
// release family without requiring Git or accessing local authentication state.
var version = "2.0.0"
var commit = "unknown"

const usage = `Usage: xstash <command> [options]

A read-only X bookmark and like archive. Sync uses the paid X API.

Commands:
  config init|import|set|show  Manage local authentication and settings
  db init|check               Create a new database or inspect an existing one
  sync                       Sync bookmarks and/or likes (bounded by default)
  export                     Export local data as JSON, Markdown or CSV
  stats                      Show local counts and estimated API costs
  pending                    List posts awaiting processing for a purpose
  complete                   Mark local processing complete for a purpose
  version                    Print version and source commit

Options:
  --help, -h                 Show this help without reading config or database
  --version, -v              Print version without reading config or database

Use xstash <command> --help for command flags (config requires a subcommand).
See README.md for first-run setup, cost limits and upgrading from v1.
`
