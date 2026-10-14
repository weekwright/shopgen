package shopify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// TokenSource supplies the Admin API access token for each request.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// Refresher is a TokenSource that can mint a new token, e.g. after a 401.
type Refresher interface {
	TokenSource
	Refresh(ctx context.Context) error
}

// StaticToken is a fixed access token, such as a legacy custom app's shpat_….
type StaticToken string

// Token returns the token.
func (t StaticToken) Token(context.Context) (string, error) { return string(t), nil }

// ClientCredentials exchanges a Dev Dashboard app's client ID and secret for
// short-lived Admin API tokens using the client credentials grant, and
// refreshes them before they expire. It works when the app and the store
// belong to the same organization.
//
// See https://shopify.dev/docs/apps/build/authentication-authorization/access-tokens/client-credentials-grant
type ClientCredentials struct {
	// TokenURL is https://<store>/admin/oauth/access_token; see TokenURL.
	TokenURL     string
	ClientID     string
	ClientSecret string
	HTTP         *http.Client
	// Now is the clock; nil means time.Now.
	Now func() time.Time

	mu      sync.Mutex
	token   string
	expires time.Time
	scope   string
}

// refreshMargin renews a token this long before it expires.
const refreshMargin = 5 * time.Minute

// TokenURL returns the OAuth token endpoint for a store domain.
func TokenURL(store string) string {
	return "https://" + ShopDomain(store) + "/admin/oauth/access_token"
}

func (c *ClientCredentials) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Token returns a valid token, fetching or renewing it as needed.
func (c *ClientCredentials) Token(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && c.now().Before(c.expires.Add(-refreshMargin)) {
		return c.token, nil
	}
	if err := c.fetch(ctx); err != nil {
		return "", err
	}
	return c.token, nil
}

// Refresh discards the current token and fetches a new one.
func (c *ClientCredentials) Refresh(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.fetch(ctx)
}

// Expiry reports when the current token expires (zero before the first fetch).
func (c *ClientCredentials) Expiry() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.expires
}

// Scope returns the scopes granted with the current token, comma separated.
func (c *ClientCredentials) Scope() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.scope
}

// fetch requests a token. Callers hold c.mu.
func (c *ClientCredentials) fetch(ctx context.Context) error {
	if c.ClientID == "" || c.ClientSecret == "" {
		return errors.New("client ID and secret are required for the client credentials grant")
	}
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {c.ClientID},
		"client_secret": {c.ClientSecret},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", UserAgent)
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var body struct {
		AccessToken      string `json:"access_token"`
		Scope            string `json:"scope"`
		ExpiresIn        int64  `json:"expires_in"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	_ = json.Unmarshal(raw, &body)
	if resp.StatusCode != http.StatusOK || body.AccessToken == "" {
		msg := body.ErrorDescription
		if msg == "" {
			msg = body.Error
		}
		if msg == "" {
			msg = truncate(string(raw), 200)
		}
		if strings.Contains(body.Error, "shop_not_permitted") {
			msg += " (the app and the store must belong to the same organization in the Dev Dashboard)"
		}
		return fmt.Errorf("token request: HTTP %d: %s", resp.StatusCode, msg)
	}
	c.token, c.scope = body.AccessToken, body.Scope
	if body.ExpiresIn <= 0 {
		body.ExpiresIn = 86399
	}
	c.expires = c.now().Add(time.Duration(body.ExpiresIn) * time.Second)
	return nil
}
