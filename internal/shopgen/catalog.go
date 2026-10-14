package shopgen

import (
	"sort"
	"strings"

	"github.com/weekwright/shopgen/internal/shopify"
)

// Variant is one purchasable product variant.
type Variant struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	PriceCents int64  `json:"price_cents"`
}

// CatalogFromShopify converts fetched variants to a sorted catalog.
func CatalogFromShopify(vs []shopify.ProductVariant) []Variant {
	out := make([]Variant, len(vs))
	for i, v := range vs {
		out[i] = Variant{ID: v.ID, Title: v.DisplayTitle(), PriceCents: v.PriceCents}
	}
	return SortCatalog(out)
}

// SortCatalog returns a copy of cat sorted by variant ID with duplicates
// removed, so the API's page order can never change a plan.
func SortCatalog(cat []Variant) []Variant {
	out := append([]Variant(nil), cat...)
	sort.SliceStable(out, func(i, j int) bool { return lessID(out[i].ID, out[j].ID) })
	dedup := out[:0]
	for i, v := range out {
		if i > 0 && v.ID == out[i-1].ID {
			continue
		}
		dedup = append(dedup, v)
	}
	return dedup
}

// lessID orders GIDs numerically when both parse, and lexically otherwise.
func lessID(a, b string) bool {
	_, na, erra := shopify.ParseGID(a)
	_, nb, errb := shopify.ParseGID(b)
	if erra == nil && errb == nil && na != nb {
		return na < nb
	}
	return a < b
}

var currencySymbols = map[string]string{
	"USD": "$", "CAD": "CA$", "AUD": "A$", "EUR": "€", "GBP": "£", "JPY": "¥", "NZD": "NZ$",
}

// FormatMoney renders cents for humans, e.g. "$12.34" or "12.34 CHF".
func FormatMoney(cents int64, currency string) string {
	if sym, ok := currencySymbols[currency]; ok {
		if cents < 0 {
			return "-" + sym + shopify.FormatAmount(-cents)
		}
		return sym + shopify.FormatAmount(cents)
	}
	return shopify.FormatAmount(cents) + " " + currency
}

// groupThousands renders 536051.58 as "536,051.58".
func groupThousands(s string) string {
	sign := ""
	if strings.HasPrefix(s, "-") {
		sign, s = "-", s[1:]
	}
	whole, frac, hasFrac := strings.Cut(s, ".")
	var b strings.Builder
	for i, r := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if hasFrac {
		return sign + b.String() + "." + frac
	}
	return sign + b.String()
}
