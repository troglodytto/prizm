package tui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/troglodytto/prizm/internal/style"
)

// The TUI draws from internal/style's palette rather than defining its own,
// so a prod badge is the same red in a picker as in `prizm status`.
var (
	titleStyle    = lipgloss.NewStyle().Bold(true)
	cursorStyle   = lipgloss.NewStyle().Bold(true).Foreground(style.Cyan)
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(style.Green)
	checkedStyle  = lipgloss.NewStyle().Foreground(style.Green)
	dimStyle      = lipgloss.NewStyle().Faint(true)
	helpStyle     = lipgloss.NewStyle().Faint(true).Italic(true)
	frameStyle    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
)

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
