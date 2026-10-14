// Command shopgen backfills months of realistic, reproducible order history
// into a Shopify development store.
package main

import (
	"os"

	"github.com/weekwright/shopgen/cmd"
)

func main() { os.Exit(cmd.Execute()) }
