package shopgen

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/weekwright/shopgen/internal/shopify"
	"github.com/weekwright/shopgen/internal/ui"
)

// PurgeConfig holds purge's inputs.
type PurgeConfig struct {
	Conn
	Run         string
	All         bool
	Yes         bool
	StateDir    string
	AllowNonDev bool

	UI    *ui.UI
	In    io.Reader // confirmation input; defaults to os.Stdin
	Sleep func(context.Context, time.Duration) error
}

// PurgeResult counts what purge did.
type PurgeResult struct {
	OrdersDeleted, OrdersFailed, CustomersDeleted, CustomersKept int
}

// Purge deletes orders and customers carrying a shopgen tag. It never
// touches anything untagged.
func Purge(ctx context.Context, cfg PurgeConfig) (PurgeResult, error) {
	var res PurgeResult
	u := cfg.UI
	if u == nil {
		u = ui.New(os.Stderr)
	}
	if cfg.Run == "" && !cfg.All || cfg.Run != "" && cfg.All {
		return res, UsageErrorf("purge needs exactly one of --run <id> or --all")
	}
	if cfg.StateDir == "" {
		cfg.StateDir = DefaultStateDir
	}
	tag, scope := TagShopgen, "every shopgen run"
	store := shopify.ShopDomain(cfg.Store)
	if cfg.Run != "" {
		if !runIDPattern.MatchString(cfg.Run) {
			return res, UsageErrorf("--run: %q is not a run ID (want YYYYMMDD-xxxxxx)", cfg.Run)
		}
		tag, scope = RunTag(cfg.Run), "run "+cfg.Run
		// Default the store to the one the run wrote to.
		if store == "" && cfg.Endpoint == "" {
			if st, err := RunDir(filepath.Join(cfg.StateDir, cfg.Run)).LoadState(); err == nil {
				store = st.Store
			}
		}
	}

	conn := cfg.Conn
	conn.Store = store
	client, err := conn.Client(cfg.Sleep, u)
	if err != nil {
		return res, err
	}
	shop, err := safetyGate(ctx, client, u, cfg.AllowNonDev)
	if err != nil {
		return res, err
	}

	if !cfg.Yes {
		in := cfg.In
		if in == nil {
			in = os.Stdin
		}
		u.Prompt(fmt.Sprintf("Permanently delete orders and customers from %s in %s? Type 'delete' to confirm:", u.Bold(scope), u.Bold(shop.Domain)))
		line, _ := bufio.NewReader(in).ReadString('\n')
		if strings.TrimSpace(line) != "delete" {
			return res, fmt.Errorf("aborted; nothing was deleted")
		}
	}

	u.Infof("Deleting orders tagged %s", u.Bold(tag))
	if err := purgeOrders(ctx, client, u, tag, &res); err != nil {
		return res, err
	}
	u.Infof("Deleting customers tagged %s", u.Bold(tag))
	if err := purgeCustomers(ctx, client, u, tag, &res); err != nil {
		return res, err
	}

	u.Successf("Orders: %s deleted, %s failed · Customers: %s deleted, %s kept",
		u.Green(fmt.Sprint(res.OrdersDeleted)), failedCount(u, res.OrdersFailed),
		u.Green(fmt.Sprint(res.CustomersDeleted)), u.Dim(fmt.Sprint(res.CustomersKept)))
	if res.OrdersFailed > 0 || res.CustomersKept > 0 {
		u.Logf("%s", u.Dim("Search indexing can lag a little; run purge again in a minute to catch stragglers."))
	}
	return res, nil
}

// pageWalker pages through a tag search while the result set shrinks under
// it: after a page with new items it restarts from the top, and after a page
// of only already-seen items it moves on, stopping at the end.
type pageWalker struct {
	cursor *string
	seen   map[string]bool
}

func (w *pageWalker) next(ids []string, pi shopify.PageInfo) (more bool) {
	fresh := false
	for _, id := range ids {
		if !w.seen[id] {
			fresh = true
		}
	}
	if fresh {
		w.cursor = nil
		return true
	}
	if pi.HasNextPage && pi.EndCursor != nil {
		w.cursor = pi.EndCursor
		return true
	}
	return false
}

func purgeOrders(ctx context.Context, c *shopify.Client, u *ui.UI, tag string, res *PurgeResult) error {
	w := &pageWalker{seen: map[string]bool{}}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		orders, pi, err := c.OrdersByTag(ctx, tag, w.cursor)
		if err != nil {
			return fmt.Errorf("list orders: %w", err)
		}
		ids := make([]string, len(orders))
		for i, o := range orders {
			ids[i] = o.ID
		}
		more := w.next(ids, pi)
		for _, o := range orders {
			if w.seen[o.ID] {
				continue
			}
			w.seen[o.ID] = true
			err := c.OrderDelete(ctx, o.ID)
			if err != nil && o.CancelledAt == nil {
				if cerr := c.OrderCancel(ctx, o.ID, false, "OTHER"); cerr == nil {
					err = c.OrderDelete(ctx, o.ID)
				}
			}
			if err != nil {
				res.OrdersFailed++
				u.Warnf("could not delete %s: %v", o.Name, err)
				continue
			}
			res.OrdersDeleted++
			if res.OrdersDeleted%25 == 0 {
				u.Logf("%d orders deleted…", res.OrdersDeleted)
			}
		}
		if !more {
			return nil
		}
	}
}

func purgeCustomers(ctx context.Context, c *shopify.Client, u *ui.UI, tag string, res *PurgeResult) error {
	w := &pageWalker{seen: map[string]bool{}}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		customers, pi, err := c.CustomersByTag(ctx, tag, w.cursor)
		if err != nil {
			return fmt.Errorf("list customers: %w", err)
		}
		ids := make([]string, len(customers))
		for i, cu := range customers {
			ids[i] = cu.ID
		}
		more := w.next(ids, pi)
		for _, cu := range customers {
			if w.seen[cu.ID] {
				continue
			}
			w.seen[cu.ID] = true
			if err := c.CustomerDelete(ctx, cu.ID); err != nil {
				res.CustomersKept++
				continue
			}
			res.CustomersDeleted++
			if res.CustomersDeleted%25 == 0 {
				u.Logf("%d customers deleted…", res.CustomersDeleted)
			}
		}
		if !more {
			return nil
		}
	}
}
