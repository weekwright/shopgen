# Security policy

shopgen handles Shopify Admin API credentials (a token, or an app's client ID and secret) with write access to orders and customers. Please report anything that could leak that token, write to a store it shouldn't, or delete data it didn't create.

**Report privately** through [GitHub security advisories](https://github.com/weekwright/shopgen/security/advisories/new). Please don't open a public issue. We aim to acknowledge reports within a few days.

Supported versions: the latest release.

## Design notes

- Tokens and client secrets are read only from the environment (`SHOPIFY_ACCESS_TOKEN`, `SHOPIFY_CLIENT_SECRET`), never from flags or config files. Tokens are sent only in the `X-Shopify-Access-Token` header to the configured store, and the secret only to that store's `/admin/oauth/access_token`. Neither is logged or written to the state directory.
- Writes are refused unless the store reports `plan.partnerDevelopment`, unless `--allow-non-dev` is passed on the command line (config files and environment variables can't enable it).
- Purge only deletes objects carrying a shopgen tag.
- Generated customer emails use reserved, undeliverable domains.
