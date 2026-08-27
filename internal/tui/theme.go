package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/troglodytto/prizm/internal/style"
)

// The TUI draws from internal/style's palette rather than defining its own,
// so a prod badge is the same red in a picker as in `prizm status`.
// A border around a picker reads as a dialog box from another decade, and it
// wraps badly when rows differ in width. Selection is shown with a left
// accent bar instead — the row is marked where the eye already is.
var (
	titleStyle     = lipgloss.NewStyle().Bold(true).Foreground(style.Cyan)
	contextStyle   = lipgloss.NewStyle().Faint(true)
	barStyle       = lipgloss.NewStyle().Foreground(style.Cyan)
	selectedStyle  = lipgloss.NewStyle().Bold(true)
	itemStyle      = lipgloss.NewStyle()
	checkedStyle   = lipgloss.NewStyle().Bold(true).Foreground(style.Green)
	uncheckedStyle = lipgloss.NewStyle().Faint(true)
	dimStyle       = lipgloss.NewStyle().Faint(true)
	countStyle     = lipgloss.NewStyle().Foreground(style.Green)
	keyStyle       = lipgloss.NewStyle().Bold(true)
	helpStyle      = lipgloss.NewStyle().Faint(true)
	filterStyle    = lipgloss.NewStyle().Foreground(style.Yellow)
)

// bar marks the row under the cursor.
const (
	bar       = "▌ "
	noBar     = "  "
	checked   = "●"
	unchecked = "○"
)

// title renders "Subject · context" — the subject in accent, the context dim.
func title(subject, context string) string {
	out := titleStyle.Render(subject)
	if context != "" {
		out += contextStyle.Render("  ·  " + context)
	}
	return out
}

// help renders a key hint line: bold keys, dim labels, separated by dots.
func help(pairs ...string) string {
	var b strings.Builder
	for i := 0; i < len(pairs)-1; i += 2 {
		if i > 0 {
			b.WriteString(dimStyle.Render("  ·  "))
		}
		b.WriteString(keyStyle.Render(pairs[i]) + helpStyle.Render(" "+pairs[i+1]))
	}
	return b.String()
}

// Option is one selectable row.
type Option struct {
	Value string
	Label string
	Desc  string
	Tag   string
}

// badge renders a workflow tag using the shared semantic palette.
func badge(tag string) string {
	if tag == "" {
		return ""
	}
	return "  " + style.Tag(tag)
}
