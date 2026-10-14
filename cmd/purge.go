package cmd

import (
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/weekwright/shopgen/internal/shopgen"
)

func (a *app) newPurgeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "purge",
		Short: "Delete the orders and customers shopgen created",
		Long: "Delete orders and customers carrying a shopgen tag, for one run or all runs.\n" +
			"Untagged data is never touched. Asks you to type 'delete' unless --yes.",
		Example: `  shopgen purge --run 20261014-a1b2c3
  shopgen purge --all --yes`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := shopgen.Purge(cmd.Context(), shopgen.PurgeConfig{
				Conn:        a.conn(),
				Run:         a.v.GetString("run"),
				All:         flagOnly(cmd, "all"),
				Yes:         flagOnly(cmd, "yes"),
				StateDir:    a.stateDir(),
				AllowNonDev: flagOnly(cmd, "allow-non-dev"),
				UI:          a.ui,
				In:          a.in,
			})
			return err
		},
	}
	return bindFlags(cmd, func(f *pflag.FlagSet) {
		f.String("run", "", "delete what run `ID` created")
		f.Bool("all", false, "delete what every shopgen run created")
		f.BoolP("yes", "y", false, "skip the typed confirmation (flag only)")
		f.Bool("allow-non-dev", false, "permit deletes on a store that is not a dev store (flag only)")
	})
}
