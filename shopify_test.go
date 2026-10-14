package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// scripted serves the given responses in order, one per request.
func scripted(t *testing.T, steps ...func(w http.ResponseWriter, r *http.Request)) (*Client, *int32, *[]time.Duration) {
	t.Helper()
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := atomic.AddInt32(&n, 1) - 1
		if r.Header.Get("X-Shopify-Access-Token") != "tok" {
			t.Errorf("missing access token header")
		}
		if int(i) >= len(steps) {
			t.Errorf("unexpected request %d", i+1)
			http.Error(w, "too many", 500)
			return
		}
		steps[i](w, r)
	}))
	t.Cleanup(srv.Close)
	var sleeps []time.Duration
	c := NewClient(srv.URL, "tok")
	c.Sleep = func(ctx context.Context, d time.Duration) error { sleeps = append(sleeps, d); return nil }
	return c, &n, &sleeps
}

func ok(w http.ResponseWriter, _ *http.Request) {
	w.Write([]byte(`{"data":{"shop":{"name":"S"}}}`))
}

func status(code int, body string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code); w.Write([]byte(body)) }
}

const query = `query { shop { name } }`
const mutation = `mutation M { orderCreate { order { id } } }`

func TestClient429RetryAfter(t *testing.T) {
	c, n, sleeps := scripted(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(429)
	}, ok)
	if err := c.Do(context.Background(), mutation, nil, nil); err != nil {
		t.Fatal(err)
	}
	if *n != 2 || len(*sleeps) != 1 || (*sleeps)[0] != 2*time.Second {
		t.Fatalf("requests=%d sleeps=%v", *n, *sleeps)
	}
}

func TestClient429Backoff(t *testing.T) {
	c, _, sleeps := scripted(t, status(429, ""), status(429, ""), ok)
	if err := c.Do(context.Background(), query, nil, nil); err != nil {
		t.Fatal(err)
	}
	if (*sleeps)[0] != time.Second || (*sleeps)[1] != 2*time.Second {
		t.Fatalf("backoff %v", *sleeps)
	}
}

func TestClientAuthFailsFast(t *testing.T) {
	c, n, _ := scripted(t, status(401, "nope"))
	err := c.Do(context.Background(), query, nil, nil)
	if !errors.Is(err, ErrAuth) || !strings.Contains(err.Error(), "write_orders") || *n != 1 {
		t.Fatalf("err=%v requests=%d", err, *n)
	}
}

func TestClientMutation5xxIsAmbiguous(t *testing.T) {
	for _, code := range []int{500, 502, 504} {
		c, n, _ := scripted(t, status(code, ""))
		if err := c.Do(context.Background(), mutation, nil, nil); !errors.Is(err, ErrAmbiguous) || *n != 1 {
			t.Fatalf("%d: err=%v requests=%d", code, err, *n)
		}
	}
}

func TestClientMutation503Retries(t *testing.T) {
	c, n, _ := scripted(t, status(503, ""), ok)
	if err := c.Do(context.Background(), mutation, nil, nil); err != nil || *n != 2 {
		t.Fatalf("err=%v requests=%d", err, *n)
	}
}

func TestClientQuery5xxRetries(t *testing.T) {
	c, n, _ := scripted(t, status(500, ""), status(502, ""), ok)
	if err := c.Do(context.Background(), query, nil, nil); err != nil || *n != 3 {
		t.Fatalf("err=%v requests=%d", err, *n)
	}
}

func TestClientOther4xxTruncated(t *testing.T) {
	c, _, _ := scripted(t, status(422, strings.Repeat("x", 2000)))
	err := c.Do(context.Background(), query, nil, nil)
	if err == nil || len(err.Error()) > 600 || !strings.Contains(err.Error(), "422") {
		t.Fatalf("err=%v", err)
	}
}

func TestClientGraphQLErrorsJoined(t *testing.T) {
	c, _, _ := scripted(t, status(200, `{"errors":[{"message":"a"},{"message":"b"}]}`))
	err := c.Do(context.Background(), query, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "a; b") {
		t.Fatalf("err=%v", err)
	}
}

func TestClientThrottledWait(t *testing.T) {
	c, _, sleeps := scripted(t,
		status(200, `{"errors":[{"message":"Throttled","extensions":{"code":"THROTTLED"}}],"extensions":{"cost":{"requestedQueryCost":102,"throttleStatus":{"currentlyAvailable":2,"restoreRate":50}}}}`),
		status(200, `{"errors":[{"message":"Throttled","extensions":{"code":"THROTTLED"}}],"extensions":{"cost":{"requestedQueryCost":10,"throttleStatus":{"currentlyAvailable":50,"restoreRate":100}}}}`),
		ok)
	if err := c.Do(context.Background(), query, nil, nil); err != nil {
		t.Fatal(err)
	}
	// (102-2)/50 = 2s, and max(10-50, 1)/100 = 10ms; each plus 250ms.
	want := []time.Duration{2250 * time.Millisecond, 260 * time.Millisecond}
	if len(*sleeps) != 2 || (*sleeps)[0] != want[0] || (*sleeps)[1] != want[1] {
		t.Fatalf("sleeps %v, want %v", *sleeps, want)
	}
}

func TestClientGivesUpAfterTenAttempts(t *testing.T) {
	steps := make([]func(http.ResponseWriter, *http.Request), 10)
	for i := range steps {
		steps[i] = status(503, "")
	}
	c, n, sleeps := scripted(t, steps...)
	err := c.Do(context.Background(), query, nil, nil)
	if err == nil || *n != 10 || len(*sleeps) != 9 || (*sleeps)[8] != 60*time.Second {
		t.Fatalf("err=%v requests=%d sleeps=%v", err, *n, *sleeps)
	}
}

func TestClientDecodesData(t *testing.T) {
	c, _, _ := scripted(t, ok)
	var out struct {
		Shop struct{ Name string } `json:"shop"`
	}
	if err := c.Do(context.Background(), query, nil, &out); err != nil || out.Shop.Name != "S" {
		t.Fatalf("err=%v out=%+v", err, out)
	}
}

func TestIsMutation(t *testing.T) {
	if !isMutation("\n  mutation X {}") || isMutation("query { mutationLog }") {
		t.Fatal("isMutation")
	}
}

func TestCLIExitCodes(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := realMain(nil, &out, &errOut); code != 2 {
		t.Fatalf("no args: %d", code)
	}
	if code := realMain([]string{"bogus"}, &out, &errOut); code != 2 {
		t.Fatalf("unknown command: %d", code)
	}
	if code := realMain([]string{"plan", "-nope"}, &out, &errOut); code != 2 {
		t.Fatalf("bad flag: %d", code)
	}
	if code := realMain([]string{"plan", "-profile", "profile.example.json", "-opm", "0"}, &out, &errOut); code != 2 {
		t.Fatalf("-opm 0: %d", code)
	}
	if code := realMain([]string{"purge", "-endpoint", "http://127.0.0.1:1"}, &out, &errOut); code != 2 {
		t.Fatalf("purge without -run/-all: %d", code)
	}
	if code := realMain([]string{"plan", "-profile", filepath.Join(t.TempDir(), "missing.json")}, &out, &errOut); code != 1 {
		t.Fatalf("missing profile: %d", code)
	}
	if code := realMain([]string{"version"}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "shopgen") {
		t.Fatalf("version: %d %q", code, out.String())
	}
}

func TestCLIPlanWritesSummaryAndJSONL(t *testing.T) {
	t.Setenv("SHOPIFY_STORE", "")
	var out, errOut bytes.Buffer
	path := filepath.Join(t.TempDir(), "plan.jsonl")
	if code := realMain([]string{"plan", "-profile", "profile.example.json", "-out", path}, &out, &errOut); code != 0 {
		t.Fatalf("plan: %d\n%s", code, errOut.String())
	}
	if !strings.HasPrefix(out.String(), "window      2026-07-15 → 2026-10-13 (91 days, America/Chicago)") {
		t.Fatalf("summary:\n%s", out.String())
	}
	b, err := os.ReadFile(path)
	if err != nil || bytes.Count(b, []byte("\n")) < 5000 {
		t.Fatalf("plan.jsonl: %v, %d lines", err, bytes.Count(b, []byte("\n")))
	}
}
