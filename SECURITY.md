# Security policy

shopgen handles a Shopify Admin API token with write access to orders and customers. Please report anything that could leak that token, write to a store it shouldn't, or delete data it didn't create.

**Report privately** through [GitHub security advisories](https://github.com/weekwright/shopgen/security/advisories/new). Please don't open a public issue. We aim to acknowledge reports within a few days.

Supported versions: the latest release.

## Design notes

- The token is read only from `SHOPIFY_ACCESS_TOKEN` and sent only in the `X-Shopify-Access-Token` header to the configured store. It is never logged or written to the state directory.
- Writes are refused unless the store reports `plan.partnerDevelopment`, unless `-allow-non-dev` is passed.
- Purge only deletes objects carrying a shopgen tag.
- Generated customer emails use reserved, undeliverable domains.
