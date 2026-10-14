package cmd

import (
	"fmt"
	"runtime"
	"runtime/debug"

	"github.com/spf13/cobra"
)

func (a *app) newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			v := Version
			if v == "dev" {
				if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
					v = bi.Main.Version // installed with go install …@vX.Y.Z
				}
			}
			fmt.Fprintf(a.out, "shopgen %s (%s, %s/%s)\n", v, runtime.Version(), runtime.GOOS, runtime.GOARCH)
			return nil
		},
	}
}
