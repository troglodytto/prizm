package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/troglodytto/prizm/internal/style"
)

// ResolveRow is one decision the user has to make.
type ResolveRow struct {
	Key         string
	Detail      string // what changed
	Note        string // why it needs a decision
	Consequence string // what else moves if they say yes
	Choices     []string
}

type resolveModel struct {
	title     string
	rows      []ResolveRow
	chosen    []int
	cursor    int
	done      bool
	submitted bool
}

func newResolveModel(title string, rows []ResolveRow) resolveModel {
	return resolveModel{title: title, rows: rows, chosen: make([]int, len(rows))}
}

func (m resolveModel) choices() []int { return m.chosen }

// Result reports the chosen index per row, and whether it was submitted.
func (m resolveModel) Result() ([]int, bool) {
	if !m.submitted {
		return nil, false
	}
	return m.chosen, true
}

func (m resolveModel) Init() tea.Cmd { return nil }

func (m resolveModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next := m.update(msg)
	if next.done {
		return next, tea.Quit
	}
	return next, nil
}

func (m resolveModel) update(msg tea.Msg) resolveModel {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m
	}

	switch key.Type {
	case tea.KeyCtrlC, tea.KeyEsc:
		m.done = true
	case tea.KeyUp:
		m = m.move(-1)
	case tea.KeyDown:
		m = m.move(1)
	case tea.KeyLeft:
		m = m.cycle(-1)
	case tea.KeyRight, tea.KeySpace:
		m = m.cycle(1)
	case tea.KeyEnter:
		m.done, m.submitted = true, true
	case tea.KeyRunes:
		switch key.Runes[0] {
		case 'k':
			m = m.move(-1)
		case 'j':
			m = m.move(1)
		case 'h':
			m = m.cycle(-1)
		case 'l':
			m = m.cycle(1)
		}
	}
	return m
}

func (m resolveModel) move(delta int) resolveModel {
	if len(m.rows) == 0 {
		return m
	}

	next := m.cursor + delta
	if next < 0 {
		next = 0
	}
	if next > len(m.rows)-1 {
		next = len(m.rows) - 1
	}
	m.cursor = next
	return m
}

func (m resolveModel) cycle(delta int) resolveModel {
	if len(m.rows) == 0 {
		return m
	}

	count := len(m.rows[m.cursor].Choices)
	if count == 0 {
		return m
	}

	next := make([]int, len(m.chosen))
	copy(next, m.chosen)
	next[m.cursor] = ((next[m.cursor]+delta)%count + count) % count

	m.chosen = next
	return m
}

func (m resolveModel) View() string {
	if m.done {
		return ""
	}

	var b strings.Builder
	b.WriteString("\n  " + title(m.title, "") + "\n\n")

	for i, row := range m.rows {
		selected := i == m.cursor
		labelStyle, descStyle := rowStyles(selected)

		marker := noBar
		if selected {
			marker = barStyle.Render(bar)
		}
		b.WriteString("  " + marker + labelStyle.Render(row.Key) + "   " + descStyle.Render(row.Detail) + "\n")

		if row.Note != "" {
			b.WriteString("      " + dimStyle.Render(row.Note) + "\n")
		}

		choice := ""
		if len(row.Choices) > 0 {
			choice = row.Choices[m.chosen[i]]
		}

		line := "      " + chevron(selected) + choiceStyle(selected).Render(choice)
		if row.Consequence != "" {
			line += "   " + warnStyle.Render(row.Consequence)
		}
		b.WriteString(line + "\n\n")
	}

	b.WriteString("  " + help("↑↓", "row", "←→", "choose", "⏎", "apply all", "esc", "cancel") + "\n")
	return b.String()
}

// chevron marks the row whose choice the arrows will change.
func chevron(selected bool) string {
	if selected {
		return barStyle.Render("‹ ")
	}
	return "  "
}

func choiceStyle(selected bool) lipgloss.Style {
	if selected {
		return lipgloss.NewStyle().Bold(true).Foreground(style.Cyan)
	}
	return lipgloss.NewStyle().Faint(true)
}

// Resolve asks the user to choose an option for each row.
func Resolve(title string, rows []ResolveRow) ([]int, error) {
	final, err := run(newResolveModel(title, rows))
	if err != nil {
		return nil, err
	}

	chosen, submitted := final.(resolveModel).Result()
	if !submitted {
		return nil, ErrCancelled
	}
	return chosen, nil
}
