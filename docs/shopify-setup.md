# Shopify setup

shopgen needs three things: a **development store**, some **active products** in it, and **Admin API credentials** with three scopes. This page walks through each, then `shopgen doctor` confirms it all works.

## 1. A development store

Create one from your [Shopify Partner](https://www.shopify.com/partners) or Dev Dashboard account ([Shopify's guide](https://help.shopify.com/en/partners/dashboard/managing-stores/development-stores)). shopgen checks `shop.plan.partnerDevelopment` before every run and refuses to write anywhere else unless you pass `--allow-non-dev`.

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

How you get credentials depends on when and where the app was made. Shopify changed this in 2026; see [Shopify's authentication docs](https://shopify.dev/docs/apps/build/authentication-authorization) for the current screens.

### Dev Dashboard app (recommended)

Create an app in the [Dev Dashboard](https://dev.shopify.com), give it the scopes above and install it on your dev store. Copy its **client ID** and **client secret**, and let shopgen handle tokens:

```sh
export SHOPIFY_STORE=my-dev-store
export SHOPIFY_CLIENT_ID=…
export SHOPIFY_CLIENT_SECRET=…
```

shopgen exchanges them for an Admin API token with the [client credentials grant](https://shopify.dev/docs/apps/build/authentication-authorization/access-tokens/client-credentials-grant) (`POST https://<store>/admin/oauth/access_token`). These tokens last 24 hours; shopgen renews them five minutes before they expire, and again if Shopify ever answers `401`, so a 19-hour run never needs attention.

The grant only works when the app and the store belong to the **same organization** in the Dev Dashboard. If they don't, Shopify answers `shop_not_permitted` and shopgen says so.

### Legacy custom app

Apps created in the store admin before January 2026 still work. Copy the Admin API access token (`shpat_…`) from the app's API credentials page:

```sh
export SHOPIFY_STORE=my-dev-store
export SHOPIFY_ACCESS_TOKEN=shpat_…
```

If both are set, `SHOPIFY_ACCESS_TOKEN` wins.

## 4. Check it

```sh
shopgen doctor
```

`doctor` fetches a token if needed, connects, confirms the store is a development store, lists the token's granted scopes (through [go-shopify](https://github.com/bold-commerce/go-shopify)'s access-scopes endpoint) and counts your active variants. Nothing is written. Fix anything marked ✗ before a run; `run` performs the same dev-store and scope checks before its first write.

Secrets are read **only** from the environment, never from flags or config files, so they don't end up in shell history, process listings or a committed `shopgen.yaml`. A `.env` file loaded by [direnv](https://direnv.net) works well (`.env` is already in `.gitignore`).

Non-secret settings, such as the store, can live in a config file:

```yaml
# shopgen.yaml
store: my-dev-store
```

## API version

shopgen targets Admin API `2026-10` by default. Pass `--api-version` to pin another. Notes on versions:

- `orderCreate` accepts past `processedAt` values. From `2026-07` on, future values are clamped to now. shopgen never generates timestamps after its reference time, so either behaviour is fine.
- `orderCancel` takes `refundMethod`, which replaced the older `refund` argument.

## Paid staging stores

Plus sandbox and other paid stores aren't development stores, so shopgen refuses them by default. If you really mean it:

```sh
shopgen run --allow-non-dev --opm 30
```

Paid stores have higher limits; the GraphQL client still backs off on `429` and `THROTTLED` responses. Never point shopgen at a store with real customers.
