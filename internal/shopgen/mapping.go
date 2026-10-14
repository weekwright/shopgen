package shopgen

import (
	"fmt"
	"strings"
	"time"

	"github.com/weekwright/shopgen/internal/shopify"
)

// Tag helpers. Every object shopgen creates carries TagShopgen and a run tag,
// which is what purge searches for.
const TagShopgen = "shopgen"

// RunTag tags everything one run created.
func RunTag(runID string) string { return "shopgen-run-" + runID }

// SeqTag is unique per planned order; it makes creates idempotent.
func SeqTag(runID string, index int) string { return fmt.Sprintf("sgseq-%s-%d", runID, index) }

// OutcomeTag tags an order with its planned outcome.
func OutcomeTag(outcome string) string { return "shopgen-" + strings.ReplaceAll(outcome, "_", "-") }

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// BuildOrder maps a planned order to an orderCreate input.
func BuildOrder(runID string, p *Profile, cat []Variant, o PlannedOrder) *shopify.OrderInput {
	money := func(cents int64) shopify.MoneyBagInput { return shopify.MoneyBag(cents, p.Currency) }
	cust := CustomerAt(p, o.Customer)
	addr := &shopify.AddressInput{
		FirstName:    cust.First,
		LastName:     cust.Last,
		Address1:     cust.Address1,
		City:         cust.City,
		ProvinceCode: cust.Province,
		Zip:          cust.Zip,
		CountryCode:  "US",
	}
	in := &shopify.OrderInput{
		ProcessedAt:     rfc3339(o.ProcessedAt),
		Currency:        p.Currency,
		Email:           cust.Email,
		ShippingAddress: addr,
		BillingAddress:  addr,
		ShippingLines:   []shopify.ShippingLineInput{{Title: o.ShipTitle, PriceSet: money(o.ShipCents)}},
		Customer: &shopify.CustomerInput{ToUpsert: shopify.CustomerUpsertInput{
			Email:     cust.Email,
			FirstName: cust.First,
			LastName:  cust.Last,
			Tags:      []string{TagShopgen, RunTag(runID)},
		}},
		Tags:             []string{TagShopgen, RunTag(runID), OutcomeTag(o.Outcome), SeqTag(runID, o.Index)},
		SourceIdentifier: fmt.Sprintf("shopgen:%s:%d", runID, o.Index),
		Test:             p.TestOrders,
	}
	for _, l := range o.Lines {
		in.LineItems = append(in.LineItems, shopify.LineItemInput{VariantID: cat[l.Variant].ID, Quantity: l.Qty, PriceSet: money(l.PriceCents)})
	}
	if o.TaxCents > 0 {
		in.TaxLines = []shopify.TaxLineInput{{Title: "Sales Tax", Rate: p.TaxRate, PriceSet: money(o.TaxCents)}}
	}
	if o.ClosedAt != nil {
		in.ClosedAt = rfc3339(*o.ClosedAt)
	}

	sale := shopify.TransactionInput{Kind: "SALE", Status: "SUCCESS", Gateway: p.Gateway, AmountSet: money(o.TotalCents), ProcessedAt: rfc3339(o.ProcessedAt), Test: p.TestOrders}
	refund := func() shopify.TransactionInput {
		return shopify.TransactionInput{Kind: "REFUND", Status: "SUCCESS", Gateway: p.Gateway, AmountSet: money(o.RefundCents), ProcessedAt: rfc3339(*o.RefundAt), Test: p.TestOrders}
	}
	switch o.Outcome {
	case OutcomeFulfilled:
		in.FinancialStatus, in.FulfillmentStatus = "PAID", "FULFILLED"
		in.Transactions = []shopify.TransactionInput{sale}
	case OutcomeUnfulfilled, OutcomeCancelled:
		in.FinancialStatus = "PAID"
		in.Transactions = []shopify.TransactionInput{sale}
	case OutcomePartiallyFulfilled:
		in.FinancialStatus, in.FulfillmentStatus = "PAID", "PARTIAL"
		in.Transactions = []shopify.TransactionInput{sale}
	case OutcomePendingPayment:
		in.FinancialStatus = "PENDING"
	case OutcomeRefunded:
		in.FinancialStatus, in.FulfillmentStatus = "REFUNDED", "FULFILLED"
		in.Transactions = []shopify.TransactionInput{sale, refund()}
	case OutcomePartiallyRefunded:
		in.FinancialStatus, in.FulfillmentStatus = "PARTIALLY_REFUNDED", "FULFILLED"
		in.Transactions = []shopify.TransactionInput{sale, refund()}
	}
	return in
}
