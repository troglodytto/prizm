package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func sharedRow() []ResolveRow {
	return []ResolveRow{{
		Key:          "MONGO_URI",
		Detail:       "…/local → …/scratch",
		Note:         "comes from ${_PRIZM_MONGO_URI} in shared:infra",
		Choices:      []string{"update the shared value", "pin to auth only", "skip"},
		Consequences: []string{"also changes ai, backend, frontend", "", ""},
	}}
}

func TestConsequenceFollowsTheChoice(t *testing.T) {
	m := newResolveModel("platform/auth ← .env", sharedRow())

	if !strings.Contains(m.View(), "also changes") {
		t.Error("updating the shared value must warn that it reaches other repos")
	}

	// Cycling to "pin to auth only" must drop the warning: it says the
	// opposite of what pinning does.
	m = m.update(tea.KeyMsg{Type: tea.KeyRight})
	view := m.View()
	if !strings.Contains(view, "pin to auth only") {
		t.Fatalf("view = %q, want the cycled choice", view)
	}
	if strings.Contains(view, "also changes") {
		t.Error("pinning is confined to this repo — the warning must not follow it")
	}

	m = m.update(tea.KeyMsg{Type: tea.KeyRight})
	if strings.Contains(m.View(), "also changes") {
		t.Error("skipping changes nothing anywhere")
	}
}

func TestConsequencesMayBeOmittedEntirely(t *testing.T) {
	rows := []ResolveRow{{Key: "PORT", Detail: "4000 → 9999", Choices: []string{"apply", "skip"}}}
	m := newResolveModel("platform/auth ← .env", rows)

	// A short or absent Consequences slice must not panic the renderer.
	if view := m.update(tea.KeyMsg{Type: tea.KeyRight}).View(); !strings.Contains(view, "skip") {
		t.Errorf("view = %q, want it to render without consequences", view)
	}
}
