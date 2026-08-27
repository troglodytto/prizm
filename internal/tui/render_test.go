package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

	// The same group the README's screenshots are built from, so the eight
	// images read as one session rather than eight unrelated tools.
	workflows := []Option{
		{Value: "frontend-only", Label: "frontend-only", Desc: "frontend", Tag: "qa"},
		{Value: "local", Label: "local", Desc: "ai auth backend frontend", Tag: "local"},
		{Value: "payments", Label: "payments", Desc: "backend frontend", Tag: "debug"},
		{Value: "production", Label: "production", Desc: "ai auth backend frontend", Tag: "prod"},
		{Value: "staging", Label: "staging", Desc: "ai auth backend frontend", Tag: "qa"},
	}
	repos := []Option{
		{Value: "ai", Label: "ai", Desc: "~/code/ai"},
		{Value: "auth", Label: "auth", Desc: "~/code/auth"},
		{Value: "backend", Label: "backend", Desc: "~/code/backend"},
		{Value: "frontend", Label: "frontend", Desc: "~/code/frontend"},
	}

	one := func() pickOneModel {
		m := newPickOneModel("Select a workflow", workflows)
		m.context = "my-saas-platform"
		// The workflow picker is the one that offers `e`, so the help line
		// in the image matches what the command actually shows.
		m.editable = true
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

	// With PRIZM_SHOWCASE_DIR set, each view is also written to its own
	// file, so the screenshot generator can render them one per image.
	dir := os.Getenv("PRIZM_SHOWCASE_DIR")
	show := func(label, view string) {
		fmt.Printf("\n\x1b[7m %-56s \x1b[0m\n%s", label, view)
		if dir == "" {
			return
		}
		name := strings.Map(func(r rune) rune {
			if r == ' ' || r == '·' || r == '\'' {
				return '-'
			}
			return r
		}, label)
		if err := os.WriteFile(filepath.Join(dir, name+".ansi"), []byte(view), 0o600); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}

	show("workflow picker · cursor on row 1", one().View())
	show("after ↓↓↓", downOne(one(), 3).View())

	filtered := one()
	filtered = filtered.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	for _, r := range "st" {
		filtered = filtered.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	show("filtering on 'st'", filtered.View())

	all := []string{"ai", "auth", "backend", "frontend"}
	show("repo checkboxes · all ticked, cursor row 2",
		downMany(newPickManyModel("Repos covered by my-saas-platform/payments", repos, all), 1).View())
	show("two unticked",
		downMany(newPickManyModel("Repos covered by my-saas-platform/payments", repos, []string{"backend", "frontend"}), 2).View())

	// The history carousel, mid-scrub: the version being considered is two
	// back, so the diff against the live state is the interesting part.
	versions := []Version{
		{ID: 1, When: "3d ago", At: "Aug 24 09:12", Source: "import", Note: ".env.local"},
		{ID: 2, When: "5h ago", At: "14:02", Source: "shared-sync", Note: "infra.env",
			Changes: []Change{
				{Key: "MONGO_URI", Mark: '~', From: "…@cluster/my_saas_local", To: "…@cluster/my_saas_scratch"},
				{Key: "LOG_LEVEL", Mark: '+', From: "debug"},
				{Key: "FEATURE_FLAGS", Mark: '-', To: "beta"},
			}},
		{ID: 3, When: "12m ago", At: "18:30", Source: "var", Note: "PORT"},
		{ID: 4, When: "just now", At: "18:42:51", Source: "sync", Note: "sync auth", Current: true},
	}
	carousel := newHistoryModel("my-saas-platform/auth · local", versions)
	show("history carousel · scrubbed back two", carousel.update(tea.KeyMsg{Type: tea.KeyRight}).View())

	// One decision per row, cycled with ←→. The shared-value case is the one
	// worth showing: it is the only one whose answer moves other repos.
	rows := []ResolveRow{
		{Key: "PORT", Detail: "4000 → 9999", Choices: []string{"apply to auth+local", "skip"}},
		{
			Key:     "MONGO_URI",
			Detail:  "…/my_saas_local → …/my_saas_scratch",
			Note:    "comes from ${_PRIZM_MONGO_URI} in shared:infra",
			Choices: []string{"update the shared value", "pin to auth only", "skip"},
			// Only the first choice reaches other repos.
			Consequences: []string{"also changes ai, backend, frontend", "", ""},
		},
		{Key: "DEBUG_TRACE", Detail: "added · on", Choices: []string{"add to auth+local", "skip"}},
	}
	resolve := newResolveModel("my-saas-platform/auth ← .env", rows)
	// Cursor on the shared-value row without cycling: the default choice is
	// the one that carries a consequence, which is the thing worth seeing.
	show("sync · one decision per row",
		resolve.update(tea.KeyMsg{Type: tea.KeyDown}).View())
}
