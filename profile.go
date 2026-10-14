package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strings"
	"time"
)

// Outcome keys, in the order they are documented.
const (
	OutcomeFulfilled          = "fulfilled"
	OutcomeUnfulfilled        = "unfulfilled"
	OutcomePartiallyFulfilled = "partially_fulfilled"
	OutcomePendingPayment     = "pending_payment"
	OutcomeRefunded           = "refunded"
	OutcomePartiallyRefunded  = "partially_refunded"
	OutcomeCancelled          = "cancelled"
)

// AllOutcomes lists every valid outcome key.
var AllOutcomes = []string{
	OutcomeFulfilled,
	OutcomeUnfulfilled,
	OutcomePartiallyFulfilled,
	OutcomePendingPayment,
	OutcomeRefunded,
	OutcomePartiallyRefunded,
	OutcomeCancelled,
}

func validOutcome(k string) bool {
	for _, o := range AllOutcomes {
		if o == k {
			return true
		}
	}
	return false
}

// Profile describes the shape of the order history to generate.
// Field defaults are applied by ParseProfile; see docs/profile.md.
type Profile struct {
	Seed               int64              `json:"seed"`
	Start              string             `json:"start"`
	End                string             `json:"end"`
	Timezone           string             `json:"timezone"`
	OrdersPerDay       float64            `json:"orders_per_day"`
	Growth             float64            `json:"growth"`
	Weekly             []float64          `json:"weekly"`
	Hourly             []float64          `json:"hourly"`
	Noise              float64            `json:"noise"`
	Spikes             []Spike            `json:"spikes"`
	Currency           string             `json:"currency"`
	TaxRate            float64            `json:"tax_rate"`
	Gateway            string             `json:"gateway"`
	Customers          CustomersConfig    `json:"customers"`
	Items              ItemsConfig        `json:"items"`
	Shipping           []ShippingOption   `json:"shipping"`
	FreeShippingOver   float64            `json:"free_shipping_over"`
	Outcomes           map[string]float64 `json:"outcomes"`
	FulfillmentLagDays float64            `json:"fulfillment_lag_days"`
	ArchiveFulfilled   bool               `json:"archive_fulfilled"`
	TestOrders         bool               `json:"test_orders"`
}

// Spike multiplies volume for Days days starting at Date.
type Spike struct {
	Date       string  `json:"date"`
	Days       int     `json:"days"`
	Multiplier float64 `json:"multiplier"`
}

// CustomersConfig controls the customer pool.
type CustomersConfig struct {
	Pool        int     `json:"pool"`
	RepeatRate  float64 `json:"repeat_rate"`
	EmailDomain string  `json:"email_domain"`
}

// ItemsConfig controls basket size and product mix.
type ItemsConfig struct {
	Min            int     `json:"min"`
	Max            int     `json:"max"`
	MaxQty         int     `json:"max_qty"`
	PopularitySkew float64 `json:"popularity_skew"`
}

// ShippingOption is one weighted shipping choice.
type ShippingOption struct {
	Title  string  `json:"title"`
	Price  float64 `json:"price"`
	Weight float64 `json:"weight"`
}

// DefaultWeekly is Sunday..Saturday.
var DefaultWeekly = []float64{0.9, 0.95, 1, 1, 1, 1.05, 1.1}

// DefaultHourly is an evening-peaked curve over local hours 0..23.
var DefaultHourly = []float64{
	0.30, 0.18, 0.12, 0.10, 0.10, 0.15, 0.30, 0.55, // 00-07
	0.80, 0.95, 1.00, 1.05, 1.10, 1.05, 1.00, 1.00, // 08-15
	1.05, 1.20, 1.40, 1.60, 1.70, 1.55, 1.10, 0.60, // 16-23
}

// DefaultOutcomes are the outcome weights used when a profile has none.
var DefaultOutcomes = map[string]float64{
	OutcomeFulfilled:          0.82,
	OutcomeUnfulfilled:        0.04,
	OutcomePartiallyFulfilled: 0.02,
	OutcomePendingPayment:     0.02,
	OutcomeRefunded:           0.04,
	OutcomePartiallyRefunded:  0.04,
	OutcomeCancelled:          0.02,
}

func defaultProfile() Profile {
	return Profile{
		Timezone: "UTC",
		Weekly:   append([]float64(nil), DefaultWeekly...),
		Hourly:   append([]float64(nil), DefaultHourly...),
		Noise:    0.15,
		Currency: "USD",
		Gateway:  "manual",
		Customers: CustomersConfig{
			Pool:        5000,
			RepeatRate:  0.3,
			EmailDomain: "shopgen.example",
		},
		Items: ItemsConfig{
			Min:            1,
			MaxQty:         3,
			PopularitySkew: 1.1,
		},
		Shipping: []ShippingOption{
			{Title: "Standard", Price: 5.99, Weight: 0.75},
			{Title: "Express", Price: 14.99, Weight: 0.25},
		},
		FulfillmentLagDays: 2,
	}
}

// LoadProfile reads and validates a profile file. It returns the exact bytes
// read so a run can snapshot them.
func LoadProfile(path string) (*Profile, []byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, fmt.Errorf("profile %s not found (copy profile.example.json to get started)", path)
		}
		return nil, nil, fmt.Errorf("read profile: %w", err)
	}
	p, err := ParseProfile(b)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	return p, b, nil
}

// ParseProfile decodes a JSON profile, applies defaults and validates it.
// Unknown fields are rejected so typos do not silently fall back to defaults.
func ParseProfile(b []byte) (*Profile, error) {
	p := defaultProfile()
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return nil, describeJSONError(err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return nil, errors.New("profile: unexpected data after the JSON object")
	}
	if p.Items.Max == 0 {
		p.Items.Max = p.Items.Min + 3
	}
	if p.Outcomes == nil {
		p.Outcomes = make(map[string]float64, len(DefaultOutcomes))
		for k, v := range DefaultOutcomes {
			p.Outcomes[k] = v
		}
	}
	for i := range p.Shipping {
		if p.Shipping[i].Weight <= 0 {
			p.Shipping[i].Weight = 1
		}
	}
	for i := range p.Spikes {
		if p.Spikes[i].Days == 0 {
			p.Spikes[i].Days = 1
		}
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

func describeJSONError(err error) error {
	var typeErr *json.UnmarshalTypeError
	var syntaxErr *json.SyntaxError
	switch {
	case errors.As(err, &typeErr):
		field := typeErr.Field
		if field == "" {
			field = "profile"
		}
		return fmt.Errorf("%s: expected %s, got JSON %s", field, typeErr.Type, typeErr.Value)
	case errors.As(err, &syntaxErr):
		return fmt.Errorf("profile: invalid JSON at byte %d: %v", syntaxErr.Offset, err)
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		return fmt.Errorf("unknown field %s", strings.TrimPrefix(err.Error(), "json: unknown field "))
	}
	return fmt.Errorf("profile: %w", err)
}

// Validate checks every field and returns an error naming the first bad one.
func (p *Profile) Validate() error {
	if p.Start == "" {
		return errors.New("start: required (YYYY-MM-DD)")
	}
	start, err := parseDate(p.Start)
	if err != nil {
		return fmt.Errorf("start: %v", err)
	}
	if p.End != "" {
		end, err := parseDate(p.End)
		if err != nil {
			return fmt.Errorf("end: %v", err)
		}
		if end.Before(start) {
			return fmt.Errorf("end: %s is before start %s", p.End, p.Start)
		}
	}
	if _, err := time.LoadLocation(p.Timezone); err != nil {
		return fmt.Errorf("timezone: unknown IANA zone %q", p.Timezone)
	}
	if !(p.OrdersPerDay > 0) || isBad(p.OrdersPerDay) {
		return fmt.Errorf("orders_per_day: must be > 0, got %v", p.OrdersPerDay)
	}
	if p.Growth <= -1 || isBad(p.Growth) {
		return fmt.Errorf("growth: must be > -1, got %v", p.Growth)
	}
	if err := checkWeights("weekly", p.Weekly, 7); err != nil {
		return err
	}
	if err := checkWeights("hourly", p.Hourly, 24); err != nil {
		return err
	}
	if p.Noise < 0 || isBad(p.Noise) {
		return fmt.Errorf("noise: must be >= 0, got %v", p.Noise)
	}
	for i, s := range p.Spikes {
		if _, err := parseDate(s.Date); err != nil {
			return fmt.Errorf("spikes[%d].date: %v", i, err)
		}
		if s.Days < 0 {
			return fmt.Errorf("spikes[%d].days: must be >= 1, got %d", i, s.Days)
		}
		if s.Multiplier < 0 || isBad(s.Multiplier) {
			return fmt.Errorf("spikes[%d].multiplier: must be >= 0, got %v", i, s.Multiplier)
		}
	}
	if len(p.Currency) != 3 || strings.ToUpper(p.Currency) != p.Currency {
		return fmt.Errorf("currency: must be a 3-letter ISO code like USD, got %q", p.Currency)
	}
	if p.TaxRate < 0 || p.TaxRate >= 1 || isBad(p.TaxRate) {
		return fmt.Errorf("tax_rate: must be in [0, 1), got %v", p.TaxRate)
	}
	if strings.TrimSpace(p.Gateway) == "" {
		return errors.New("gateway: must not be empty")
	}
	if p.Customers.Pool <= 0 {
		return fmt.Errorf("customers.pool: must be > 0, got %d", p.Customers.Pool)
	}
	if p.Customers.RepeatRate < 0 || p.Customers.RepeatRate > 1 || isBad(p.Customers.RepeatRate) {
		return fmt.Errorf("customers.repeat_rate: must be in [0, 1], got %v", p.Customers.RepeatRate)
	}
	if err := checkEmailDomain(p.Customers.EmailDomain); err != nil {
		return fmt.Errorf("customers.email_domain: %v", err)
	}
	if p.Items.Min < 1 {
		return fmt.Errorf("items.min: must be >= 1, got %d", p.Items.Min)
	}
	if p.Items.Max < p.Items.Min {
		return fmt.Errorf("items.max: must be >= items.min (%d), got %d", p.Items.Min, p.Items.Max)
	}
	if p.Items.MaxQty < 1 {
		return fmt.Errorf("items.max_qty: must be >= 1, got %d", p.Items.MaxQty)
	}
	if p.Items.PopularitySkew < 0 || isBad(p.Items.PopularitySkew) {
		return fmt.Errorf("items.popularity_skew: must be >= 0, got %v", p.Items.PopularitySkew)
	}
	if len(p.Shipping) == 0 {
		return errors.New("shipping: needs at least one option")
	}
	for i, s := range p.Shipping {
		if strings.TrimSpace(s.Title) == "" {
			return fmt.Errorf("shipping[%d].title: must not be empty", i)
		}
		if s.Price < 0 || isBad(s.Price) {
			return fmt.Errorf("shipping[%d].price: must be >= 0, got %v", i, s.Price)
		}
		if isBad(s.Weight) {
			return fmt.Errorf("shipping[%d].weight: must be a number", i)
		}
	}
	if p.FreeShippingOver < 0 || isBad(p.FreeShippingOver) {
		return fmt.Errorf("free_shipping_over: must be >= 0, got %v", p.FreeShippingOver)
	}
	var sum float64
	for _, k := range sortedKeys(p.Outcomes) {
		v := p.Outcomes[k]
		if !validOutcome(k) {
			return fmt.Errorf("outcomes: unknown key %q (valid: %s)", k, strings.Join(AllOutcomes, ", "))
		}
		if v < 0 || isBad(v) {
			return fmt.Errorf("outcomes.%s: must be >= 0, got %v", k, v)
		}
		sum += v
	}
	if !(sum > 0) {
		return errors.New("outcomes: weights must sum to more than 0")
	}
	if p.FulfillmentLagDays < 0 || isBad(p.FulfillmentLagDays) {
		return fmt.Errorf("fulfillment_lag_days: must be >= 0, got %v", p.FulfillmentLagDays)
	}
	return nil
}

func checkWeights(name string, w []float64, n int) error {
	if len(w) != n {
		return fmt.Errorf("%s: needs exactly %d values, got %d", name, n, len(w))
	}
	var sum float64
	for i, v := range w {
		if v < 0 || isBad(v) {
			return fmt.Errorf("%s[%d]: must be >= 0, got %v", name, i, v)
		}
		sum += v
	}
	if !(sum > 0) {
		return fmt.Errorf("%s: values must sum to more than 0", name)
	}
	return nil
}

// reservedDomains can never receive mail, so generated customers never email
// a real person. See RFC 2606 and RFC 6761. A domain qualifies if it is one
// of these or a subdomain of one.
var reservedDomains = []string{"example", "test", "invalid", "localhost", "example.com", "example.net", "example.org"}

func checkEmailDomain(d string) error {
	d = strings.ToLower(strings.TrimSpace(d))
	if d == "" || strings.ContainsAny(d, "@ /") {
		return fmt.Errorf("invalid domain %q", d)
	}
	for _, r := range reservedDomains {
		if d == r || strings.HasSuffix(d, "."+r) {
			return nil
		}
	}
	return fmt.Errorf("%q is not a reserved domain; use one ending in .example, .test or .invalid so no real inbox is used", d)
}

func isBad(f float64) bool { return math.IsNaN(f) || math.IsInf(f, 0) }

func parseDate(s string) (time.Time, error) {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, fmt.Errorf("want YYYY-MM-DD, got %q", s)
	}
	return t, nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// toCents converts a profile amount to integer cents.
func toCents(f float64) int64 { return int64(math.Round(f * 100)) }
