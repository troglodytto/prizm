package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/go-cmp/cmp"
)

func key(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

func opts() []Option {
	return []Option{
		{Value: "local", Label: "local", Desc: "auth backend", Tag: "local"},
		{Value: "staging", Label: "staging", Desc: "auth backend", Tag: "qa"},
		{Value: "production", Label: "production", Desc: "auth backend", Tag: "prod"},
	}
}

func TestPickOnePreservesCallerOrder(t *testing.T) {
	m := newPickOneModel("Workflow", opts())

	got, ok := m.current()
	if !ok || got.Value != "local" {
		t.Errorf("current() = %+v, want the first option — ranking must survive", got)
	}
}

func TestPickOneMovesAndSelects(t *testing.T) {
	m := newPickOneModel("Workflow", opts())
	m = m.update(tea.KeyMsg{Type: tea.KeyDown})
	m = m.update(key('j'))
	m = m.update(key('k'))
	m = m.update(tea.KeyMsg{Type: tea.KeyEnter})

	value, chose := m.Result()
	if !chose || value != "staging" {
		t.Errorf("Result() = (%q, %v), want (staging, true)", value, chose)
	}
}

func TestPickOneDoesNotWrap(t *testing.T) {
	m := newPickOneModel("Workflow", opts())
	m = m.update(tea.KeyMsg{Type: tea.KeyUp})
	if got, _ := m.current(); got.Value != "local" {
		t.Errorf("up at the top moved to %q", got.Value)
	}

	for i := 0; i < 10; i++ {
		m = m.update(tea.KeyMsg{Type: tea.KeyDown})
	}
	if got, _ := m.current(); got.Value != "production" {
		t.Errorf("down past the end landed on %q", got.Value)
	}
}

func TestPickOneFilters(t *testing.T) {
	m := newPickOneModel("Workflow", opts())
	m = m.update(key('/'))
	for _, r := range "prod" {
		m = m.update(key(r))
	}

	if got := len(m.visible()); got != 1 {
		t.Fatalf("visible() = %d, want 1", got)
	}
	if got, _ := m.current(); got.Value != "production" {
		t.Errorf("current = %q, want production", got.Value)
	}
}

func TestPickOneEscapeInFilterClearsFilterNotPicker(t *testing.T) {
	m := newPickOneModel("Workflow", opts())
	m = m.update(key('/'))
	m = m.update(key('q'))
	m = m.update(tea.KeyMsg{Type: tea.KeyEsc})

	if m.done {
		t.Error("escape while filtering closed the picker")
	}
	if len(m.visible()) != 3 {
		t.Error("escape while filtering did not clear the filter")
	}
}

func TestPickOneEscapeCancels(t *testing.T) {
	m := newPickOneModel("Workflow", opts())
	m = m.update(tea.KeyMsg{Type: tea.KeyEsc})

	if _, chose := m.Result(); chose {
		t.Error("Result() reported a choice after escape")
	}
}

func repoOpts() []Option {
	return []Option{
		{Value: "auth", Label: "auth"},
		{Value: "backend", Label: "backend"},
		{Value: "frontend", Label: "frontend"},
	}
}

func TestPickManyPreselection(t *testing.T) {
	m := newPickManyModel("Repos", repoOpts(), []string{"auth", "backend"})
	if diff := cmp.Diff([]string{"auth", "backend"}, m.selectedValues()); diff != "" {
		t.Errorf("selectedValues() mismatch (-want +got):\n%s", diff)
	}
}

func TestPickManyToggle(t *testing.T) {
	m := newPickManyModel("Repos", repoOpts(), nil)
	m = m.update(tea.KeyMsg{Type: tea.KeySpace})

	if diff := cmp.Diff([]string{"auth"}, m.selectedValues()); diff != "" {
		t.Errorf("after toggle (-want +got):\n%s", diff)
	}
	m = m.update(tea.KeyMsg{Type: tea.KeySpace})
	if len(m.selectedValues()) != 0 {
		t.Error("second toggle did not clear it")
	}
}

// Results come back in caller order, never click order.
func TestPickManyReturnsCallerOrder(t *testing.T) {
	m := newPickManyModel("Repos", repoOpts(), nil)
	m = m.update(tea.KeyMsg{Type: tea.KeyDown})
	m = m.update(tea.KeyMsg{Type: tea.KeyDown})
	m = m.update(tea.KeyMsg{Type: tea.KeySpace}) // frontend
	m = m.update(tea.KeyMsg{Type: tea.KeyUp})
	m = m.update(tea.KeyMsg{Type: tea.KeyUp})
	m = m.update(tea.KeyMsg{Type: tea.KeySpace}) // auth

	if diff := cmp.Diff([]string{"auth", "frontend"}, m.selectedValues()); diff != "" {
		t.Errorf("selectedValues() mismatch (-want +got):\n%s", diff)
	}
}

func TestPickManyToggleAll(t *testing.T) {
	m := newPickManyModel("Repos", repoOpts(), nil)

	m = m.update(key('a'))
	if len(m.selectedValues()) != 3 {
		t.Error("'a' did not select everything")
	}
	m = m.update(key('a'))
	if len(m.selectedValues()) != 0 {
		t.Error("'a' again did not clear everything")
	}
}

// Submitting nothing is legitimate — a workflow may cover no repos.
func TestPickManyAllowsEmptySubmission(t *testing.T) {
	m := newPickManyModel("Repos", repoOpts(), nil)
	m = m.update(tea.KeyMsg{Type: tea.KeyEnter})

	values, submitted := m.Result()
	if !submitted || len(values) != 0 {
		t.Errorf("Result() = (%v, %v), want an empty submission", values, submitted)
	}
}

func TestPickManyEscapeCancels(t *testing.T) {
	m := newPickManyModel("Repos", repoOpts(), []string{"auth"})
	m = m.update(tea.KeyMsg{Type: tea.KeyEsc})

	if _, submitted := m.Result(); submitted {
		t.Error("Result() submitted = true after escape")
	}
}

// A picker is a question, not a result. Bubble Tea renders the final View on
// quit, so anything drawn there stays on screen after the answer — the frame
// must be gone by then.
func TestPickOneLeavesNothingBehind(t *testing.T) {
	m := newPickOneModel("Workflow", opts())
	if m.View() == "" {
		t.Fatal("View() is empty before the user has answered")
	}

	m = m.update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := m.View(); got != "" {
		t.Errorf("View() after selecting = %q, want empty", got)
	}
}

func TestPickOneLeavesNothingBehindOnCancel(t *testing.T) {
	m := newPickOneModel("Workflow", opts())
	m = m.update(tea.KeyMsg{Type: tea.KeyEsc})

	if got := m.View(); got != "" {
		t.Errorf("View() after escape = %q, want empty", got)
	}
}

func TestPickManyLeavesNothingBehind(t *testing.T) {
	m := newPickManyModel("Repos", repoOpts(), nil)
	if m.View() == "" {
		t.Fatal("View() is empty before the user has answered")
	}

	m = m.update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := m.View(); got != "" {
		t.Errorf("View() after submitting = %q, want empty", got)
	}
}
