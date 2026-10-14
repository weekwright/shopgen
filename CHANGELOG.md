# Changelog

All notable changes to this project are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses [semantic versioning](https://semver.org/).

## [Unreleased]

### Added

- `shopgen plan`: validate a profile and preview its history against the store's catalog or a built-in 40-variant catalog; `-out` writes every planned order as JSON lines.
- `shopgen run`: create a plan's orders through `orderCreate` with backdated `processedAt`, paced to 5 per minute, with dev-store safety gate, resumable state, idempotent creates (`sgseq` tag lookup), in-flight reconciliation after crashes, and fail-fast on early errors.
- `shopgen purge`: delete orders and customers by run or across all runs, with typed confirmation.
- Profile format with growth, weekly and hourly curves, spikes, lognormal noise, Zipf product popularity, repeat customers, shipping, tax, free-shipping threshold, seven outcomes, refunds and archiving.
- Example profiles in `examples/`.
