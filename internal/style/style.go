// Package style is prizm's single source of visual language. Every
// user-facing line — plain text from the CLI, rendered screens from the TUI —
// uses these glyphs, colours and widths, so the tool looks like one tool.
//
// Colour is drawn from the terminal's own 16-colour palette rather than fixed
// hex values. A user has already chosen colours they can read; a tool that
// hardcodes its own fights that choice, and looks wrong in every scheme but
// the one it was designed against. It also means prizm degrades correctly on
// a 16-colour terminal, and Lip Gloss drops colour entirely when the output
// is not a terminal or NO_COLOR is set.
package style

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// The palette, as ANSI indices. Colour is spent on one thing — outcome — and
// everything else is carried by weight and dimming, so nothing competes with
// a red for attention.
var (
	Red    = lipgloss.Color("1")
	Green  = lipgloss.Color("2")
	Yellow = lipgloss.Color("3")
	Cyan   = lipgloss.Color("6")
	Base   = lipgloss.Color("0") // for text on an inverted badge
)

// MinWidth is the narrowest a name column gets. Wider names push it out.
const MinWidth = 12

var (
	// A heading is bold and uncoloured: it is structure, not status.
	headingStyle = lipgloss.NewStyle().Bold(true)
	detailStyle  = lipgloss.NewStyle().Faint(true)
	hintStyle    = lipgloss.NewStyle().Faint(true).Italic(true)
	alertStyle   = lipgloss.NewStyle().Bold(true).Foreground(Red)

	okStyle     = lipgloss.NewStyle().Foreground(Green)
	failStyle   = lipgloss.NewStyle().Bold(true).Foreground(Red)
	warnStyle   = lipgloss.NewStyle().Foreground(Yellow)
	addStyle    = lipgloss.NewStyle().Foreground(Green)
	removeStyle = lipgloss.NewStyle().Foreground(Red)
	changeStyle = lipgloss.NewStyle().Foreground(Yellow)
	plainStyle  = lipgloss.NewStyle()
)

// Mark is the leading glyph on a status line.
type Mark int

const (
	// OK is a completed action.
	OK Mark = iota
	// Fail is an action that did not happen.
	Fail
	// Warn is something that happened but deserves attention.
	Warn
	// Add is a key that appeared.
	Add
	// Remove is a key that went away.
	Remove
	// Change is a key whose value differs.
	Change
	// Same is an unchanged item.
	Same
	// Ask is something prizm will not decide on its own.
	Ask
)

// Glyph renders the mark.
func (m Mark) Glyph() string {
	switch m {
	case OK:
		return okStyle.Render("✓")
	case Fail:
		return failStyle.Render("✗")
	case Warn:
		return warnStyle.Render("⚠")
	case Add:
		return addStyle.Render("+")
	case Remove:
		return removeStyle.Render("-")
	case Change:
		return changeStyle.Render("~")
	case Same:
		return plainStyle.Render("=")
	case Ask:
		return warnStyle.Render("?")
	}
	return " "
}

// Column is a measured name-column width. Take one with WidthOf so a long
// name like "search-svc" widens the column instead of pushing every
// other row's detail out of alignment.
type Column int

// WidthOf measures the widest name, never going below MinWidth.
func WidthOf(names []string) Column {
	w := MinWidth
	for _, n := range names {
		if n := lipgloss.Width(n); n > w {
			w = n
		}
	}
	return Column(w)
}

// Row is the standard status line: a mark, a name padded to the column, and
// a dim detail.
func (c Column) Row(m Mark, name, detail string) string {
	line := m.Glyph() + " " + pad(name, int(c))
	if detail == "" {
		return strings.TrimRight(line, " ")
	}
	return line + " " + detailStyle.Render(detail)
}

// Field is a Row without a mark, for listings that are not outcomes.
func (c Column) Field(name, detail string) string {
	return "  " + pad(name, int(c)) + " " + detailStyle.Render(detail)
}

// Row renders at the default width, for one-off lines with nothing to align to.
func Row(m Mark, name, detail string) string {
	return Column(MinWidth).Row(m, name, detail)
}

func pad(s string, n int) string {
	if fill := n - lipgloss.Width(s); fill > 0 {
		return s + strings.Repeat(" ", fill)
	}
	return s
}

// Heading names a group or a section.
func Heading(s string) string { return headingStyle.Render(s) }

// Detail is secondary text: paths, values, counts.
func Detail(s string) string { return detailStyle.Render(s) }

// Hint points at the next command to run.
func Hint(s string) string { return hintStyle.Render(s) }

// Alert is for text that should stop someone.
func Alert(s string) string { return alertStyle.Render(s) }

// tagColors maps a workflow tag to the colour of its badge.
var tagColors = map[string]lipgloss.TerminalColor{
	"prod":  Red,
	"qa":    Yellow,
	"local": Cyan,
}

// TagColor returns a tag's colour, or nil for an unknown or empty tag.
func TagColor(tag string) lipgloss.TerminalColor {
	if c, ok := tagColors[tag]; ok {
		return c
	}
	return nil
}

// Tag renders a workflow tag as an inverted badge.
//
// Inversion rather than coloured text is deliberate: a tag is the one thing
// on screen someone should be able to find without reading, and a filled
// block survives a colour scheme that mangles hues in a way that coloured
// text does not.
func Tag(tag string) string {
	if tag == "" {
		return ""
	}

	c := TagColor(tag)
	if c == nil {
		return detailStyle.Render(tag)
	}
	return lipgloss.NewStyle().Bold(true).Foreground(Base).Background(c).Render(" " + tag + " ")
}
