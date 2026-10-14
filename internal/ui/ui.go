// Package ui renders shopgen's terminal output: styled, timestamped log
// lines on stderr, a transient countdown while pacing, and small text
// helpers. Styling uses lipgloss and degrades to plain text when the output
// is not a terminal or NO_COLOR is set.
package ui

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"golang.org/x/term"
)

// Palette. Adaptive colors pick a light- or dark-background variant.
var (
	colorBrand   = lipgloss.AdaptiveColor{Light: "#0b7a4b", Dark: "#5fd38d"}
	colorMuted   = lipgloss.AdaptiveColor{Light: "#6b7079", Dark: "#7e848a"}
	colorRed     = lipgloss.AdaptiveColor{Light: "#c62f2e", Dark: "#ff7b72"}
	colorGreen   = lipgloss.AdaptiveColor{Light: "#0b7a4b", Dark: "#56d38a"}
	colorYellow  = lipgloss.AdaptiveColor{Light: "#946200", Dark: "#f2c14e"}
	colorBlue    = lipgloss.AdaptiveColor{Light: "#1f63b8", Dark: "#6cb6ff"}
	colorMagenta = lipgloss.AdaptiveColor{Light: "#6a4bc4", Dark: "#d2a8ff"}
	colorCyan    = lipgloss.AdaptiveColor{Light: "#08707f", Dark: "#56c8d8"}
)

// Style styles text for one output stream. The zero value renders plain text.
type Style struct{ r *lipgloss.Renderer }

// StyleFor returns a Style for w. Color is on when w is a terminal, off when
// NO_COLOR is set or TERM is dumb, and forced on by FORCE_COLOR.
func StyleFor(w io.Writer) Style {
	switch {
	case os.Getenv("FORCE_COLOR") != "":
		return ForceColor(w)
	case os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" || !IsTerminal(w):
		return Style{}
	}
	return Style{r: lipgloss.NewRenderer(w)}
}

// ForceColor returns a Style that always emits 256-color ANSI codes.
func ForceColor(w io.Writer) Style {
	r := lipgloss.NewRenderer(w)
	r.SetColorProfile(termenv.ANSI256)
	r.SetHasDarkBackground(true)
	return Style{r: r}
}

// Enabled reports whether s emits styling.
func (s Style) Enabled() bool { return s.r != nil }

// IsTerminal reports whether w is a terminal.
func IsTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

func (s Style) render(st func(lipgloss.Style) lipgloss.Style, text string) string {
	if s.r == nil || text == "" {
		return text
	}
	return st(s.r.NewStyle()).Render(text)
}

func fg(c lipgloss.TerminalColor) func(lipgloss.Style) lipgloss.Style {
	return func(st lipgloss.Style) lipgloss.Style { return st.Foreground(c) }
}

func (s Style) Bold(t string) string {
	return s.render(func(st lipgloss.Style) lipgloss.Style { return st.Bold(true) }, t)
}
func (s Style) Dim(t string) string     { return s.render(fg(colorMuted), t) }
func (s Style) Red(t string) string     { return s.render(fg(colorRed), t) }
func (s Style) Green(t string) string   { return s.render(fg(colorGreen), t) }
func (s Style) Yellow(t string) string  { return s.render(fg(colorYellow), t) }
func (s Style) Blue(t string) string    { return s.render(fg(colorBlue), t) }
func (s Style) Magenta(t string) string { return s.render(fg(colorMagenta), t) }
func (s Style) Cyan(t string) string    { return s.render(fg(colorCyan), t) }

// Brand is shopgen's accent color.
func (s Style) Brand(t string) string { return s.render(fg(colorBrand), t) }

// Heading renders a bold accent heading.
func (s Style) Heading(t string) string {
	return s.render(func(st lipgloss.Style) lipgloss.Style { return st.Bold(true).Foreground(colorBrand) }, t)
}

// Outcome colors an order outcome key consistently everywhere.
func (s Style) Outcome(o string) string {
	switch o {
	case "fulfilled":
		return s.Green(o)
	case "unfulfilled", "partially_fulfilled":
		return s.Cyan(o)
	case "pending_payment":
		return s.Yellow(o)
	case "refunded", "partially_refunded":
		return s.Magenta(o)
	case "cancelled":
		return s.Red(o)
	}
	return o
}

// UI writes timestamped log lines to a stream (stderr) and, on a terminal, a
// transient status line beneath them.
type UI struct {
	Style
	mu     sync.Mutex
	w      io.Writer
	tty    bool
	clock  func() time.Time
	status string
}

// New returns a UI writing to w, styled for w.
func New(w io.Writer) *UI {
	return &UI{Style: StyleFor(w), w: w, tty: IsTerminal(w) && os.Getenv("TERM") != "dumb", clock: time.Now}
}

// Writer returns the underlying stream.
func (u *UI) Writer() io.Writer { return u.w }

// SetClock replaces the clock used for timestamps (for tests and demos).
func (u *UI) SetClock(f func() time.Time) { u.clock = f }

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

// Line logs one line with a timestamp and a symbol.
func (u *UI) Line(symbol, msg string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.clearStatus()
	fmt.Fprintf(u.w, "%s %s %s\n", u.Dim(u.clock().Format("15:04:05")), symbol, msg)
	u.drawStatus()
}

// Logf logs a plain line.
func (u *UI) Logf(format string, a ...any) { u.Line(u.Dim("│"), fmt.Sprintf(format, a...)) }

// Infof logs a line with an accent marker.
func (u *UI) Infof(format string, a ...any) { u.Line(u.Brand("●"), fmt.Sprintf(format, a...)) }

// Successf logs a success line.
func (u *UI) Successf(format string, a ...any) { u.Line(u.Green("✓"), fmt.Sprintf(format, a...)) }

// Warnf logs a warning line.
func (u *UI) Warnf(format string, a ...any) { u.Line(u.Yellow("!"), fmt.Sprintf(format, a...)) }

// Errorf logs an error line.
func (u *UI) Errorf(format string, a ...any) { u.Line(u.Red("✗"), fmt.Sprintf(format, a...)) }

// Hint logs an indented follow-up line, such as a command to run next.
func (u *UI) Hint(label, cmd string) { u.Line(" ", u.Dim(label)+" "+u.Bold(cmd)) }

// Prompt writes a question without a trailing newline.
func (u *UI) Prompt(msg string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.clearStatus()
	fmt.Fprintf(u.w, "%s %s ", u.Yellow("?"), msg)
}

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
		return Sleep(ctx, d)
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

// Sleep sleeps for d or until ctx is done.
func Sleep(ctx context.Context, d time.Duration) error {
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

// FormatDuration renders 19h16m, 7m05s, 42s or 0.4s.
func FormatDuration(d time.Duration) string {
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

// Sparkline renders values as block characters, averaged into at most width
// columns.
func Sparkline(vals []int, width int) string {
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
	maxV := 0.0
	for i := range buckets {
		if sizes[i] > 0 {
			buckets[i] /= float64(sizes[i])
		}
		if buckets[i] > maxV {
			maxV = buckets[i]
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

// PadRight pads s to n visible cells, ignoring ANSI escapes.
func PadRight(s string, n int) string {
	if w := lipgloss.Width(s); w < n {
		return s + strings.Repeat(" ", n-w)
	}
	return s
}
