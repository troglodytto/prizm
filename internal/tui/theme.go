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

// selectionBG is the row highlight. Every segment of a selected row sets it
// explicitly: a nested style that resets colour would punch a hole in the
// band, so the background cannot be applied once around the outside.
var selectionBG = lipgloss.Color("8")

// rowStyles returns the styles for one row, background-aware so a highlighted
// row stays a continuous band.
func rowStyles(selected bool) (label, desc lipgloss.Style) {
	if !selected {
		return itemStyle, dimStyle
	}
	return lipgloss.NewStyle().Bold(true).Background(selectionBG),
		lipgloss.NewStyle().Faint(true).Background(selectionBG)
}

// tagOn renders a tag inside a row, carrying the row's background so the
// highlight is not broken by it.
func tagOn(tag string, selected bool) string {
	if tag == "" {
		return ""
	}

	s := lipgloss.NewStyle().Bold(true).Foreground(style.TagColor(tag))
	if selected {
		s = s.Background(selectionBG)
	}
	return s.Render("(" + tag + ")")
}

// padTo fills to width so a highlighted row is a rectangle. Unselected rows
// get nothing — padding them only leaves trailing whitespace behind.
func padTo(s string, width int, selected bool) string {
	if !selected {
		return ""
	}

	fill := width - lipgloss.Width(s)
	if fill <= 0 {
		return ""
	}
	return lipgloss.NewStyle().Background(selectionBG).Render(strings.Repeat(" ", fill))
}

// padLabel aligns the label column on every row, highlighted or not.
func padLabel(s string, width int, selected bool) string {
	fill := width - lipgloss.Width(s)
	if fill <= 0 {
		return ""
	}

	spaces := strings.Repeat(" ", fill)
	if selected {
		return lipgloss.NewStyle().Background(selectionBG).Render(spaces)
	}
	return spaces
}

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
