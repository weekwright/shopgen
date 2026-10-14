package ui

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestFormatDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0:                             "0s",
		350 * time.Millisecond:        "0.3s",
		42 * time.Second:              "42s",
		7*time.Minute + 5*time.Second: "7m05s",
		19*time.Hour + 16*time.Minute + 3*time.Second: "19h16m",
	} {
		if got := FormatDuration(d); got != want {
			t.Errorf("FormatDuration(%s) = %s, want %s", d, got, want)
		}
	}
}

func TestSparkline(t *testing.T) {
	if got := Sparkline([]int{0, 5, 10}, 0); got != "▁▄█" {
		t.Errorf("Sparkline = %q", got)
	}
	if got := Sparkline(make([]int, 91), 60); len([]rune(got)) != 60 {
		t.Errorf("Sparkline width = %d", len([]rune(got)))
	}
}

func TestPlainStyleHasNoEscapes(t *testing.T) {
	var buf bytes.Buffer
	u := New(&buf) // a buffer is not a terminal
	u.SetClock(func() time.Time { return time.Date(2026, 1, 1, 9, 30, 0, 0, time.UTC) })
	u.Successf("created %s", u.Bold("#1001"))
	if got := buf.String(); got != "09:30:00 ✓ created #1001\n" || strings.Contains(got, "\x1b") {
		t.Fatalf("got %q", got)
	}
}

func TestForceColorStyles(t *testing.T) {
	var buf bytes.Buffer
	st := ForceColor(&buf)
	if s := st.Green("ok"); !strings.Contains(s, "\x1b[") || PadRight(s, 4) != s+"  " {
		t.Fatalf("styled %q, padded %q", s, PadRight(s, 4))
	}
}
