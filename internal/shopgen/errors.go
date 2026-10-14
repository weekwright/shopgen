// Package shopgen plans and creates realistic order history: profiles, the
// deterministic generator, the resumable run loop and purge.
package shopgen

import (
	"errors"
	"fmt"
)

// UsageError marks bad flags or arguments; the CLI exits with code 2.
type UsageError struct{ Msg string }

func (e *UsageError) Error() string { return e.Msg }

// UsageErrorf returns a *UsageError.
func UsageErrorf(format string, a ...any) error { return &UsageError{fmt.Sprintf(format, a...)} }

// ErrInterrupted is returned when the user pauses a run with Ctrl-C.
var ErrInterrupted = errors.New("interrupted")
