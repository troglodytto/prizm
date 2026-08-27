package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// press builds a non-rune key event; the package's `key` helper covers runes.
func press(t tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: t} }

func timeline() []Version {
	return []Version{
		{ID: 1, When: "2h ago", Source: "var", Changes: []Change{{Key: "PORT", Mark: '~', From: "4000", To: "9999"}}},
		{ID: 2, When: "1h ago", Source: "sync", Changes: []Change{{Key: "NEW", Mark: '-', To: "x"}}},
		{ID: 3, When: "just now", Source: "var", Current: true},
	}
}

func TestHistoryScrubsWithinBounds(t *testing.T) {
	m := newHistoryModel("k/auth", timeline())

	// The cursor starts on the oldest entry, and left cannot walk off it.
	m = m.update(press(tea.KeyLeft)).update(press(tea.KeyLeft))
	if m.cursor != 0 {
		t.Errorf("cursor = %d, want it pinned at 0", m.cursor)
	}

	for i := 0; i < 5; i++ {
		m = m.update(press(tea.KeyRight))
	}
	if m.cursor != 2 {
		t.Errorf("cursor = %d, want it pinned at the newest", m.cursor)
	}
}

func TestHistoryEscCancels(t *testing.T) {
	m := newHistoryModel("k/auth", timeline()).update(press(tea.KeyEsc))
	if _, picked := m.Result(); picked {
		t.Error("esc must not restore anything")
	}
}

func TestHistoryEnterReturnsTheHighlightedVersion(t *testing.T) {
	m := newHistoryModel("k/auth", timeline()).
		update(press(tea.KeyRight)).
		update(press(tea.KeyEnter))

	got, picked := m.Result()
	if !picked || got.ID != 2 {
		t.Errorf("got id=%d picked=%v, want the version under the cursor", got.ID, picked)
	}
}

func TestHistoryShowsTheDiffAgainstLive(t *testing.T) {
	m := newHistoryModel("k/auth", timeline())
	view := m.View()

	if !strings.Contains(view, "PORT") || !strings.Contains(view, "9999 → 4000") {
		t.Errorf("view = %q, want the restore direction: current → restored", view)
	}
}

func TestHistoryLeavesNothingBehind(t *testing.T) {
	m := newHistoryModel("k/auth", timeline()).update(press(tea.KeyEnter))
	if m.View() != "" {
		t.Errorf("view = %q, want empty once done", m.View())
	}
}

func TestHistoryHandlesAnEmptyTimeline(t *testing.T) {
	m := newHistoryModel("k/auth", nil).update(press(tea.KeyRight)).update(press(tea.KeyEnter))
	if _, picked := m.Result(); picked {
		t.Error("an empty timeline has nothing to pick")
	}
	if m.View() != "" {
		t.Errorf("view = %q, want empty", m.View())
	}
}
