<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/header-dark.png">
    <img alt="shopgen: realistic, reproducible order history for Shopify development stores" src="docs/assets/header-light.png" width="100%">
  </picture>
</p>

<p align="center">
  <a href="https://github.com/weekwright/shopgen/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/weekwright/shopgen/actions/workflows/ci.yml/badge.svg"></a>
  <a href="https://pkg.go.dev/github.com/weekwright/shopgen"><img alt="Go Reference" src="https://pkg.go.dev/badge/github.com/weekwright/shopgen.svg"></a>
  <a href="go.mod"><img alt="Go 1.22+" src="https://img.shields.io/badge/go-1.22%2B-00ADD8"></a>
  <img alt="Zero dependencies" src="https://img.shields.io/badge/dependencies-0-12935e">
  <a href="LICENSE"><img alt="MIT License" src="https://img.shields.io/badge/license-MIT-blue"></a>
</p>

**shopgen** backfills months of realistic order history into a Shopify development store, so reports, dashboards, syncs and apps have something real to chew on. You get growth trends, weekly cycles, sales spikes, repeat customers, refunds and cancellations, all landing on the right days in Shopify analytics.

```sh
shopgen plan                 # preview 90 days of orders, no writes
shopgen run -limit 3         # smoke test: create 3 orders
shopgen run                  # create them all, paced to Shopify's dev-store limit
shopgen purge -run <id>      # remove everything that run created
```

<p align="center"><img alt="shopgen plan and shopgen run in a terminal" src="docs/assets/terminal.png" width="100%"></p>

## Why

An empty dev store makes every report look the same: flat. Hand-made test orders all land "today", carry no history, and never get refunded. shopgen generates a whole history up front from a small JSON profile, then creates it through the Admin API with backdated `processedAt`, so the orders appear in analytics on their historical days.

- **Realistic.** Trend, day-of-week and hour-of-day curves, spikes, lognormal noise, Zipf-distributed best sellers, preferential-attachment repeat buyers, tax, free-shipping thresholds, and seven order outcomes with backdated refunds and archiving.
- **Reproducible.** Same profile + same catalog = the same orders, every time. A plan is a pure function of its inputs.
- **Survives overnight runs.** Dev stores allow 5 creates per minute, so 90 days takes about 19 hours. Stop it, crash it, `kill -9` it: resume picks up with no gaps and no duplicates.
- **Safe.** Refuses to write to anything but a development store, never emails anyone, never touches inventory, and `purge` removes only what it tagged.
- **Zero dependencies.** One static Go binary built from the standard library.

## Contents

- [Quick start](#quick-start)
- [How it works](#how-it-works)
- [Commands](#commands)
- [Profiles](#profiles)
- [What the orders look like](#what-the-orders-look-like)
- [Safety](#safety)
- [Known limits](#known-limits)
- [FAQ](#faq)
- Deeper docs: [Shopify setup](docs/shopify-setup.md) · [Profile reference](docs/profile.md) · [How it works](docs/how-it-works.md) · [Troubleshooting](docs/troubleshooting.md)

## Quick start

**1. Install.** With Go 1.22 or later:

```sh
go install github.com/weekwright/shopgen@latest
```

or download a binary from [Releases](https://github.com/weekwright/shopgen/releases).

**2. Connect a dev store.** Create a [development store](https://help.shopify.com/en/partners/dashboard/managing-stores/development-stores) with a few active products, then create an app with the `read_products`, `write_orders` and `write_customers` Admin API scopes. [docs/shopify-setup.md](docs/shopify-setup.md) walks through it.

```sh
export SHOPIFY_STORE=my-dev-store            # .myshopify.com is added for you
export SHOPIFY_ACCESS_TOKEN=shpat_…           # read from the environment only, never a flag
```

**3. Write a profile.** Start from the example (90 days, 60 orders a day, +25% growth, a Labor Day spike):

```sh
curl -O https://raw.githubusercontent.com/weekwright/shopgen/main/profile.example.json
cp profile.example.json profile.json
```

**4. Preview.** `plan` reads your catalog and prints what it would create. Nothing is written.

```sh
shopgen plan
```

```text
window      2026-07-15 → 2026-10-13 (91 days, America/Chicago)
orders      5593  (avg 61/day, busiest 2026-09-07 with 92)
customers   3000 distinct
revenue     587517.35 USD gross, 34401.42 refunded, AOV 105.05
outcomes    cancelled 124 (2.2%), fulfilled 4550 (81.4%), partially_fulfilled 103 (1.8%), ...
duration    19h00m at 5.0 orders/min
```

Without `-store`, `plan` uses a built-in 40-variant catalog, so you can design a profile before connecting anything.

**5. Smoke test, then run.**

```sh
shopgen run -limit 3          # three orders: check them in the admin
shopgen run -resume <run-id>  # carry on with the same run
```

Press Ctrl-C at any time. shopgen finishes the order in flight, saves its place and prints the exact resume command. A second Ctrl-C abandons the in-flight request; resume works out whether it landed.

**6. Clean up.**

```sh
shopgen purge -run <run-id>   # or: shopgen purge -all
```

## How it works

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/architecture-dark.png">
  <img alt="Profile and catalog feed a pure Generate function that produces a plan; the run loop paces orderCreate calls into the dev store and saves state after every order" src="docs/assets/architecture-light.png" width="100%">
</picture>

1. **Generate.** `Generate(profile, catalog, now)` builds the entire history in memory: every order's timestamp, customer, lines, totals and outcome. It uses one seeded PRNG and no I/O, and the catalog is sorted by variant ID, so the result never depends on API page order or the wall clock.
2. **Snapshot.** A new run gets an ID like `20261014-a1b2c3` and a directory in `.shopgen/` holding the exact profile bytes, the sorted catalog and `gen_now`, the reference time it was generated with.
3. **Create.** The loop paces `orderCreate` calls to 5 per minute (plus 2% headroom), cancels the orders the plan says are cancelled, and rewrites `state.json` atomically after every order.
4. **Resume.** Resume never re-reads your profile or re-fetches the catalog. It regenerates the identical plan from the snapshot and `gen_now` and continues from the next index.

Every order carries a unique `sgseq-<run>-<index>` tag. When a create may have committed but no reply arrived (a dropped connection, a 502), shopgen looks the order up by that tag instead of retrying blindly. That's how a run stays free of duplicates even across crashes.

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/lifecycle-dark.png">
  <img alt="Per-order lifecycle: record intent, orderCreate, then created, ambiguous (look up by tag), rate limited (wait and retry) or failed" src="docs/assets/lifecycle-light.png" width="100%">
</picture>

More detail, including the volume formula and the GraphQL client's retry rules: [docs/how-it-works.md](docs/how-it-works.md).

## Commands

Logs go to stderr with an `HH:MM:SS` prefix; `plan`'s summary goes to stdout. Exit codes: `0` success, `1` error, `2` usage error, `130` paused with Ctrl-C. Color follows the terminal and honors [`NO_COLOR`](https://no-color.org).

### `shopgen plan`

Preview a profile. Reads the store's catalog when `-store` is set; otherwise uses the built-in synthetic catalog.

| Flag | Default | |
|---|---|---|
| `-profile` | `profile.json` | Profile file |
| `-store` | `$SHOPIFY_STORE` | Store domain; a bare name gets `.myshopify.com` |
| `-opm` | `5` | Orders per minute, for the duration estimate |
| `-out` | | Write every planned order as JSON lines |
| `-api-version` | `2026-10` | Admin API version |

### `shopgen run`

| Flag | Default | |
|---|---|---|
| `-profile` | `profile.json` | Profile file; ignored with `-resume` |
| `-store` | `$SHOPIFY_STORE` | Store domain |
| `-opm` | `5` | Orders per minute. Dev and trial stores allow 5 |
| `-limit` | `0` (none) | Stop after N successful creates this session |
| `-resume` | | Continue a run by ID |
| `-state-dir` | `.shopgen` | Where run state lives |
| `-allow-non-dev` | `false` | Permit writes to a store that is not a dev store |
| `-skip-cancels` | `false` | Create planned-cancelled orders without cancelling them |
| `-api-version` | `2026-10` | Admin API version |
| `-endpoint` | | Override the GraphQL URL (for tests; skips the token check) |

### `shopgen purge`

Deletes orders and customers carrying a shopgen tag. Orders Shopify refuses to delete outright are cancelled first, then deleted. Customers who still have other orders are kept.

| Flag | Default | |
|---|---|---|
| `-run` | | Delete what one run created |
| `-all` | | Delete what every shopgen run created |
| `-yes` | `false` | Skip the typed `delete` confirmation |
| `-store` | `$SHOPIFY_STORE`, or the run's store | Store domain |
| `-state-dir` | `.shopgen` | Used to find a run's store |

Exactly one of `-run` and `-all` is required.

## Profiles

A profile is one JSON file. Everything except `start` and `orders_per_day` has a default, and invalid values are rejected at load time with a message naming the field.

```json
{
  "seed": 42,
  "start": "2026-07-15",
  "end": "2026-10-13",
  "timezone": "America/Chicago",
  "orders_per_day": 60,
  "growth": 0.25,
  "spikes": [{ "date": "2026-09-07", "days": 1, "multiplier": 2.2 }],
  "tax_rate": 0.0825,
  "customers": { "pool": 3000, "repeat_rate": 0.32 },
  "items": { "min": 1, "max": 5 },
  "free_shipping_over": 75,
  "archive_fulfilled": true
}
```

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/volume-dark.png">
  <img alt="Bar chart of daily generated orders for the example profile, with the expected mean line showing growth, the weekly cycle and a spike on September 7" src="docs/assets/volume-light.png" width="100%">
</picture>

Daily volume is the mean × a linear growth trend × the weekday multiplier × any spike × lognormal noise. Each order then gets an hour from an evening-peaked curve, a customer, a basket, shipping, tax and an outcome. See the [profile reference](docs/profile.md) for every field. [`examples/`](examples) has ready-made profiles for a smoke test, a holiday season and a refund-heavy store.

## What the orders look like

| Outcome | Default share | `financialStatus` | `fulfillmentStatus` | Transactions | After create |
|---|---|---|---|---|---|
| `fulfilled` | 82% | `PAID` | `FULFILLED` | sale | |
| `unfulfilled` | 4% | `PAID` | | sale | |
| `partially_fulfilled` | 2% | `PAID` | `PARTIAL` | sale | |
| `pending_payment` | 2% | `PENDING` | | none | |
| `refunded` | 4% | `REFUNDED` | `FULFILLED` | sale + full refund | |
| `partially_refunded` | 4% | `PARTIALLY_REFUNDED` | `FULFILLED` | sale + partial refund | |
| `cancelled` | 2% | `PAID` | | sale | `orderCancel` |

Recent orders skew unfulfilled, as in a real store: an order younger than `fulfillment_lag_days` is still unfulfilled with probability `1 − age/lag`. Refunds land 2–20 days after the sale, and with `archive_fulfilled` orders are closed 3–10 days later.

Every order is tagged `shopgen`, `shopgen-run-<id>`, `shopgen-<outcome>` and `sgseq-<id>-<index>`, and has `sourceIdentifier: shopgen:<id>:<index>`. Customers are upserted by email (`first.last.<n>@shopgen.example`), so repeat orders link to one customer, and are tagged too. Addresses are real US city/state/ZIP combinations so address validation passes.

## Safety

- **Dev stores only.** Before any write, shopgen checks `shop.plan.partnerDevelopment` and stops if it's false. `-allow-non-dev` overrides this for paid staging stores.
- **No emails, no stock changes.** Every create sends `sendReceipt: false`, `sendFulfillmentReceipt: false` and `inventoryBehaviour: BYPASS`; cancellations use `notifyCustomer: false`.
- **Undeliverable addresses.** `email_domain` must be a reserved domain (`.example`, `.test`, `.invalid`, `example.com`, …), so no generated email can reach a real inbox.
- **The token stays out of shell history.** It's read only from `SHOPIFY_ACCESS_TOKEN`.
- **Purge is scoped.** It only deletes objects carrying a shopgen tag and asks you to type `delete` first.

## Known limits

These are deliberate in v1:

- **Refunds are transactions, not Refund objects.** Financial reports see them; line-level return reports don't.
- **`cancelledAt` is the run's time.** Shopify sets it when `orderCancel` runs; it can't be backdated.
- **Today's prices.** Every line uses the variant's current price; there are no discount codes yet.
- **US addresses only**, and amounts assume a two-decimal currency.
- **One order per call.** The 5/min cap makes batching pointless on dev stores; paid staging stores can raise `-opm` together with `-allow-non-dev`.

## FAQ

**Why does a 90-day profile take 19 hours?** Shopify caps `orderCreate` at 5 calls per minute on development and trial stores. `plan` prints the duration up front. Run it overnight with `-limit` off, or in `tmux`/`screen`; it's built to be stopped and resumed.

**Will the same profile give the same orders next week?** For a window that ended more than `fulfillment_lag_days` ago, yes, exactly. Generation takes "now" as an input (orders can't be in the future, and recent ones skew unfulfilled), so a window ending today shifts slightly as the clock moves. A run records its `gen_now` once and reuses it on every resume, so a run's plan never changes.

**Can I point it at my production store?** Not by accident: it refuses non-dev stores unless you pass `-allow-non-dev`. Please don't.

**Do test orders show up in analytics?** Shopify hides `test: true` orders from analytics, so shopgen sends `test: false` by default. Set `"test_orders": true` if you want them flagged.

**What if my store has no products?** `run` stops with a message asking you to add some. shopgen reads your catalog; it never creates products.

## Contributing

Issues and pull requests are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md). The test suite runs offline against a fake Admin API:

```sh
gofmt -l . && go vet ./... && go test -race ./...
```

## License

[MIT](LICENSE). shopgen is an independent project and is not affiliated with or endorsed by Shopify.
