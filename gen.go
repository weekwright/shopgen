package main

import (
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"sort"
	"strings"
	"time"
)

// Plan is the full order history for a profile, generated up front.
type Plan struct {
	Location *time.Location
	Start    time.Time // local midnight of the first day
	Days     int
	Now      time.Time
	Orders   []PlannedOrder
}

// PlannedOrder is one order record. Amounts are integer cents.
type PlannedOrder struct {
	Index         int        `json:"index"`
	ProcessedAt   time.Time  `json:"processed_at"`
	Customer      int        `json:"customer"`
	Lines         []Line     `json:"lines"`
	ShipTitle     string     `json:"ship_title"`
	SubtotalCents int64      `json:"subtotal_cents"`
	ShipCents     int64      `json:"ship_cents"`
	TaxCents      int64      `json:"tax_cents"`
	TotalCents    int64      `json:"total_cents"`
	Outcome       string     `json:"outcome"`
	RefundCents   int64      `json:"refund_cents,omitempty"`
	RefundAt      *time.Time `json:"refund_at,omitempty"`
	ClosedAt      *time.Time `json:"closed_at,omitempty"`
}

// Line is one line item: a catalog index, quantity and unit price.
type Line struct {
	Variant    int   `json:"variant"`
	Qty        int   `json:"qty"`
	PriceCents int64 `json:"price_cents"`
}

// Customer is a deterministic synthetic buyer.
type Customer struct {
	Index    int
	First    string
	Last     string
	Email    string
	Address1 string
	City     string
	Province string
	Zip      string
}

const (
	seedMix     = 0x9e3779b97f4a7c15
	customerMix = 0x5eed_c057_0bad_cafe
)

// CustomerAt returns customer i for a profile. It uses its own PRNG seeded by
// (seed, i), so customer i is the same no matter how the plan was built.
func CustomerAt(p *Profile, i int) Customer {
	r := rand.New(rand.NewPCG(uint64(p.Seed)^customerMix, uint64(i)))
	first := firstNames[r.IntN(len(firstNames))]
	last := lastNames[r.IntN(len(lastNames))]
	city := usCities[r.IntN(len(usCities))]
	street := streetNames[r.IntN(len(streetNames))]
	num := 100 + r.IntN(9900)
	return Customer{
		Index:    i,
		First:    first,
		Last:     last,
		Email:    fmt.Sprintf("%s.%s.%d@%s", strings.ToLower(first), strings.ToLower(last), i, strings.ToLower(p.Customers.EmailDomain)),
		Address1: fmt.Sprintf("%d %s", num, street),
		City:     city.City,
		Province: city.Province,
		Zip:      city.Zip,
	}
}

// Window resolves the profile's first and last local days. An empty end means
// "today" in the profile's timezone as of now.
func (p *Profile) Window(now time.Time) (loc *time.Location, start time.Time, days int, err error) {
	loc, err = time.LoadLocation(p.Timezone)
	if err != nil {
		return nil, time.Time{}, 0, fmt.Errorf("timezone: %v", err)
	}
	s, err := parseDate(p.Start)
	if err != nil {
		return nil, time.Time{}, 0, fmt.Errorf("start: %v", err)
	}
	var e time.Time
	if p.End == "" {
		ln := now.In(loc)
		e = time.Date(ln.Year(), ln.Month(), ln.Day(), 0, 0, 0, 0, time.UTC)
		if e.Before(s) {
			return nil, time.Time{}, 0, fmt.Errorf("start: %s is after today (%s)", p.Start, e.Format("2006-01-02"))
		}
	} else if e, err = parseDate(p.End); err != nil {
		return nil, time.Time{}, 0, fmt.Errorf("end: %v", err)
	}
	if e.Before(s) {
		return nil, time.Time{}, 0, fmt.Errorf("end: %s is before start %s", p.End, p.Start)
	}
	days = int(e.Sub(s).Hours()/24) + 1
	start = time.Date(s.Year(), s.Month(), s.Day(), 0, 0, 0, 0, loc)
	return loc, start, days, nil
}

// dayAt returns local midnight of day d of the window. It uses calendar
// arithmetic so DST changes never shift a day.
func dayAt(start time.Time, d int) time.Time {
	return time.Date(start.Year(), start.Month(), start.Day()+d, 0, 0, 0, 0, start.Location())
}

// Generate builds the whole plan. It is a pure function: no I/O, no global
// randomness and no wall clock, so the same inputs always give the same plan.
func Generate(p *Profile, catalog []Variant, now time.Time) (*Plan, error) {
	if len(catalog) == 0 {
		return nil, errors.New("catalog is empty: add at least one active product with a price above 0")
	}
	cat := SortCatalog(catalog)
	loc, start, days, err := p.Window(now)
	if err != nil {
		return nil, err
	}
	rng := rand.New(rand.NewPCG(uint64(p.Seed), uint64(p.Seed)^seedMix))

	// Popularity: Zipf weights over a seeded permutation of the catalog, so
	// best sellers are random rather than the lowest IDs.
	perm := rng.Perm(len(cat))
	zipf := newWeighted(len(cat), func(k int) float64 { return 1 / math.Pow(float64(k+1), p.Items.PopularitySkew) })

	hourly := newWeighted(24, func(h int) float64 { return p.Hourly[h] })
	shipW := newWeighted(len(p.Shipping), func(i int) float64 { return p.Shipping[i].Weight })
	outcomeKeys := sortedKeys(p.Outcomes)
	outcomeW := newWeighted(len(outcomeKeys), func(i int) float64 { return p.Outcomes[outcomeKeys[i]] })

	spikes := make([]struct {
		from time.Time
		days int
		mult float64
	}, len(p.Spikes))
	for i, s := range p.Spikes {
		d, _ := parseDate(s.Date)
		spikes[i].from, spikes[i].days, spikes[i].mult = d, s.Days, s.Multiplier
	}

	g, sigma := p.Growth, p.Noise
	freeOver := toCents(p.FreeShippingOver)
	lag := p.FulfillmentLagDays
	plan := &Plan{Location: loc, Start: start, Days: days, Now: now.UTC()}
	var previous []int // customer index of every earlier order
	nextNew := 0

	for d := 0; d < days; d++ {
		day := dayAt(start, d)
		t := 0.0
		if days > 1 {
			t = float64(d) / float64(days-1)
		}
		s := 1.0
		date := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
		for _, sp := range spikes {
			if !date.Before(sp.from) && date.Before(sp.from.AddDate(0, 0, sp.days)) {
				s *= sp.mult
			}
		}
		z := rng.NormFloat64()
		mu := p.OrdersPerDay * (1 + g*t) / (1 + g/2) * p.Weekly[int(day.Weekday())] * s * math.Exp(sigma*z-sigma*sigma/2)
		n := int(math.Floor(mu))
		if rng.Float64() < mu-math.Floor(mu) {
			n++
		}

		times := make([]time.Time, 0, n)
		for i := 0; i < n; i++ {
			h := hourly.pick(rng)
			sec := rng.IntN(3600)
			ts := time.Date(day.Year(), day.Month(), day.Day(), h, 0, sec, 0, loc)
			if ts.After(now) {
				continue
			}
			times = append(times, ts.UTC())
		}
		sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })

		for _, ts := range times {
			o := PlannedOrder{Index: len(plan.Orders), ProcessedAt: ts}

			// Customer: preferential attachment to earlier buyers.
			switch {
			case len(previous) > 0 && rng.Float64() < p.Customers.RepeatRate:
				o.Customer = previous[rng.IntN(len(previous))]
			case nextNew < p.Customers.Pool:
				o.Customer = nextNew
				nextNew++
			default:
				o.Customer = rng.IntN(p.Customers.Pool)
			}
			previous = append(previous, o.Customer)

			// Basket.
			lines := p.Items.Min
			if lines > len(cat) {
				lines = len(cat)
			}
			for lines < p.Items.Max && lines < len(cat) && rng.Float64() < 0.35 {
				lines++
			}
			chosen := make(map[int]bool, lines)
			for len(o.Lines) < lines {
				rank := zipf.pickExcluding(rng, chosen)
				chosen[rank] = true
				v := perm[rank]
				qty := 1
				for qty < p.Items.MaxQty && rng.Float64() < 0.2 {
					qty++
				}
				o.Lines = append(o.Lines, Line{Variant: v, Qty: qty, PriceCents: cat[v].PriceCents})
				o.SubtotalCents += cat[v].PriceCents * int64(qty)
			}

			// Money.
			si := shipW.pick(rng)
			o.ShipTitle = p.Shipping[si].Title
			o.ShipCents = toCents(p.Shipping[si].Price)
			if si == 0 && freeOver > 0 && o.SubtotalCents >= freeOver {
				o.ShipCents = 0
			}
			o.TaxCents = roundCents(float64(o.SubtotalCents) * p.TaxRate)
			o.TotalCents = o.SubtotalCents + o.ShipCents + o.TaxCents

			// Outcome, adjusted for recency.
			o.Outcome = outcomeKeys[outcomeW.pick(rng)]
			age := now.Sub(ts).Hours() / 24
			if lag > 0 && age < lag && o.Outcome != OutcomePendingPayment && o.Outcome != OutcomeCancelled {
				if rng.Float64() < 1-age/lag {
					o.Outcome = OutcomeUnfulfilled
				} else if isRefund(o.Outcome) {
					o.Outcome = OutcomeFulfilled
				}
			}

			if isRefund(o.Outcome) {
				at := ts.Add(time.Duration(2+rng.IntN(19))*24*time.Hour + time.Duration(rng.IntN(86400))*time.Second)
				if at.After(now) {
					at = now.Add(-time.Duration(1+rng.IntN(3600)) * time.Second).UTC()
				}
				amount := o.TotalCents
				if o.Outcome == OutcomePartiallyRefunded {
					l := o.Lines[rng.IntN(len(o.Lines))]
					amount = l.PriceCents + roundCents(float64(l.PriceCents)*p.TaxRate)
					if amount >= o.TotalCents {
						amount = o.TotalCents / 2
					}
				}
				if !at.After(ts) || amount <= 0 {
					o.Outcome = OutcomeFulfilled
				} else {
					o.RefundAt = &at
					o.RefundCents = amount
				}
			}

			if p.ArchiveFulfilled && (o.Outcome == OutcomeFulfilled || isRefund(o.Outcome)) {
				at := ts.Add(time.Duration(3+rng.IntN(8))*24*time.Hour + time.Duration(rng.IntN(86400))*time.Second)
				if o.RefundAt != nil && at.Before(o.RefundAt.Add(time.Hour)) {
					at = o.RefundAt.Add(time.Hour)
				}
				if !at.After(now) {
					o.ClosedAt = &at
				}
			}

			plan.Orders = append(plan.Orders, o)
		}
	}
	return plan, nil
}

func isRefund(outcome string) bool {
	return outcome == OutcomeRefunded || outcome == OutcomePartiallyRefunded
}

func roundCents(f float64) int64 { return int64(math.Round(f)) }

// weighted draws indices in proportion to fixed weights via a CDF.
type weighted struct {
	cdf []float64
	w   []float64
}

func newWeighted(n int, weight func(int) float64) *weighted {
	ws := &weighted{cdf: make([]float64, n), w: make([]float64, n)}
	var sum float64
	for i := 0; i < n; i++ {
		ws.w[i] = weight(i)
		sum += ws.w[i]
		ws.cdf[i] = sum
	}
	return ws
}

func (ws *weighted) pick(r *rand.Rand) int {
	u := r.Float64() * ws.cdf[len(ws.cdf)-1]
	// The first index whose cumulative weight exceeds u; zero-weight entries
	// never qualify because their cdf equals their predecessor's.
	i := sort.Search(len(ws.cdf), func(i int) bool { return ws.cdf[i] > u })
	if i == len(ws.cdf) {
		i = len(ws.cdf) - 1
	}
	return i
}

// pickExcluding draws without replacement: it rejects indices already in
// skip, falling back to an exact scan when rejections pile up.
func (ws *weighted) pickExcluding(r *rand.Rand, skip map[int]bool) int {
	for try := 0; try < 32; try++ {
		if i := ws.pick(r); !skip[i] {
			return i
		}
	}
	var remaining float64
	for i, w := range ws.w {
		if !skip[i] {
			remaining += w
		}
	}
	u := r.Float64() * remaining
	last := -1
	for i, w := range ws.w {
		if skip[i] {
			continue
		}
		last = i
		if u < w {
			return i
		}
		u -= w
	}
	return last
}
