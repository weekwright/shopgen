package shopifytest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Server is an in-memory Shopify Admin API for tests. GraphQL requests are
// dispatched by pattern-matching the query text; it also serves the REST
// access-scopes endpoint and the OAuth client credentials grant.
//
// Configure the exported fields before the first request.
type Server struct {
	Name string // shop name; the domain is derived from it
	Dev  bool   // shop.plan.partnerDevelopment
	// ThrottleOn returns THROTTLED on this orderCreate call (1-based).
	ThrottleOn int
	// DropOn commits this orderCreate call, then drops the connection.
	DropOn int
	// RejectCreates answers every orderCreate with a non-retryable userError.
	RejectCreates bool
	// Scopes are returned by /admin/oauth/access_scopes.json.
	Scopes []string
	// ClientID and ClientSecret enable the client credentials grant; when
	// set, GraphQL requests must carry a token it issued.
	ClientID, ClientSecret string

	mu        sync.Mutex
	variants  []map[string]any
	orders    []*Order
	customers []*Customer
	nextID    int

	creates int // orderCreate calls received
	cancels map[string]int
	tokens  map[string]bool
	issued  int
	srv     *httptest.Server
}

// Order is an order the fake stores.
type Order struct {
	ID, Name    string
	Tags        []string
	Cancelled   bool
	Financial   string
	Fulfillment string
	CustomerID  string
	Input       map[string]any
}

// Customer is a customer the fake stores.
type Customer struct {
	ID, Email string
	Tags      []string
}

// New starts a fake dev store with 12 variants and the required scopes. It
// is closed when the test ends.
func New(t testing.TB) *Server {
	f := &Server{Name: "Fake Dev Store", Dev: true, Scopes: []string{"read_products", "write_orders", "write_customers"},
		cancels: map[string]int{}, tokens: map[string]bool{}, nextID: 1}
	for i := 0; i < 12; i++ {
		f.variants = append(f.variants, map[string]any{
			"id":      fmt.Sprintf("gid://shopify/ProductVariant/%d", 9000+i*7),
			"title":   []string{"Small", "Large", "Default Title"}[i%3],
			"price":   fmt.Sprintf("%d.%02d", 12+i*3, (i*37)%100),
			"product": map[string]any{"title": fmt.Sprintf("Product %d", i/3)},
		})
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

// URL is the GraphQL endpoint.
func (f *Server) URL() string { return f.srv.URL + "/admin/api/2026-10/graphql.json" }

// BaseURL is the server root, for the REST and OAuth endpoints.
func (f *Server) BaseURL() string { return f.srv.URL }

// ExpireTokens invalidates every issued access token.
func (f *Server) ExpireTokens() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokens = map[string]bool{}
}

// TokensIssued counts client credentials grants served.
func (f *Server) TokensIssued() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.issued
}

func (f *Server) id(kind string) string {
	f.nextID++
	return fmt.Sprintf("gid://shopify/%s/%d", kind, f.nextID)
}

func reply(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}

func userErr(msg string) []map[string]any {
	return []map[string]any{{"field": []string{"order"}, "message": msg}}
}

// HasTag reports whether tags contains tag, ignoring case as Shopify does.
func HasTag(tags []string, tag string) bool {
	for _, t := range tags {
		if strings.EqualFold(t, tag) {
			return true
		}
	}
	return false
}

func tagFromQuery(q string) string {
	q = strings.TrimPrefix(q, "tag:'")
	return strings.TrimSuffix(q, "'")
}

func page[T any](items []T, cursor any, size int) ([]T, map[string]any) {
	start := 0
	if c, ok := cursor.(string); ok {
		start, _ = strconv.Atoi(c)
	}
	if start > len(items) {
		start = len(items)
	}
	end := start + size
	if end > len(items) {
		end = len(items)
	}
	pi := map[string]any{"hasNextPage": end < len(items), "endCursor": strconv.Itoa(end)}
	return items[start:end], pi
}

func (f *Server) serve(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/admin/oauth/access_token":
		f.serveToken(w, r)
		return
	case "/admin/oauth/access_scopes.json":
		f.mu.Lock()
		scopes := make([]map[string]string, len(f.Scopes))
		for i, sc := range f.Scopes {
			scopes[i] = map[string]string{"handle": sc}
		}
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_scopes": scopes})
		return
	}
	if f.ClientID != "" {
		f.mu.Lock()
		ok := f.tokens[r.Header.Get("X-Shopify-Access-Token")]
		f.mu.Unlock()
		if !ok {
			http.Error(w, `{"errors":"[API] Invalid API key or access token (unrecognized login or wrong password)"}`, http.StatusUnauthorized)
			return
		}
	}
	if r.Header.Get("Content-Type") != "application/json" {
		http.Error(w, "bad content type", 400)
		return
	}
	var req struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	q, v := req.Query, req.Variables
	f.mu.Lock()
	defer f.mu.Unlock()

	switch {
	case strings.Contains(q, "orderCreate("):
		f.creates++
		n := f.creates
		if n == f.ThrottleOn {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"errors":[{"message":"Throttled","extensions":{"code":"THROTTLED"}}],"extensions":{"cost":{"requestedQueryCost":10,"throttleStatus":{"maximumAvailable":1000,"currentlyAvailable":5,"restoreRate":50}}}}`)
			return
		}
		if f.RejectCreates {
			reply(w, map[string]any{"orderCreate": map[string]any{"order": nil, "userErrors": userErr("Variant is not available")}})
			return
		}
		order, _ := v["order"].(map[string]any)
		created, errs := f.createOrder(order)
		if n == f.DropOn && created != nil {
			hj, ok := w.(http.Hijacker)
			if !ok {
				panic("no hijacker")
			}
			conn, _, _ := hj.Hijack()
			conn.Close()
			return
		}
		payload := map[string]any{"order": nil, "userErrors": errs}
		if created != nil {
			payload["order"] = map[string]any{"id": created.ID, "name": created.Name}
			payload["userErrors"] = []any{}
		}
		reply(w, map[string]any{"orderCreate": payload})

	case strings.Contains(q, "orderCancel("):
		o := f.findOrder(v["orderId"])
		if o == nil {
			reply(w, map[string]any{"orderCancel": map[string]any{"orderCancelUserErrors": userErr("not found")}})
			return
		}
		o.Cancelled = true
		f.cancels[o.ID]++
		reply(w, map[string]any{"orderCancel": map[string]any{"job": map[string]any{"id": "gid://shopify/Job/1"}, "orderCancelUserErrors": []any{}}})

	case strings.Contains(q, "orderDelete("):
		o := f.findOrder(v["orderId"])
		switch {
		case o == nil:
			reply(w, map[string]any{"orderDelete": map[string]any{"deletedId": nil, "userErrors": userErr("not found")}})
		case !o.Cancelled && o.Financial == "PAID" && o.Fulfillment != "FULFILLED":
			// Mimic Shopify refusing to delete open, paid orders.
			reply(w, map[string]any{"orderDelete": map[string]any{"deletedId": nil, "userErrors": userErr("Order cannot be deleted")}})
		default:
			f.removeOrder(o.ID)
			reply(w, map[string]any{"orderDelete": map[string]any{"deletedId": o.ID, "userErrors": []any{}}})
		}

	case strings.Contains(q, "customerDelete("):
		in, _ := v["input"].(map[string]any)
		id, _ := in["id"].(string)
		for _, o := range f.orders {
			if o.CustomerID == id {
				reply(w, map[string]any{"customerDelete": map[string]any{"userErrors": userErr("Customer has orders")}})
				return
			}
		}
		for i, c := range f.customers {
			if c.ID == id {
				f.customers = append(f.customers[:i], f.customers[i+1:]...)
				break
			}
		}
		reply(w, map[string]any{"customerDelete": map[string]any{"deletedCustomerId": id, "userErrors": []any{}}})

	case strings.Contains(q, "productVariants("):
		// Small pages so the client's cursor paging is exercised.
		nodes, pi := page(f.variants, v["cursor"], 5)
		reply(w, map[string]any{"productVariants": map[string]any{"nodes": nodes, "pageInfo": pi}})

	case strings.Contains(q, "orders("):
		tag := tagFromQuery(v["query"].(string))
		var match []map[string]any
		for _, o := range f.orders {
			if HasTag(o.Tags, tag) {
				var cancelledAt any
				if o.Cancelled {
					cancelledAt = "2026-10-14T00:00:00Z"
				}
				match = append(match, map[string]any{"id": o.ID, "name": o.Name, "cancelledAt": cancelledAt, "tags": o.Tags})
			}
		}
		nodes, pi := page(match, v["cursor"], 50)
		reply(w, map[string]any{"orders": map[string]any{"nodes": nodes, "pageInfo": pi}})

	case strings.Contains(q, "customers("):
		tag := tagFromQuery(v["query"].(string))
		var match []map[string]any
		for _, c := range f.customers {
			if HasTag(c.Tags, tag) {
				match = append(match, map[string]any{"id": c.ID, "email": c.Email})
			}
		}
		nodes, pi := page(match, v["cursor"], 50)
		reply(w, map[string]any{"customers": map[string]any{"nodes": nodes, "pageInfo": pi}})

	case strings.Contains(q, "shop {"):
		reply(w, map[string]any{"shop": map[string]any{
			"name": f.Name, "myshopifyDomain": strings.ToLower(strings.ReplaceAll(f.Name, " ", "-")) + ".myshopify.com", "currencyCode": "USD",
			"plan": map[string]any{"partnerDevelopment": f.Dev},
		}})

	default:
		http.Error(w, "fake: unhandled query: "+q, 400)
	}
}

func (f *Server) serveToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil || r.PostForm.Get("grant_type") != "client_credentials" {
		http.Error(w, `{"error":"unsupported_grant_type"}`, http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.PostForm.Get("client_id") != f.ClientID || r.PostForm.Get("client_secret") != f.ClientSecret || f.ClientID == "" {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"invalid_client","error_description":"Client authentication failed"}`)
		return
	}
	f.issued++
	tok := fmt.Sprintf("shpat_fake_%d", f.issued)
	f.tokens[tok] = true
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"access_token": tok, "scope": strings.Join(f.Scopes, ","), "expires_in": 86399})
}

func (f *Server) createOrder(in map[string]any) (*Order, []map[string]any) {
	if in == nil {
		return nil, userErr("order is required")
	}
	if s, _ := in["processedAt"].(string); s == "" {
		return nil, userErr("processedAt is required")
	}
	if items, _ := in["lineItems"].([]any); len(items) == 0 {
		return nil, userErr("lineItems is required")
	}
	rawTags, _ := in["tags"].([]any)
	if len(rawTags) == 0 {
		return nil, userErr("tags are required")
	}
	cust, _ := in["customer"].(map[string]any)
	up, _ := cust["toUpsert"].(map[string]any)
	email, _ := up["email"].(string)
	if email == "" {
		return nil, userErr("customer is required")
	}
	var custTags []string
	if ts, ok := up["tags"].([]any); ok {
		for _, t := range ts {
			custTags = append(custTags, t.(string))
		}
	}
	var c *Customer
	for _, existing := range f.customers {
		if existing.Email == email {
			c = existing
		}
	}
	if c == nil {
		c = &Customer{ID: f.id("Customer"), Email: email}
		f.customers = append(f.customers, c)
	}
	for _, t := range custTags {
		if !HasTag(c.Tags, t) {
			c.Tags = append(c.Tags, t)
		}
	}
	o := &Order{ID: f.id("Order"), CustomerID: c.ID, Input: in}
	o.Name = fmt.Sprintf("#%d", 1000+len(f.orders)+1)
	for _, t := range rawTags {
		o.Tags = append(o.Tags, t.(string))
	}
	o.Financial, _ = in["financialStatus"].(string)
	o.Fulfillment, _ = in["fulfillmentStatus"].(string)
	f.orders = append(f.orders, o)
	return o, nil
}

func (f *Server) findOrder(id any) *Order {
	for _, o := range f.orders {
		if o.ID == id {
			return o
		}
	}
	return nil
}

func (f *Server) removeOrder(id string) {
	for i, o := range f.orders {
		if o.ID == id {
			f.orders = append(f.orders[:i], f.orders[i+1:]...)
			return
		}
	}
}

// Snapshot returns copies of the store's state, safe to inspect.
func (f *Server) Snapshot() (orders []Order, customers []Customer, cancels map[string]int, creates int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, o := range f.orders {
		orders = append(orders, *o)
	}
	for _, c := range f.customers {
		customers = append(customers, *c)
	}
	cancels = map[string]int{}
	for k, v := range f.cancels {
		cancels[k] = v
	}
	return orders, customers, cancels, f.creates
}
