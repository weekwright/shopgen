package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PurgeConfig holds purge's inputs.
type PurgeConfig struct {
	Store       string
	Endpoint    string
	APIVersion  string
	Token       string
	Run         string
	All         bool
	Yes         bool
	StateDir    string
	AllowNonDev bool

	UI    *UI
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
	ui := cfg.UI
	if ui == nil {
		ui = NewUI(os.Stderr)
	}
	if cfg.Run == "" && !cfg.All || cfg.Run != "" && cfg.All {
		return res, usageErrorf("purge needs exactly one of -run <id> or -all")
	}
	if cfg.StateDir == "" {
		cfg.StateDir = defaultStateDir
	}
	tag, scope := tagShopgen, "every shopgen run"
	store := NormalizeStore(cfg.Store)
	if cfg.Run != "" {
		if !runIDPattern.MatchString(cfg.Run) {
			return res, usageErrorf("-run: %q is not a run ID (want YYYYMMDD-xxxxxx)", cfg.Run)
		}
		tag, scope = runTag(cfg.Run), "run "+cfg.Run
		// Default the store to the one the run wrote to.
		if store == "" && cfg.Endpoint == "" {
			if st, err := RunDir(filepath.Join(cfg.StateDir, cfg.Run)).LoadState(); err == nil {
				store = st.Store
			}
		}
	}

	client, err := newClient(store, cfg.Endpoint, cfg.APIVersion, cfg.Token, cfg.Sleep, ui)
	if err != nil {
		return res, err
	}
	shop, err := safetyGate(ctx, client, ui, cfg.AllowNonDev)
	if err != nil {
		return res, err
	}

	if !cfg.Yes {
		in := cfg.In
		if in == nil {
			in = os.Stdin
		}
		fmt.Fprintf(ui.w, "%s Permanently delete orders and customers from %s in %s? Type 'delete' to confirm: ",
			ui.Yellow("?"), ui.Bold(scope), ui.Bold(shop.Domain))
		line, _ := bufio.NewReader(in).ReadString('\n')
		if strings.TrimSpace(line) != "delete" {
			return res, fmt.Errorf("aborted; nothing was deleted")
		}
	}

	ui.Infof("Deleting orders tagged %s", ui.Bold(tag))
	if err := purgeOrders(ctx, client, ui, tag, &res); err != nil {
		return res, err
	}
	ui.Infof("Deleting customers tagged %s", ui.Bold(tag))
	if err := purgeCustomers(ctx, client, ui, tag, &res); err != nil {
		return res, err
	}

	ui.Successf("Orders: %s deleted, %s failed · Customers: %s deleted, %s kept",
		ui.Green(fmt.Sprint(res.OrdersDeleted)), failedCount(ui, res.OrdersFailed),
		ui.Green(fmt.Sprint(res.CustomersDeleted)), ui.Dim(fmt.Sprint(res.CustomersKept)))
	if res.OrdersFailed > 0 || res.CustomersKept > 0 {
		ui.Logf("%s", ui.Dim("Search indexing can lag a little; run purge again in a minute to catch stragglers."))
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

func (w *pageWalker) next(ids []string, pi pageInfo) (more bool) {
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

func purgeOrders(ctx context.Context, c *Client, ui *UI, tag string, res *PurgeResult) error {
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
				ui.Warnf("could not delete %s: %v", o.Name, err)
				continue
			}
			res.OrdersDeleted++
			if res.OrdersDeleted%25 == 0 {
				ui.Logf("%d orders deleted…", res.OrdersDeleted)
			}
		}
		if !more {
			return nil
		}
	}
}

func purgeCustomers(ctx context.Context, c *Client, ui *UI, tag string, res *PurgeResult) error {
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
				ui.Logf("%d customers deleted…", res.CustomersDeleted)
			}
		}
		if !more {
			return nil
		}
	}
}
