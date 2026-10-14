package main

import (
	"os"
	"strings"
	"testing"
)

func TestParseProfileDefaults(t *testing.T) {
	p, err := ParseProfile([]byte(`{"start": "2026-07-01", "orders_per_day": 10}`))
	if err != nil {
		t.Fatal(err)
	}
	if p.Timezone != "UTC" || p.Currency != "USD" || p.Gateway != "manual" || p.Noise != 0.15 {
		t.Fatalf("scalar defaults not applied: %+v", p)
	}
	if p.Customers.Pool != 5000 || p.Customers.RepeatRate != 0.3 || p.Customers.EmailDomain != "shopgen.example" {
		t.Fatalf("customer defaults not applied: %+v", p.Customers)
	}
	if p.Items.Min != 1 || p.Items.Max != 4 || p.Items.MaxQty != 3 || p.Items.PopularitySkew != 1.1 {
		t.Fatalf("item defaults not applied: %+v", p.Items)
	}
	if len(p.Weekly) != 7 || len(p.Hourly) != 24 || len(p.Shipping) != 2 || len(p.Outcomes) != 7 {
		t.Fatal("list defaults not applied")
	}
	if p.FulfillmentLagDays != 2 {
		t.Fatalf("fulfillment_lag_days = %v", p.FulfillmentLagDays)
	}
}

func TestParseProfileItemsMaxDefaultsFromMin(t *testing.T) {
	p, err := ParseProfile([]byte(`{"start": "2026-07-01", "orders_per_day": 10, "items": {"min": 2}}`))
	if err != nil {
		t.Fatal(err)
	}
	if p.Items.Max != 5 {
		t.Fatalf("items.max = %d, want min+3 = 5", p.Items.Max)
	}
}

func TestParseProfileOutcomesReplaceDefaults(t *testing.T) {
	p, err := ParseProfile([]byte(`{"start": "2026-07-01", "orders_per_day": 10, "outcomes": {"fulfilled": 1}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Outcomes) != 1 {
		t.Fatalf("outcomes merged with defaults: %v", p.Outcomes)
	}
}

func TestParseProfileShippingWeightDefaults(t *testing.T) {
	p, err := ParseProfile([]byte(`{"start": "2026-07-01", "orders_per_day": 10, "shipping": [{"title": "Flat", "price": 4, "weight": 0}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if p.Shipping[0].Weight != 1 {
		t.Fatalf("weight <= 0 should mean 1, got %v", p.Shipping[0].Weight)
	}
}

func TestValidationRejects(t *testing.T) {
	cases := []struct {
		name, json, field string
	}{
		{"orders_per_day zero", `{"start":"2026-07-01","orders_per_day":0}`, "orders_per_day"},
		{"orders_per_day negative", `{"start":"2026-07-01","orders_per_day":-3}`, "orders_per_day"},
		{"missing start", `{"orders_per_day":5}`, "start"},
		{"end before start", `{"start":"2026-07-10","end":"2026-07-01","orders_per_day":5}`, "end"},
		{"unknown outcome", `{"start":"2026-07-01","orders_per_day":5,"outcomes":{"lost":1}}`, "outcomes"},
		{"negative outcome", `{"start":"2026-07-01","orders_per_day":5,"outcomes":{"fulfilled":-1}}`, "outcomes.fulfilled"},
		{"zero outcomes", `{"start":"2026-07-01","orders_per_day":5,"outcomes":{"fulfilled":0}}`, "outcomes"},
		{"weekly length", `{"start":"2026-07-01","orders_per_day":5,"weekly":[1,1,1,1,1,1]}`, "weekly"},
		{"hourly length", `{"start":"2026-07-01","orders_per_day":5,"hourly":[1,2]}`, "hourly"},
		{"bad timezone", `{"start":"2026-07-01","orders_per_day":5,"timezone":"Mars/Olympus"}`, "timezone"},
		{"bad date", `{"start":"07/01/2026","orders_per_day":5}`, "start"},
		{"real email domain", `{"start":"2026-07-01","orders_per_day":5,"customers":{"email_domain":"gmail.com"}}`, "customers.email_domain"},
		{"items max < min", `{"start":"2026-07-01","orders_per_day":5,"items":{"min":3,"max":2}}`, "items.max"},
		{"currency", `{"start":"2026-07-01","orders_per_day":5,"currency":"usd"}`, "currency"},
		{"unknown field", `{"start":"2026-07-01","orders_per_day":5,"order_per_day":9}`, "order_per_day"},
		{"wrong type", `{"start":"2026-07-01","orders_per_day":"lots"}`, "orders_per_day"},
		{"empty shipping", `{"start":"2026-07-01","orders_per_day":5,"shipping":[]}`, "shipping"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseProfile([]byte(c.json))
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), c.field) {
				t.Fatalf("error %q does not name %q", err, c.field)
			}
		})
	}
}

func TestEmailDomainAllowsReserved(t *testing.T) {
	for _, d := range []string{"shopgen.example", "example.com", "qa.example.org", "store.test", "x.invalid"} {
		if err := checkEmailDomain(d); err != nil {
			t.Errorf("%s: %v", d, err)
		}
	}
	for _, d := range []string{"gmail.com", "notexample.com", "example.com.evil.io"} {
		if err := checkEmailDomain(d); err == nil {
			t.Errorf("%s: expected rejection", d)
		}
	}
}

func TestExampleProfileLoads(t *testing.T) {
	p, b, err := LoadProfile("profile.example.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(b) == 0 || p.Seed != 42 || p.Timezone != "America/Chicago" || p.Customers.Pool != 3000 || !p.ArchiveFulfilled {
		t.Fatalf("unexpected example profile: %+v", p)
	}
	if _, err := os.Stat("examples"); err == nil {
		entries, _ := os.ReadDir("examples")
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".json") {
				if _, _, err := LoadProfile("examples/" + e.Name()); err != nil {
					t.Errorf("examples/%s: %v", e.Name(), err)
				}
			}
		}
	}
}

func TestMoneyHelpers(t *testing.T) {
	for in, want := range map[string]int64{"12.34": 1234, "12.3": 1230, "12": 1200, "0.995": 100, "1.005": 101, "0.01": 1} {
		got, err := parseMoney(in)
		if err != nil || got != want {
			t.Errorf("parseMoney(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	if _, err := parseMoney("abc"); err == nil {
		t.Error("parseMoney(abc) should fail")
	}
	if got := formatAmount(53605158); got != "536051.58" {
		t.Errorf("formatAmount = %s", got)
	}
	if got := formatMoney(9454, "USD"); got != "$94.54" {
		t.Errorf("formatMoney = %s", got)
	}
	if got := groupThousands("536051.58"); got != "536,051.58" {
		t.Errorf("groupThousands = %s", got)
	}
}

func TestSortCatalogNumericAndDedup(t *testing.T) {
	cat := SortCatalog([]Variant{
		{ID: "gid://shopify/ProductVariant/100"},
		{ID: "gid://shopify/ProductVariant/9"},
		{ID: "gid://shopify/ProductVariant/100"},
	})
	if len(cat) != 2 || cat[0].ID != "gid://shopify/ProductVariant/9" {
		t.Fatalf("got %+v", cat)
	}
}

func TestSyntheticCatalogSize(t *testing.T) {
	if n := len(SyntheticCatalog()); n != 40 {
		t.Fatalf("synthetic catalog has %d variants, want 40", n)
	}
}

func TestNormalizeStore(t *testing.T) {
	for in, want := range map[string]string{
		"my-shop":                        "my-shop.myshopify.com",
		"https://My-Shop.myshopify.com/": "my-shop.myshopify.com",
		"my-shop.myshopify.com":          "my-shop.myshopify.com",
		"":                               "",
	} {
		if got := NormalizeStore(in); got != want {
			t.Errorf("NormalizeStore(%q) = %q, want %q", in, got, want)
		}
	}
}
