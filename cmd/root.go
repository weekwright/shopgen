// Package cmd is shopgen's command line, built with cobra and viper. Each
// command lives in its own file; this one holds the root command, shared
// flags, configuration and exit codes.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"github.com/weekwright/shopgen/internal/shopgen"
	"github.com/weekwright/shopgen/internal/shopify"
	"github.com/weekwright/shopgen/internal/ui"
)

// Version is set at build time with -ldflags "-X github.com/weekwright/shopgen/cmd.Version=v1.2.3".
var Version = "dev"

const defaultAPIVersion = "2026-10"

// Exit codes.
const (
	exitOK          = 0
	exitError       = 1
	exitUsage       = 2
	exitInterrupted = 130
)

// app is the state shared by every command in one invocation.
type app struct {
	v      *viper.Viper
	in     io.Reader
	out    io.Writer // stdout: plan's summary, version
	ui     *ui.UI    // stderr: logs
	ctx    context.Context
	abort  context.Context // done on a second Ctrl-C
	signal string          // what the first Ctrl-C does, for the warning
}

// Execute runs shopgen with the process's arguments and returns the exit code.
func Execute() int {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	abort, abortNow := context.WithCancel(context.Background())
	defer abortNow()

	a := &app{in: os.Stdin, out: os.Stdout, ui: ui.New(os.Stderr), ctx: ctx, abort: abort}
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigs)
	go func() {
		select {
		case <-sigs:
		case <-ctx.Done():
			return
		}
		if a.signal != "" {
			a.ui.Warnf("%s %s", a.signal, a.ui.Dim("(Ctrl-C again to abandon it)"))
		}
		stop()
		select {
		case <-sigs:
			abortNow()
		case <-abort.Done():
		}
	}()
	return a.execute(os.Args[1:])
}

// run executes args with the given streams; tests call it directly.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	a := &app{in: stdin, out: stdout, ui: ui.New(stderr), ctx: context.Background(), abort: context.Background()}
	return a.execute(args)
}

func (a *app) execute(args []string) int {
	a.v = viper.New()
	root := a.newRootCmd()
	root.SetArgs(args)
	root.SetIn(a.in)
	root.SetOut(a.out)
	root.SetErr(a.ui.Writer())
	if len(args) == 0 {
		_ = root.Help()
		return exitUsage
	}
	err := root.ExecuteContext(a.ctx)
	if err != nil && strings.HasPrefix(err.Error(), "unknown command ") {
		err = shopgen.UsageErrorf("%s", strings.SplitN(err.Error(), "\n", 2)[0])
	}

	var ue *shopgen.UsageError
	switch {
	case err == nil:
		return exitOK
	case errors.As(err, &ue):
		if ue.Msg != "" {
			a.ui.Errorf("%v", err)
			a.ui.Hint("see", strings.Join(append([]string{"shopgen"}, usageTarget(args)...), " ")+" --help")
		}
		return exitUsage
	case errors.Is(err, shopgen.ErrInterrupted), errors.Is(err, context.Canceled):
		return exitInterrupted
	default:
		a.ui.Errorf("%v", err)
		return exitError
	}
}

// usageTarget picks the subcommand to point --help at, if any.
func usageTarget(args []string) []string {
	for _, a := range args {
		switch a {
		case "plan", "run", "purge", "doctor", "version":
			return []string{a}
		}
	}
	return nil
}

func (a *app) newRootCmd() *cobra.Command {
	st := a.ui.Style
	cobra.AddTemplateFunc("heading", func(s string) string { return st.Heading(s) })
	cobra.AddTemplateFunc("accent", func(s string) string { return st.Brand(s) })

	root := &cobra.Command{
		Use:   "shopgen",
		Short: "Backfill realistic, reproducible order history into a Shopify dev store",
		Long: st.Heading("shopgen") + " backfills months of realistic, reproducible order history into a\n" +
			"Shopify development store: trends, weekly cycles, spikes, repeat customers,\n" +
			"refunds and cancellations, landing on the right days in analytics.",
		Example: `  shopgen doctor                           # check credentials, scopes and catalog
  shopgen plan -p profile.example.json     # preview the history, no writes
  shopgen run --limit 3                    # smoke test: create 3 orders
  shopgen run                              # the whole history, ~5 orders/min
  shopgen run --resume 20261014-a1b2c3     # pick up where you left off
  shopgen purge --run 20261014-a1b2c3      # delete what that run created`,
		SilenceErrors:     true,
		SilenceUsage:      true,
		Version:           Version,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error { return a.loadConfig(cmd) },
	}
	root.SetVersionTemplate("shopgen {{.Version}}\n")
	root.SetUsageTemplate(usageTemplate)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return shopgen.UsageErrorf("%v", err) })

	pf := root.PersistentFlags()
	pf.String("config", "", "config file (default ./shopgen.yaml, then <user config dir>/shopgen/shopgen.yaml)")
	pf.StringP("store", "s", "", "store domain; a bare name gets .myshopify.com (env SHOPIFY_STORE)")
	pf.String("api-version", defaultAPIVersion, "Admin API version")
	pf.String("state-dir", shopgen.DefaultStateDir, "where run state lives")
	pf.String("endpoint", "", "override the GraphQL URL (tests); skips the token requirement")
	pf.Bool("no-color", false, "disable colored output (or set NO_COLOR)")
	_ = pf.MarkHidden("endpoint")

	root.AddCommand(a.newPlanCmd(), a.newRunCmd(), a.newPurgeCmd(), a.newDoctorCmd(), a.newVersionCmd())
	return root
}

// noArgs rejects positional arguments as a usage error.
func noArgs(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	return shopgen.UsageErrorf("unexpected argument %q (flags start with --)", args[0])
}

// loadConfig layers configuration: flags beat environment variables, which
// beat the config file, which beats defaults.
func (a *app) loadConfig(cmd *cobra.Command) error {
	v := a.v
	v.SetEnvPrefix("SHOPGEN")
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	v.AutomaticEnv()
	_ = v.BindEnv("store", "SHOPGEN_STORE", "SHOPIFY_STORE")
	_ = v.BindEnv("client-id", "SHOPGEN_CLIENT_ID", "SHOPIFY_CLIENT_ID")
	if err := v.BindPFlags(cmd.Flags()); err != nil {
		return err
	}

	if path, _ := cmd.Flags().GetString("config"); path != "" {
		v.SetConfigFile(path)
	} else {
		// No SetConfigType: with it, viper also tries the bare name and
		// would read a ./shopgen binary as YAML.
		v.SetConfigName("shopgen")
		v.AddConfigPath(".")
		if dir, err := os.UserConfigDir(); err == nil {
			v.AddConfigPath(filepath.Join(dir, "shopgen"))
		}
	}
	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if !errors.As(err, &notFound) {
			return fmt.Errorf("config: %w", err)
		}
	}
	if v.GetBool("no-color") {
		a.ui.Style = ui.Style{}
	}
	return nil
}

// flagOnly reads a safety-relevant boolean from the command line only, so a
// config file or environment variable can never switch it on silently.
func flagOnly(cmd *cobra.Command, name string) bool {
	b, _ := cmd.Flags().GetBool(name)
	return b
}

// conn builds the store connection from configuration. Credentials come
// only from the environment: SHOPIFY_ACCESS_TOKEN, or SHOPIFY_CLIENT_ID and
// SHOPIFY_CLIENT_SECRET for a Dev Dashboard app's client credentials grant.
func (a *app) conn() shopgen.Conn {
	c := shopgen.Conn{
		Store:      shopify.ShopDomain(a.v.GetString("store")),
		Endpoint:   a.v.GetString("endpoint"),
		APIVersion: a.v.GetString("api-version"),
	}
	if tok := os.Getenv("SHOPIFY_ACCESS_TOKEN"); tok != "" {
		c.Tokens = shopify.StaticToken(tok)
	} else if id, secret := a.v.GetString("client-id"), os.Getenv("SHOPIFY_CLIENT_SECRET"); id != "" && secret != "" {
		tokenURL := shopify.TokenURL(c.Store)
		if c.Endpoint != "" {
			if u, err := url.Parse(c.Endpoint); err == nil {
				tokenURL = u.Scheme + "://" + u.Host + "/admin/oauth/access_token"
			}
		}
		c.Tokens = &shopify.ClientCredentials{TokenURL: tokenURL, ClientID: id, ClientSecret: secret}
	}
	return c
}

// stateDir is the configured run state directory.
func (a *app) stateDir() string {
	if d := a.v.GetString("state-dir"); d != "" {
		return d
	}
	return shopgen.DefaultStateDir
}

// bindFlags keeps a command's flag definitions in one place and returns its
// flag set for chaining.
func bindFlags(cmd *cobra.Command, f func(*pflag.FlagSet)) *cobra.Command {
	f(cmd.Flags())
	return cmd
}

const usageTemplate = `{{heading "Usage"}}{{if .Runnable}}
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [command]{{end}}{{if .HasAvailableSubCommands}}

{{heading "Commands"}}{{range .Commands}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{accent (rpad .Name .NamePadding) }} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

{{heading "Flags"}}
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

{{heading "Global flags"}}
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasExample}}

{{heading "Examples"}}
{{.Example}}{{end}}{{if not .HasParent}}

{{heading "Environment"}}
  SHOPIFY_STORE          default for --store
  SHOPIFY_ACCESS_TOKEN   Admin API access token (read from the environment only)
  SHOPIFY_CLIENT_ID      Dev Dashboard app client ID, for automatic tokens
  SHOPIFY_CLIENT_SECRET  …and its secret (environment only)
  SHOPGEN_*              any flag, e.g. SHOPGEN_STATE_DIR, SHOPGEN_OPM{{end}}{{if .HasAvailableSubCommands}}

Run "{{.CommandPath}} [command] --help" for a command's flags. Docs: https://github.com/weekwright/shopgen{{end}}
`
