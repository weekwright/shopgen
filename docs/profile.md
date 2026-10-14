# Profile reference

A profile is one JSON file describing the history to generate. Only `start` and `orders_per_day` are required. Unknown fields, unknown outcome keys and invalid values are rejected when the profile loads, with a message naming the field:

```text
✗ profile.json: customers.repeat_rate: must be in [0, 1], got 1.3
✗ profile.json: unknown field "order_per_day"
```

Run `shopgen plan` after every edit; it validates the profile and shows the result in seconds.

## Fields

### Window and volume

| Field | Type | Default | Rule |
|---|---|---|---|
| `seed` | int | `0` | Seeds every random choice. Change it for a different but equally shaped history |
| `start` | `YYYY-MM-DD` | required | First day, inclusive, in `timezone` |
| `end` | `YYYY-MM-DD` | today | Last day, inclusive. Error if before `start` |
| `timezone` | IANA zone | `UTC` | Days, weekdays and hours are local to it, e.g. `America/Chicago` |
| `orders_per_day` | float | required | Mean daily volume over the window; `> 0` |
| `growth` | float | `0` | Linear trend from start to end, e.g. `0.25` = +25%. The mean is preserved |
| `weekly` | 7 floats | `[0.9, 0.95, 1, 1, 1, 1.05, 1.1]` | Sunday..Saturday multipliers |
| `hourly` | 24 floats | evening-peaked | Relative weight of each local hour; peaks around 8 pm |
| `noise` | float | `0.15` | Standard deviation of mean-1 lognormal daily noise. `0` = smooth |
| `spikes` | list | none | `{date, days, multiplier}`: multiply volume for `days` days (default 1) from `date` |

Daily volume for day *d* of *D*, with *t = d / (D − 1)*:

```text
μ_d = orders_per_day × (1 + g·t) / (1 + g/2) × weekly[weekday] × spike × e^(σZ − σ²/2)
```

where *g* is `growth`, *σ* is `noise` and *Z* is standard normal. The day's count is ⌊μ⌋, plus one with probability equal to the fractional part. The `(1 + g/2)` divisor keeps the average at `orders_per_day` whatever the growth, and the `−σ²/2` term keeps the noise mean at 1.

Timestamps after the reference time are dropped, so a window ending today stops at the current hour.

### Money

| Field | Type | Default | Rule |
|---|---|---|---|
| `currency` | ISO code | `USD` | Shop currency for all amounts. Match your store's currency |
| `tax_rate` | float | `0` | Applied to the subtotal and rounded to the cent; sent as one "Sales Tax" line |
| `gateway` | string | `manual` | Gateway name on transactions |
| `shipping` | list | Standard 5.99 (0.75), Express 14.99 (0.25) | `{title, price, weight}`, a weighted choice. Weight ≤ 0 means 1 |
| `free_shipping_over` | float | `0` (off) | Subtotal at or above which the **first** shipping option is free |

All stored amounts are integer cents. `total = subtotal + shipping + tax`.

### Customers

| Field | Type | Default | Rule |
|---|---|---|---|
| `customers.pool` | int | `5000` | Maximum distinct customers |
| `customers.repeat_rate` | float | `0.3` | Probability an order comes from an existing customer |
| `customers.email_domain` | string | `shopgen.example` | Must be a reserved domain: `*.example`, `*.test`, `*.invalid`, `*.localhost`, or `example.com/.net/.org` |

Repeat buyers are chosen uniformly from the customers of *all previous orders*, so customers who already bought often are more likely to buy again (preferential attachment). This gives the long tail of loyal customers real stores have. Once `pool` new customers exist, every order picks one of them at random.

Customer *i* is always the same person: the same name, `first.last.i@<email_domain>`, and a street address in a real US city/state/ZIP.

### Baskets

| Field | Type | Default | Rule |
|---|---|---|---|
| `items.min` | int | `1` | Minimum distinct lines per order |
| `items.max` | int | `min + 3` | Maximum distinct lines per order |
| `items.max_qty` | int | `3` | Maximum quantity per line |
| `items.popularity_skew` | float | `1.1` | Zipf exponent over the catalog. `0` = uniform, higher = a few best sellers dominate |

The line count starts at `min` and grows by one with probability 0.35, up to `max` (and the catalog size). Products are drawn without replacement from a Zipf distribution over a *seeded shuffle* of the catalog, so the best sellers are random products rather than the lowest IDs. Each line's quantity starts at 1 and grows with probability 0.2 up to `max_qty`. Line prices are the variant's current price.

### Outcomes

| Field | Type | Default | Rule |
|---|---|---|---|
| `outcomes` | map | see below | Outcome key → weight. Keys must be valid, no negatives, sum > 0. Replaces the defaults entirely |
| `fulfillment_lag_days` | float | `2` | Window in which recent orders skew unfulfilled |
| `archive_fulfilled` | bool | `false` | Close (archive) fulfilled and refunded orders 3–10 days after the sale |
| `test_orders` | bool | `false` | Send `test: true`. Off by default because Shopify analytics hides test orders |

Default weights: `fulfilled` 0.82, `unfulfilled` 0.04, `partially_fulfilled` 0.02, `pending_payment` 0.02, `refunded` 0.04, `partially_refunded` 0.04, `cancelled` 0.02. Weights don't need to sum to 1.

Rules applied after the draw:

- **Recency.** An order younger than `fulfillment_lag_days` that isn't `pending_payment` or `cancelled` becomes `unfulfilled` with probability `1 − age/lag`. If it stays as it was and was a refund outcome, it becomes `fulfilled`, since a brand-new order hasn't been refunded yet.
- **Refund timing.** Refunds happen 2–20 days after the sale, at a random time of day. One that would land in the future is pulled back into the hour before the reference time; if that's still not after the sale, the order becomes `fulfilled`.
- **Refund amounts.** `refunded` returns the full total. `partially_refunded` returns one random line's unit price plus its tax, or half the total if that would be the whole order.
- **Archiving.** With `archive_fulfilled`, closing happens at least an hour after any refund, and is skipped if it would be in the future.

## Recipes

**Smooth, predictable data for assertions.** `"noise": 0`, `"growth": 0`, and a fixed `"end"`. Daily counts then follow `weekly` alone, give or take one order from rounding.

**Black Friday / Cyber Monday.** `"spikes": [{ "date": "2025-11-28", "days": 4, "multiplier": 3 }]`. Overlapping spikes multiply.

**A store with a refund problem.** Raise `refunded` and `partially_refunded` in `outcomes`. See [`examples/refund-heavy.json`](https://github.com/weekwright/shopgen/blob/main/examples/refund-heavy.json).

**Bigger baskets.** `"items": { "min": 2, "max": 8, "max_qty": 5 }`.

**Night-owl store.** Supply your own 24-value `hourly` curve; only the relative sizes matter.

**Quick smoke profile.** Three days at 8 orders a day is about 24 orders, roughly 5 minutes at 5/min. See [`examples/smoke.json`](https://github.com/weekwright/shopgen/blob/main/examples/smoke.json).

## Reproducibility

Same profile + same catalog + same reference time = the same orders, byte for byte. The reference time matters only near the end of the window: it drops future timestamps and drives the recency rules. For a window that ended more than `fulfillment_lag_days` ago, plans are identical whenever you run them.

A run records its reference time once (`gen_now` in `state.json`) and snapshots the profile and catalog, so every resume regenerates exactly the same plan.
