package shopgen

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/weekwright/shopgen/internal/shopify"
	"github.com/weekwright/shopgen/internal/ui"
)

// genNow is the fixed reference time for generator tests: 8 pm Chicago on
// the window's last day, so the final day is only partly elapsed.
var genNow = time.Date(2026, 10, 14, 1, 0, 0, 0, time.UTC)

func testProfile(t *testing.T, overrides string) *Profile {
	t.Helper()
	base := map[string]any{
		"seed":               7,
		"start":              "2026-08-01",
		"end":                "2026-10-13",
		"timezone":           "America/Chicago",
		"orders_per_day":     40,
		"growth":             0.2,
		"tax_rate":           0.08,
		"free_shipping_over": 75,
		"archive_fulfilled":  true,
		"customers":          map[string]any{"pool": 800, "repeat_rate": 0.35},
		"items":              map[string]any{"min": 1, "max": 4},
	}
	if overrides != "" {
		var o map[string]any
		if err := json.Unmarshal([]byte(overrides), &o); err != nil {
			t.Fatalf("bad overrides: %v", err)
		}
		for k, v := range o {
			base[k] = v
		}
	}
	b, _ := json.Marshal(base)
	p, err := ParseProfile(b)
	if err != nil {
		t.Fatalf("ParseProfile: %v", err)
	}
	return p
}

func mustGenerate(t *testing.T, p *Profile, cat []Variant, now time.Time) *Plan {
	t.Helper()
	plan, err := Generate(p, cat, now)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(plan.Orders) == 0 {
		t.Fatal("plan has no orders")
	}
	return plan
}

func TestGenerateDeterministic(t *testing.T) {
	p := testProfile(t, "")
	cat := SyntheticCatalog()
	a := mustGenerate(t, p, cat, genNow)
	b := mustGenerate(t, p, cat, genNow)
	if !reflect.DeepEqual(a.Orders, b.Orders) {
		t.Fatal("same seed gave different plans")
	}

	reversed := make([]Variant, len(cat))
	for i, v := range cat {
		reversed[len(cat)-1-i] = v
	}
	c := mustGenerate(t, p, reversed, genNow)
	if !reflect.DeepEqual(a.Orders, c.Orders) {
		t.Fatal("reversed catalog gave a different plan")
	}

	p2 := testProfile(t, `{"seed": 8}`)
	d := mustGenerate(t, p2, cat, genNow)
	if reflect.DeepEqual(a.Orders, d.Orders) {
		t.Fatal("different seed gave an identical plan")
	}
}

func TestGenerateOrderingAndBounds(t *testing.T) {
	p := testProfile(t, "")
	plan := mustGenerate(t, p, SyntheticCatalog(), genNow)
	start := plan.Start
	for i, o := range plan.Orders {
		if o.Index != i {
			t.Fatalf("order %d has index %d", i, o.Index)
		}
		if i > 0 && o.ProcessedAt.Before(plan.Orders[i-1].ProcessedAt) {
			t.Fatalf("order %d at %s is before order %d", i, o.ProcessedAt, i-1)
		}
		if o.ProcessedAt.Before(start) || o.ProcessedAt.After(genNow) {
			t.Fatalf("order %d at %s outside [%s, %s]", i, o.ProcessedAt, start, genNow)
		}
		if o.ProcessedAt.Location() != time.UTC {
			t.Fatalf("order %d processed_at not stored in UTC", i)
		}
	}
}

func TestGenerateMoney(t *testing.T) {
	p := testProfile(t, "")
	plan := mustGenerate(t, p, SyntheticCatalog(), genNow)
	freeShipping := 0
	for _, o := range plan.Orders {
		var sum int64
		for _, l := range o.Lines {
			if l.Qty < 1 || l.Qty > p.Items.MaxQty {
				t.Fatalf("order %d: qty %d out of range", o.Index, l.Qty)
			}
			sum += l.PriceCents * int64(l.Qty)
		}
		if o.SubtotalCents != sum {
			t.Fatalf("order %d: subtotal %d != lines %d", o.Index, o.SubtotalCents, sum)
		}
		if o.TotalCents != o.SubtotalCents+o.ShipCents+o.TaxCents {
			t.Fatalf("order %d: total %d != %d+%d+%d", o.Index, o.TotalCents, o.SubtotalCents, o.ShipCents, o.TaxCents)
		}
		if n := len(o.Lines); n < p.Items.Min || n > p.Items.Max {
			t.Fatalf("order %d: %d lines", o.Index, n)
		}
		seen := map[int]bool{}
		for _, l := range o.Lines {
			if seen[l.Variant] {
				t.Fatalf("order %d: variant %d repeated", o.Index, l.Variant)
			}
			seen[l.Variant] = true
		}
		if o.ShipCents == 0 {
			freeShipping++
			if o.SubtotalCents < 7500 {
				t.Fatalf("order %d: free shipping under threshold", o.Index)
			}
		}
	}
	if freeShipping == 0 {
		t.Fatal("expected some free-shipping orders")
	}
}

func TestGenerateRefundsAndClosed(t *testing.T) {
	p := testProfile(t, "")
	plan := mustGenerate(t, p, SyntheticCatalog(), genNow)
	var full, partial, closed int
	for _, o := range plan.Orders {
		switch o.Outcome {
		case OutcomeRefunded, OutcomePartiallyRefunded:
			if o.RefundAt == nil {
				t.Fatalf("order %d: refund outcome without refund_at", o.Index)
			}
			if !o.RefundAt.After(o.ProcessedAt) || o.RefundAt.After(genNow) {
				t.Fatalf("order %d: refund at %s not in (%s, %s]", o.Index, o.RefundAt, o.ProcessedAt, genNow)
			}
			if o.Outcome == OutcomeRefunded {
				full++
				if o.RefundCents != o.TotalCents {
					t.Fatalf("order %d: full refund %d != total %d", o.Index, o.RefundCents, o.TotalCents)
				}
			} else {
				partial++
				if o.RefundCents <= 0 || o.RefundCents >= o.TotalCents {
					t.Fatalf("order %d: partial refund %d not in (0, %d)", o.Index, o.RefundCents, o.TotalCents)
				}
			}
		default:
			if o.RefundAt != nil || o.RefundCents != 0 {
				t.Fatalf("order %d: %s has a refund", o.Index, o.Outcome)
			}
		}
		if o.ClosedAt != nil {
			closed++
			if !o.ClosedAt.After(o.ProcessedAt) || o.ClosedAt.After(genNow) {
				t.Fatalf("order %d: closed_at %s not in (%s, %s]", o.Index, o.ClosedAt, o.ProcessedAt, genNow)
			}
			if o.RefundAt != nil && o.ClosedAt.Before(o.RefundAt.Add(time.Hour)) {
				t.Fatalf("order %d: closed less than an hour after refund", o.Index)
			}
		}
	}
	if full == 0 || partial == 0 || closed == 0 {
		t.Fatalf("want refunds and closed orders, got full=%d partial=%d closed=%d", full, partial, closed)
	}
}

func TestGenerateSpike(t *testing.T) {
	p := testProfile(t, `{"noise": 0, "growth": 0, "spikes": [{"date": "2026-09-16", "multiplier": 3}]}`)
	plan := mustGenerate(t, p, SyntheticCatalog(), genNow)
	s := Summarize(p, plan, 5)
	day := int(time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC).Sub(time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)).Hours() / 24)
	if s.PerDay[day] < 2*s.PerDay[day-1] {
		t.Fatalf("spike day has %d orders, previous day %d", s.PerDay[day], s.PerDay[day-1])
	}
	if s.PerDay[day+1] >= 2*s.PerDay[day-1] {
		t.Fatalf("a 1-day spike leaked into the next day (%d)", s.PerDay[day+1])
	}
}

func TestGenerateRecencySkewsUnfulfilled(t *testing.T) {
	p := testProfile(t, "")
	plan := mustGenerate(t, p, SyntheticCatalog(), genNow)
	var recentN, recentU, oldN, oldU int
	for _, o := range plan.Orders {
		age := genNow.Sub(o.ProcessedAt)
		switch {
		case age <= 24*time.Hour:
			recentN++
			if o.Outcome == OutcomeUnfulfilled {
				recentU++
			}
		case age > 7*24*time.Hour:
			oldN++
			if o.Outcome == OutcomeUnfulfilled {
				oldU++
			}
		}
	}
	if recentN == 0 || oldN == 0 {
		t.Fatalf("not enough orders: recent=%d old=%d", recentN, oldN)
	}
	recent := float64(recentU) / float64(recentN)
	old := float64(oldU) / float64(oldN)
	if recent < 3*old {
		t.Fatalf("unfulfilled share last 24h %.3f is not >= 3x older share %.3f", recent, old)
	}
}

func TestGenerateDropsFutureTimestamps(t *testing.T) {
	p := testProfile(t, `{"end": "2026-12-31"}`)
	plan := mustGenerate(t, p, SyntheticCatalog(), genNow)
	last := plan.Orders[len(plan.Orders)-1]
	if last.ProcessedAt.After(genNow) {
		t.Fatalf("order after now: %s", last.ProcessedAt)
	}
}

func TestGenerateDefaultEndIsToday(t *testing.T) {
	p := testProfile(t, `{"end": ""}`)
	plan := mustGenerate(t, p, SyntheticCatalog(), genNow)
	// 8 pm on Oct 13 in Chicago: the window ends Oct 13 local.
	if want := 74; plan.Days != want {
		t.Fatalf("days = %d, want %d", plan.Days, want)
	}
}

func TestGenerateRepeatCustomers(t *testing.T) {
	p := testProfile(t, "")
	plan := mustGenerate(t, p, SyntheticCatalog(), genNow)
	counts := map[int]int{}
	for _, o := range plan.Orders {
		if o.Customer < 0 || o.Customer >= p.Customers.Pool {
			t.Fatalf("customer %d outside pool", o.Customer)
		}
		counts[o.Customer]++
	}
	repeat := 0
	for _, n := range counts {
		if n > 1 {
			repeat++
		}
	}
	if repeat == 0 {
		t.Fatal("expected repeat customers")
	}
}

func TestGenerateEmptyCatalog(t *testing.T) {
	if _, err := Generate(testProfile(t, ""), nil, genNow); err == nil {
		t.Fatal("expected an error for an empty catalog")
	}
}

func TestCustomerAtStable(t *testing.T) {
	p := testProfile(t, "")
	a, b := CustomerAt(p, 17), CustomerAt(p, 17)
	if a != b {
		t.Fatal("CustomerAt not stable")
	}
	if !strings.HasSuffix(a.Email, ".17@shopgen.example") {
		t.Fatalf("email %q", a.Email)
	}
	if a.Zip == "" || a.Province == "" || a.Address1 == "" {
		t.Fatalf("incomplete address: %+v", a)
	}
	if CustomerAt(p, 18) == a {
		t.Fatal("customers 17 and 18 identical")
	}
}

func TestBuildOrderMapping(t *testing.T) {
	// Equal weights so every outcome appears.
	p := testProfile(t, `{"outcomes": {"fulfilled":1,"unfulfilled":1,"partially_fulfilled":1,"pending_payment":1,"refunded":1,"partially_refunded":1,"cancelled":1}}`)
	cat := SyntheticCatalog()
	plan := mustGenerate(t, p, cat, genNow)
	seen := map[string]bool{}
	for _, o := range plan.Orders {
		seen[o.Outcome] = true
		in := BuildOrder("20261014-abcdef", p, cat, o)
		switch o.Outcome {
		case OutcomePendingPayment:
			if len(in.Transactions) != 0 || in.FinancialStatus != "PENDING" {
				t.Fatalf("pending_payment: %d transactions, %s", len(in.Transactions), in.FinancialStatus)
			}
		case OutcomeRefunded, OutcomePartiallyRefunded:
			if len(in.Transactions) != 2 || in.Transactions[1].Kind != "REFUND" {
				t.Fatalf("%s: want sale + refund, got %+v", o.Outcome, in.Transactions)
			}
			if in.Transactions[1].AmountSet.ShopMoney.Amount != shopify.FormatAmount(o.RefundCents) {
				t.Fatalf("%s: refund amount %s", o.Outcome, in.Transactions[1].AmountSet.ShopMoney.Amount)
			}
		default:
			if len(in.Transactions) != 1 || in.Transactions[0].Kind != "SALE" {
				t.Fatalf("%s: want 1 sale, got %+v", o.Outcome, in.Transactions)
			}
		}
		if len(in.Tags) != 4 || in.Tags[3] != SeqTag("20261014-abcdef", o.Index) {
			t.Fatalf("tags %v", in.Tags)
		}
		if in.Tags[2] != "shopgen-"+strings.ReplaceAll(o.Outcome, "_", "-") {
			t.Fatalf("outcome tag %q", in.Tags[2])
		}
		if (o.TaxCents > 0) != (len(in.TaxLines) == 1) {
			t.Fatalf("tax lines %v for tax %d", in.TaxLines, o.TaxCents)
		}
		if (o.ClosedAt != nil) != (in.ClosedAt != "") {
			t.Fatal("closedAt mismatch")
		}
		if in.Customer == nil || in.Customer.ToUpsert.Email == "" || in.ShippingAddress.CountryCode != "US" {
			t.Fatal("missing customer or address")
		}
		if in.SourceIdentifier != "shopgen:20261014-abcdef:"+itoa(o.Index) {
			t.Fatalf("sourceIdentifier %q", in.SourceIdentifier)
		}
	}
	for _, k := range AllOutcomes {
		if !seen[k] {
			t.Fatalf("test plan never produced %s", k)
		}
	}
}

func itoa(i int) string { b, _ := json.Marshal(i); return string(b) }

func TestSummaryFormat(t *testing.T) {
	p := testProfile(t, "")
	plan := mustGenerate(t, p, SyntheticCatalog(), genNow)
	var sb strings.Builder
	Summarize(p, plan, 5).Write(&sb, ui.Style{})
	out := sb.String()
	for _, prefix := range []string{"window      2026-08-01 → 2026-10-13 (74 days, America/Chicago)", "orders      ", "customers   ", "revenue     ", "outcomes    ", "duration    "} {
		if !strings.Contains(out, "\n"+prefix) && !strings.HasPrefix(out, prefix) {
			t.Fatalf("summary missing %q:\n%s", prefix, out)
		}
	}
	if strings.Contains(out, "\x1b[") {
		t.Fatal("plain summary contains ANSI escapes")
	}
}

func TestDurationMatchesSpecExample(t *testing.T) {
	s := Summary{Orders: 5670, OPM: 5}
	if got := ui.FormatDuration(s.Duration()); got != "19h16m" {
		t.Fatalf("duration %s, want 19h16m", got)
	}
}
