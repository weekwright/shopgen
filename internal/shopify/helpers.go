package shopify

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	goshopify "github.com/bold-commerce/go-shopify/v4"
)

// RequiredScopes are the Admin API scopes shopgen needs.
var RequiredScopes = []string{"read_products", "write_orders", "write_customers"}

// ShopDomain normalizes "my-shop", "My-Shop.myshopify.com" or
// "https://my-shop.myshopify.com/admin" to "my-shop.myshopify.com".
// Custom domains are kept as given (lower-cased).
func ShopDomain(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	if u, err := url.Parse(s); err == nil && u.Host != "" {
		s = u.Host
	}
	s, _, _ = strings.Cut(s, "/")
	if strings.Contains(s, ".") && !strings.HasSuffix(s, ".myshopify.com") {
		return s // a custom domain or a test host
	}
	return goshopify.ShopFullName(s)
}

// ShopHandle is the store's short name: "my-shop" for my-shop.myshopify.com.
func ShopHandle(domain string) string { return goshopify.ShopShortName(ShopDomain(domain)) }

// AdminEndpoint is the Admin GraphQL URL for a store and API version.
func AdminEndpoint(store, version string) string {
	return "https://" + ShopDomain(store) + "/admin/api/" + version + "/graphql.json"
}

// AdminURL links to a page of the store admin, e.g. AdminURL(d, "orders").
func AdminURL(domain, path string) string {
	return "https://admin.shopify.com/store/" + ShopHandle(domain) + "/" + strings.TrimPrefix(path, "/")
}

// AdminOrderURL links to one order in the store admin.
func AdminOrderURL(domain, orderGID string) string {
	_, id, err := ParseGID(orderGID)
	if err != nil {
		return AdminURL(domain, "orders")
	}
	return AdminURL(domain, "orders/"+strconv.FormatUint(id, 10))
}

// GID builds a global ID such as gid://shopify/Order/123.
func GID(kind string, id uint64) string { return fmt.Sprintf("gid://shopify/%s/%d", kind, id) }

// ParseGID splits gid://shopify/Order/123 into ("Order", 123).
func ParseGID(gid string) (kind string, id uint64, err error) {
	rest, ok := strings.CutPrefix(gid, "gid://shopify/")
	if !ok {
		return "", 0, fmt.Errorf("not a Shopify GID: %q", gid)
	}
	kind, num, ok := strings.Cut(rest, "/")
	if !ok || kind == "" {
		return "", 0, fmt.Errorf("not a Shopify GID: %q", gid)
	}
	num, _, _ = strings.Cut(num, "?")
	id, err = strconv.ParseUint(num, 10, 64)
	if err != nil {
		return "", 0, fmt.Errorf("not a Shopify GID: %q", gid)
	}
	return kind, id, nil
}

// ParseMoney converts a Shopify decimal string such as "12.3" to cents
// without floating point, rounding extra digits half up.
func ParseMoney(s string) (int64, error) {
	s = strings.TrimSpace(s)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	whole, frac, _ := strings.Cut(s, ".")
	if whole == "" {
		whole = "0"
	}
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid amount %q", s)
	}
	if strings.Trim(frac, "0123456789") != "" {
		return 0, fmt.Errorf("invalid amount %q", s)
	}
	for len(frac) < 3 {
		frac += "0"
	}
	f, _ := strconv.ParseInt(frac[:3], 10, 64)
	cents := w*100 + (f+5)/10
	if neg {
		cents = -cents
	}
	return cents, nil
}

// FormatAmount renders cents as a Shopify decimal string: 1234 → "12.34".
func FormatAmount(cents int64) string {
	sign := ""
	if cents < 0 {
		sign, cents = "-", -cents
	}
	return fmt.Sprintf("%s%d.%02d", sign, cents/100, cents%100)
}

// MoneyBag is Shopify's MoneyBagInput for a shop-currency amount.
func MoneyBag(cents int64, currency string) MoneyBagInput {
	return MoneyBagInput{ShopMoney: MoneyInput{Amount: FormatAmount(cents), CurrencyCode: currency}}
}

// GrantedScopes lists the access scopes granted to the token, using the
// go-shopify REST client's access_scopes endpoint. When endpoint is set (a
// test server), requests are sent there instead of the store's domain.
func GrantedScopes(ctx context.Context, store, endpoint string, tokens TokenSource) ([]string, error) {
	tok := ""
	if tokens != nil {
		var err error
		if tok, err = tokens.Token(ctx); err != nil {
			return nil, err
		}
	}
	var opts []goshopify.Option
	if endpoint != "" {
		u, err := url.Parse(endpoint)
		if err != nil {
			return nil, err
		}
		opts = append(opts, goshopify.WithHTTPClient(&http.Client{Transport: redirectTransport{target: u}}))
		if store == "" {
			store = "shopgen-test"
		}
	}
	client, err := goshopify.NewClient(goshopify.App{}, ShopDomain(store), tok, opts...)
	if err != nil {
		return nil, err
	}
	scopes, err := client.AccessScopes.List(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("list access scopes: %w", err)
	}
	out := make([]string, len(scopes))
	for i, s := range scopes {
		out[i] = s.Handle
	}
	sort.Strings(out)
	return out, nil
}

// MissingScopes returns the required scopes not covered by granted. A
// write_x scope implies read_x.
func MissingScopes(required, granted []string) []string {
	have := map[string]bool{}
	for _, g := range granted {
		have[g] = true
		if r, ok := strings.CutPrefix(g, "write_"); ok {
			have["read_"+r] = true
		}
	}
	var missing []string
	for _, r := range required {
		if !have[r] {
			missing = append(missing, r)
		}
	}
	return missing
}

// redirectTransport sends every request to target's scheme and host, keeping
// the path. It lets go-shopify, which derives URLs from the shop name, talk
// to a local test server.
type redirectTransport struct{ target *url.URL }

func (t redirectTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.URL.Scheme, r.URL.Host = t.target.Scheme, t.target.Host
	r.Host = t.target.Host
	return http.DefaultTransport.RoundTrip(r)
}
