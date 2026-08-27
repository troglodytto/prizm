package tui

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/troglodytto/prizm/internal/style"
)

// Version is one point on a scope's timeline.
type Version struct {
	ID      int64
	When    string   // "12m ago"
	At      string   // "14:32" — relative ages tie during a burst of edits
	Source  string   // what wrote it
	Note    string   // which keys moved
	Current bool     // this is the live state
	Changes []Change // how it differs from the live state
}

// Change is one key's difference between a version and the live state.
type Change struct {
	Key  string
	Mark rune // '+' present then, gone now · '-' absent then · '~' different
	From string
	To   string
}

type historyModel struct {
	title     string
	versions  []Version
	cursor    int
	done      bool
	submitted bool
}

func newHistoryModel(title string, versions []Version) historyModel {
	return historyModel{title: title, versions: versions}
}

// Result reports the chosen version, and whether the user picked one.
func (m historyModel) Result() (Version, bool) {
	if !m.submitted || len(m.versions) == 0 {
		return Version{}, false
	}
	return m.versions[m.cursor], true
}

func (m historyModel) Init() tea.Cmd { return nil }

func (m historyModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next := m.update(msg)
	if next.done {
		return next, tea.Quit
	}
	return next, nil
}

func (m historyModel) update(msg tea.Msg) historyModel {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m
	}

	switch key.Type {
	case tea.KeyCtrlC, tea.KeyEsc:
		m.done = true
	case tea.KeyLeft:
		m = m.move(-1)
	case tea.KeyRight:
		m = m.move(1)
	case tea.KeyEnter:
		// Restoring the live state is a no-op, not a mistake — accept it and
		// let the caller report that nothing changed.
		m.done, m.submitted = true, true
	case tea.KeyRunes:
		switch key.Runes[0] {
		case 'h':
			m = m.move(-1)
		case 'l':
			m = m.move(1)
		}
	}
	return m
}

// move scrubs along the timeline. Left is older, matching the way the strip
// is drawn — newest on the right, where the cursor starts.
func (m historyModel) move(delta int) historyModel {
	next := m.cursor + delta
	if next < 0 || next > len(m.versions)-1 {
		return m
	}
	m.cursor = next
	return m
}

func (m historyModel) View() string {
	if m.done || len(m.versions) == 0 {
		return ""
	}

	v := m.versions[m.cursor]

	var b strings.Builder
	b.WriteString("\n  " + title(m.title, m.position()) + "\n\n")
	b.WriteString("  " + m.strip() + "\n\n")

	label := v.Source
	if v.Note != "" {
		label += dimStyle.Render("  ·  " + v.Note)
	}
	when := selectedStyle.Render(v.When)
	if v.At != "" {
		when += dimStyle.Render("  " + v.At)
	}
	b.WriteString("  " + when + "   " + label + "\n\n")
	b.WriteString(m.changes(v))
	b.WriteString("\n  " + help("←→", "scrub", "⏎", "restore this", "esc", "cancel") + "\n")
	return b.String()
}

// position reads "3 of 9 · current" — cheap orientation when the strip is
// wider than the terminal and has been clipped.
func (m historyModel) position() string {
	out := ordinal(m.cursor+1, len(m.versions))
	if m.versions[m.cursor].Current {
		out += "  ·  current"
	}
	return out
}

// strip draws the timeline itself: one dot per version, oldest to newest.
// The point of a carousel is seeing where you are in the whole history, not
// just the entry you are on.
func (m historyModel) strip() string {
	var b strings.Builder
	for i, v := range m.versions {
		if i > 0 {
			b.WriteString(dimStyle.Render("──"))
		}
		switch {
		case i == m.cursor:
			b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(style.Cyan).Render(checked))
		case v.Current:
			b.WriteString(lipgloss.NewStyle().Foreground(style.Green).Render(checked))
		default:
			b.WriteString(uncheckedStyle.Render(unchecked))
		}
	}
	return b.String()
}

// changes renders the diff between the highlighted version and the live
// state — the question being asked is "what do I get back", not "what
// happened that day".
func (m historyModel) changes(v Version) string {
	if v.Current {
		return "      " + dimStyle.Render("this is the current state") + "\n"
	}
	if len(v.Changes) == 0 {
		return "      " + dimStyle.Render("identical to the current state") + "\n"
	}

	width := 0
	for _, c := range v.Changes {
		if w := lipgloss.Width(c.Key); w > width {
			width = w
		}
	}

	var b strings.Builder
	for _, c := range v.Changes {
		b.WriteString("      " + changeMark(c.Mark) + " " +
			c.Key + padLabel(c.Key, width) + "   " + dimStyle.Render(changeDetail(c)) + "\n")
	}
	return b.String()
}

func changeMark(mark rune) string {
	switch mark {
	case '+':
		return lipgloss.NewStyle().Foreground(style.Green).Render("+")
	case '-':
		return lipgloss.NewStyle().Foreground(style.Red).Render("-")
	default:
		return lipgloss.NewStyle().Foreground(style.Yellow).Render("~")
	}
}

// changeDetail describes a restore in the direction it would happen: what
// the value becomes, not what it was.
func changeDetail(c Change) string {
	switch c.Mark {
	case '+':
		return "restored · " + c.From
	case '-':
		return "removed · currently " + c.To
	default:
		return c.To + " → " + c.From
	}
}

func ordinal(n, total int) string {
	return strconv.Itoa(n) + " of " + strconv.Itoa(total)
}

// History shows a scope's timeline and returns the version to restore.
func History(title string, versions []Version) (Version, bool, error) {
	final, err := run(newHistoryModel(title, versions))
	if err != nil {
		return Version{}, false, err
	}

	model, ok := final.(historyModel)
	if !ok {
		return Version{}, false, nil
	}

	chosen, picked := model.Result()
	return chosen, picked, nil
}
