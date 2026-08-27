package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type pickOneModel struct {
	title     string
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
	var b strings.Builder

	b.WriteString(titleStyle.Render(m.title) + "\n\n")

	visible := m.visible()
	if len(visible) == 0 {
		b.WriteString(dimStyle.Render("  no matches") + "\n")
	}

	width := 0
	for _, o := range visible {
		if n := len(o.Label); n > width {
			width = n
		}
	}

	for i, o := range visible {
		cursor, label := "  ", o.Label
		if i == m.cursor {
			cursor, label = cursorStyle.Render("❯ "), selectedStyle.Render(o.Label)
		}
		pad := strings.Repeat(" ", width-len(o.Label))
		b.WriteString(cursor + label + pad + "  " + dimStyle.Render(o.Desc) + badge(o.Tag) + "\n")
	}

	b.WriteString("\n")
	if m.filtering {
		b.WriteString(helpStyle.Render(fmt.Sprintf("filter: %s   ⏎ select   esc clear", m.filter)))
	} else {
		b.WriteString(helpStyle.Render("↑↓ move   / filter   ⏎ select   esc cancel"))
	}
	return frameStyle.Render(b.String())
}

// PickOne shows a filterable list and returns the chosen option's value.
func PickOne(title string, options []Option) (string, error) {
	final, err := run(newPickOneModel(title, options))
	if err != nil {
		return "", err
	}

	value, chose := final.(pickOneModel).Result()
	if !chose {
		return "", ErrCancelled
	}
	return value, nil
}
