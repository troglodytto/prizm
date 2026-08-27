// Package tui is prizm's interactive layer.
//
// Every entry point is guarded by Available(), and every component returns a
// plain Go value, so call sites read like ordinary function calls rather than
// event loops. Without a terminal the caller falls back to its flag-driven
// path — a tool only a human can drive is a tool that cannot be scripted.
package tui

import (
	"errors"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"
)

// ErrCancelled means the user aborted. Callers exit quietly, not noisily.
var ErrCancelled = errors.New("cancelled")

var disabled bool

// Disable turns the interactive layer off for this process.
func Disable() { disabled = true }

// Available reports whether an interactive surface may be shown.
func Available() bool {
	if disabled || os.Getenv("PRIZM_NO_TUI") != "" {
		return false
	}
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

// run drives a model to completion.
//
// Rendering goes to stderr so a command that shows a picker and then prints a
// result stays pipeable.
func run(m tea.Model) (tea.Model, error) {
	return tea.NewProgram(m, tea.WithOutput(os.Stderr)).Run()
}
