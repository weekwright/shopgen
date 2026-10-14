package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// Style decides whether ANSI styling is used for one output stream.
type Style struct{ on bool }

// styleFor enables color when w is a terminal, unless NO_COLOR is set or
// TERM is dumb. FORCE_COLOR turns it on regardless.
func styleFor(w io.Writer) Style {
	if os.Getenv("FORCE_COLOR") != "" {
		return Style{on: true}
	}
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return Style{}
	}
	return Style{on: isTerminal(w)}
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func (s Style) wrap(code, text string) string {
	if !s.on || text == "" {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func (s Style) Bold(t string) string    { return s.wrap("1", t) }
func (s Style) Dim(t string) string     { return s.wrap("2", t) }
func (s Style) Red(t string) string     { return s.wrap("31", t) }
func (s Style) Green(t string) string   { return s.wrap("32", t) }
func (s Style) Yellow(t string) string  { return s.wrap("33", t) }
func (s Style) Blue(t string) string    { return s.wrap("34", t) }
func (s Style) Magenta(t string) string { return s.wrap("35", t) }
func (s Style) Cyan(t string) string    { return s.wrap("36", t) }

// Brand is shopgen's accent color (Shopify-ish green).
func (s Style) Brand(t string) string { return s.wrap("38;5;77", t) }

// Outcome colors an outcome key consistently everywhere.
func (s Style) Outcome(o string) string {
	switch o {
	case OutcomeFulfilled:
		return s.Green(o)
	case OutcomeUnfulfilled, OutcomePartiallyFulfilled:
		return s.Cyan(o)
	case OutcomePendingPayment:
		return s.Yellow(o)
	case OutcomeRefunded, OutcomePartiallyRefunded:
		return s.Magenta(o)
	case OutcomeCancelled:
		return s.Red(o)
	}
	return o
}

// UI writes timestamped log lines to stderr and, on a terminal, a transient
// status line (a countdown while pacing) beneath them.
type UI struct {
	Style
	mu     sync.Mutex
	w      io.Writer
	tty    bool
	clock  func() time.Time
	status string
}

// NewUI returns a UI writing to w.
func NewUI(w io.Writer) *UI {
	return &UI{Style: styleFor(w), w: w, tty: isTerminal(w) && os.Getenv("TERM") != "dumb", clock: time.Now}
}

func (u *UI) clearStatus() {
	if u.tty && u.status != "" {
		fmt.Fprint(u.w, "\r\x1b[2K")
	}
}

func (u *UI) drawStatus() {
	if u.tty && u.status != "" {
		fmt.Fprint(u.w, u.status)
	}
}

func (u *UI) line(symbol, msg string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.clearStatus()
	fmt.Fprintf(u.w, "%s %s %s\n", u.Dim(u.clock().Format("15:04:05")), symbol, msg)
	u.drawStatus()
}

// Logf logs a plain line.
func (u *UI) Logf(format string, a ...any) { u.line(u.Dim("│"), fmt.Sprintf(format, a...)) }

// Infof logs a line with an accent marker.
func (u *UI) Infof(format string, a ...any) { u.line(u.Brand("●"), fmt.Sprintf(format, a...)) }

// Successf logs a success line.
func (u *UI) Successf(format string, a ...any) { u.line(u.Green("✓"), fmt.Sprintf(format, a...)) }

// Warnf logs a warning line.
func (u *UI) Warnf(format string, a ...any) { u.line(u.Yellow("!"), fmt.Sprintf(format, a...)) }

// Errorf logs an error line.
func (u *UI) Errorf(format string, a ...any) { u.line(u.Red("✗"), fmt.Sprintf(format, a...)) }

// Hint logs an indented follow-up line, such as a command to run next.
func (u *UI) Hint(label, cmd string) {
	u.line(" ", u.Dim(label)+" "+u.Bold(cmd))
}

// setStatus replaces the transient status line.
func (u *UI) setStatus(s string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.clearStatus()
	u.status = s
	u.drawStatus()
}

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Wait sleeps for d, honoring ctx. On a terminal it shows a countdown with
// label; elsewhere it just sleeps.
func (u *UI) Wait(ctx context.Context, d time.Duration, label string) error {
	if d <= 0 {
		return ctx.Err()
	}
	if !u.tty {
		return sleepCtx(ctx, d)
	}
	deadline := time.Now().Add(d)
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	defer u.setStatus("")
	for i := 0; ; i++ {
		left := time.Until(deadline)
		if left <= 0 {
			return nil
		}
		u.setStatus(fmt.Sprintf("%s %s %s", u.Brand(spinnerFrames[i%len(spinnerFrames)]), u.Dim(label), u.Bold(fmt.Sprintf("%.0fs", left.Seconds()+0.49))))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}

// sleepCtx sleeps for d or until ctx is done.
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// formatDuration renders 19h16m, 7m05s or 42s.
func formatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d > 0 && d < 10*time.Second && d%time.Second != 0 {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	d = d.Round(time.Second)
	h := int(d / time.Hour)
	m := int(d % time.Hour / time.Minute)
	s := int(d % time.Minute / time.Second)
	switch {
	case h > 0:
		return fmt.Sprintf("%dh%02dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm%02ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

var sparkRunes = []rune("▁▂▃▄▅▆▇█")

// sparkline renders values as a row of block characters, bucketed to at most
// width columns.
func sparkline(vals []int, width int) string {
	if len(vals) == 0 {
		return ""
	}
	if width <= 0 || width > len(vals) {
		width = len(vals)
	}
	buckets := make([]float64, width)
	sizes := make([]int, width)
	for i, v := range vals {
		b := i * width / len(vals)
		buckets[b] += float64(v)
		sizes[b]++
	}
	for i := range buckets {
		if sizes[i] > 0 {
			buckets[i] /= float64(sizes[i])
		}
	}
	maxV := 0.0
	for _, b := range buckets {
		if b > maxV {
			maxV = b
		}
	}
	var sb strings.Builder
	for _, b := range buckets {
		i := 0
		if maxV > 0 {
			i = int(b / maxV * float64(len(sparkRunes)-1))
		}
		sb.WriteRune(sparkRunes[i])
	}
	return sb.String()
}

// padRight pads s to n visible runes (ignoring ANSI escapes).
func padRight(s string, n int) string {
	if w := visibleWidth(s); w < n {
		return s + strings.Repeat(" ", n-w)
	}
	return s
}

func visibleWidth(s string) int {
	w, inEsc := 0, false
	for _, r := range s {
		switch {
		case r == '\x1b':
			inEsc = true
		case inEsc:
			if r == 'm' {
				inEsc = false
			}
		default:
			w++
		}
	}
	return w
}
