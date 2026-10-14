package shopgen

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/weekwright/shopgen/internal/shopify"
	"github.com/weekwright/shopgen/internal/ui"
)

// CheckStatus is the result of one doctor check.
type CheckStatus int

const (
	CheckOK CheckStatus = iota
	CheckWarn
	CheckFail
	CheckSkip
)

// Check is one line of the doctor report.
type Check struct {
	Name   string
	Status CheckStatus
	Detail string
}

// DoctorConfig holds doctor's inputs.
type DoctorConfig struct {
	Conn
	ProfilePath string
	OPM         float64
	Now         func() time.Time
}

// Doctor checks everything a run depends on, without writing anything:
// credentials, connectivity, the dev-store flag, access scopes, the catalog,
// and the profile.
func Doctor(ctx context.Context, cfg DoctorConfig) []Check {
	var checks []Check
	add := func(name string, st CheckStatus, format string, a ...any) {
		checks = append(checks, Check{Name: name, Status: st, Detail: fmt.Sprintf(format, a...)})
	}
	now := time.Now
	if cfg.Now != nil {
		now = cfg.Now
	}

	// Profile first: it needs no network.
	var profile *Profile
	if p, _, err := LoadProfile(cfg.ProfilePath); err != nil {
		if errors.Is(err, os.ErrNotExist) || strings.Contains(err.Error(), "not found") {
			add("profile", CheckWarn, "%s not found; copy profile.example.json to start", cfg.ProfilePath)
		} else {
			add("profile", CheckFail, "%v", err)
		}
	} else {
		profile = p
		add("profile", CheckOK, "%s is valid", cfg.ProfilePath)
	}

	// Credentials.
	switch t := cfg.Tokens.(type) {
	case nil:
		if cfg.Endpoint == "" {
			add("credentials", CheckFail, "%v", ErrNoToken)
			return checks
		}
		add("credentials", CheckSkip, "none needed for --endpoint")
	case *shopify.ClientCredentials:
		if _, err := t.Token(ctx); err != nil {
			add("credentials", CheckFail, "client credentials grant failed: %v", err)
			return checks
		}
		add("credentials", CheckOK, "client credentials token, valid until %s (refreshed automatically)", t.Expiry().Local().Format("Jan 2 15:04"))
	default:
		add("credentials", CheckOK, "access token from SHOPIFY_ACCESS_TOKEN")
	}

	client, err := cfg.Conn.Client(nil, nil)
	if err != nil {
		add("store", CheckFail, "%v", err)
		return checks
	}
	shop, err := client.Shop(ctx)
	if err != nil {
		add("store", CheckFail, "%v", err)
		return checks
	}
	add("store", CheckOK, "%s (%s), Admin API %s", shop.Name, shop.Domain, firstNonEmpty(cfg.APIVersion, "default"))
	if shop.PartnerDevelopment {
		add("dev store", CheckOK, "partner development store: writes allowed")
	} else {
		add("dev store", CheckFail, "not a development store: run and purge refuse it without --allow-non-dev")
	}

	if granted, err := shopify.GrantedScopes(ctx, cfg.Store, cfg.Endpoint, cfg.Tokens); err != nil {
		add("scopes", CheckWarn, "could not list scopes: %v", err)
	} else if missing := shopify.MissingScopes(shopify.RequiredScopes, granted); len(missing) > 0 {
		add("scopes", CheckFail, "missing %s (granted: %s)", strings.Join(missing, ", "), strings.Join(granted, ", "))
	} else {
		add("scopes", CheckOK, "%s granted", strings.Join(shopify.RequiredScopes, ", "))
	}

	variants, err := client.ActiveVariants(ctx)
	switch {
	case err != nil:
		add("catalog", CheckFail, "%v", err)
	case len(variants) == 0:
		add("catalog", CheckFail, "%v", errNoProducts)
	case len(variants) < 5:
		add("catalog", CheckWarn, "only %d active variants; reports look more realistic with a dozen or more", len(variants))
	default:
		add("catalog", CheckOK, "%d active variants", len(variants))
	}

	if profile != nil {
		if shop.Currency != "" && shop.Currency != profile.Currency {
			add("currency", CheckWarn, "profile uses %s but the shop uses %s", profile.Currency, shop.Currency)
		} else {
			add("currency", CheckOK, "%s", profile.Currency)
		}
		if len(variants) > 0 {
			if plan, err := Generate(profile, CatalogFromShopify(variants), now().UTC()); err == nil {
				opm := cfg.OPM
				if opm <= 0 {
					opm = 5
				}
				add("plan", CheckOK, "%d orders, about %s at %.1f/min", len(plan.Orders), ui.FormatDuration(runInterval(opm)*time.Duration(len(plan.Orders))), opm)
			} else {
				add("plan", CheckFail, "%v", err)
			}
		}
	}
	return checks
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}
