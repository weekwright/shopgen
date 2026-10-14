package cmd

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/weekwright/shopgen/internal/shopgen"
	"github.com/weekwright/shopgen/internal/ui"
)

var errDoctorFailed = errors.New("some checks failed")

func (a *app) newDoctorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check credentials, store, scopes and catalog before a run",
		Long: "Check everything a run depends on without writing anything: the profile,\n" +
			"credentials, the connection, the dev-store flag, access scopes, the catalog\n" +
			"and the shop currency. Exits 1 if any check fails.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			checks := shopgen.Doctor(cmd.Context(), shopgen.DoctorConfig{
				Conn:        a.conn(),
				ProfilePath: a.v.GetString("profile"),
				OPM:         a.v.GetFloat64("opm"),
			})
			st := ui.StyleFor(a.out)
			if a.v.GetBool("no-color") {
				st = ui.Style{}
			}
			failed := false
			for _, c := range checks {
				var mark string
				switch c.Status {
				case shopgen.CheckOK:
					mark = st.Green("✓")
				case shopgen.CheckWarn:
					mark = st.Yellow("!")
				case shopgen.CheckFail:
					mark, failed = st.Red("✗"), true
				default:
					mark = st.Dim("–")
				}
				fmt.Fprintf(a.out, "%s %s %s\n", mark, st.Bold(ui.PadRight(c.Name, 12)), c.Detail)
			}
			if failed {
				return errDoctorFailed
			}
			fmt.Fprintf(a.out, "\n%s\n", st.Green("Ready to run."))
			return nil
		},
	}
	return bindFlags(cmd, func(f *pflag.FlagSet) {
		f.StringP("profile", "p", "profile.json", "profile to check")
		f.Float64("opm", 5, "orders per minute, for the duration estimate")
	})
}
