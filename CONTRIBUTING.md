# Contributing to shopgen

Thanks for helping. shopgen is small on purpose: a handful of packages, a few well-known dependencies, and a test suite that runs offline in a few seconds. Please keep it that way.

## Getting started

```sh
git clone https://github.com/weekwright/shopgen && cd shopgen
make check        # gofmt, go vet, go test -race
go run . plan -p profile.example.json
```

You don't need a Shopify store to work on shopgen. Store-facing code is tested against [`internal/shopify/shopifytest`](internal/shopify/shopifytest), an in-memory Admin API that pattern-matches on query text, serves the access-scopes and OAuth token endpoints, and can inject throttling, dropped connections and expired tokens.

## Layout

| Path | What's in it |
|---|---|
| `main.go` | Calls `cmd.Execute()` |
| `cmd/root.go` | Root command, global flags, viper config (flags > env > file > defaults), credentials, exit codes, signals, help template |
| `cmd/plan.go`, `run.go`, `purge.go`, `doctor.go`, `version.go` | One file per command: flags and wiring only |
| `internal/shopgen/profile.go` | Profile schema, defaults and validation |
| `internal/shopgen/gen.go` | `Generate` and `CustomerAt`: the pure, deterministic planner |
| `internal/shopgen/catalog.go`, `data.go` | Variants, built-in names, cities and synthetic catalog |
| `internal/shopgen/mapping.go` | Plan record → `orderCreate` input, tags |
| `internal/shopgen/connect.go` | Store connection, dev-store gate, scope check |
| `internal/shopgen/run.go`, `state.go` | The run loop, idempotent create, state files |
| `internal/shopgen/purge.go`, `doctor.go`, `summary.go` | Purge, doctor checks, `plan`'s summary |
| `internal/shopify/client.go` | The GraphQL client and its retry rules |
| `internal/shopify/api.go`, `orders.go` | GraphQL documents, typed operations, order input types |
| `internal/shopify/token.go` | Static tokens and the client credentials grant |
| `internal/shopify/helpers.go` | Store domains, admin URLs, GIDs, money, scopes (with go-shopify) |
| `internal/shopify/shopifytest/` | The fake Admin API |
| `internal/ui/` | lipgloss styles, log lines, countdown |

Adding a command: create `cmd/<name>.go` with a `newXxxCmd` method on `app`, register it in `newRootCmd`, keep logic in `internal/`, and add a CLI test in `cmd/cmd_test.go`.

## Ground rules

- **Determinism is the top rule.** Anything inside `Generate` must draw from the one seeded PRNG in a fixed order: no maps iterated without sorting, no wall clock, no global `rand`. If a change alters the plan for an existing profile, say so in the PR and the changelog, because resuming an in-progress run across that change will be refused.
- **Never retry a mutation that may have committed.** Route anything ambiguous through `ErrAmbiguous` and the tag lookup.
- **Dependencies earn their place.** Prefer well-maintained, widely used modules, and check that a new one still builds with the Go version in `go.mod`.
- **Tests first** for behavior changes. Store-facing behavior gets an end-to-end test against the fake.
- **Docs move with code.** Flags, profile fields and output formats are documented in `README.md` and `docs/`.

## Docs images

The PNGs in `docs/assets/` are rendered from HTML in `docs/assets/src/` using real plan data:

```sh
make demo      # re-record the run shown in the terminal screenshot
make assets    # needs Node and Playwright with Chromium
```

Commit the sources and the PNGs together.

## Releasing

Tag `vX.Y.Z` on `main`. The release workflow runs the tests and publishes binaries with GoReleaser. Update `CHANGELOG.md` first.
