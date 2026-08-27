package tui

import (
	"fmt"
	"os"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// TestRenderShowcase prints the pickers as they actually appear, so a change
// to their look can be reviewed without a terminal or a human at the
// keyboard. It asserts nothing; run it with -v to look.
//
//	go test ./internal/tui/ -run TestRenderShowcase -v
func TestRenderShowcase(t *testing.T) {
	if os.Getenv("PRIZM_SHOWCASE") == "" {
		t.Skip("set PRIZM_SHOWCASE=1 to print the pickers")
	}

	// Tests have no terminal, so colour is stripped unless it is forced.
	lipgloss.SetColorProfile(termenv.ANSI256)

	workflows := []Option{
		{Value: "ai-only", Label: "ai-only", Desc: "ai search-svc web-backend", Tag: "demo"},
		{Value: "local", Label: "local", Desc: "ai auth search-svc web-backend", Tag: "local"},
		{Value: "perf-test", Label: "perf-test", Desc: "search-svc web-backend", Tag: "perf"},
		{Value: "production", Label: "production", Desc: "ai auth search-svc web-backend", Tag: "prod"},
		{Value: "staging", Label: "staging", Desc: "ai auth search-svc web-backend", Tag: "qa"},
	}
	repos := []Option{
		{Value: "ai", Label: "ai", Desc: "~/Projects/Acme/ai"},
		{Value: "auth", Label: "auth", Desc: "~/Projects/Acme/acme-auth"},
		{Value: "search-svc", Label: "search-svc", Desc: "~/Projects/Acme/search-svc"},
		{Value: "web-backend", Label: "web-backend", Desc: "~/Projects/Acme/platform/backend"},
	}

	one := func() pickOneModel {
		m := newPickOneModel("Select a workflow", workflows)
		m.context = "acme"
		return m
	}
	downOne := func(m pickOneModel, n int) pickOneModel {
		for i := 0; i < n; i++ {
			m = m.update(tea.KeyMsg{Type: tea.KeyDown})
		}
		return m
	}
	downMany := func(m pickManyModel, n int) pickManyModel {
		for i := 0; i < n; i++ {
			m = m.update(tea.KeyMsg{Type: tea.KeyDown})
		}
		return m
	}

	show := func(label, view string) {
		fmt.Printf("\n\x1b[7m %-56s \x1b[0m\n%s", label, view)
	}

	show("workflow picker · cursor on row 1", one().View())
	show("after ↓↓↓", downOne(one(), 3).View())

	filtered := one()
	filtered = filtered.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	for _, r := range "st" {
		filtered = filtered.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	show("filtering on 'st'", filtered.View())

	all := []string{"ai", "auth", "search-svc", "web-backend"}
	show("repo checkboxes · all ticked, cursor row 2",
		downMany(newPickManyModel("Repos covered by acme/experiment", repos, all), 1).View())
	show("two unticked",
		downMany(newPickManyModel("Repos covered by acme/experiment", repos, []string{"auth", "web-backend"}), 2).View())
}
