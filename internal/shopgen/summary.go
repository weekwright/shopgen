package shopgen

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/weekwright/shopgen/internal/shopify"
	"github.com/weekwright/shopgen/internal/ui"
)

// Summary is the headline numbers of a plan.
type Summary struct {
	From, To      time.Time
	Days          int
	Timezone      string
	Orders        int
	BusiestDay    time.Time
	BusiestCount  int
	PerDay        []int
	Customers     int
	Currency      string
	GrossCents    int64
	RefundedCents int64
	Outcomes      map[string]int
	OPM           float64
	CatalogSize   int
	CatalogSource string
}

// Duration is how long a run takes at the configured pace, including the 2%
// headroom kept under Shopify's create limit.
func (s Summary) Duration() time.Duration { return runInterval(s.OPM) * time.Duration(s.Orders) }

// Summarize computes plan statistics.
func Summarize(p *Profile, plan *Plan, opm float64) Summary {
	s := Summary{
		From:     plan.Start,
		To:       dayAt(plan.Start, plan.Days-1),
		Days:     plan.Days,
		Timezone: p.Timezone,
		Orders:   len(plan.Orders),
		PerDay:   make([]int, plan.Days),
		Currency: p.Currency,
		Outcomes: map[string]int{},
		OPM:      opm,
	}
	customers := map[int]bool{}
	dayIndex := map[string]int{}
	for d := 0; d < plan.Days; d++ {
		dayIndex[dayAt(plan.Start, d).Format("2006-01-02")] = d
	}
	for _, o := range plan.Orders {
		customers[o.Customer] = true
		s.GrossCents += o.TotalCents
		s.RefundedCents += o.RefundCents
		s.Outcomes[o.Outcome]++
		if d, ok := dayIndex[o.ProcessedAt.In(plan.Location).Format("2006-01-02")]; ok {
			s.PerDay[d]++
		}
	}
	s.Customers = len(customers)
	for d, n := range s.PerDay {
		if n > s.BusiestCount {
			s.BusiestCount, s.BusiestDay = n, dayAt(plan.Start, d)
		}
	}
	return s
}

// Write prints the summary. The first six lines are a stable format that
// scripts may parse; styling is added only when st is enabled.
func (s Summary) Write(w io.Writer, st ui.Style) {
	row := func(label, value string) {
		fmt.Fprintf(w, "%s%s\n", st.Bold(ui.PadRight(label, 12)), value)
	}
	dash := func(t time.Time) string { return t.Format("2006-01-02") }

	row("window", fmt.Sprintf("%s → %s %s", dash(s.From), dash(s.To), st.Dim(fmt.Sprintf("(%d days, %s)", s.Days, s.Timezone))))
	avg := 0.0
	if s.Days > 0 {
		avg = float64(s.Orders) / float64(s.Days)
	}
	busiest := ""
	if s.BusiestCount > 0 {
		busiest = fmt.Sprintf(", busiest %s with %d", dash(s.BusiestDay), s.BusiestCount)
	}
	row("orders", fmt.Sprintf("%s  %s", st.Brand(fmt.Sprint(s.Orders)), st.Dim(fmt.Sprintf("(avg %.0f/day%s)", avg, busiest))))
	row("customers", fmt.Sprintf("%d distinct", s.Customers))
	aov := int64(0)
	if s.Orders > 0 {
		aov = (s.GrossCents + int64(s.Orders)/2) / int64(s.Orders)
	}
	row("revenue", fmt.Sprintf("%s %s gross, %s refunded, AOV %s",
		shopify.FormatAmount(s.GrossCents), s.Currency, shopify.FormatAmount(s.RefundedCents), shopify.FormatAmount(aov)))

	keys := sortedKeys(s.Outcomes)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		pct := 0.0
		if s.Orders > 0 {
			pct = float64(s.Outcomes[k]) * 100 / float64(s.Orders)
		}
		parts = append(parts, fmt.Sprintf("%s %d (%.1f%%)", st.Outcome(k), s.Outcomes[k], pct))
	}
	row("outcomes", strings.Join(parts, ", "))
	row("duration", fmt.Sprintf("%s at %.1f orders/min", st.Bold(ui.FormatDuration(s.Duration())), s.OPM))

	if s.CatalogSize > 0 {
		row("catalog", fmt.Sprintf("%d variants %s", s.CatalogSize, st.Dim("("+s.CatalogSource+")")))
	}
	if len(s.PerDay) > 1 {
		row("volume", st.Brand(ui.Sparkline(s.PerDay, 60)))
	}
}

// outcomeBreakdown is a compact "fulfilled 4, cancelled 1" string, most
// common first.
func outcomeBreakdown(counts map[string]int, st ui.Style) string {
	keys := sortedKeys(counts)
	sort.SliceStable(keys, func(i, j int) bool { return counts[keys[i]] > counts[keys[j]] })
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %d", st.Outcome(k), counts[k]))
	}
	return strings.Join(parts, ", ")
}
