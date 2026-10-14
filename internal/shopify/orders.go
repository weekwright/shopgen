package shopify

// OrderCreateOptions keep backfilled history from draining stock or emailing
// anyone: inventory is bypassed and no receipts are sent.
var OrderCreateOptions = map[string]any{
	"inventoryBehaviour":     "BYPASS",
	"sendReceipt":            false,
	"sendFulfillmentReceipt": false,
}

// MoneyBagInput is Shopify's MoneyBagInput.
type MoneyBagInput struct {
	ShopMoney MoneyInput `json:"shopMoney"`
}

// MoneyInput is Shopify's MoneyInput.
type MoneyInput struct {
	Amount       string `json:"amount"`
	CurrencyCode string `json:"currencyCode"`
}

// OrderInput mirrors the subset of OrderCreateOrderInput that shopgen sends.
type OrderInput struct {
	ProcessedAt       string              `json:"processedAt"`
	Currency          string              `json:"currency"`
	Email             string              `json:"email"`
	LineItems         []LineItemInput     `json:"lineItems"`
	ShippingAddress   *AddressInput       `json:"shippingAddress"`
	BillingAddress    *AddressInput       `json:"billingAddress"`
	ShippingLines     []ShippingLineInput `json:"shippingLines"`
	Customer          *CustomerInput      `json:"customer"`
	TaxLines          []TaxLineInput      `json:"taxLines,omitempty"`
	Tags              []string            `json:"tags"`
	SourceIdentifier  string              `json:"sourceIdentifier"`
	Test              bool                `json:"test"`
	ClosedAt          string              `json:"closedAt,omitempty"`
	FinancialStatus   string              `json:"financialStatus"`
	FulfillmentStatus string              `json:"fulfillmentStatus,omitempty"`
	Transactions      []TransactionInput  `json:"transactions,omitempty"`
}

// LineItemInput is OrderCreateLineItemInput.
type LineItemInput struct {
	VariantID string        `json:"variantId"`
	Quantity  int           `json:"quantity"`
	PriceSet  MoneyBagInput `json:"priceSet"`
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

// ShippingLineInput is OrderCreateShippingLineInput.
type ShippingLineInput struct {
	Title    string        `json:"title"`
	PriceSet MoneyBagInput `json:"priceSet"`
}

// CustomerInput is OrderCreateCustomerInput.
type CustomerInput struct {
	ToUpsert CustomerUpsertInput `json:"toUpsert"`
}

// CustomerUpsertInput is the customer to create or match by email.
type CustomerUpsertInput struct {
	Email     string   `json:"email"`
	FirstName string   `json:"firstName"`
	LastName  string   `json:"lastName"`
	Tags      []string `json:"tags"`
}

// TaxLineInput is OrderCreateTaxLineInput.
type TaxLineInput struct {
	Title    string        `json:"title"`
	Rate     float64       `json:"rate"`
	PriceSet MoneyBagInput `json:"priceSet"`
}

// TransactionInput is OrderCreateOrderTransactionInput.
type TransactionInput struct {
	Kind        string        `json:"kind"`
	Status      string        `json:"status"`
	Gateway     string        `json:"gateway"`
	AmountSet   MoneyBagInput `json:"amountSet"`
	ProcessedAt string        `json:"processedAt"`
	Test        bool          `json:"test"`
}
