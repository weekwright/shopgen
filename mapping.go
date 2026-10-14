package main

import (
	"fmt"
	"strings"
	"time"
)

// orderOptions keep history from draining stock or emailing anyone.
var orderOptions = map[string]any{
	"inventoryBehaviour":     "BYPASS",
	"sendReceipt":            false,
	"sendFulfillmentReceipt": false,
}

// MoneyBag is Shopify's MoneyBagInput.
type MoneyBag struct {
	ShopMoney Money `json:"shopMoney"`
}

// Money is Shopify's MoneyInput.
type Money struct {
	Amount       string `json:"amount"`
	CurrencyCode string `json:"currencyCode"`
}

// OrderInput mirrors the subset of OrderCreateOrderInput that shopgen sends.
type OrderInput struct {
	ProcessedAt       string             `json:"processedAt"`
	Currency          string             `json:"currency"`
	Email             string             `json:"email"`
	LineItems         []LineItemInput    `json:"lineItems"`
	ShippingAddress   *AddressInput      `json:"shippingAddress"`
	BillingAddress    *AddressInput      `json:"billingAddress"`
	ShippingLines     []ShippingInput    `json:"shippingLines"`
	Customer          *CustomerInput     `json:"customer"`
	TaxLines          []TaxLineInput     `json:"taxLines,omitempty"`
	Tags              []string           `json:"tags"`
	SourceIdentifier  string             `json:"sourceIdentifier"`
	Test              bool               `json:"test"`
	ClosedAt          string             `json:"closedAt,omitempty"`
	FinancialStatus   string             `json:"financialStatus"`
	FulfillmentStatus string             `json:"fulfillmentStatus,omitempty"`
	Transactions      []TransactionInput `json:"transactions,omitempty"`
}

// LineItemInput is OrderCreateLineItemInput.
type LineItemInput struct {
	VariantID string   `json:"variantId"`
	Quantity  int      `json:"quantity"`
	PriceSet  MoneyBag `json:"priceSet"`
}

// AddressInput is MailingAddressInput.
type AddressInput struct {
	FirstName    string `json:"firstName"`
	LastName     string `json:"lastName"`
	Address1     string `json:"address1"`
	City         string `json:"city"`
	ProvinceCode string `json:"provinceCode"`
	Zip          string `json:"zip"`
	CountryCode  string `json:"countryCode"`
}

// ShippingInput is OrderCreateShippingLineInput.
type ShippingInput struct {
	Title    string   `json:"title"`
	PriceSet MoneyBag `json:"priceSet"`
}

// CustomerInput is OrderCreateCustomerInput.
type CustomerInput struct {
	ToUpsert CustomerUpsert `json:"toUpsert"`
}

// CustomerUpsert is the customer to create or match by email.
type CustomerUpsert struct {
	Email     string   `json:"email"`
	FirstName string   `json:"firstName"`
	LastName  string   `json:"lastName"`
	Tags      []string `json:"tags"`
}

// TaxLineInput is OrderCreateTaxLineInput.
type TaxLineInput struct {
	Title    string   `json:"title"`
	Rate     float64  `json:"rate"`
	PriceSet MoneyBag `json:"priceSet"`
}

// TransactionInput is OrderCreateOrderTransactionInput.
type TransactionInput struct {
	Kind        string   `json:"kind"`
	Status      string   `json:"status"`
	Gateway     string   `json:"gateway"`
	AmountSet   MoneyBag `json:"amountSet"`
	ProcessedAt string   `json:"processedAt"`
	Test        bool     `json:"test"`
}

// Tag helpers. Every object shopgen creates carries tagShopgen and a run tag,
// which is what purge searches for.
const tagShopgen = "shopgen"

func runTag(runID string) string { return "shopgen-run-" + runID }

func seqTag(runID string, index int) string { return fmt.Sprintf("sgseq-%s-%d", runID, index) }

func outcomeTag(outcome string) string { return "shopgen-" + strings.ReplaceAll(outcome, "_", "-") }

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// BuildOrder maps a planned order to an orderCreate input.
func BuildOrder(runID string, p *Profile, cat []Variant, o PlannedOrder) *OrderInput {
	money := func(cents int64) MoneyBag {
		return MoneyBag{ShopMoney: Money{Amount: formatAmount(cents), CurrencyCode: p.Currency}}
	}
	cust := CustomerAt(p, o.Customer)
	addr := &AddressInput{
		FirstName:    cust.First,
		LastName:     cust.Last,
		Address1:     cust.Address1,
		City:         cust.City,
		ProvinceCode: cust.Province,
		Zip:          cust.Zip,
		CountryCode:  "US",
	}
	in := &OrderInput{
		ProcessedAt:     rfc3339(o.ProcessedAt),
		Currency:        p.Currency,
		Email:           cust.Email,
		ShippingAddress: addr,
		BillingAddress:  addr,
		ShippingLines:   []ShippingInput{{Title: o.ShipTitle, PriceSet: money(o.ShipCents)}},
		Customer: &CustomerInput{ToUpsert: CustomerUpsert{
			Email:     cust.Email,
			FirstName: cust.First,
			LastName:  cust.Last,
			Tags:      []string{tagShopgen, runTag(runID)},
		}},
		Tags:             []string{tagShopgen, runTag(runID), outcomeTag(o.Outcome), seqTag(runID, o.Index)},
		SourceIdentifier: fmt.Sprintf("shopgen:%s:%d", runID, o.Index),
		Test:             p.TestOrders,
	}
	for _, l := range o.Lines {
		in.LineItems = append(in.LineItems, LineItemInput{VariantID: cat[l.Variant].ID, Quantity: l.Qty, PriceSet: money(l.PriceCents)})
	}
	if o.TaxCents > 0 {
		in.TaxLines = []TaxLineInput{{Title: "Sales Tax", Rate: p.TaxRate, PriceSet: money(o.TaxCents)}}
	}
	if o.ClosedAt != nil {
		in.ClosedAt = rfc3339(*o.ClosedAt)
	}

	sale := TransactionInput{Kind: "SALE", Status: "SUCCESS", Gateway: p.Gateway, AmountSet: money(o.TotalCents), ProcessedAt: rfc3339(o.ProcessedAt), Test: p.TestOrders}
	refund := func() TransactionInput {
		return TransactionInput{Kind: "REFUND", Status: "SUCCESS", Gateway: p.Gateway, AmountSet: money(o.RefundCents), ProcessedAt: rfc3339(*o.RefundAt), Test: p.TestOrders}
	}
	switch o.Outcome {
	case OutcomeFulfilled:
		in.FinancialStatus, in.FulfillmentStatus = "PAID", "FULFILLED"
		in.Transactions = []TransactionInput{sale}
	case OutcomeUnfulfilled, OutcomeCancelled:
		in.FinancialStatus = "PAID"
		in.Transactions = []TransactionInput{sale}
	case OutcomePartiallyFulfilled:
		in.FinancialStatus, in.FulfillmentStatus = "PAID", "PARTIAL"
		in.Transactions = []TransactionInput{sale}
	case OutcomePendingPayment:
		in.FinancialStatus = "PENDING"
	case OutcomeRefunded:
		in.FinancialStatus, in.FulfillmentStatus = "REFUNDED", "FULFILLED"
		in.Transactions = []TransactionInput{sale, refund()}
	case OutcomePartiallyRefunded:
		in.FinancialStatus, in.FulfillmentStatus = "PARTIALLY_REFUNDED", "FULFILLED"
		in.Transactions = []TransactionInput{sale, refund()}
	}
	return in
}
