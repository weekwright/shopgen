# How it works

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/architecture-dark.png">
  <img alt="Architecture: profile and catalog feed Generate, which produces a plan; the run loop creates orders in the dev store and saves state after each one" src="assets/architecture-light.png" width="100%">
</picture>

shopgen splits the job into a **pure planning step** and a **slow, resumable execution step**. Planning takes milliseconds; execution takes hours because of Shopify's dev-store limit. Keeping them apart is what makes stopping and resuming safe.

## Generation

`Generate(profile, catalog, now) → Plan` (in [`gen.go`](../gen.go)) has no I/O. Determinism is its first rule, because resume depends on regenerating exactly the same plan:

- **One seeded PRNG.** A `math/rand/v2` PCG seeded from `seed`. No global `rand`, no map iteration order, no wall clock.
- **Sorted inputs.** The catalog is sorted by variant ID and de-duplicated before use, so the order of API pages can't change anything. Outcome keys are sorted before their weights are built.
- **`now` is a parameter.** It drops timestamps in the future and drives the recency rules. A run records it once as `gen_now`.
- **Customers stand alone.** `CustomerAt(profile, i)` uses its own PRNG seeded by `(seed, i)`, so customer *i* is identical however the plan was built.

For each day it computes the expected volume (see the [formula](profile.md#window-and-volume)), draws that many local timestamps from the hourly curve, sorts them, and builds each order in time order: customer, basket, shipping, tax, outcome, refund and archive times. Because orders are created in time order, Shopify's order numbers rise with date, just as in a real store.

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/volume-dark.png">
  <img alt="Daily generated orders for the example profile against the expected mean" src="assets/volume-light.png" width="100%">
</picture>

Money is integer cents throughout. Floating point appears only while computing tax, which is immediately rounded.

## Execution

### Startup

1. **Safety gate.** `query { shop { name myshopifyDomain plan { partnerDevelopment } } }`. Not a dev store and no `--allow-non-dev`: exit before any write.
2. **Scope check.** List the token's granted scopes (`/admin/oauth/access_scopes.json`, via go-shopify) and stop if `read_products`, `write_orders` or `write_customers` is missing. A `write_` scope implies its `read_` scope. If the list can't be fetched, shopgen warns and lets the first write decide.
3. **New run.** Read the profile bytes, fetch and sort the catalog, create a run ID `YYYYMMDD-<6 hex>`, and write `profile.json` (the exact bytes), `catalog.json` and `state.json` with `gen_now` into `.shopgen/<run-id>/`.
4. **Resume.** Load those three files from the run directory. Never re-read the original profile path, never re-fetch the catalog. Regenerate the plan with the stored `gen_now` and refuse to continue if the order count differs from `state.total`, or if `profile.json` no longer matches its recorded hash.

### The loop

For each index from `done` to `total`:

1. Stop if `--limit` successes have been reached this session.
2. **Pace**: wait until the previous start + 60 s ÷ opm × 1.02. The 2% headroom keeps clock skew from tripping the 5/min cap. Ctrl-C during the wait pauses immediately.
3. Record `inflight = index` in `state.json`.
4. **Create** the order (below). On success append to `orders.jsonl` and, if the plan says so, `orderCancel`. On failure append to `failures.jsonl`.
5. Advance `done`, clear `inflight`, rewrite `state.json`.

**Fail fast:** if the first three attempts of a session all fail, shopgen rolls `done` back so those orders aren't skipped, saves, and exits with a message telling you to fix the cause and resume. A bad scope or an archived product fails loudly instead of burning through the plan.

### Exactly once

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/lifecycle-dark.png">
  <img alt="Per-order lifecycle diagram" src="assets/lifecycle-light.png" width="100%">
</picture>

A mutation that might have committed is never simply retried. The client returns `ErrAmbiguous` when:

- the request was written but the connection failed before a reply (detected with `httptrace`, so a failure *before* sending is safely retried),
- a mutation gets HTTP 500, 502 or 504,
- a mutation's response body can't be read or decoded.

On `ErrAmbiguous`, shopgen waits 15 seconds for search indexing and queries `orders(query: "tag:'sgseq-<run>-<index>'")`. Found: the order exists, so record it as created. Not found: retry. Each order gets at most six attempts.

`orderCreate` userErrors mentioning `limit`, `too many` or `throttl` wait 60 seconds and retry within the same budget. Any other userError fails the order and is recorded as `field.path: message`.

The `inflight` marker covers what the in-process retry can't: a crash, `kill -9`, or a second Ctrl-C that abandons a request mid-flight. On resume, if `inflight` equals `done`, shopgen looks that order up by tag before creating anything, and cancels it if it should have been cancelled.

### Signals

| You press | shopgen does |
|---|---|
| Ctrl-C while pacing | Pauses immediately, saves, prints the resume command |
| Ctrl-C mid-request | Lets the in-flight order (and its cancel) finish, then pauses |
| Ctrl-C twice | Abandons the in-flight request; resume reconciles it by tag |

`orderCancel` runs with its own two-minute context, so a Ctrl-C between create and cancel never leaves a planned-cancelled order open.

### The GraphQL client

`Do(ctx, query, vars, out)` makes up to 10 attempts:

| Response | Action |
|---|---|
| `429` | Wait `Retry-After` seconds, else exponential backoff |
| `503`, or any `5xx` on a query | Backoff and retry |
| `500`/`502`/`504` on a mutation | `ErrAmbiguous` (see above) |
| `401` with client credentials | Fetch a new token and resend once (a rejected token means nothing ran, so even a mutation is safe to resend) |
| `401` / `403` otherwise | Fail immediately with a hint about scopes |
| Other `4xx` | Fail with the body, truncated to 500 characters |
| GraphQL `THROTTLED` | Wait `max(requested − available, 1) ÷ restoreRate + 250 ms`, from `extensions.cost.throttleStatus` |
| Other GraphQL errors | Fail with their messages joined |

Backoff is 1 s × 2^(attempt − 1), capped at 60 s. Every sleep honors cancellation. The HTTP timeout is 60 s.

### Access tokens

Requests get their token from a `TokenSource`. `SHOPIFY_ACCESS_TOKEN` becomes a fixed token. `SHOPIFY_CLIENT_ID` plus `SHOPIFY_CLIENT_SECRET` become a `ClientCredentials` source, which runs the [client credentials grant](https://shopify.dev/docs/apps/build/authentication-authorization/access-tokens/client-credentials-grant), caches the 24-hour token, renews it five minutes before expiry, and renews it early if Shopify answers `401`.

## State files

Written atomically (write `*.tmp`, fsync, rename), so a crash never leaves a half-written file.

| File | Contents |
|---|---|
| `state.json` | `run_id`, `store`, `profile_hash`, `gen_now`, `total`, `done` (next index), `created`, `failed`, `inflight`, `started_at`, `updated_at` |
| `profile.json` | The profile exactly as given |
| `catalog.json` | The sorted catalog snapshot (ID, title, price in cents) |
| `orders.jsonl` | One line per created order: `index`, `id`, `name`, `outcome`, `processed_at` |
| `failures.jsonl` | One line per failure: `index`, `error`, `time`, and `order_id` for cancel failures |

`orders.jsonl` gives you a ready-made map from plan index to Shopify order ID, handy for asserting on reports in tests.

## Purge

Purge deletes only objects carrying a shopgen tag: `shopgen-run-<id>` with `--run`, `shopgen` with `--all`.

1. Page through `orders(first: 50, query: "tag:'<tag>'")`. For each order, `orderDelete`. If Shopify refuses and the order isn't cancelled, `orderCancel` (no refund, no restock, no notification), then delete again. Orders that still fail are remembered and skipped on later pages.
2. Do the same for `customers(...)` with `customerDelete`. Customers who still have orders can't be deleted and are counted as kept.

Search indexing can lag a little behind deletes, so a second `purge` a minute later sometimes catches stragglers.

## Code layout

| Path | What's in it |
|---|---|
| [`main.go`](../main.go) | Calls `cmd.Execute()` |
| [`cmd/`](../cmd) | The cobra commands, one file each (`root`, `plan`, `run`, `purge`, `doctor`, `version`), and viper configuration in `root.go` |
| [`internal/shopgen/`](../internal/shopgen) | Profiles, `Generate`, the run loop, state files, purge and doctor |
| [`internal/shopify/`](../internal/shopify) | The GraphQL client, typed operations, order input types, token sources, and helpers for store domains, admin URLs, GIDs, money and scopes |
| [`internal/shopify/shopifytest/`](../internal/shopify/shopifytest) | An in-memory fake Admin API with fault injection, used by every store-facing test |
| [`internal/ui/`](../internal/ui) | lipgloss styling, log lines and the pacing countdown |
