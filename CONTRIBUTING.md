# Contributing to shopgen

Thanks for helping. shopgen is small on purpose: one Go package, standard library only, a test suite that runs offline in a couple of seconds. Please keep it that way.

## Getting started

```sh
git clone https://github.com/weekwright/shopgen && cd shopgen
make check        # gofmt, go vet, go test -race
go run . plan -profile profile.example.json
```

You don't need a Shopify store to work on shopgen. Store-facing code is tested against `fake_test.go`, an in-memory Admin API that pattern-matches on query text and can inject throttling and dropped connections.

## Layout

| File | What's in it |
|---|---|
| `main.go` | CLI: flags, help, exit codes, signal handling |
| `profile.go` | Profile schema, defaults and validation |
| `gen.go` | `Generate` and `CustomerAt`: the pure, deterministic planner |
| `catalog.go`, `data.go` | Variants, money helpers, built-in names, cities and synthetic catalog |
| `summary.go` | `plan`'s summary |
| `mapping.go` | Plan record → `orderCreate` input |
| `shopify.go` | The GraphQL client and its retry rules |
| `api.go` | GraphQL documents and typed wrappers |
| `run.go`, `state.go` | The run loop, idempotent create, state files |
| `purge.go` | Purge |
| `ui.go` | Terminal styling, log lines, countdown |
| `*_test.go` | Tests; `fake_test.go` is the fake Admin API |

## Ground rules

- **Determinism is the top rule.** Anything inside `Generate` must draw from the one seeded PRNG in a fixed order: no maps iterated without sorting, no wall clock, no global `rand`. If a change alters the plan for an existing profile, say so in the PR and the changelog, because resuming an in-progress run across that change will be refused.
- **Never retry a mutation that may have committed.** Route anything ambiguous through `ErrAmbiguous` and the tag lookup.
- **No third-party modules.** If you need something, it probably fits in 30 lines.
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
