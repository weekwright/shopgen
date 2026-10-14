package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestDocsDemo regenerates docs/assets/src/run-demo.ansi, the run shown in
// the README's terminal screenshot, against the fake Admin API:
//
//	DEMO_OUT=$PWD/docs/assets/src/run-demo.ansi go test -run TestDocsDemo -count=1 .
func TestDocsDemo(t *testing.T) {
	out := os.Getenv("DEMO_OUT")
	if out == "" {
		t.Skip("set DEMO_OUT to regenerate the docs demo")
	}
	h := newHarness(t)
	h.fake.name = "Acme Dev"
	h.fake.throttleOn = 4
	h.fake.dropOn = 7
	b, _ := os.ReadFile("profile.example.json")
	os.WriteFile(h.prof, b, 0o644)
	f, _ := os.Create(out)
	defer f.Close()
	ui := NewUI(f)
	ui.Style = Style{on: true}
	clock := time.Date(2026, 10, 14, 21, 4, 12, 0, time.Local)
	step := []time.Duration{300 * time.Millisecond, 1200 * time.Millisecond, 300 * time.Millisecond, 100 * time.Millisecond, 0}
	i := 0
	ui.clock = func() time.Time {
		if i < len(step) {
			clock = clock.Add(step[i])
		} else {
			clock = clock.Add(12240 * time.Millisecond)
		}
		i++
		return clock
	}
	wd, _ := os.Getwd()
	os.Chdir(h.dir)
	defer os.Chdir(wd)
	cfg := h.config()
	cfg.StateDir = ".shopgen"
	cfg.ProfilePath = filepath.Base(h.prof)
	cfg.UI = ui
	cfg.Now = func() time.Time { return time.Date(2026, 10, 14, 2, 0, 0, 0, time.UTC) }
	cfg.Limit = 8
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
}
