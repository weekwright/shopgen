package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weekwright/shopgen/internal/shopify/shopifytest"
)

// cli runs shopgen with args and returns the exit code and both streams.
func cli(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := run(args, strings.NewReader(""), &out, &errOut)
	return code, out.String(), errOut.String()
}

// isolate runs the test in an empty directory with no shopgen env vars.
func isolate(t *testing.T) string {
	t.Helper()
	for _, k := range []string{"SHOPIFY_STORE", "SHOPIFY_ACCESS_TOKEN", "SHOPIFY_CLIENT_ID", "SHOPIFY_CLIENT_SECRET", "SHOPGEN_OPM", "SHOPGEN_PROFILE", "XDG_CONFIG_HOME"} {
		t.Setenv(k, "")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	wd, _ := os.Getwd()
	example, err := os.ReadFile(filepath.Join(wd, "..", "profile.example.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "profile.example.json"), example, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	return dir
}

func TestExitCodes(t *testing.T) {
	isolate(t)
	cases := []struct {
		args []string
		code int
	}{
		{nil, exitUsage},
		{[]string{"bogus"}, exitUsage},
		{[]string{"plan", "--nope"}, exitUsage},
		{[]string{"plan", "extra"}, exitUsage},
		{[]string{"plan", "-p", "profile.example.json", "--opm", "0"}, exitUsage},
		{[]string{"purge", "--endpoint", "http://127.0.0.1:1"}, exitUsage},
		{[]string{"run", "--limit", "-1"}, exitUsage},
		{[]string{"plan", "-p", "missing.json"}, exitError},
		{[]string{"run", "-p", "profile.example.json"}, exitUsage}, // no store
		{[]string{"version"}, exitOK},
		{[]string{"--help"}, exitOK},
		{[]string{"plan", "--help"}, exitOK},
	}
	for _, c := range cases {
		code, _, stderr := cli(t, c.args...)
		if code != c.code {
			t.Errorf("shopgen %s: exit %d, want %d\n%s", strings.Join(c.args, " "), code, c.code, stderr)
		}
	}
}

func TestHelpListsCommands(t *testing.T) {
	isolate(t)
	_, out, _ := cli(t, "--help")
	for _, want := range []string{"plan", "run", "purge", "doctor", "version", "SHOPIFY_ACCESS_TOKEN", "--store"} {
		if !strings.Contains(out, want) {
			t.Errorf("help is missing %q:\n%s", want, out)
		}
	}
}

func TestPlanSummaryAndJSONL(t *testing.T) {
	dir := isolate(t)
	path := filepath.Join(dir, "plan.jsonl")
	code, out, stderr := cli(t, "plan", "-p", "profile.example.json", "-o", path)
	if code != exitOK {
		t.Fatalf("plan: %d\n%s", code, stderr)
	}
	if !strings.HasPrefix(out, "window      2026-07-15 → 2026-10-13 (91 days, America/Chicago)") {
		t.Fatalf("summary:\n%s", out)
	}
	b, err := os.ReadFile(path)
	if err != nil || bytes.Count(b, []byte("\n")) < 5000 {
		t.Fatalf("plan.jsonl: %v, %d lines", err, bytes.Count(b, []byte("\n")))
	}
}

func TestConfigFileAndEnvPrecedence(t *testing.T) {
	dir := isolate(t)
	cfg := "profile: profile.example.json\nopm: 10\n"
	if err := os.WriteFile(filepath.Join(dir, "shopgen.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	_, out, _ := cli(t, "plan")
	if !strings.Contains(out, "at 10.0 orders/min") {
		t.Fatalf("config file not applied:\n%s", out)
	}
	t.Setenv("SHOPGEN_OPM", "20")
	_, out, _ = cli(t, "plan")
	if !strings.Contains(out, "at 20.0 orders/min") {
		t.Fatalf("env should beat the config file:\n%s", out)
	}
	_, out, _ = cli(t, "plan", "--opm", "2.5")
	if !strings.Contains(out, "at 2.5 orders/min") {
		t.Fatalf("flag should beat env:\n%s", out)
	}
}

func TestAllowNonDevIgnoresConfig(t *testing.T) {
	dir := isolate(t)
	fake := shopifytest.New(t)
	fake.Dev = false
	os.WriteFile(filepath.Join(dir, "shopgen.yaml"), []byte("allow-non-dev: true\nprofile: profile.example.json\n"), 0o644)
	t.Setenv("SHOPGEN_ALLOW_NON_DEV", "true")
	code, _, stderr := cli(t, "run", "--endpoint", fake.URL(), "--limit", "1")
	if code != exitError || !strings.Contains(stderr, "not a development store") {
		t.Fatalf("exit %d:\n%s", code, stderr)
	}
}

func TestDoctor(t *testing.T) {
	isolate(t)
	fake := shopifytest.New(t)
	code, out, stderr := cli(t, "doctor", "--endpoint", fake.URL(), "-p", "profile.example.json")
	if code != exitOK {
		t.Fatalf("doctor: %d\n%s%s", code, out, stderr)
	}
	for _, want := range []string{"profile", "store", "dev store", "scopes", "12 active variants", "Ready to run"} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor output missing %q:\n%s", want, out)
		}
	}

	fake.Scopes = []string{"read_products"}
	code, out, _ = cli(t, "doctor", "--endpoint", fake.URL(), "-p", "profile.example.json")
	if code != exitError || !strings.Contains(out, "missing write_orders, write_customers") {
		t.Fatalf("missing scopes not reported (exit %d):\n%s", code, out)
	}
}

func TestRunAndPurgeWithClientCredentials(t *testing.T) {
	dir := isolate(t)
	fake := shopifytest.New(t)
	fake.ClientID, fake.ClientSecret = "app-id", "app-secret"
	t.Setenv("SHOPIFY_CLIENT_ID", "app-id")
	t.Setenv("SHOPIFY_CLIENT_SECRET", "app-secret")
	os.WriteFile(filepath.Join(dir, "smoke.json"), []byte(`{"seed":1,"start":"2026-10-01","end":"2026-10-02","orders_per_day":3,"noise":0}`), 0o644)

	code, _, stderr := cli(t, "run", "--endpoint", fake.URL(), "-p", "smoke.json", "--opm", "100000", "-n", "2")
	if code != exitOK {
		t.Fatalf("run: %d\n%s", code, stderr)
	}
	if orders, _, _, _ := fake.Snapshot(); len(orders) != 2 || fake.TokensIssued() != 1 {
		t.Fatalf("want 2 orders with one issued token, got %d orders, %d tokens\n%s", len(orders), fake.TokensIssued(), stderr)
	}
	if !strings.Contains(stderr, "shopgen run --resume ") {
		t.Fatalf("resume hint missing:\n%s", stderr)
	}

	entries, _ := os.ReadDir(filepath.Join(dir, ".shopgen"))
	if len(entries) != 1 {
		t.Fatalf("run dirs: %v", entries)
	}
	id := entries[0].Name()
	code, _, stderr = cli(t, "purge", "--endpoint", fake.URL(), "--run", id, "-y")
	if code != exitOK {
		t.Fatalf("purge: %d\n%s", code, stderr)
	}
	if orders, _, _, _ := fake.Snapshot(); len(orders) != 0 {
		t.Fatalf("%d orders left after purge", len(orders))
	}
}

func TestPurgeConfirmation(t *testing.T) {
	isolate(t)
	fake := shopifytest.New(t)
	var out, errOut bytes.Buffer
	code := run([]string{"purge", "--endpoint", fake.URL(), "--all"}, strings.NewReader("nope\n"), &out, &errOut)
	if code != exitError || !strings.Contains(errOut.String(), "Type 'delete' to confirm") || !strings.Contains(errOut.String(), "aborted") {
		t.Fatalf("exit %d:\n%s", code, errOut.String())
	}
}

func TestBinaryNamedShopgenIsNotConfig(t *testing.T) {
	dir := isolate(t)
	os.WriteFile(filepath.Join(dir, "shopgen"), []byte("\x7fELF\x00\x01binary"), 0o755)
	if code, _, stderr := cli(t, "version"); code != exitOK {
		t.Fatalf("exit %d:\n%s", code, stderr)
	}
}
