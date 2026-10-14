package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// runNow is the fixed clock for end-to-end tests.
var runNow = time.Date(2026, 10, 13, 18, 0, 0, 0, time.UTC)

const e2eProfile = `{
  "seed": 3,
  "start": "2026-10-10",
  "end": "2026-10-12",
  "timezone": "UTC",
  "orders_per_day": 5,
  "noise": 0,
  "tax_rate": 0.07,
  "customers": {"pool": 6, "repeat_rate": 0.3},
  "outcomes": {"fulfilled": 3, "cancelled": 2, "refunded": 1, "pending_payment": 1, "unfulfilled": 1},
  "archive_fulfilled": true
}`

type sleepRecorder struct {
	mu     sync.Mutex
	sleeps []time.Duration
	hook   func(n int)
}

func (s *sleepRecorder) Sleep(ctx context.Context, d time.Duration) error {
	s.mu.Lock()
	s.sleeps = append(s.sleeps, d)
	n := len(s.sleeps)
	hook := s.hook
	s.mu.Unlock()
	if hook != nil {
		hook(n)
	}
	return ctx.Err()
}

type harness struct {
	t      *testing.T
	fake   *fakeShop
	dir    string
	prof   string
	log    *bytes.Buffer
	sleeps *sleepRecorder
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	prof := filepath.Join(dir, "profile.json")
	if err := os.WriteFile(prof, []byte(e2eProfile), 0o644); err != nil {
		t.Fatal(err)
	}
	return &harness{t: t, fake: newFakeShop(t), dir: dir, prof: prof, log: &bytes.Buffer{}, sleeps: &sleepRecorder{}}
}

func (h *harness) config() RunConfig {
	return RunConfig{
		Endpoint:    h.fake.URL(),
		APIVersion:  defaultAPIVersion,
		ProfilePath: h.prof,
		OPM:         5,
		StateDir:    filepath.Join(h.dir, "state"),
		UI:          NewUI(h.log),
		Now:         func() time.Time { return runNow },
		Sleep:       h.sleeps.Sleep,
	}
}

func (h *harness) runIDs() []string {
	entries, _ := os.ReadDir(filepath.Join(h.dir, "state"))
	var ids []string
	for _, e := range entries {
		if e.IsDir() {
			ids = append(ids, e.Name())
		}
	}
	return ids
}

func (h *harness) state(id string) *State {
	st, err := RunDir(filepath.Join(h.dir, "state", id)).LoadState()
	if err != nil {
		h.t.Fatal(err)
	}
	return st
}

// assertNoDuplicates checks every sgseq tag appears at most once and returns
// the set of indices seen.
func assertNoDuplicates(t *testing.T, orders []fakeOrder) map[string]bool {
	t.Helper()
	seen := map[string]bool{}
	for _, o := range orders {
		for _, tag := range o.Tags {
			if strings.HasPrefix(tag, "sgseq-") {
				if seen[tag] {
					t.Fatalf("duplicate order for %s", tag)
				}
				seen[tag] = true
			}
		}
	}
	return seen
}

func TestRunLimitResumeAndPurge(t *testing.T) {
	h := newHarness(t)
	h.fake.throttleOn = 2

	cfg := h.config()
	cfg.Limit = 5
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("run: %v\n%s", err, h.log)
	}
	orders, _, _, creates := h.fake.snapshot()
	if len(orders) != 5 {
		t.Fatalf("want 5 orders after -limit 5, got %d\n%s", len(orders), h.log)
	}
	if creates != 6 {
		t.Fatalf("want 6 create calls (one throttled), got %d", creates)
	}
	// The throttle wait is (10 requested - 5 available) / 50 per second + 250ms.
	found := false
	for _, d := range h.sleeps.sleeps {
		if d == 350*time.Millisecond {
			found = true
		}
	}
	if !found {
		t.Fatalf("no 350ms throttle wait in %v", h.sleeps.sleeps)
	}
	ids := h.runIDs()
	if len(ids) != 1 {
		t.Fatalf("want one run dir, got %v", ids)
	}
	id := ids[0]
	if st := h.state(id); st.Done != 5 || st.Created != 5 || st.Inflight != nil {
		t.Fatalf("state after limit: %+v", st)
	}

	// Resume to completion.
	cfg = h.config()
	cfg.Resume = id
	cfg.ProfilePath = "does-not-exist.json" // ignored with -resume
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("resume: %v\n%s", err, h.log)
	}
	st := h.state(id)
	orders, customers, cancels, _ := h.fake.snapshot()
	if st.Done != st.Total || st.Created != st.Total || len(orders) != st.Total {
		t.Fatalf("done=%d created=%d total=%d store=%d", st.Done, st.Created, st.Total, len(orders))
	}
	seen := assertNoDuplicates(t, orders)
	if len(seen) != st.Total {
		t.Fatalf("%d sgseq tags for %d orders", len(seen), st.Total)
	}

	cancelled := 0
	for _, o := range orders {
		if hasTag(o.Tags, "shopgen-cancelled") {
			cancelled++
			if cancels[o.ID] != 1 {
				t.Fatalf("order %s tagged cancelled got %d cancels", o.Name, cancels[o.ID])
			}
		} else if cancels[o.ID] != 0 {
			t.Fatalf("order %s was cancelled but not planned to be", o.Name)
		}
	}
	if cancelled == 0 {
		t.Fatal("test profile produced no cancelled orders")
	}
	if len(customers) == 0 || len(customers) > 6 {
		t.Fatalf("customers upserted by email: got %d, want 1..6", len(customers))
	}
	if b, _ := os.ReadFile(filepath.Join(h.dir, "state", id, "orders.jsonl")); bytes.Count(b, []byte("\n")) != st.Total {
		t.Fatalf("orders.jsonl has %d lines, want %d", bytes.Count(b, []byte("\n")), st.Total)
	}

	// Purge the run.
	res, err := Purge(context.Background(), PurgeConfig{
		Endpoint: h.fake.URL(), Run: id, Yes: true, StateDir: filepath.Join(h.dir, "state"),
		UI: NewUI(h.log), Sleep: h.sleeps.Sleep,
	})
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	orders, customers, _, _ = h.fake.snapshot()
	if len(orders) != 0 {
		t.Fatalf("%d orders left after purge", len(orders))
	}
	for _, c := range customers {
		if hasTag(c.Tags, tagShopgen) {
			t.Fatalf("tagged customer %s left after purge", c.Email)
		}
	}
	if res.OrdersDeleted != st.Total || res.OrdersFailed != 0 {
		t.Fatalf("purge result %+v", res)
	}
}

func TestRunDroppedConnectionNoDuplicate(t *testing.T) {
	h := newHarness(t)
	h.fake.dropOn = 3
	cfg := h.config()
	cfg.Limit = 6
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("run: %v\n%s", err, h.log)
	}
	orders, _, _, _ := h.fake.snapshot()
	if len(orders) != 6 {
		t.Fatalf("want exactly 6 orders, got %d\n%s", len(orders), h.log)
	}
	seen := assertNoDuplicates(t, orders)
	id := h.runIDs()[0]
	for i := 0; i < 6; i++ {
		if !seen[seqTag(id, i)] {
			t.Fatalf("gap: no order for index %d", i)
		}
	}
	if st := h.state(id); st.Done != 6 || st.Created != 6 || st.Failed != 0 {
		t.Fatalf("state %+v", st)
	}
	if !strings.Contains(h.log.String(), "checking whether it was created") {
		t.Fatalf("ambiguous path not logged:\n%s", h.log)
	}
}

func TestRunRefusesNonDevStore(t *testing.T) {
	h := newHarness(t)
	h.fake.dev = false
	err := Run(context.Background(), h.config())
	if err == nil || !strings.Contains(err.Error(), "not a development store") {
		t.Fatalf("want refusal, got %v", err)
	}
	if _, _, _, creates := h.fake.snapshot(); creates != 0 {
		t.Fatalf("%d creates on a non-dev store", creates)
	}
	if ids := h.runIDs(); len(ids) != 0 {
		t.Fatalf("run dir created on refusal: %v", ids)
	}
}

func TestRunAllowNonDev(t *testing.T) {
	h := newHarness(t)
	h.fake.dev = false
	cfg := h.config()
	cfg.AllowNonDev = true
	cfg.Limit = 1
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if orders, _, _, _ := h.fake.snapshot(); len(orders) != 1 {
		t.Fatalf("want 1 order, got %d", len(orders))
	}
}

func TestRunInterruptAndResume(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Pacing sleeps happen before every order but the first; cancel during
	// the third one, i.e. after two orders.
	h.sleeps.hook = func(n int) {
		if n == 2 {
			cancel()
		}
	}
	err := Run(ctx, h.config())
	if !errors.Is(err, ErrInterrupted) {
		t.Fatalf("want ErrInterrupted, got %v", err)
	}
	id := h.runIDs()[0]
	if st := h.state(id); st.Done != 2 || st.Inflight != nil {
		t.Fatalf("state after pause %+v", st)
	}
	if !strings.Contains(h.log.String(), "shopgen run -resume "+id) {
		t.Fatalf("resume command not printed:\n%s", h.log)
	}

	h.sleeps.hook = nil
	cfg := h.config()
	cfg.Resume = id
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	orders, _, _, _ := h.fake.snapshot()
	st := h.state(id)
	if len(orders) != st.Total || len(assertNoDuplicates(t, orders)) != st.Total {
		t.Fatalf("after resume: %d orders, total %d", len(orders), st.Total)
	}
}

func TestResumeReconcilesInflightOrder(t *testing.T) {
	h := newHarness(t)
	cfg := h.config()
	cfg.Limit = 2
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	id := h.runIDs()[0]

	// Simulate a crash right after order index 2 was committed but before
	// state.json recorded it.
	dir := RunDir(filepath.Join(h.dir, "state", id))
	st := h.state(id)
	idx := st.Done
	st.Inflight = &idx
	st.UpdatedAt = runNow.Add(-time.Hour)
	if err := dir.SaveState(st); err != nil {
		t.Fatal(err)
	}
	p, _, _ := LoadProfile(h.prof)
	var cat []Variant
	if err := readJSON(dir.path("catalog.json"), &cat); err != nil {
		t.Fatal(err)
	}
	plan, _ := Generate(p, cat, st.GenNow)
	in := BuildOrder(id, p, cat, plan.Orders[idx])
	client := NewClient(h.fake.URL(), "")
	if _, uerrs, err := client.OrderCreate(context.Background(), in); err != nil || len(uerrs) > 0 {
		t.Fatalf("seed order: %v %v", err, uerrs)
	}

	cfg = h.config()
	cfg.Resume = id
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	orders, _, _, _ := h.fake.snapshot()
	final := h.state(id)
	if len(orders) != final.Total || len(assertNoDuplicates(t, orders)) != final.Total {
		t.Fatalf("%d orders for total %d", len(orders), final.Total)
	}
	if !strings.Contains(h.log.String(), "already exists") {
		t.Fatalf("reconcile not logged:\n%s", h.log)
	}
}

func TestResumeRejectsTamperedProfile(t *testing.T) {
	h := newHarness(t)
	cfg := h.config()
	cfg.Limit = 1
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	id := h.runIDs()[0]
	path := filepath.Join(h.dir, "state", id, "profile.json")
	b, _ := os.ReadFile(path)
	_ = os.WriteFile(path, bytes.Replace(b, []byte(`"seed": 3`), []byte(`"seed": 4`), 1), 0o644)
	cfg = h.config()
	cfg.Resume = id
	if err := Run(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "modified") {
		t.Fatalf("want tamper refusal, got %v", err)
	}
}

func TestRunFailFast(t *testing.T) {
	h := newHarness(t)
	h.fake.rejectCreates = true

	err := Run(context.Background(), h.config())
	if err == nil || !strings.Contains(err.Error(), "first 3 creates failed") {
		t.Fatalf("want fail-fast, got %v\n%s", err, h.log)
	}
	st := h.state(h.runIDs()[0])
	if st.Done != 0 || st.Failed != 0 {
		t.Fatalf("fail-fast should roll back so orders are retried: %+v", st)
	}
}
