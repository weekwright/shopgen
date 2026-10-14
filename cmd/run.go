package cmd

import (
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/weekwright/shopgen/internal/shopgen"
)

func (a *app) newRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Create a profile's orders in a dev store, resumably",
		Long: "Create the planned orders through orderCreate with backdated processedAt,\n" +
			"paced to Shopify's 5-per-minute dev-store limit. Safe to stop at any time:\n" +
			"Ctrl-C finishes the order in flight, saves state and prints the resume command.",
		Example: `  shopgen run --limit 3                     # smoke test
  shopgen run -p profile.json               # the whole history
  shopgen run --resume 20261014-a1b2c3      # continue a run`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if limit := a.v.GetInt("limit"); limit < 0 {
				return shopgen.UsageErrorf("--limit must be >= 0")
			}
			a.signal = "Stopping after the in-flight order…"
			return shopgen.Run(cmd.Context(), shopgen.RunConfig{
				Conn:        a.conn(),
				ProfilePath: a.v.GetString("profile"),
				OPM:         a.v.GetFloat64("opm"),
				Limit:       a.v.GetInt("limit"),
				Resume:      a.v.GetString("resume"),
				StateDir:    a.stateDir(),
				AllowNonDev: flagOnly(cmd, "allow-non-dev"),
				SkipCancels: a.v.GetBool("skip-cancels"),
				UI:          a.ui,
				Abort:       a.abort,
			})
		},
	}
	return bindFlags(cmd, func(f *pflag.FlagSet) {
		f.StringP("profile", "p", "profile.json", "profile file; ignored with --resume")
		f.Float64("opm", 5, "orders per minute; dev and trial stores allow 5")
		f.IntP("limit", "n", 0, "stop after `N` successful creates this session")
		f.StringP("resume", "r", "", "continue run `ID` from its state dir")
		f.Bool("allow-non-dev", false, "permit writes to a store that is not a dev store (flag only)")
		f.Bool("skip-cancels", false, "create planned-cancelled orders without calling orderCancel")
	})
}
