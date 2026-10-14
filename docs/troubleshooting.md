# Troubleshooting

**`HTTP 401` / `403` … check SHOPIFY_ACCESS_TOKEN and scopes**
The token is missing, wrong, expired, or lacks a scope. The app needs `read_products`, `write_orders` and `write_customers`, and must be installed on the store you're targeting. Tokens from the client credentials grant expire after about a day: export a fresh one and `shopgen run -resume <id>`. Nothing is lost. See [Shopify setup](shopify-setup.md).

**`… is not a development store; refusing to write`**
Working as intended. Point `SHOPIFY_STORE` at a dev store, or pass `-allow-non-dev` if this is a staging store you own and understand the risk.

**`the store has no active product variants priced above 0`**
shopgen reads your catalog and never creates products. Add a few active products with prices.

**`The first 3 orders all failed; stopping so nothing is skipped`**
Something is wrong for every order: usually a scope, a currency mismatch, or a product archived since the run started. Read `.shopgen/<run-id>/failures.jsonl`, fix the cause, then resume; the three orders are retried.

**`profile currency USD differs from the shop currency CAD`**
Set `"currency"` in the profile to match the store. Shopify may reject or convert mismatched amounts.

**`regenerated plan has N orders but run … expects M; refusing to resume`**
The run directory was edited, or a shopgen upgrade changed generation. Finish the run with the version that started it, or purge it and start a new run.

**`.shopgen/…/profile.json was modified after the run started`**
Resume uses the snapshot, not your working profile. Restore the snapshot, or start a new run with the edited profile.

**Orders appear but not in analytics**
Analytics hides `test: true` orders. Make sure `test_orders` is `false` (the default). Analytics can also take a while to catch up after a large backfill.

**Duplicate-looking orders**
Each planned order carries a unique `sgseq-<run>-<index>` tag. Search the admin for that tag: two orders with the same tag would be a bug, so please [open an issue](https://github.com/weekwright/shopgen/issues). Two *runs* of the same profile create two separate histories, by design.

**`purge` left some orders behind**
Search indexing lags a little behind deletes. Run `purge` again after a minute. Orders Shopify won't delete even after cancelling are listed as failed with Shopify's reason.

**The run is slow**
5 orders a minute is Shopify's cap for dev and trial stores. Raising `-opm` above 5 on a dev store just produces rate-limit errors, which shopgen waits out. Use a smaller window or `orders_per_day` for faster iterations.

**Colors look wrong or garbled in CI logs**
shopgen only colors output on a terminal. Set `NO_COLOR=1` to force plain output, or `FORCE_COLOR=1` to force color.
