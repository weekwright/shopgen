package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Variant is one purchasable product variant.
type Variant struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	PriceCents int64  `json:"price_cents"`
}

// displayTitle is the product title plus " - variant" unless the variant is
// Shopify's placeholder "Default Title".
func displayTitle(product, variant string) string {
	if variant == "" || variant == "Default Title" {
		return product
	}
	return product + " - " + variant
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

// lessID orders gid://shopify/ProductVariant/123 numerically when both IDs
// end in a number, and lexically otherwise.
func lessID(a, b string) bool {
	na, oka := idNumber(a)
	nb, okb := idNumber(b)
	if oka && okb && na != nb {
		return na < nb
	}
	return a < b
}

func idNumber(id string) (uint64, bool) {
	i := strings.LastIndexByte(id, '/')
	n, err := strconv.ParseUint(id[i+1:], 10, 64)
	return n, err == nil
}

// parseMoney converts a decimal string such as "12.3" or "12.345" to cents
// without going through floating point. Extra digits are rounded half up.
func parseMoney(s string) (int64, error) {
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
	for len(frac) < 3 {
		frac += "0"
	}
	f, err := strconv.ParseInt(frac[:3], 10, 64)
	if err != nil || strings.Trim(frac, "0123456789") != "" {
		return 0, fmt.Errorf("invalid amount %q", s)
	}
	cents := w*100 + (f+5)/10
	if neg {
		cents = -cents
	}
	return cents, nil
}

// formatAmount renders cents as a plain decimal string, e.g. 1234 -> "12.34".
func formatAmount(cents int64) string {
	sign := ""
	if cents < 0 {
		sign = "-"
		cents = -cents
	}
	return fmt.Sprintf("%s%d.%02d", sign, cents/100, cents%100)
}

var currencySymbols = map[string]string{
	"USD": "$", "CAD": "CA$", "AUD": "A$", "EUR": "€", "GBP": "£", "JPY": "¥", "NZD": "NZ$",
}

// formatMoney renders cents for humans, e.g. "$12.34" or "12.34 CHF".
func formatMoney(cents int64, currency string) string {
	if sym, ok := currencySymbols[currency]; ok {
		if cents < 0 {
			return "-" + sym + formatAmount(-cents)
		}
		return sym + formatAmount(cents)
	}
	return formatAmount(cents) + " " + currency
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
