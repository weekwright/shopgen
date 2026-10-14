package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/weekwright/shopgen/internal/shopgen"
	"github.com/weekwright/shopgen/internal/ui"
)

func (a *app) newPlanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Preview the history a profile generates (no writes)",
		Long: "Validate a profile and summarize the order history it generates. Nothing is\n" +
			"written to Shopify. With --store (or SHOPIFY_STORE) the store's real catalog is\n" +
			"used; otherwise a built-in 40-variant catalog.",
		Example: `  shopgen plan -p profile.example.json
  shopgen plan -p profile.json --out plan.jsonl     # every planned order as JSON lines`,
		Args: noArgs,
		RunE: a.runPlan,
	}
	return bindFlags(cmd, func(f *pflag.FlagSet) {
		f.StringP("profile", "p", "profile.json", "profile file")
		f.Float64("opm", 5, "orders per minute, for the duration estimate")
		f.StringP("out", "o", "", "write every planned order as JSON lines to `file`")
	})
}

func (a *app) runPlan(cmd *cobra.Command, _ []string) error {
	opm := a.v.GetFloat64("opm")
	if !(opm > 0) {
		return shopgen.UsageErrorf("--opm must be > 0")
	}
	profilePath := a.v.GetString("profile")
	p, _, err := shopgen.LoadProfile(profilePath)
	if err != nil {
		return err
	}

	var cat []shopgen.Variant
	source := "built-in synthetic catalog"
	conn := a.conn()
	if conn.Store != "" || conn.Endpoint != "" {
		client, err := conn.Client(nil, a.ui)
		if err != nil {
			return err
		}
		where := conn.Store
		if where == "" {
			where = conn.Endpoint
		}
		a.ui.Logf("Fetching catalog from %s…", a.ui.Bold(where))
		variants, err := client.ActiveVariants(cmd.Context())
		if err != nil {
			return err
		}
		if len(variants) == 0 {
			return fmt.Errorf("%s has no active product variants priced above 0; add some products, or omit --store to use the built-in catalog", where)
		}
		cat, source = shopgen.CatalogFromShopify(variants), "from "+where
	} else {
		cat = shopgen.SyntheticCatalog()
		a.ui.Logf("No store given; using the %s %s", source, a.ui.Dim("(set --store to plan against your products)"))
	}

	plan, err := shopgen.Generate(p, cat, time.Now().UTC().Truncate(time.Second))
	if err != nil {
		return err
	}
	s := shopgen.Summarize(p, plan, opm)
	s.CatalogSize, s.CatalogSource = len(cat), source
	st := ui.StyleFor(a.out)
	if a.v.GetBool("no-color") {
		st = ui.Style{}
	}
	s.Write(a.out, st)

	if out := a.v.GetString("out"); out != "" {
		if err := writePlanJSONL(out, plan); err != nil {
			return fmt.Errorf("write %s: %w", out, err)
		}
		a.ui.Successf("Wrote %d planned orders to %s", len(plan.Orders), a.ui.Bold(out))
	}
	return nil
}

func writePlanJSONL(path string, plan *shopgen.Plan) error {
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
