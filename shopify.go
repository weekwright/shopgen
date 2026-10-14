package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ErrAmbiguous means a mutation may or may not have been committed: the
// request was sent but no trustworthy reply came back. Never blindly retry.
var ErrAmbiguous = errors.New("no reliable reply from Shopify after sending")

// ErrAuth is returned for 401 and 403 responses.
var ErrAuth = errors.New("unauthorized")

const maxAttempts = 10

// Client talks to the Shopify Admin GraphQL API.
type Client struct {
	Endpoint string
	Token    string
	HTTP     *http.Client
	// Sleep waits between retries; it must honor ctx. Tests replace it.
	Sleep func(ctx context.Context, d time.Duration) error
	// Notify, if set, is told about retries worth surfacing (throttling).
	Notify func(msg string)
}

// NewClient returns a client for endpoint with a 60 s HTTP timeout.
func NewClient(endpoint, token string) *Client {
	return &Client{
		Endpoint: endpoint,
		Token:    token,
		HTTP:     &http.Client{Timeout: 60 * time.Second},
		Sleep:    sleepCtx,
	}
}

// AdminEndpoint builds the GraphQL URL for a store domain.
func AdminEndpoint(store, version string) string {
	return "https://" + store + "/admin/api/" + version + "/graphql.json"
}

// NormalizeStore turns "my-shop" or "https://my-shop.myshopify.com/" into
// "my-shop.myshopify.com".
func NormalizeStore(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.TrimPrefix(strings.TrimPrefix(s, "https://"), "http://")
	s = strings.TrimSuffix(s, "/")
	if s != "" && !strings.Contains(s, ".") {
		s += ".myshopify.com"
	}
	return s
}

type gqlError struct {
	Message    string `json:"message"`
	Extensions struct {
		Code string `json:"code"`
	} `json:"extensions"`
}

type gqlResponse struct {
	Data       json.RawMessage `json:"data"`
	Errors     []gqlError      `json:"errors"`
	Extensions struct {
		Cost struct {
			RequestedQueryCost float64 `json:"requestedQueryCost"`
			ThrottleStatus     struct {
				MaximumAvailable   float64 `json:"maximumAvailable"`
				CurrentlyAvailable float64 `json:"currentlyAvailable"`
				RestoreRate        float64 `json:"restoreRate"`
			} `json:"throttleStatus"`
		} `json:"cost"`
	} `json:"extensions"`
}

func isMutation(q string) bool {
	return strings.HasPrefix(strings.TrimSpace(q), "mutation")
}

func backoff(attempt int) time.Duration {
	d := time.Second * time.Duration(math.Pow(2, float64(attempt-1)))
	if d > 60*time.Second || d <= 0 {
		d = 60 * time.Second
	}
	return d
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// Do runs one GraphQL operation and decodes its data into out. It retries
// transient failures and throttling, and returns ErrAmbiguous when a mutation
// may have been committed without a reply.
func (c *Client) Do(ctx context.Context, query string, vars map[string]any, out any) error {
	mutation := isMutation(query)
	body, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	sleep := c.Sleep
	if sleep == nil {
		sleep = sleepCtx
	}
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		wait, err := c.attempt(ctx, mutation, body, attempt, out)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil && !errors.Is(err, ErrAmbiguous) {
			return ctx.Err()
		}
		if wait < 0 {
			return err
		}
		lastErr = err
		if attempt < maxAttempts {
			if err := sleep(ctx, wait); err != nil {
				return err
			}
		}
	}
	return fmt.Errorf("gave up after %d attempts: %w", maxAttempts, lastErr)
}

// attempt makes one request. It returns (wait >= 0, err) for a retryable
// failure and (-1, err) for a final one.
func (c *Client) attempt(ctx context.Context, mutation bool, body []byte, attempt int, out any) (time.Duration, error) {
	var wrote bool
	trace := &httptrace.ClientTrace{WroteRequest: func(i httptrace.WroteRequestInfo) {
		if i.Err == nil {
			wrote = true
		}
	}}
	req, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), http.MethodPost, c.Endpoint, bytes.NewReader(body))
	if err != nil {
		return -1, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "shopgen/"+version)
	if c.Token != "" {
		req.Header.Set("X-Shopify-Access-Token", c.Token)
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		if mutation && wrote {
			reason := err
			var ue *url.Error
			if errors.As(err, &ue) {
				reason = ue.Err
			}
			return -1, fmt.Errorf("%w (%v)", ErrAmbiguous, reason)
		}
		return backoff(attempt), fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(resp.Body)

	switch code := resp.StatusCode; {
	case code == http.StatusTooManyRequests:
		wait := backoff(attempt)
		if s, err := strconv.ParseFloat(resp.Header.Get("Retry-After"), 64); err == nil && s >= 0 {
			wait = time.Duration(s * float64(time.Second))
		}
		return wait, errors.New("HTTP 429 Too Many Requests")
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return -1, fmt.Errorf("%w: HTTP %d from Shopify; check SHOPIFY_ACCESS_TOKEN and that the app has the read_products, write_orders and write_customers scopes", ErrAuth, code)
	case code == http.StatusServiceUnavailable:
		return backoff(attempt), fmt.Errorf("HTTP %d", code)
	case code >= 500:
		if mutation && (code == 500 || code == 502 || code == 504) {
			return -1, fmt.Errorf("%w (HTTP %d)", ErrAmbiguous, code)
		}
		return backoff(attempt), fmt.Errorf("HTTP %d", code)
	case code >= 400:
		return -1, fmt.Errorf("HTTP %d: %s", code, truncate(string(raw), 500))
	}
	if readErr != nil {
		if mutation {
			return -1, fmt.Errorf("%w (reading response: %v)", ErrAmbiguous, readErr)
		}
		return backoff(attempt), fmt.Errorf("reading response: %w", readErr)
	}

	var gr gqlResponse
	if err := json.Unmarshal(raw, &gr); err != nil {
		if mutation {
			return -1, fmt.Errorf("%w (undecodable response: %s)", ErrAmbiguous, truncate(string(raw), 200))
		}
		return backoff(attempt), fmt.Errorf("decode response: %w", err)
	}
	if len(gr.Errors) > 0 {
		for _, e := range gr.Errors {
			if e.Extensions.Code == "THROTTLED" {
				ts := gr.Extensions.Cost.ThrottleStatus
				wait := backoff(attempt)
				if ts.RestoreRate > 0 {
					need := math.Max(gr.Extensions.Cost.RequestedQueryCost-ts.CurrentlyAvailable, 1)
					wait = time.Duration(need/ts.RestoreRate*float64(time.Second)) + 250*time.Millisecond
				}
				if c.Notify != nil {
					c.Notify(fmt.Sprintf("throttled by Shopify; retrying in %s", formatDuration(wait)))
				}
				return wait, errors.New("THROTTLED")
			}
		}
		msgs := make([]string, len(gr.Errors))
		for i, e := range gr.Errors {
			msgs[i] = e.Message
		}
		return -1, fmt.Errorf("GraphQL: %s", strings.Join(msgs, "; "))
	}
	if out != nil && len(gr.Data) > 0 {
		if err := json.Unmarshal(gr.Data, out); err != nil {
			return -1, fmt.Errorf("decode data: %w", err)
		}
	}
	return 0, nil
}
