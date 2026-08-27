// Package style is prizm's single source of visual language. Every user-facing
// line — plain text from the CLI, rendered screens from the TUI — uses these
// glyphs, colours and widths, so the tool looks like one tool.
//
// Lip Gloss disables colour automatically when the output is not a terminal
// and honours NO_COLOR, so piped output and tests see plain text.
package style

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// The palette. Exported so the TUI theme extends it rather than inventing a
// second set of colours.
var (
	Accent  = lipgloss.AdaptiveColor{Light: "#5A189A", Dark: "#C77DFF"}
	Success = lipgloss.AdaptiveColor{Light: "#1B4332", Dark: "#95D5B2"}
	Danger  = lipgloss.AdaptiveColor{Light: "#9D0208", Dark: "#FF758F"}
	Caution = lipgloss.AdaptiveColor{Light: "#7F5539", Dark: "#E9C46A"}
	Neutral = lipgloss.AdaptiveColor{Light: "#6C757D", Dark: "#6C757D"}
)

// NameWidth is the column every status line aligns its subject in.
const NameWidth = 14

var (
	headingStyle = lipgloss.NewStyle().Bold(true).Foreground(Accent)
	detailStyle  = lipgloss.NewStyle().Faint(true)
	hintStyle    = lipgloss.NewStyle().Faint(true)
	dangerStyle  = lipgloss.NewStyle().Bold(true).Foreground(Danger)

	okStyle      = lipgloss.NewStyle().Foreground(Success)
	failStyle    = lipgloss.NewStyle().Bold(true).Foreground(Danger)
	warnStyle    = lipgloss.NewStyle().Foreground(Caution)
	changeStyle  = lipgloss.NewStyle().Foreground(Caution)
	addStyle     = lipgloss.NewStyle().Foreground(Success)
	removeStyle  = lipgloss.NewStyle().Foreground(Danger)
	neutralStyle = lipgloss.NewStyle()
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
		return neutralStyle.Render("=")
	case Ask:
		return warnStyle.Render("?")
	}
	return " "
}

// Row is the standard status line: a mark, a subject padded to NameWidth, and
// a dim detail. A name longer than the column pushes the detail rather than
// being truncated — a silently cut repo name is worse than a ragged line.
func Row(m Mark, name, detail string) string {
	padded := name
	if pad := NameWidth - lipgloss.Width(name); pad > 0 {
		padded += strings.Repeat(" ", pad)
	}

	line := m.Glyph() + " " + padded
	if detail == "" {
		return strings.TrimRight(line, " ")
	}
	return line + " " + detailStyle.Render(detail)
}

// Heading names a group or a section.
func Heading(s string) string { return headingStyle.Render(s) }

// Detail is secondary text: paths, values, counts.
func Detail(s string) string { return detailStyle.Render(s) }

// Hint points at the next command to run.
func Hint(s string) string { return hintStyle.Render(s) }

// Alert is for text that should stop someone.
func Alert(s string) string { return dangerStyle.Render(s) }

// tagColors is the semantic palette. Red means production everywhere in prizm:
// in a status line, in a picker badge, and in a prod confirmation.
var tagColors = map[string]lipgloss.TerminalColor{
	"prod":  Danger,
	"qa":    Caution,
	"local": Success,
}

// TagColor returns a tag's colour. Unknown and empty tags share the neutral one.
func TagColor(tag string) lipgloss.TerminalColor {
	if c, ok := tagColors[tag]; ok {
		return c
	}
	return Neutral
}

// Tag renders a workflow tag, or nothing for an untagged workflow.
func Tag(tag string) string {
	if tag == "" {
		return ""
	}
	return lipgloss.NewStyle().Foreground(TagColor(tag)).Render(tag)
}
