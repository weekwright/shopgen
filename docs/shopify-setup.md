# Shopify setup

shopgen needs three things: a **development store**, some **active products** in it, and an **Admin API access token** with three scopes. This page walks through each.

## 1. A development store

Create one from your [Shopify Partner](https://www.shopify.com/partners) or Dev Dashboard account ([Shopify's guide](https://help.shopify.com/en/partners/dashboard/managing-stores/development-stores)). shopgen checks `shop.plan.partnerDevelopment` before every run and refuses to write anywhere else unless you pass `-allow-non-dev`.

Development and trial stores allow **5 `orderCreate` calls per minute**. That limit, not shopgen, sets how long a run takes: about 7,200 orders a day at most. `shopgen plan` prints the expected duration before you commit.

## 2. Products

shopgen reads the catalog; it never creates products. It uses every **active** variant priced above zero. A dozen products with a few variants each is plenty. Shopify's sample product CSV or any theme's demo content works.

Prices are read once, when a run starts, and snapshotted to `.shopgen/<run-id>/catalog.json`. Changing products mid-run doesn't affect that run.

## 3. An access token

The app needs these Admin API scopes:

| Scope | Used for |
|---|---|
| `read_products` | Reading the catalog (`productVariants`) |
| `write_orders` | `orderCreate`, `orderCancel`, `orderDelete`, and searching orders by tag |
| `write_customers` | Upserting customers on create, `customerDelete` during purge |

How you get a token depends on when and where the app was made. Shopify changed this in 2026, so check [Shopify's current docs](https://shopify.dev/docs/apps/build/authentication-authorization) for exact steps.

- **Dev Dashboard app** (the current path). Create an app in the [Dev Dashboard](https://dev.shopify.com), give it the scopes above and install it on your dev store. Then exchange its client ID and secret for an access token, using the client credentials grant when the app and store belong to the same organization. Tokens from that grant are short-lived (about 24 hours), which is fine: a 90-day profile runs in about 19 hours, and if a token does expire mid-run shopgen stops cleanly on the `401` so you can export a fresh one and resume.
- **Legacy custom app** (created in the store admin before January 2026). These still work: copy the Admin API access token (`shpat_…`) from the app's API credentials page.

## 4. Environment

```sh
export SHOPIFY_STORE=my-dev-store          # or my-dev-store.myshopify.com
export SHOPIFY_ACCESS_TOKEN=shpat_…
```

The token is read **only** from the environment, never from a flag, so it doesn't end up in shell history or process listings. Consider a `.env` file loaded by [direnv](https://direnv.net) (`.env` is already in `.gitignore`).

Check the connection without writing anything:

```sh
shopgen plan            # with SHOPIFY_STORE set, this fetches your real catalog
```

## API version

shopgen targets Admin API `2026-10` by default. Pass `-api-version` to pin another. Notes on versions:

- `orderCreate` accepts past `processedAt` values. From `2026-07` on, future values are clamped to now. shopgen never generates timestamps after its reference time, so either behaviour is fine.
- `orderCancel` takes `refundMethod`, which replaced the older `refund` argument.

## Paid staging stores

Plus sandbox and other paid stores aren't development stores, so shopgen refuses them by default. If you really mean it:

```sh
shopgen run -allow-non-dev -opm 30
```

Paid stores have higher limits; the GraphQL client still backs off on `429` and `THROTTLED` responses. Never point shopgen at a store with real customers.
