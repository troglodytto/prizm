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

func TestHistoryOpensOnTheNewestVersion(t *testing.T) {
	m := newHistoryModel("k/auth", timeline())

	// ⏎ is the likeliest first keystroke, so whatever the cursor rests on is
	// effectively the default action. It must be the no-op, not a revert to
	// the oldest recorded state.
	if m.cursor != len(timeline())-1 {
		t.Fatalf("cursor = %d, want the newest (%d)", m.cursor, len(timeline())-1)
	}
	if got, _ := m.update(press(tea.KeyEnter)).Result(); !got.Current {
		t.Error("pressing enter immediately must select the current state")
	}
}

func TestHistoryScrubsWithinBounds(t *testing.T) {
	m := newHistoryModel("k/auth", timeline())

	for i := 0; i < 5; i++ {
		m = m.update(press(tea.KeyLeft))
	}
	if m.cursor != 0 {
		t.Errorf("cursor = %d, want it pinned at the oldest", m.cursor)
	}

	for i := 0; i < 9; i++ {
		m = m.update(press(tea.KeyRight))
	}
	if m.cursor != len(timeline())-1 {
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
		update(press(tea.KeyLeft)).
		update(press(tea.KeyEnter))

	got, picked := m.Result()
	if !picked || got.ID != 2 {
		t.Errorf("got id=%d picked=%v, want the version under the cursor", got.ID, picked)
	}
}

func TestHistoryShowsTheDiffAgainstLive(t *testing.T) {
	// Scrub back to a version that differs; the newest is the live state and
	// has nothing to show.
	m := newHistoryModel("k/auth", timeline()).
		update(press(tea.KeyLeft)).update(press(tea.KeyLeft))
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
