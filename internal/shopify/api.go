package shopify

import (
	"context"
	"fmt"
	"strings"
)

const (
	qShop = `query Shop { shop { name myshopifyDomain currencyCode plan { partnerDevelopment } } }`

	qVariants = `query Variants($cursor: String) {
  productVariants(first: 250, after: $cursor, query: "product_status:active") {
    nodes { id title price product { title } }
    PageInfo { hasNextPage endCursor }
  }
}`

	mOrderCreate = `mutation OrderCreate($order: OrderCreateOrderInput!, $options: OrderCreateOptionsInput) {
  orderCreate(order: $order, options: $options) {
    order { id name }
    userErrors { field message }
  }
}`

	mOrderCancel = `mutation OrderCancel($orderId: ID!, $refundMethod: OrderCancelRefundMethodInput, $restock: Boolean!, $reason: OrderCancelReason!, $notifyCustomer: Boolean) {
  orderCancel(orderId: $orderId, refundMethod: $refundMethod, restock: $restock, reason: $reason, notifyCustomer: $notifyCustomer) {
    job { id }
    orderCancelUserErrors { field message code }
  }
}`

	qOrdersByTag = `query OrdersByTag($query: String!, $cursor: String) {
  orders(first: 50, after: $cursor, query: $query) {
    nodes { id name cancelledAt tags }
    PageInfo { hasNextPage endCursor }
  }
}`

	mOrderDelete = `mutation OrderDelete($orderId: ID!) {
  orderDelete(orderId: $orderId) {
    deletedId
    userErrors { field message }
  }
}`

	qCustomersByTag = `query CustomersByTag($query: String!, $cursor: String) {
  customers(first: 50, after: $cursor, query: $query) {
    nodes { id email }
    PageInfo { hasNextPage endCursor }
  }
}`

	mCustomerDelete = `mutation CustomerDelete($input: CustomerDeleteInput!) {
  customerDelete(input: $input) {
    deletedCustomerId
    userErrors { field message }
  }
}`
)

// ShopInfo is what the safety gate learns about a store.
type ShopInfo struct {
	Name               string
	Domain             string
	Currency           string
	PartnerDevelopment bool
}

// UserError is a Shopify mutation userError.
type UserError struct {
	Field   []string `json:"field"`
	Message string   `json:"message"`
	Code    string   `json:"code,omitempty"`
}

// FormatUserErrors renders userErrors as "field.path: message; …".
func FormatUserErrors(errs []UserError) string {
	parts := make([]string, len(errs))
	for i, e := range errs {
		if len(e.Field) > 0 {
			parts[i] = strings.Join(e.Field, ".") + ": " + e.Message
		} else {
			parts[i] = e.Message
		}
	}
	return strings.Join(parts, "; ")
}

// PageInfo is a connection's cursor state.
type PageInfo struct {
	HasNextPage bool    `json:"hasNextPage"`
	EndCursor   *string `json:"endCursor"`
}

// Shop fetches store identity and the dev-store flag.
func (c *Client) Shop(ctx context.Context) (ShopInfo, error) {
	var out struct {
		Shop struct {
			Name            string `json:"name"`
			MyshopifyDomain string `json:"myshopifyDomain"`
			CurrencyCode    string `json:"currencyCode"`
			Plan            struct {
				PartnerDevelopment bool `json:"partnerDevelopment"`
			} `json:"plan"`
		} `json:"shop"`
	}
	if err := c.Do(ctx, qShop, nil, &out); err != nil {
		return ShopInfo{}, err
	}
	s := out.Shop
	return ShopInfo{Name: s.Name, Domain: s.MyshopifyDomain, Currency: s.CurrencyCode, PartnerDevelopment: s.Plan.PartnerDevelopment}, nil
}

// ProductVariant is an active variant with a positive price.
type ProductVariant struct {
	ID           string
	Title        string
	ProductTitle string
	PriceCents   int64
}

// DisplayTitle is the product title plus " - variant" unless the variant is
// Shopify's placeholder "Default Title".
func (v ProductVariant) DisplayTitle() string {
	if v.Title == "" || v.Title == "Default Title" {
		return v.ProductTitle
	}
	return v.ProductTitle + " - " + v.Title
}

// ActiveVariants pages through every variant of an active product priced
// above zero, 250 at a time.
func (c *Client) ActiveVariants(ctx context.Context) ([]ProductVariant, error) {
	var out []ProductVariant
	var cursor *string
	for {
		var page struct {
			ProductVariants struct {
				Nodes []struct {
					ID      string `json:"id"`
					Title   string `json:"title"`
					Price   string `json:"price"`
					Product struct {
						Title string `json:"title"`
					} `json:"product"`
				} `json:"nodes"`
				PageInfo PageInfo `json:"pageInfo"`
			} `json:"productVariants"`
		}
		if err := c.Do(ctx, qVariants, map[string]any{"cursor": cursor}, &page); err != nil {
			return nil, fmt.Errorf("fetch catalog: %w", err)
		}
		for _, n := range page.ProductVariants.Nodes {
			cents, err := ParseMoney(n.Price)
			if err != nil || cents <= 0 {
				continue
			}
			out = append(out, ProductVariant{ID: n.ID, Title: n.Title, ProductTitle: n.Product.Title, PriceCents: cents})
		}
		pi := page.ProductVariants.PageInfo
		if !pi.HasNextPage || pi.EndCursor == nil {
			return out, nil
		}
		cursor = pi.EndCursor
	}
}

// CreatedOrder identifies an order Shopify created.
type CreatedOrder struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// OrderCreate sends one orderCreate. A non-empty userErrors slice means
// Shopify rejected the order.
func (c *Client) OrderCreate(ctx context.Context, order *OrderInput) (*CreatedOrder, []UserError, error) {
	var out struct {
		OrderCreate struct {
			Order      *CreatedOrder `json:"order"`
			UserErrors []UserError   `json:"userErrors"`
		} `json:"orderCreate"`
	}
	vars := map[string]any{"order": order, "options": OrderCreateOptions}
	if err := c.Do(ctx, mOrderCreate, vars, &out); err != nil {
		return nil, nil, err
	}
	if len(out.OrderCreate.UserErrors) > 0 {
		return nil, out.OrderCreate.UserErrors, nil
	}
	if out.OrderCreate.Order == nil {
		return nil, []UserError{{Message: "orderCreate returned no order"}}, nil
	}
	return out.OrderCreate.Order, nil, nil
}

// OrderCancel cancels an order without notifying the customer or restocking.
func (c *Client) OrderCancel(ctx context.Context, orderID string, refund bool, reason string) error {
	var out struct {
		OrderCancel struct {
			UserErrors []UserError `json:"orderCancelUserErrors"`
		} `json:"orderCancel"`
	}
	vars := map[string]any{
		"orderId":        orderID,
		"refundMethod":   map[string]any{"originalPaymentMethodsRefund": refund},
		"restock":        false,
		"reason":         reason,
		"notifyCustomer": false,
	}
	if err := c.Do(ctx, mOrderCancel, vars, &out); err != nil {
		return err
	}
	if len(out.OrderCancel.UserErrors) > 0 {
		return fmt.Errorf("orderCancel: %s", FormatUserErrors(out.OrderCancel.UserErrors))
	}
	return nil
}

// TaggedOrder is an order found by tag search.
type TaggedOrder struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	CancelledAt *string  `json:"cancelledAt"`
	Tags        []string `json:"tags"`
}

// OrdersByTag returns one page of orders carrying tag.
func (c *Client) OrdersByTag(ctx context.Context, tag string, cursor *string) ([]TaggedOrder, PageInfo, error) {
	var out struct {
		Orders struct {
			Nodes    []TaggedOrder `json:"nodes"`
			PageInfo PageInfo      `json:"PageInfo"`
		} `json:"orders"`
	}
	vars := map[string]any{"query": "tag:'" + tag + "'", "cursor": cursor}
	if err := c.Do(ctx, qOrdersByTag, vars, &out); err != nil {
		return nil, PageInfo{}, err
	}
	return out.Orders.Nodes, out.Orders.PageInfo, nil
}

// FindOrderByTag returns the order carrying an exact tag, or nil.
func (c *Client) FindOrderByTag(ctx context.Context, tag string) (*TaggedOrder, error) {
	orders, _, err := c.OrdersByTag(ctx, tag, nil)
	if err != nil {
		return nil, err
	}
	for i := range orders {
		for _, t := range orders[i].Tags {
			if strings.EqualFold(t, tag) {
				return &orders[i], nil
			}
		}
	}
	return nil, nil
}

// OrderDelete permanently deletes an order.
func (c *Client) OrderDelete(ctx context.Context, id string) error {
	var out struct {
		OrderDelete struct {
			UserErrors []UserError `json:"userErrors"`
		} `json:"orderDelete"`
	}
	if err := c.Do(ctx, mOrderDelete, map[string]any{"orderId": id}, &out); err != nil {
		return err
	}
	if len(out.OrderDelete.UserErrors) > 0 {
		return fmt.Errorf("orderDelete: %s", FormatUserErrors(out.OrderDelete.UserErrors))
	}
	return nil
}

// TaggedCustomer is a customer found by tag search.
type TaggedCustomer struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

// CustomersByTag returns one page of customers carrying tag.
func (c *Client) CustomersByTag(ctx context.Context, tag string, cursor *string) ([]TaggedCustomer, PageInfo, error) {
	var out struct {
		Customers struct {
			Nodes    []TaggedCustomer `json:"nodes"`
			PageInfo PageInfo         `json:"PageInfo"`
		} `json:"customers"`
	}
	vars := map[string]any{"query": "tag:'" + tag + "'", "cursor": cursor}
	if err := c.Do(ctx, qCustomersByTag, vars, &out); err != nil {
		return nil, PageInfo{}, err
	}
	return out.Customers.Nodes, out.Customers.PageInfo, nil
}

// CustomerDelete deletes a customer. Customers with orders cannot be deleted.
func (c *Client) CustomerDelete(ctx context.Context, id string) error {
	var out struct {
		CustomerDelete struct {
			UserErrors []UserError `json:"userErrors"`
		} `json:"customerDelete"`
	}
	if err := c.Do(ctx, mCustomerDelete, map[string]any{"input": map[string]any{"id": id}}, &out); err != nil {
		return err
	}
	if len(out.CustomerDelete.UserErrors) > 0 {
		return fmt.Errorf("customerDelete: %s", FormatUserErrors(out.CustomerDelete.UserErrors))
	}
	return nil
}
