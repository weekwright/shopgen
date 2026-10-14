package shopgen

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/weekwright/shopgen/internal/shopify"
	"github.com/weekwright/shopgen/internal/ui"
)

// Conn says how to reach a store: a domain or an endpoint override, an API
// version and a token source.
type Conn struct {
	Store      string
	Endpoint   string // overrides the GraphQL URL; skips the token requirement
	APIVersion string
	Tokens     shopify.TokenSource
}

// ErrNoToken is returned when no credentials are configured.
var ErrNoToken = errors.New("no Shopify credentials: set SHOPIFY_ACCESS_TOKEN, or SHOPIFY_CLIENT_ID and SHOPIFY_CLIENT_SECRET for a Dev Dashboard app (see docs/shopify-setup.md)")

// Client builds a GraphQL client for the connection.
func (c Conn) Client(sleep func(context.Context, time.Duration) error, u *ui.UI) (*shopify.Client, error) {
	endpoint := c.Endpoint
	if endpoint == "" {
		if c.Store == "" {
			return nil, UsageErrorf("no store: pass --store or set SHOPIFY_STORE")
		}
		if c.Tokens == nil {
			return nil, ErrNoToken
		}
		endpoint = shopify.AdminEndpoint(c.Store, c.APIVersion)
	}
	client := shopify.NewClient(endpoint, c.Tokens)
	if sleep != nil {
		client.Sleep = sleep
	}
	if u != nil {
		client.Notify = func(msg string) { u.Warnf("%s", msg) }
	}
	return client, nil
}

// safetyGate refuses to write to anything but a development store unless
// explicitly allowed.
func safetyGate(ctx context.Context, c *shopify.Client, u *ui.UI, allowNonDev bool) (shopify.ShopInfo, error) {
	shop, err := c.Shop(ctx)
	if err != nil {
		return shop, fmt.Errorf("connect to store: %w", err)
	}
	if !shop.PartnerDevelopment {
		if !allowNonDev {
			return shop, fmt.Errorf("%s (%s) is not a development store; refusing to write. Pass --allow-non-dev if you really mean it", shop.Name, shop.Domain)
		}
		u.Warnf("%s is %s a development store; continuing because of --allow-non-dev", u.Bold(shop.Name), u.Bold("not"))
	} else {
		u.Infof("Connected to %s %s %s", u.Bold(shop.Name), u.Dim("("+shop.Domain+")"), u.Green("· dev store ✓"))
	}
	return shop, nil
}

// checkScopes fails before any write when the token lacks a required scope.
// If the scopes can't be listed it only warns: the first write will tell.
func checkScopes(ctx context.Context, conn Conn, u *ui.UI) error {
	granted, err := shopify.GrantedScopes(ctx, conn.Store, conn.Endpoint, conn.Tokens)
	if err != nil {
		u.Warnf("could not check access scopes (%v); continuing", err)
		return nil
	}
	if missing := shopify.MissingScopes(shopify.RequiredScopes, granted); len(missing) > 0 {
		return fmt.Errorf("the access token is missing the %s scope(s); add them to the app and reinstall it", strings.Join(missing, ", "))
	}
	return nil
}
