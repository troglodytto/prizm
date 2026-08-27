package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestPickerEditKeyReturnsTheHighlightedRow(t *testing.T) {
	m := newPickOneModel("Select a workflow", opts())
	m.editable = true

	m = m.update(press(tea.KeyDown)).update(key('e'))

	value, chose := m.Result()
	if !chose || value != "staging" {
		t.Errorf("got %q chose=%v, want the row under the cursor", value, chose)
	}
	if m.action != ActionEdit {
		t.Errorf("action = %v, want ActionEdit", m.action)
	}
}

func TestPickerEditKeyIsInertWhereNothingIsEditable(t *testing.T) {
	m := newPickOneModel("Select a group", opts()).update(key('e'))

	if _, chose := m.Result(); chose {
		t.Error("`e` must do nothing when the caller did not offer it")
	}
}

func TestPickerEditKeyTypesIntoTheFilter(t *testing.T) {
	m := newPickOneModel("Select a workflow", opts())
	m.editable = true

	// Once filtering, letters are text. A keybind that ate them would make
	// the filter unusable for anything containing an `e`.
	m = m.update(key('/')).update(key('e'))

	if m.filter != "e" {
		t.Errorf("filter = %q, want the letter typed", m.filter)
	}
	if _, chose := m.Result(); chose {
		t.Error("filtering must not select")
	}
}

func TestEnterStillSelects(t *testing.T) {
	m := newPickOneModel("Select a workflow", opts())
	m.editable = true
	m = m.update(press(tea.KeyEnter))

	value, chose := m.Result()
	if !chose || value != "local" || m.action != ActionSelect {
		t.Errorf("got %q chose=%v action=%v, want a plain selection", value, chose, m.action)
	}
}
