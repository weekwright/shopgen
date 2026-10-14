package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Waits used around ambiguous and rate-limited creates. Package vars so tests
// can shorten them.
var (
	AmbiguousWait = 15 * time.Second
	RateLimitWait = 60 * time.Second
)

const (
	maxCreateAttempts = 6
	failFastAttempts  = 3
	cancelTimeout     = 2 * time.Minute
	defaultStateDir   = ".shopgen"
)

// ErrInterrupted is returned when the user pauses a run with Ctrl-C.
var ErrInterrupted = errors.New("interrupted")

// RunConfig holds everything run needs. Zero values are not defaults; main
// fills them from flags.
type RunConfig struct {
	Store       string
	Endpoint    string
	APIVersion  string
	Token       string
	ProfilePath string
	OPM         float64
	Limit       int
	Resume      string
	StateDir    string
	AllowNonDev bool
	SkipCancels bool

	UI    *UI
	Now   func() time.Time
	Sleep func(context.Context, time.Duration) error
	// Abort, when done, abandons the in-flight request. The ctx passed to Run
	// only stops the loop between orders.
	Abort context.Context
}

// runInterval is the gap between create starts: 60s/opm plus 2% headroom.
func runInterval(opm float64) time.Duration {
	return time.Duration(float64(time.Minute) / opm * 1.02)
}

type runner struct {
	cfg     RunConfig
	ui      *UI
	client  *Client
	dir     RunDir
	st      *State
	profile *Profile
	catalog []Variant
	plan    *Plan
	shop    ShopInfo
}

func (r *runner) sleep(ctx context.Context, d time.Duration) error {
	if r.cfg.Sleep != nil {
		return r.cfg.Sleep(ctx, d)
	}
	return sleepCtx(ctx, d)
}

func (r *runner) now() time.Time {
	if r.cfg.Now != nil {
		return r.cfg.Now()
	}
	return time.Now()
}

// newClient builds a client for a store or an -endpoint override.
func newClient(store, endpoint, apiVersion, token string, sleep func(context.Context, time.Duration) error, ui *UI) (*Client, error) {
	if endpoint == "" {
		if store == "" {
			return nil, usageErrorf("no store: pass -store or set SHOPIFY_STORE")
		}
		if token == "" {
			return nil, errors.New("SHOPIFY_ACCESS_TOKEN is not set; create a custom app in the store admin with read_products, write_orders and write_customers, and export its Admin API token")
		}
		endpoint = AdminEndpoint(store, apiVersion)
	}
	c := NewClient(endpoint, token)
	if sleep != nil {
		c.Sleep = sleep
	}
	if ui != nil {
		c.Notify = func(msg string) { ui.Warnf("%s", msg) }
	}
	return c, nil
}

// safetyGate refuses to write to anything but a development store unless
// explicitly allowed.
func safetyGate(ctx context.Context, c *Client, ui *UI, allowNonDev bool) (ShopInfo, error) {
	shop, err := c.Shop(ctx)
	if err != nil {
		return shop, fmt.Errorf("connect to store: %w", err)
	}
	if !shop.PartnerDevelopment {
		if !allowNonDev {
			return shop, fmt.Errorf("%s (%s) is not a development store; refusing to write. Pass -allow-non-dev if you really mean it", shop.Name, shop.Domain)
		}
		ui.Warnf("%s is %s a development store; continuing because of -allow-non-dev", ui.Bold(shop.Name), ui.Bold("not"))
	} else {
		ui.Infof("Connected to %s %s %s", ui.Bold(shop.Name), ui.Dim("("+shop.Domain+")"), ui.Green("· dev store ✓"))
	}
	return shop, nil
}

// Run creates (or resumes creating) a run's orders.
func Run(ctx context.Context, cfg RunConfig) error {
	if !(cfg.OPM > 0) {
		return usageErrorf("-opm must be > 0")
	}
	if cfg.StateDir == "" {
		cfg.StateDir = defaultStateDir
	}
	r := &runner{cfg: cfg, ui: cfg.UI}
	if r.ui == nil {
		r.ui = NewUI(os.Stderr)
	}

	// Load what we can before touching the network, so bad input fails fast.
	var profileBytes []byte
	store := NormalizeStore(cfg.Store)
	if cfg.Resume != "" {
		if !runIDPattern.MatchString(cfg.Resume) {
			return usageErrorf("-resume: %q is not a run ID (want YYYYMMDD-xxxxxx)", cfg.Resume)
		}
		r.dir = RunDir(filepath.Join(cfg.StateDir, cfg.Resume))
		st, err := r.dir.LoadState()
		if err != nil {
			return err
		}
		r.st = st
		if store != "" && cfg.Endpoint == "" && store != st.Store {
			return fmt.Errorf("run %s belongs to %s, not %s", st.RunID, st.Store, store)
		}
		if cfg.Endpoint == "" {
			store = st.Store
		}
		b, err := os.ReadFile(r.dir.path("profile.json"))
		if err != nil {
			return fmt.Errorf("read run profile: %w", err)
		}
		if profileHash(b) != st.ProfileHash {
			return fmt.Errorf("%s was modified after the run started; refusing to resume", r.dir.path("profile.json"))
		}
		if r.profile, err = ParseProfile(b); err != nil {
			return fmt.Errorf("run profile: %w", err)
		}
		if err := readJSON(r.dir.path("catalog.json"), &r.catalog); err != nil {
			return fmt.Errorf("read run catalog: %w", err)
		}
	} else {
		p, b, err := LoadProfile(cfg.ProfilePath)
		if err != nil {
			return err
		}
		r.profile, profileBytes = p, b
	}

	client, err := newClient(store, cfg.Endpoint, cfg.APIVersion, cfg.Token, cfg.Sleep, r.ui)
	if err != nil {
		return err
	}
	r.client = client
	if r.shop, err = safetyGate(ctx, client, r.ui, cfg.AllowNonDev); err != nil {
		return err
	}
	if r.shop.Currency != "" && r.shop.Currency != r.profile.Currency {
		r.ui.Warnf("profile currency %s differs from the shop currency %s; Shopify may reject or convert amounts", r.profile.Currency, r.shop.Currency)
	}

	if r.st == nil {
		if err := r.startNew(ctx, store, profileBytes); err != nil {
			return err
		}
	} else {
		if r.plan, err = Generate(r.profile, r.catalog, r.st.GenNow); err != nil {
			return err
		}
		if len(r.plan.Orders) != r.st.Total {
			return fmt.Errorf("regenerated plan has %d orders but run %s expects %d; refusing to resume", len(r.plan.Orders), r.st.RunID, r.st.Total)
		}
		r.ui.Infof("Resuming run %s %s", r.ui.Bold(r.st.RunID), r.ui.Dim(fmt.Sprintf("(%d of %d done)", r.st.Done, r.st.Total)))
	}

	interval := runInterval(cfg.OPM)
	remaining := r.st.Total - r.st.Done
	r.ui.Logf("%s orders remaining at %.1f/min · eta %s", r.ui.Bold(fmt.Sprint(remaining)), cfg.OPM, r.ui.Bold(formatDuration(interval*time.Duration(remaining))))
	r.ui.Hint("pause anytime with Ctrl-C, resume with", r.resumeCommand())

	if err := r.reconcileInflight(ctx); err != nil {
		return err
	}
	return r.loop(ctx, interval)
}

func (r *runner) startNew(ctx context.Context, store string, profileBytes []byte) error {
	r.ui.Logf("Fetching catalog…")
	cat, err := r.client.FetchCatalog(ctx)
	if err != nil {
		return err
	}
	r.catalog = cat
	now := r.now().UTC().Truncate(time.Second)
	r.plan, err = Generate(r.profile, cat, now)
	if err != nil {
		return err
	}
	if len(r.plan.Orders) == 0 {
		return errors.New("the plan has no orders: check start, end and orders_per_day in the profile")
	}
	if store == "" {
		store = r.cfg.Endpoint
	}
	r.st = &State{
		RunID:       newRunID(now),
		Store:       store,
		ProfileHash: profileHash(profileBytes),
		GenNow:      now,
		Total:       len(r.plan.Orders),
		StartedAt:   now,
		UpdatedAt:   now,
	}
	r.dir = RunDir(filepath.Join(r.cfg.StateDir, r.st.RunID))
	if err := os.MkdirAll(string(r.dir), 0o755); err != nil {
		return fmt.Errorf("create run dir: %w", err)
	}
	if err := writeAtomic(r.dir.path("profile.json"), profileBytes); err != nil {
		return err
	}
	if err := writeJSONAtomic(r.dir.path("catalog.json"), cat); err != nil {
		return err
	}
	if err := r.dir.SaveState(r.st); err != nil {
		return err
	}
	r.ui.Infof("Started run %s · %d orders from %d variants %s", r.ui.Bold(r.st.RunID), r.st.Total, len(cat), r.ui.Dim("→ "+string(r.dir)))
	return nil
}

func (r *runner) resumeCommand() string {
	id := ""
	if r.st != nil {
		id = r.st.RunID
	}
	cmd := "shopgen run -resume " + id
	if r.cfg.StateDir != defaultStateDir {
		cmd += " -state-dir " + shellQuote(r.cfg.StateDir)
	}
	return cmd
}

func (r *runner) purgeCommand() string {
	cmd := "shopgen purge -run " + r.st.RunID
	if r.cfg.StateDir != defaultStateDir {
		cmd += " -state-dir " + shellQuote(r.cfg.StateDir)
	}
	return cmd
}

func shellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t'\"$\\`") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// inflightContext survives a first Ctrl-C (so the in-flight order finishes)
// but is cancelled by Abort (a second Ctrl-C).
func (r *runner) inflightContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	c, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	if r.cfg.Abort == nil {
		return c, cancel
	}
	stop := context.AfterFunc(r.cfg.Abort, cancel)
	return c, func() { stop(); cancel() }
}

func (r *runner) save() error {
	r.st.UpdatedAt = r.now().UTC()
	return r.dir.SaveState(r.st)
}

// reconcileInflight settles an order whose create was started but never
// recorded (a crash or an abandoned request) by looking it up by tag.
func (r *runner) reconcileInflight(ctx context.Context) error {
	if r.st.Inflight == nil {
		return nil
	}
	idx := *r.st.Inflight
	if idx != r.st.Done {
		r.st.Inflight = nil
		return r.save()
	}
	r.ui.Logf("Checking whether order %d was created before the last stop…", idx+1)
	if wait := AmbiguousWait - r.now().Sub(r.st.UpdatedAt); wait > 0 {
		if err := r.sleep(ctx, wait); err != nil {
			return ErrInterrupted
		}
	}
	found, err := r.client.FindOrderByTag(ctx, seqTag(r.st.RunID, idx))
	if err != nil {
		return fmt.Errorf("look up order %d: %w", idx+1, err)
	}
	if found != nil {
		o := r.plan.Orders[idx]
		r.ui.Successf("Order %d already exists as %s; not creating it again", idx+1, found.Name)
		if !r.dir.HasOrder(idx) {
			if err := r.dir.AppendOrder(OrderRecord{Index: idx, ID: found.ID, Name: found.Name, Outcome: o.Outcome, ProcessedAt: o.ProcessedAt}); err != nil {
				return err
			}
		}
		if o.Outcome == OutcomeCancelled && !r.cfg.SkipCancels && found.CancelledAt == nil {
			r.cancel(ctx, idx, found.ID)
		}
		r.st.Created++
		r.st.Done++
	}
	r.st.Inflight = nil
	return r.save()
}

// fatalError stops the run without recording the order as failed.
type fatalError struct {
	err          error
	keepInflight bool
}

func (e *fatalError) Error() string { return e.err.Error() }
func (e *fatalError) Unwrap() error { return e.err }

func isRateLimitMessage(msg string) bool {
	m := strings.ToLower(msg)
	return strings.Contains(m, "limit") || strings.Contains(m, "too many") || strings.Contains(m, "throttl")
}

// create runs orderCreate with idempotent retries: an ambiguous outcome is
// resolved by looking the order up by its unique sgseq tag before retrying.
func (r *runner) create(ctx context.Context, o PlannedOrder) (*CreatedOrder, error) {
	input := BuildOrder(r.st.RunID, r.profile, r.catalog, o)
	tag := seqTag(r.st.RunID, o.Index)
	var lastErr error
	for attempt := 1; attempt <= maxCreateAttempts; attempt++ {
		created, userErrs, err := r.client.OrderCreate(ctx, input)
		switch {
		case errors.Is(err, ErrAmbiguous):
			r.ui.Warnf("order %d: %v; checking whether it was created…", o.Index+1, err)
			if err := r.sleep(ctx, AmbiguousWait); err != nil {
				return nil, &fatalError{err: err, keepInflight: true}
			}
			found, ferr := r.client.FindOrderByTag(ctx, tag)
			if ferr != nil {
				return nil, &fatalError{err: fmt.Errorf("could not check whether order %d was created: %w", o.Index+1, ferr), keepInflight: true}
			}
			if found != nil {
				return &CreatedOrder{ID: found.ID, Name: found.Name}, nil
			}
			lastErr = err
		case err != nil:
			if ctx.Err() != nil {
				return nil, &fatalError{err: ctx.Err(), keepInflight: true}
			}
			if errors.Is(err, ErrAuth) {
				return nil, &fatalError{err: err}
			}
			return nil, err
		case len(userErrs) > 0:
			msg := formatUserErrors(userErrs)
			if !isRateLimitMessage(msg) {
				return nil, errors.New(msg)
			}
			r.ui.Warnf("order %d: rate limited (%s); waiting %s", o.Index+1, msg, formatDuration(RateLimitWait))
			if err := r.sleep(ctx, RateLimitWait); err != nil {
				return nil, &fatalError{err: err}
			}
			lastErr = errors.New(msg)
		default:
			return created, nil
		}
	}
	return nil, fmt.Errorf("gave up after %d attempts: %w", maxCreateAttempts, lastErr)
}

// cancel runs orderCancel with its own timeout so a Ctrl-C between create
// and cancel never leaves a planned-cancelled order open.
func (r *runner) cancel(ctx context.Context, idx int, orderID string) {
	cctx, done := r.inflightContext(ctx, cancelTimeout)
	defer done()
	if err := r.client.OrderCancel(cctx, orderID, true, "CUSTOMER"); err != nil {
		r.ui.Warnf("order %d: cancel failed: %v", idx+1, err)
		_ = r.dir.AppendFailure(FailureRecord{Index: idx, Error: "cancel: " + err.Error(), Time: r.now().UTC(), OrderID: orderID})
	}
}

func (r *runner) loop(ctx context.Context, interval time.Duration) error {
	sessionStart := r.st.Done
	var sessionCreated, sessionFailed, sessionAttempts int
	var lastStart time.Time
	counts := map[string]int{}
	width := len(fmt.Sprint(r.st.Total))
	pace := func(d time.Duration) error {
		if r.cfg.Sleep != nil {
			return r.cfg.Sleep(ctx, d)
		}
		return r.ui.Wait(ctx, d, "next order in")
	}

	for r.st.Done < r.st.Total {
		if r.cfg.Limit > 0 && sessionCreated >= r.cfg.Limit {
			r.ui.Infof("Reached -limit %d", r.cfg.Limit)
			r.ui.Hint("continue with", r.resumeCommand())
			return nil
		}
		if ctx.Err() != nil {
			return r.paused()
		}
		if !lastStart.IsZero() {
			if wait := lastStart.Add(interval).Sub(r.now()); wait > 0 {
				if err := pace(wait); err != nil {
					return r.paused()
				}
			}
		}
		lastStart = r.now()

		o := r.plan.Orders[r.st.Done]
		idx := o.Index
		r.st.Inflight = &idx
		if err := r.save(); err != nil {
			return err
		}
		cctx, done := r.inflightContext(ctx, 30*time.Minute)
		created, err := r.create(cctx, o)
		done()
		sessionAttempts++

		var fatal *fatalError
		if errors.As(err, &fatal) {
			if !fatal.keepInflight {
				r.st.Inflight = nil
			}
			_ = r.save()
			if r.cfg.Abort != nil && r.cfg.Abort.Err() != nil {
				r.ui.Warnf("Abandoned order %d mid-request; resume will check whether it was created", idx+1)
				r.ui.Hint("resume with", r.resumeCommand())
				return ErrInterrupted
			}
			r.ui.Errorf("order %d: %v", idx+1, fatal.err)
			r.ui.Hint("fix the cause, then resume with", r.resumeCommand())
			return fatal.err
		}

		local := o.ProcessedAt.In(r.plan.Location).Format("2006-01-02 15:04")
		prefix := r.ui.Dim(fmt.Sprintf("[%*d/%d]", width, idx+1, r.st.Total))
		if err != nil {
			sessionFailed++
			r.st.Failed++
			_ = r.dir.AppendFailure(FailureRecord{Index: idx, Error: err.Error(), Time: r.now().UTC()})
			r.ui.Errorf("%s %s %s failed: %v", prefix, local, o.Outcome, err)
		} else {
			sessionCreated++
			r.st.Created++
			counts[o.Outcome]++
			if err := r.dir.AppendOrder(OrderRecord{Index: idx, ID: created.ID, Name: created.Name, Outcome: o.Outcome, ProcessedAt: o.ProcessedAt}); err != nil {
				return err
			}
			if o.Outcome == OutcomeCancelled && !r.cfg.SkipCancels {
				r.cancel(ctx, idx, created.ID)
			}
		}
		r.st.Done++
		r.st.Inflight = nil
		if err := r.save(); err != nil {
			return err
		}
		if created != nil {
			eta := interval * time.Duration(r.st.Total-r.st.Done)
			r.ui.line(r.ui.Green("✓"), fmt.Sprintf("%s %s %s %s %s %s",
				prefix, r.ui.Bold(padRight(created.Name, 7)), r.ui.Dim(local), padRight(r.ui.Outcome(o.Outcome), 19),
				padRight(formatMoney(o.TotalCents, r.profile.Currency), 10), r.ui.Dim("eta "+formatDuration(eta))))
		}

		if sessionCreated == 0 && sessionFailed == failFastAttempts && sessionAttempts == failFastAttempts {
			// Roll back so the failed orders are retried on resume.
			r.st.Done = sessionStart
			r.st.Failed -= failFastAttempts
			_ = r.save()
			r.ui.Errorf("The first %d orders all failed; stopping so nothing is skipped", failFastAttempts)
			r.ui.Hint("fix the cause (see failures.jsonl), then resume with", r.resumeCommand())
			return fmt.Errorf("first %d creates failed; last error: %v", failFastAttempts, err)
		}
	}

	r.ui.Successf("Run %s complete: %s created, %s failed", r.ui.Bold(r.st.RunID), r.ui.Green(fmt.Sprint(r.st.Created)), failedCount(r.ui, r.st.Failed))
	if len(counts) > 0 {
		r.ui.Logf("this session: %s", outcomeBreakdown(counts, r.ui.Style))
	}
	if r.shop.Domain != "" {
		r.ui.Hint("see them at", "https://admin.shopify.com/store/"+strings.TrimSuffix(r.shop.Domain, ".myshopify.com")+"/orders")
	}
	r.ui.Hint("remove everything this run created with", r.purgeCommand())
	return nil
}

func failedCount(ui *UI, n int) string {
	if n == 0 {
		return ui.Dim("0")
	}
	return ui.Red(fmt.Sprint(n))
}

func (r *runner) paused() error {
	_ = r.save()
	r.ui.Warnf("Paused at %d/%d (%d created, %d failed)", r.st.Done, r.st.Total, r.st.Created, r.st.Failed)
	r.ui.Hint("resume with", r.resumeCommand())
	return ErrInterrupted
}
