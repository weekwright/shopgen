# Changelog

All notable changes to this project are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses [semantic versioning](https://semver.org/).

## [Unreleased]

### Added

- `shopgen doctor`: check the profile, credentials, connection, dev-store flag, granted scopes, catalog and currency before a run, without writing anything.
- Automatic Admin API tokens for Dev Dashboard apps: set `SHOPIFY_CLIENT_ID` and `SHOPIFY_CLIENT_SECRET` and shopgen runs the client credentials grant, renewing the 24-hour token before it expires and on `401`.
- `run` checks the token's granted scopes before its first write.
- Config file (`shopgen.yaml`) and `SHOPGEN_*` environment variables via viper; precedence is flags > env > file > defaults. Safety flags (`--allow-non-dev`, `--all`, `--yes`) are honored on the command line only.
- Shell completions (`shopgen completion`), short flags (`-s`, `-p`, `-n`, `-r`, `-o`, `-y`) and `--no-color`.
- `internal/shopify/shopifytest`, a reusable fake Admin API.

### Changed

- The CLI is built with cobra, one file per command under `cmd/`. Flags use the `--flag` form.
- Terminal styling uses lipgloss.
- Code moved into `internal/shopgen`, `internal/shopify` and `internal/ui`.
- Requires Go 1.24 to build.

- `shopgen plan`: validate a profile and preview its history against the store's catalog or a built-in 40-variant catalog; `--out` writes every planned order as JSON lines.
- `shopgen run`: create a plan's orders through `orderCreate` with backdated `processedAt`, paced to 5 per minute, with dev-store safety gate, resumable state, idempotent creates (`sgseq` tag lookup), in-flight reconciliation after crashes, and fail-fast on early errors.
- `shopgen purge`: delete orders and customers by run or across all runs, with typed confirmation.
- Profile format with growth, weekly and hourly curves, spikes, lognormal noise, Zipf product popularity, repeat customers, shipping, tax, free-shipping threshold, seven outcomes, refunds and archiving.
- Example profiles in `examples/`.
