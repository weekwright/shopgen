<!-- Generated from README.md by scripts/sync-docs.py. Edit the README, then run `make docs-sync`. -->

# Commands and configuration

## Commands


Logs go to stderr with an `HH:MM:SS` prefix; `plan`'s summary and `doctor`'s report go to stdout. Exit codes: `0` success, `1` error, `2` usage error, `130` paused with Ctrl-C. Color follows the terminal and honors [`NO_COLOR`](https://no-color.org) and `--no-color`. `shopgen completion bash|zsh|fish|powershell` prints shell completions.

### Global flags

| Flag | Default | |
|---|---|---|
| `-s`, `--store` | `$SHOPIFY_STORE` | Store domain; a bare name gets `.myshopify.com` |
| `--api-version` | `2026-10` | Admin API version |
| `--state-dir` | `.shopgen` | Where run state lives |
| `--config` | `./shopgen.yaml` | Config file; see [Configuration](#configuration) |
| `--no-color` | `false` | Plain output |

### `shopgen doctor`

Checks the profile, credentials, connection, dev-store flag, granted scopes, catalog and currency, and estimates the run. Writes nothing. Exits `1` if any check fails.

| Flag | Default | |
|---|---|---|
| `-p`, `--profile` | `profile.json` | Profile to check |
| `--opm` | `5` | Orders per minute, for the estimate |

### `shopgen plan`

Preview a profile. Reads the store's catalog when a store is set; otherwise uses the built-in synthetic catalog.

| Flag | Default | |
|---|---|---|
| `-p`, `--profile` | `profile.json` | Profile file |
| `--opm` | `5` | Orders per minute, for the duration estimate |
| `-o`, `--out` | | Write every planned order as JSON lines |

### `shopgen run`

| Flag | Default | |
|---|---|---|
| `-p`, `--profile` | `profile.json` | Profile file; ignored with `--resume` |
| `--opm` | `5` | Orders per minute. Dev and trial stores allow 5 |
| `-n`, `--limit` | `0` (none) | Stop after N successful creates this session |
| `-r`, `--resume` | | Continue a run by ID |
| `--allow-non-dev` | `false` | Permit writes to a store that is not a dev store. Command line only |
| `--skip-cancels` | `false` | Create planned-cancelled orders without cancelling them |

### `shopgen purge`

Deletes orders and customers carrying a shopgen tag. Orders Shopify refuses to delete outright are cancelled first, then deleted. Customers who still have other orders are kept. Without `--store`, `--run` uses the store the run wrote to.

| Flag | Default | |
|---|---|---|
| `--run` | | Delete what one run created |
| `--all` | | Delete what every shopgen run created. Command line only |
| `-y`, `--yes` | `false` | Skip the typed `delete` confirmation. Command line only |
| `--allow-non-dev` | `false` | Permit deletes on a store that is not a dev store. Command line only |

Exactly one of `--run` and `--all` is required.

## Configuration


Settings come from, highest priority first: **flags**, **environment variables**, a **config file**, then defaults.

- **Config file:** `./shopgen.yaml` (or `.yml`, `.json`, `.toml`), else `shopgen.yaml` in your user config directory (`~/.config/shopgen/` on Linux, `~/Library/Application Support/shopgen/` on macOS), or any file passed with `--config`.
- **Environment:** `SHOPGEN_<FLAG>` for any flag, e.g. `SHOPGEN_STATE_DIR` or `SHOPGEN_OPM`. `SHOPIFY_STORE` and `SHOPIFY_CLIENT_ID` also work.

```yaml
# shopgen.yaml
store: acme-dev
profile: profiles/q3.json
state-dir: .shopgen
api-version: "2026-10"
```

Secrets (`SHOPIFY_ACCESS_TOKEN`, `SHOPIFY_CLIENT_SECRET`) are read from the environment only. Flags that loosen safety (`--allow-non-dev`, `--all`, `--yes`) are honored only on the command line, so no config file can switch them on silently.
