package shopify_test

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/weekwright/shopgen/internal/shopify"
	"github.com/weekwright/shopgen/internal/shopify/shopifytest"
)

func TestShopDomain(t *testing.T) {
	for in, want := range map[string]string{
		"my-shop":                              "my-shop.myshopify.com",
		"My-Shop.myshopify.com":                "my-shop.myshopify.com",
		"https://my-shop.myshopify.com/admin/": "my-shop.myshopify.com",
		" my-shop ":                            "my-shop.myshopify.com",
		"shop.example.com":                     "shop.example.com",
		"":                                     "",
	} {
		if got := shopify.ShopDomain(in); got != want {
			t.Errorf("ShopDomain(%q) = %q, want %q", in, got, want)
		}
	}
	if got := shopify.ShopHandle("my-shop.myshopify.com"); got != "my-shop" {
		t.Errorf("ShopHandle = %q", got)
	}
}

func TestAdminURLs(t *testing.T) {
	if got := shopify.AdminEndpoint("my-shop", "2026-10"); got != "https://my-shop.myshopify.com/admin/api/2026-10/graphql.json" {
		t.Errorf("AdminEndpoint = %s", got)
	}
	if got := shopify.AdminURL("my-shop.myshopify.com", "/orders"); got != "https://admin.shopify.com/store/my-shop/orders" {
		t.Errorf("AdminURL = %s", got)
	}
	if got := shopify.AdminOrderURL("my-shop", "gid://shopify/Order/42"); got != "https://admin.shopify.com/store/my-shop/orders/42" {
		t.Errorf("AdminOrderURL = %s", got)
	}
}

func TestGID(t *testing.T) {
	g := shopify.GID("Order", 123)
	kind, id, err := shopify.ParseGID(g)
	if g != "gid://shopify/Order/123" || kind != "Order" || id != 123 || err != nil {
		t.Fatalf("round trip: %s %s %d %v", g, kind, id, err)
	}
	for _, bad := range []string{"", "123", "gid://shopify/Order", "gid://shopify/Order/x", "gid://other/Order/1"} {
		if _, _, err := shopify.ParseGID(bad); err == nil {
			t.Errorf("ParseGID(%q) should fail", bad)
		}
	}
}

func TestMoney(t *testing.T) {
	for in, want := range map[string]int64{"12.34": 1234, "12.3": 1230, "12": 1200, "0.995": 100, "1.005": 101, "0.01": 1, "-2.50": -250} {
		got, err := shopify.ParseMoney(in)
		if err != nil || got != want {
			t.Errorf("ParseMoney(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"abc", "1.2x", "1..2"} {
		if _, err := shopify.ParseMoney(bad); err == nil {
			t.Errorf("ParseMoney(%q) should fail", bad)
		}
	}
	if got := shopify.FormatAmount(53605158); got != "536051.58" {
		t.Errorf("FormatAmount = %s", got)
	}
	if got := shopify.MoneyBag(1999, "CAD"); got.ShopMoney.Amount != "19.99" || got.ShopMoney.CurrencyCode != "CAD" {
		t.Errorf("MoneyBag = %+v", got)
	}
}

func TestMissingScopes(t *testing.T) {
	got := shopify.MissingScopes(shopify.RequiredScopes, []string{"write_products", "write_orders"})
	if !reflect.DeepEqual(got, []string{"write_customers"}) {
		t.Fatalf("missing = %v (write_products should imply read_products)", got)
	}
	if got := shopify.MissingScopes(shopify.RequiredScopes, []string{"read_products", "write_orders", "write_customers"}); len(got) != 0 {
		t.Fatalf("missing = %v", got)
	}
}

func TestGrantedScopesViaGoShopify(t *testing.T) {
	fake := shopifytest.New(t)
	fake.Scopes = []string{"write_orders", "read_products"}
	got, err := shopify.GrantedScopes(context.Background(), "", fake.URL(), shopify.StaticToken("x"))
	if err != nil || !reflect.DeepEqual(got, []string{"read_products", "write_orders"}) {
		t.Fatalf("scopes %v, err %v", got, err)
	}
}

func TestClientCredentialsFetchAndCache(t *testing.T) {
	fake := shopifytest.New(t)
	fake.ClientID, fake.ClientSecret = "id", "secret"
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	cc := &shopify.ClientCredentials{TokenURL: fake.BaseURL() + "/admin/oauth/access_token", ClientID: "id", ClientSecret: "secret", Now: func() time.Time { return now }}

	tok, err := cc.Token(context.Background())
	if err != nil || !strings.HasPrefix(tok, "shpat_fake_") {
		t.Fatalf("token %q, err %v", tok, err)
	}
	if again, _ := cc.Token(context.Background()); again != tok || fake.TokensIssued() != 1 {
		t.Fatalf("token not cached: %q vs %q, issued %d", again, tok, fake.TokensIssued())
	}
	if want := now.Add(86399 * time.Second); !cc.Expiry().Equal(want) {
		t.Fatalf("expiry %s, want %s", cc.Expiry(), want)
	}
	// Within five minutes of expiry the token is renewed proactively.
	now = now.Add(86399*time.Second - 4*time.Minute)
	if renewed, _ := cc.Token(context.Background()); renewed == tok || fake.TokensIssued() != 2 {
		t.Fatalf("token not renewed near expiry")
	}
}

func TestClientCredentialsBadSecret(t *testing.T) {
	fake := shopifytest.New(t)
	fake.ClientID, fake.ClientSecret = "id", "secret"
	cc := &shopify.ClientCredentials{TokenURL: fake.BaseURL() + "/admin/oauth/access_token", ClientID: "id", ClientSecret: "nope"}
	_, err := cc.Token(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Client authentication failed") {
		t.Fatalf("err = %v", err)
	}
}

func TestClientRefreshesExpiredTokenOn401(t *testing.T) {
	fake := shopifytest.New(t)
	fake.ClientID, fake.ClientSecret = "id", "secret"
	cc := &shopify.ClientCredentials{TokenURL: fake.BaseURL() + "/admin/oauth/access_token", ClientID: "id", ClientSecret: "secret"}
	var notes []string
	c := shopify.NewClient(fake.URL(), cc)
	c.Notify = func(m string) { notes = append(notes, m) }

	if _, err := c.Shop(context.Background()); err != nil {
		t.Fatal(err)
	}
	fake.ExpireTokens() // the cached token is now rejected with 401
	shop, err := c.Shop(context.Background())
	if err != nil || shop.Name == "" {
		t.Fatalf("after expiry: %+v, %v", shop, err)
	}
	if fake.TokensIssued() != 2 || len(notes) != 1 {
		t.Fatalf("issued %d tokens, notes %v", fake.TokensIssued(), notes)
	}
}

func TestStaticTokenDoesNotRefresh(t *testing.T) {
	fake := shopifytest.New(t)
	fake.ClientID, fake.ClientSecret = "id", "secret" // demands issued tokens
	c := shopify.NewClient(fake.URL(), shopify.StaticToken("shpat_wrong"))
	if _, err := c.Shop(context.Background()); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err = %v", err)
	}
}
