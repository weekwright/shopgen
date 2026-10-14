// Command shopgen backfills months of realistic, reproducible order history
// into a Shopify development store.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// version is set at build time with -ldflags "-X main.version=v1.2.3".
var version = "dev"

const defaultAPIVersion = "2026-10"

// usageError marks bad flags or arguments (exit code 2).
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

func usageErrorf(format string, a ...any) error { return &usageError{fmt.Sprintf(format, a...)} }

func main() {
	os.Exit(realMain(os.Args[1:], os.Stdout, os.Stderr))
}

func realMain(args []string, stdout, stderr io.Writer) int {
	ui := NewUI(stderr)
	if len(args) == 0 {
		printUsage(stderr, styleFor(stderr))
		return 2
	}
	cmd, rest := args[0], args[1:]

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	abort, abortNow := context.WithCancel(context.Background())
	defer abortNow()
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigs)
	go func() {
		select {
		case <-sigs:
		case <-ctx.Done():
			return
		}
		if cmd == "run" {
			ui.Warnf("Stopping after the in-flight order… %s", ui.Dim("(Ctrl-C again to abandon it)"))
		}
		stop()
		select {
		case <-sigs:
			abortNow()
		case <-abort.Done():
		}
	}()

	var err error
	switch cmd {
	case "plan":
		err = cmdPlan(ctx, rest, stdout, ui)
	case "run":
		err = cmdRun(ctx, abort, rest, ui)
	case "purge":
		err = cmdPurge(ctx, rest, ui)
	case "version", "-version", "--version":
		fmt.Fprintf(stdout, "shopgen %s\n", version)
		return 0
	case "help", "-h", "-help", "--help":
		printUsage(stdout, styleFor(stdout))
		return 0
	default:
		ui.Errorf("unknown command %q", cmd)
		printUsage(stderr, styleFor(stderr))
		return 2
	}

	var ue *usageError
	switch {
	case err == nil:
		return 0
	case errors.Is(err, flag.ErrHelp):
		return 0
	case errors.As(err, &ue):
		ui.Errorf("%v", err)
		fmt.Fprintf(stderr, "  Run %s for usage.\n", ui.Bold("shopgen "+cmd+" -h"))
		return 2
	case errors.Is(err, ErrInterrupted), errors.Is(err, context.Canceled):
		return 130
	default:
		ui.Errorf("%v", err)
		return 1
	}
}

func printUsage(w io.Writer, st Style) {
	fmt.Fprintf(w, `%s %s
%s

%s
  shopgen %s  [flags]   Preview the history a profile generates (no writes)
  shopgen %s   [flags]   Create those orders in a dev store, resumably
  shopgen %s [flags]   Delete everything shopgen created
  shopgen version

%s
  SHOPIFY_STORE          Default for -store (e.g. my-dev-store)
  SHOPIFY_ACCESS_TOKEN   Admin API access token (only read from the environment)

%s
  shopgen plan -profile profile.example.json
  shopgen run -limit 3                     # smoke test: create 3 orders
  shopgen run                              # the whole history, ~5 orders/min
  shopgen run -resume 20261014-a1b2c3      # pick up where you left off
  shopgen purge -run 20261014-a1b2c3

Run %s for a command's flags. Docs: https://github.com/weekwright/shopgen
`,
		st.Brand(st.Bold("shopgen")), st.Dim(version),
		"Backfill months of realistic, reproducible order history into a Shopify dev store.",
		st.Bold("Usage"), st.Brand("plan"), st.Brand("run"), st.Brand("purge"),
		st.Bold("Environment"), st.Bold("Examples"), st.Bold("shopgen <command> -h"))
}

// commonFlags are shared by every command.
type commonFlags struct {
	store, apiVersion, endpoint, stateDir string
}

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	return fs
}

func addCommon(fs *flag.FlagSet, c *commonFlags, withStateDir bool) {
	fs.StringVar(&c.store, "store", os.Getenv("SHOPIFY_STORE"), "Store domain; a bare name gets .myshopify.com (default $SHOPIFY_STORE)")
	fs.StringVar(&c.apiVersion, "api-version", defaultAPIVersion, "Admin API version")
	fs.StringVar(&c.endpoint, "endpoint", "", "Override the GraphQL URL (for tests); skips the token requirement")
	if withStateDir {
		fs.StringVar(&c.stateDir, "state-dir", defaultStateDir, "Where run state lives")
	}
}

// parseFlags parses and prints friendly help for -h.
func parseFlags(fs *flag.FlagSet, args []string, summary string, w io.Writer) error {
	err := fs.Parse(args)
	if errors.Is(err, flag.ErrHelp) {
		printFlagHelp(fs, summary, w)
		return err
	}
	if err != nil {
		return usageErrorf("%v", err)
	}
	if fs.NArg() > 0 {
		return usageErrorf("unexpected argument %q (flags start with -)", fs.Arg(0))
	}
	return nil
}

func printFlagHelp(fs *flag.FlagSet, summary string, w io.Writer) {
	st := styleFor(w)
	fmt.Fprintf(w, "%s %s\n%s\n\n%s\n", st.Brand(st.Bold("shopgen")), st.Brand(fs.Name()), summary, st.Bold("Flags"))
	fs.VisitAll(func(f *flag.Flag) {
		name, usage := flag.UnquoteUsage(f)
		left := "  -" + f.Name
		if name != "" {
			left += " " + name
		}
		def := ""
		if f.DefValue != "" && f.DefValue != "false" && f.DefValue != "0" && !strings.Contains(usage, "(default") {
			def = st.Dim(" (default " + f.DefValue + ")")
		}
		fmt.Fprintf(w, "%s %s%s\n", padRight(st.Bold(left), 26), usage, def)
	})
}

func cmdPlan(ctx context.Context, args []string, stdout io.Writer, ui *UI) error {
	var c commonFlags
	fs := newFlagSet("plan")
	addCommon(fs, &c, false)
	profilePath := fs.String("profile", "profile.json", "Profile file")
	opm := fs.Float64("opm", 5, "Orders per minute, for the duration estimate")
	out := fs.String("out", "", "Write every planned order as JSON lines to this `file`")
	if err := parseFlags(fs, args, "Preview the order history a profile generates. Nothing is written to Shopify.", stdout); err != nil {
		return err
	}
	if !(*opm > 0) {
		return usageErrorf("-opm must be > 0")
	}
	p, _, err := LoadProfile(*profilePath)
	if err != nil {
		return err
	}

	var cat []Variant
	source := "built-in synthetic catalog"
	if store := NormalizeStore(c.store); store != "" || c.endpoint != "" {
		client, err := newClient(store, c.endpoint, c.apiVersion, os.Getenv("SHOPIFY_ACCESS_TOKEN"), nil, ui)
		if err != nil {
			return err
		}
		ui.Logf("Fetching catalog from %s…", ui.Bold(firstNonEmpty(store, c.endpoint)))
		if cat, err = client.FetchCatalog(ctx); err != nil {
			return err
		}
		source = "from " + firstNonEmpty(store, c.endpoint)
	} else {
		cat = SyntheticCatalog()
		ui.Logf("No -store given; using the %s %s", source, ui.Dim("(set -store to plan against your products)"))
	}

	now := time.Now().UTC().Truncate(time.Second)
	plan, err := Generate(p, cat, now)
	if err != nil {
		return err
	}
	s := Summarize(p, plan, *opm)
	s.CatalogSize, s.CatalogSource = len(cat), source
	s.Write(stdout, styleFor(stdout))

	if *out != "" {
		if err := writePlanJSONL(*out, plan); err != nil {
			return fmt.Errorf("write %s: %w", *out, err)
		}
		ui.Successf("Wrote %d planned orders to %s", len(plan.Orders), ui.Bold(*out))
	}
	return nil
}

func writePlanJSONL(path string, plan *Plan) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	bw := bufio.NewWriter(f)
	enc := json.NewEncoder(bw)
	for i := range plan.Orders {
		if err := enc.Encode(&plan.Orders[i]); err != nil {
			f.Close()
			return err
		}
	}
	if err := bw.Flush(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

func cmdRun(ctx, abort context.Context, args []string, ui *UI) error {
	var c commonFlags
	fs := newFlagSet("run")
	addCommon(fs, &c, true)
	profilePath := fs.String("profile", "profile.json", "Profile file; ignored with -resume")
	opm := fs.Float64("opm", 5, "Orders per minute; dev stores allow 5")
	limit := fs.Int("limit", 0, "Stop after `N` successful creates this session (smoke test)")
	resume := fs.String("resume", "", "Continue a run by `ID` from its state dir")
	allowNonDev := fs.Bool("allow-non-dev", false, "Permit writes to a store that is not a dev store")
	skipCancels := fs.Bool("skip-cancels", false, "Create planned-cancelled orders without calling orderCancel")
	if err := parseFlags(fs, args, "Create a profile's orders in a dev store. Safe to stop and resume at any time.", os.Stdout); err != nil {
		return err
	}
	if *limit < 0 {
		return usageErrorf("-limit must be >= 0")
	}
	return Run(ctx, RunConfig{
		Store:       c.store,
		Endpoint:    c.endpoint,
		APIVersion:  c.apiVersion,
		Token:       os.Getenv("SHOPIFY_ACCESS_TOKEN"),
		ProfilePath: *profilePath,
		OPM:         *opm,
		Limit:       *limit,
		Resume:      *resume,
		StateDir:    c.stateDir,
		AllowNonDev: *allowNonDev,
		SkipCancels: *skipCancels,
		UI:          ui,
		Abort:       abort,
	})
}

func cmdPurge(ctx context.Context, args []string, ui *UI) error {
	var c commonFlags
	fs := newFlagSet("purge")
	addCommon(fs, &c, true)
	run := fs.String("run", "", "Delete what run `ID` created")
	all := fs.Bool("all", false, "Delete what every shopgen run created")
	yes := fs.Bool("yes", false, "Skip the typed delete confirmation")
	allowNonDev := fs.Bool("allow-non-dev", false, "Permit deletes on a store that is not a dev store")
	if err := parseFlags(fs, args, "Delete orders and customers shopgen created. Untagged data is never touched.", os.Stdout); err != nil {
		return err
	}
	_, err := Purge(ctx, PurgeConfig{
		Store:       c.store,
		Endpoint:    c.endpoint,
		APIVersion:  c.apiVersion,
		Token:       os.Getenv("SHOPIFY_ACCESS_TOKEN"),
		Run:         *run,
		All:         *all,
		Yes:         *yes,
		StateDir:    c.stateDir,
		AllowNonDev: *allowNonDev,
		UI:          ui,
	})
	return err
}
