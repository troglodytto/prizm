package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type pickOneModel struct {
	title     string
	context   string
	options   []Option
	cursor    int
	filter    string
	filtering bool
	done      bool
	chose     bool
}

func newPickOneModel(title string, options []Option) pickOneModel {
	return pickOneModel{title: title, options: options}
}

// visible returns the options matching the filter, in caller order — the
// caller's order is the directory-relevance ranking, so it must survive.
func (m pickOneModel) visible() []Option {
	if m.filter == "" {
		return m.options
	}

	needle := strings.ToLower(m.filter)
	out := make([]Option, 0, len(m.options))
	for _, o := range m.options {
		if strings.Contains(strings.ToLower(o.Label+" "+o.Desc), needle) {
			out = append(out, o)
		}
	}
	return out
}

func (m pickOneModel) current() (Option, bool) {
	visible := m.visible()
	if m.cursor < 0 || m.cursor >= len(visible) {
		return Option{}, false
	}
	return visible[m.cursor], true
}

// Result reports the chosen value, and whether anything was chosen.
func (m pickOneModel) Result() (string, bool) {
	if !m.chose {
		return "", false
	}
	o, ok := m.current()
	return o.Value, ok
}

func (m pickOneModel) Init() tea.Cmd { return nil }

func (m pickOneModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next := m.update(msg)
	if next.done {
		return next, tea.Quit
	}
	return next, nil
}

// update is the pure transition, and the seam the tests drive — no terminal
// is allocated to test any of this.
func (m pickOneModel) update(msg tea.Msg) pickOneModel {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m
	}

	if m.filtering {
		return m.updateFiltering(key)
	}

	switch key.Type {
	case tea.KeyCtrlC, tea.KeyEsc:
		m.done = true
	case tea.KeyUp:
		m = m.move(-1)
	case tea.KeyDown:
		m = m.move(1)
	case tea.KeyEnter:
		if _, ok := m.current(); ok {
			m.done, m.chose = true, true
		}
	case tea.KeyRunes:
		switch key.Runes[0] {
		case 'k':
			m = m.move(-1)
		case 'j':
			m = m.move(1)
		case '/':
			m.filtering = true
		case 'q':
			m.done = true
		}
	}
	return m
}

func (m pickOneModel) updateFiltering(key tea.KeyMsg) pickOneModel {
	switch key.Type {
	case tea.KeyCtrlC:
		m.done = true
	case tea.KeyEsc:
		// Escape leaves the filter, not the picker.
		m.filter, m.filtering, m.cursor = "", false, 0
	case tea.KeyEnter:
		if _, ok := m.current(); ok {
			m.done, m.chose = true, true
		}
	case tea.KeyUp:
		m = m.move(-1)
	case tea.KeyDown:
		m = m.move(1)
	case tea.KeyBackspace:
		if m.filter != "" {
			m.filter = m.filter[:len(m.filter)-1]
			m.cursor = 0
		}
	case tea.KeyRunes:
		m.filter += string(key.Runes)
		m.cursor = 0
	}
	return m
}

func (m pickOneModel) move(delta int) pickOneModel {
	next := m.cursor + delta
	if next < 0 {
		next = 0
	}
	if limit := len(m.visible()) - 1; next > limit {
		next = limit
	}
	if next < 0 {
		next = 0
	}
	m.cursor = next
	return m
}

func (m pickOneModel) View() string {
	// Bubble Tea renders the final View on quit. A picker is a question, not
	// a result — once answered it should leave nothing behind, so the
	// command's own output is what remains on screen.
	if m.done {
		return ""
	}

	var b strings.Builder
	b.WriteString("\n  " + title(m.title, m.context) + "\n\n")

	visible := m.visible()
	if len(visible) == 0 {
		b.WriteString("  " + noBar + dimStyle.Render("nothing matches "+m.filter) + "\n")
	}

	labelWidth := 0
	for _, o := range visible {
		if n := lipgloss.Width(o.Label); n > labelWidth {
			labelWidth = n
		}
	}

	for i, o := range visible {
		selected := i == m.cursor
		labelStyle, descStyle := rowStyles(selected)

		marker := noBar
		if selected {
			marker = barStyle.Render(bar)
		}

		row := "  " + marker + labelStyle.Render(o.Label) + padLabel(o.Label, labelWidth)
		if o.Desc != "" {
			row += "   " + descStyle.Render(o.Desc)
		}
		if o.Tag != "" {
			row += "  " + tagStyle(o.Tag)
		}
		b.WriteString(row + "\n")
	}

	b.WriteString("\n  ")
	if m.filtering {
		b.WriteString(filterStyle.Render("/"+m.filter+"▏") + "   " +
			help("⏎", "select", "esc", "clear"))
	} else {
		b.WriteString(help("↑↓", "move", "/", "filter", "⏎", "select", "esc", "cancel"))
	}
	return b.String() + "\n"
}

// PickOne shows a filterable list and returns the chosen option's value.
func PickOne(heading, context string, options []Option) (string, error) {
	m := newPickOneModel(heading, options)
	m.context = context

	final, err := run(m)
	if err != nil {
		return "", err
	}

	value, chose := final.(pickOneModel).Result()
	if !chose {
		return "", ErrCancelled
	}
	return value, nil
}
