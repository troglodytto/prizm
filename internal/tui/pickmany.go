package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type pickManyModel struct {
	title     string
	options   []Option
	selected  map[string]bool
	cursor    int
	done      bool
	submitted bool
}

func newPickManyModel(title string, options []Option, preselected []string) pickManyModel {
	selected := make(map[string]bool, len(preselected))
	for _, v := range preselected {
		selected[v] = true
	}
	return pickManyModel{title: title, options: options, selected: selected}
}

// selectedValues returns ticked values in the caller's order, so a workflow's
// repo list does not depend on the order the user clicked.
func (m pickManyModel) selectedValues() []string {
	var out []string
	for _, o := range m.options {
		if m.selected[o.Value] {
			out = append(out, o.Value)
		}
	}
	return out
}

// Result reports the selection, and whether it was submitted or cancelled.
func (m pickManyModel) Result() ([]string, bool) {
	if !m.submitted {
		return nil, false
	}
	return m.selectedValues(), true
}

func (m pickManyModel) Init() tea.Cmd { return nil }

func (m pickManyModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next := m.update(msg)
	if next.done {
		return next, tea.Quit
	}
	return next, nil
}

func (m pickManyModel) update(msg tea.Msg) pickManyModel {
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
	case tea.KeySpace:
		m = m.toggle()
	case tea.KeyEnter:
		m.done, m.submitted = true, true
	case tea.KeyRunes:
		switch key.Runes[0] {
		case 'k':
			m = m.move(-1)
		case 'j':
			m = m.move(1)
		case ' ':
			m = m.toggle()
		case 'a':
			m = m.toggleAll()
		case 'q':
			m.done = true
		}
	}
	return m
}

func (m pickManyModel) toggle() pickManyModel {
	if m.cursor < 0 || m.cursor >= len(m.options) {
		return m
	}

	next := make(map[string]bool, len(m.selected)+1)
	for k, v := range m.selected {
		next[k] = v
	}
	value := m.options[m.cursor].Value
	next[value] = !next[value]

	m.selected = next
	return m
}

// toggleAll selects everything, or clears it when everything is selected.
func (m pickManyModel) toggleAll() pickManyModel {
	allOn := true
	for _, o := range m.options {
		if !m.selected[o.Value] {
			allOn = false
			break
		}
	}

	next := make(map[string]bool, len(m.options))
	for _, o := range m.options {
		next[o.Value] = !allOn
	}

	m.selected = next
	return m
}

func (m pickManyModel) move(delta int) pickManyModel {
	next := m.cursor + delta
	if next < 0 {
		next = 0
	}
	if next > len(m.options)-1 {
		next = len(m.options) - 1
	}
	if next < 0 {
		next = 0
	}
	m.cursor = next
	return m
}

func (m pickManyModel) View() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render(m.title) + "\n\n")

	width := 0
	for _, o := range m.options {
		if n := len(o.Label); n > width {
			width = n
		}
	}

	for i, o := range m.options {
		cursor := "  "
		if i == m.cursor {
			cursor = cursorStyle.Render("❯ ")
		}

		box := "[ ]"
		if m.selected[o.Value] {
			box = checkedStyle.Render("[x]")
		}

		pad := strings.Repeat(" ", width-len(o.Label))
		b.WriteString(cursor + box + " " + o.Label + pad + "  " + dimStyle.Render(o.Desc) + "\n")
	}

	b.WriteString("\n" + helpStyle.Render("↑↓ move   space toggle   a all   ⏎ submit   esc cancel"))
	return frameStyle.Render(b.String())
}

// PickMany shows a checkbox list and returns the ticked values.
func PickMany(title string, options []Option, preselected []string) ([]string, error) {
	final, err := run(newPickManyModel(title, options, preselected))
	if err != nil {
		return nil, err
	}

	values, submitted := final.(pickManyModel).Result()
	if !submitted {
		return nil, ErrCancelled
	}
	return values, nil
}
