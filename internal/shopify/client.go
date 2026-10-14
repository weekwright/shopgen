// Package shopify is a small Admin GraphQL client with the retry and
// idempotency rules shopgen depends on, plus helpers for Shopify-specific
// chores: store domains, GIDs, money, access tokens and access scopes.
package shopify

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

// UserAgent is sent with every request.
var UserAgent = "shopgen"

const maxAttempts = 10

// Client talks to the Shopify Admin GraphQL API.
type Client struct {
	Endpoint string
	Tokens   TokenSource
	HTTP     *http.Client
	// Sleep waits between retries; it must honor ctx. Tests replace it.
	Sleep func(ctx context.Context, d time.Duration) error
	// Notify, if set, is told about retries worth surfacing (throttling).
	Notify func(msg string)
}

// NewClient returns a client for a GraphQL endpoint with a 60 s HTTP timeout.
// A nil tokens source sends no access token (for tests against a fake).
func NewClient(endpoint string, tokens TokenSource) *Client {
	return &Client{
		Endpoint: endpoint,
		Tokens:   tokens,
		HTTP:     &http.Client{Timeout: 60 * time.Second},
		Sleep:    sleep,
	}
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
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

// IsMutation reports whether a GraphQL document starts with "mutation".
func IsMutation(q string) bool {
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

// errRefreshed marks a 401 after which the token source issued a new token;
// the request is retried immediately, once.
var errRefreshed = errors.New("access token refreshed")

// Do runs one GraphQL operation and decodes its data into out. It retries
// transient failures and throttling, and returns ErrAmbiguous when a mutation
// may have been committed without a reply.
func (c *Client) Do(ctx context.Context, query string, vars map[string]any, out any) error {
	mutation := IsMutation(query)
	body, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	wait := c.Sleep
	if wait == nil {
		wait = sleep
	}
	refreshed := false
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		d, err := c.attempt(ctx, mutation, body, attempt, !refreshed, out)
		if errors.Is(err, errRefreshed) {
			refreshed = true
			attempt--
			continue
		}
		if err == nil {
			return nil
		}
		if ctx.Err() != nil && !errors.Is(err, ErrAmbiguous) {
			return ctx.Err()
		}
		if d < 0 {
			return err
		}
		lastErr = err
		if attempt < maxAttempts {
			if err := wait(ctx, d); err != nil {
				return err
			}
		}
	}
	return fmt.Errorf("gave up after %d attempts: %w", maxAttempts, lastErr)
}

// attempt makes one request. It returns (wait >= 0, err) for a retryable
// failure and (-1, err) for a final one.
func (c *Client) attempt(ctx context.Context, mutation bool, body []byte, attempt int, mayRefresh bool, out any) (time.Duration, error) {
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
	req.Header.Set("User-Agent", UserAgent)
	if c.Tokens != nil {
		tok, err := c.Tokens.Token(ctx)
		if err != nil {
			return -1, fmt.Errorf("%w: %v", ErrAuth, err)
		}
		req.Header.Set("X-Shopify-Access-Token", tok)
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
	case code == http.StatusUnauthorized && mayRefresh && c.refresh(ctx):
		// An expired token is rejected before anything runs, so even a
		// mutation is safe to resend.
		return 0, errRefreshed
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return -1, fmt.Errorf("%w: HTTP %d from Shopify; check the access token and that the app has the %s scopes", ErrAuth, code, strings.Join(RequiredScopes, ", "))
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
					c.Notify(fmt.Sprintf("throttled by Shopify; retrying in %.1fs", wait.Seconds()))
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

// refresh asks a refreshable token source for a new token.
func (c *Client) refresh(ctx context.Context) bool {
	r, ok := c.Tokens.(Refresher)
	if !ok {
		return false
	}
	if err := r.Refresh(ctx); err != nil {
		if c.Notify != nil {
			c.Notify("could not refresh the access token: " + err.Error())
		}
		return false
	}
	if c.Notify != nil {
		c.Notify("access token expired; fetched a fresh one")
	}
	return true
}
