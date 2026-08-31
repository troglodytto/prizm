# prizm Phase 4 — Audit & Restore Implementation Plan

**Goal:** Make the history that Phase 2 has been recording since day one readable and reversible — a version list, a key-level diff between any two versions, a left/right carousel for stepping through them, and a restore.

**Architecture:** An `internal/audit` package turns snapshot rows into numbered versions with diffs, using the same `sharedfile.Compare` every other comparison in prizm uses. Two renderers sit on it: plain text (`prizm audit`) and a Bubble Tea carousel (`prizm audit --browse`, automatic when a terminal is present). Restore is not a new write path — it replaces a scope's variables with an old snapshot's map and records the result as a new version tagged `restore`, so the audit trail never loses the fact that a rollback happened.

**Tech Stack:** Unchanged. `internal/audit` uses only Phase 1–3 packages.

**Spec:** The transcript's audit section — per-key diffs rather than per-line, scope per `(repo, workflow)`, snapshot-source tagging, the arrow-key carousel, and restore as a near-free consequence of storing full snapshots.

**Prerequisite:** Phases 1–3 complete. In particular Phase 2 Task 1–2, which is what makes this phase mostly presentation.

## Global Constraints

- All earlier constraints hold.
- **Diffs are per key, never per line.** Editors reorder env files constantly; a line diff would report noise as change. This is the spec's explicit instruction and the reason snapshots store maps.
- **Restore is a forward operation.** It never deletes history. Restoring v2 creates v5 whose content equals v2's.
- **No retention policy.** Env maps are tiny and the spec is explicit that thousands of snapshots are nothing for a local SQLite file. Pruning is a knob to add if it ever becomes a problem, not before.
- **`--browse` degrades.** Without a terminal, `prizm audit` prints the version list; the carousel is never the only way to read history.
- **All output goes through `internal/style`.** The diff marks `audit` prints are the same ones `sync` and `up --dry-run` print, because `renderDiff` is the one diff renderer in prizm and it draws from the one palette.

---

## Design: Numbering, and What a Version Means

Snapshots are stored newest-first with database IDs. Users think in `v1, v2, v3…` oldest-first, because that is how versions read in every other tool. So `audit` reverses on load and numbers from 1, and **the numbers are stable**: v3 is always the third thing that ever happened to that scope, no matter how many versions come after it, because nothing is ever deleted.

Each version answers three questions, which is why `RecordSnapshot` takes a source and a note:

```
v4  2h ago    sync         reconciled from backend
v3  1d ago    var
v2  3d ago    shared-sync  from db.env
v1  5d ago    import       .env.local
```

The diff shown for vN is always **against vN-1**, computed on the fly. Storing diffs would be redundant — full maps are small, and a stored diff cannot answer "what did it look like at v2" without replaying.

**v1 has no predecessor**, so its diff is rendered as "initial" with every key shown as an addition. Getting this wrong is the classic off-by-one in carousels; it has its own test.

---

### Task 1: The audit engine

**Files:**
- Create: `internal/audit/audit.go`
- Test: `internal/audit/audit_test.go`

**Interfaces:**
- Consumes: `store.ListSnapshots`, `store.SnapshotVars`, `store.Scope`; `sharedfile.Compare`/`Diff`.
- Produces:
  - `audit.Version` struct: `Number int`, `SnapshotID int64`, `Source string`, `Note string`, `CreatedAt time.Time`, `Vars map[string]string`.
  - `audit.History` struct: `Versions []Version` (oldest first); `(History).Len() int`; `(History).At(n int) (Version, bool)`.
  - `audit.Load(s *store.Store, scope store.Scope) (History, error)`
  - `audit.(History).DiffAt(index int) sharedfile.Diff` — version `index` against its predecessor; for index 0 every key is an addition.
  - `audit.Since(t, now time.Time) string` — "2h ago", "3d ago".

- [ ] **Step 1: Write the failing test**

Create `internal/audit/audit_test.go`:

```go
package audit

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/troglodytto/prizm/internal/crypto"
	"github.com/troglodytto/prizm/internal/store"
)

var base = time.Unix(1700000000, 0)

func seeded(t *testing.T) (*store.Store, store.Scope) {
	t.Helper()

	s, err := store.Open(filepath.Join(t.TempDir(), "prizm.db"), crypto.Plaintext{})
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { s.Close() })

	g, _ := s.CreateGroup("XYZ")
	r, _ := s.AddRepo(g.ID, "backend", "/code/backend", "")
	wf, _ := s.AddWorkflow(g.ID, "local", "", []int64{r.ID})
	scope := store.WorkflowRepoScope(wf.ID, r.ID)

	s.RecordSnapshot(scope, map[string]string{"A": "1"}, store.SourceImport, ".env.local", base)
	s.RecordSnapshot(scope, map[string]string{"A": "1", "B": "2"}, store.SourceVar, "", base.Add(time.Hour))
	s.RecordSnapshot(scope, map[string]string{"A": "9"}, store.SourceSync, "reconciled from backend", base.Add(2*time.Hour))

	return s, scope
}

func TestLoadNumbersVersionsOldestFirst(t *testing.T) {
	s, scope := seeded(t)

	h, err := Load(s, scope)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if h.Len() != 3 {
		t.Fatalf("Len() = %d, want 3", h.Len())
	}

	for i, want := range []struct {
		number int
		source string
	}{
		{1, store.SourceImport},
		{2, store.SourceVar},
		{3, store.SourceSync},
	} {
		got := h.Versions[i]
		if got.Number != want.number || got.Source != want.source {
			t.Errorf("Versions[%d] = (v%d, %s), want (v%d, %s)", i, got.Number, got.Source, want.number, want.source)
		}
	}
}

func TestLoadCarriesNotesAndVars(t *testing.T) {
	s, scope := seeded(t)
	h, _ := Load(s, scope)

	if h.Versions[0].Note != ".env.local" {
		t.Errorf("Note = %q, want the recorded note", h.Versions[0].Note)
	}
	if diff := cmp.Diff(map[string]string{"A": "9"}, h.Versions[2].Vars); diff != "" {
		t.Errorf("newest Vars mismatch (-want +got):\n%s", diff)
	}
}

func TestDiffAtComparesAgainstThePredecessor(t *testing.T) {
	s, scope := seeded(t)
	h, _ := Load(s, scope)

	// v2 added B.
	d := h.DiffAt(1)
	if diff := cmp.Diff([]string{"B"}, d.Added); diff != "" {
		t.Errorf("v2 Added mismatch (-want +got):\n%s", diff)
	}

	// v3 changed A and removed B.
	d = h.DiffAt(2)
	if len(d.Changed) != 1 || d.Changed[0].Key != "A" || d.Changed[0].To != "9" {
		t.Errorf("v3 Changed = %+v, want A 1→9", d.Changed)
	}
	if diff := cmp.Diff([]string{"B"}, d.Removed); diff != "" {
		t.Errorf("v3 Removed mismatch (-want +got):\n%s", diff)
	}
}

// The classic carousel off-by-one: v1 has no predecessor.
func TestDiffAtFirstVersionIsAllAdditions(t *testing.T) {
	s, scope := seeded(t)
	h, _ := Load(s, scope)

	d := h.DiffAt(0)
	if diff := cmp.Diff([]string{"A"}, d.Added); diff != "" {
		t.Errorf("v1 Added mismatch (-want +got):\n%s", diff)
	}
	if len(d.Removed) != 0 || len(d.Changed) != 0 {
		t.Errorf("v1 diff = %+v, want additions only", d)
	}
}

func TestDiffAtOutOfRangeIsEmpty(t *testing.T) {
	s, scope := seeded(t)
	h, _ := Load(s, scope)

	for _, i := range []int{-1, 3, 99} {
		if d := h.DiffAt(i); !d.Empty() {
			t.Errorf("DiffAt(%d) = %+v, want empty", i, d)
		}
	}
}

func TestAtIsOneBasedForUsers(t *testing.T) {
	s, scope := seeded(t)
	h, _ := Load(s, scope)

	v, ok := h.At(2)
	if !ok || v.Source != store.SourceVar {
		t.Errorf("At(2) = (%+v, %v), want v2", v, ok)
	}
	if _, ok := h.At(0); ok {
		t.Error("At(0) ok = true; versions are numbered from 1")
	}
	if _, ok := h.At(4); ok {
		t.Error("At(4) ok = true; there are only 3 versions")
	}
}

func TestLoadEmptyScope(t *testing.T) {
	s, _ := seeded(t)

	h, err := Load(s, store.WorkflowRepoScope(999, 999))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if h.Len() != 0 {
		t.Errorf("Len() = %d, want 0", h.Len())
	}
}

func TestSince(t *testing.T) {
	now := base.Add(100 * 24 * time.Hour)

	tests := []struct {
		age  time.Duration
		want string
	}{
		{age: 30 * time.Second, want: "just now"},
		{age: 5 * time.Minute, want: "5m ago"},
		{age: 2 * time.Hour, want: "2h ago"},
		{age: 3 * 24 * time.Hour, want: "3d ago"},
		{age: 60 * 24 * time.Hour, want: "8w ago"},
	}

	for _, tt := range tests {
		if got := Since(now.Add(-tt.age), now); got != tt.want {
			t.Errorf("Since(-%v) = %q, want %q", tt.age, got, tt.want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/audit/`
Expected: FAIL — `undefined: Load`.

- [ ] **Step 3: Implement the engine**

Create `internal/audit/audit.go`:

```go
// Package audit turns snapshot history into numbered versions and diffs.
package audit

import (
	"fmt"
	"time"

	"github.com/troglodytto/prizm/internal/sharedfile"
	"github.com/troglodytto/prizm/internal/store"
)

// Version is one recorded state of a scope, numbered from 1 oldest-first.
type Version struct {
	Number     int
	SnapshotID int64
	Source     string
	Note       string
	CreatedAt  time.Time
	Vars       map[string]string
}

// History is a scope's versions, oldest first.
type History struct {
	Versions []Version
}

// Len is how many versions exist.
func (h History) Len() int { return len(h.Versions) }

// At returns the version with the given user-facing number (1-based).
func (h History) At(number int) (Version, bool) {
	if number < 1 || number > len(h.Versions) {
		return Version{}, false
	}
	return h.Versions[number-1], true
}

// DiffAt compares the version at index (0-based) with its predecessor. The
// first version has none, so every key reads as an addition.
func (h History) DiffAt(index int) sharedfile.Diff {
	if index < 0 || index >= len(h.Versions) {
		return sharedfile.Diff{}
	}

	previous := map[string]string{}
	if index > 0 {
		previous = h.Versions[index-1].Vars
	}
	return sharedfile.Compare(previous, h.Versions[index].Vars)
}

// Load reads a scope's whole history.
func Load(s *store.Store, scope store.Scope) (History, error) {
	snapshots, err := s.ListSnapshots(scope) // newest first
	if err != nil {
		return History{}, err
	}

	versions := make([]Version, len(snapshots))
	for i, snap := range snapshots {
		vars, err := s.SnapshotVars(snap.ID)
		if err != nil {
			return History{}, err
		}

		// Reverse: users number versions oldest-first.
		position := len(snapshots) - 1 - i
		versions[position] = Version{
			Number:     position + 1,
			SnapshotID: snap.ID,
			Source:     snap.Source,
			Note:       snap.Note,
			CreatedAt:  snap.CreatedAt,
			Vars:       vars,
		}
	}
	return History{Versions: versions}, nil
}

// Since renders an age the way a changelog would.
func Since(t, now time.Time) string {
	age := now.Sub(t)

	switch {
	case age < time.Minute:
		return "just now"
	case age < time.Hour:
		return fmt.Sprintf("%dm ago", int(age.Minutes()))
	case age < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(age.Hours()))
	case age < 28*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(age.Hours()/24))
	default:
		return fmt.Sprintf("%dw ago", int(age.Hours()/24/7))
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/audit/ -v`
Expected: PASS — all eight tests.

- [ ] **Step 5: Commit**

```bash
git add internal/audit/
git commit -m "feat(audit): numbered versions and per-key diffs over snapshot history"
```

---

### Task 2: `prizm audit` — plain-text history

**Files:**
- Create: `internal/cli/audit.go`, `internal/cli/renderdiff.go`
- Modify: `internal/cli/root.go` (register)
- Test: `internal/cli/audit_test.go`

**Interfaces:**
- Consumes: `audit.Load`, `audit.Since`, `sharedfile.Diff`.
- Produces:
  - `cli.renderDiff(w io.Writer, d sharedfile.Diff, indent string)` — the one diff renderer, shared by `audit`, the carousel and `shared-sync`.
  - `prizm audit [group] [repo] [--workflow W] [--bag NAME] [--version N]`

Scoping follows the spec: history is per `(repo, workflow)`, so two workflows touching the same repo keep separate timelines rather than interleaving unrelated changes. `--bag` switches the scope to a shared bag's timeline, which is the other thing that has its own history.

- [ ] **Step 1: Write the failing test**

Create `internal/cli/audit_test.go`:

```go
package cli

import (
	"strings"
	"testing"
)

func (h *harness) seedHistory(t *testing.T) {
	t.Helper()

	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend")
	h.run(t, "add-workflow", "XYZ", "local", "--repos", "backend")
	h.run(t, "var", "XYZ", "backend", "A=1", "--workflow", "local")
	h.run(t, "var", "XYZ", "backend", "B=2", "--workflow", "local")
	h.run(t, "var", "XYZ", "backend", "A=9", "--workflow", "local")
}

func TestAuditListsVersionsNewestFirst(t *testing.T) {
	h := newHarness(t)
	h.seedHistory(t)

	if err := h.run(t, "audit", "XYZ", "backend", "--workflow", "local"); err != nil {
		t.Fatalf("audit error = %v", err)
	}

	out := h.out.String()
	for _, want := range []string{"v3", "v2", "v1", "var"} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q, want it to mention %q", out, want)
		}
	}
	if strings.Index(out, "v3") > strings.Index(out, "v1") {
		t.Error("versions are listed oldest-first; newest should lead")
	}
}

func TestAuditShowsOneVersionsDiff(t *testing.T) {
	h := newHarness(t)
	h.seedHistory(t)

	if err := h.run(t, "audit", "XYZ", "backend", "--workflow", "local", "--version", "3"); err != nil {
		t.Fatalf("audit error = %v", err)
	}

	out := h.out.String()
	if !strings.Contains(out, "A") {
		t.Errorf("output = %q, want the changed key", out)
	}
	if !strings.Contains(out, "9") {
		t.Errorf("output = %q, want the new value", out)
	}
}

func TestAuditRejectsAnUnknownVersion(t *testing.T) {
	h := newHarness(t)
	h.seedHistory(t)

	err := h.run(t, "audit", "XYZ", "backend", "--workflow", "local", "--version", "99")
	if err == nil || !strings.Contains(err.Error(), "99") {
		t.Errorf("error = %v, want it to name the bad version", err)
	}
}

func TestAuditWithNoHistoryIsClear(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend")
	h.run(t, "add-workflow", "XYZ", "local", "--repos", "backend")

	if err := h.run(t, "audit", "XYZ", "backend", "--workflow", "local"); err != nil {
		t.Fatalf("audit error = %v", err)
	}
	if !strings.Contains(h.out.String(), "no history") {
		t.Errorf("output = %q, want a clear empty-history message", h.out.String())
	}
}

func TestAuditScopesPerWorkflow(t *testing.T) {
	h := newHarness(t)
	h.seedHistory(t)
	h.run(t, "add-workflow", "XYZ", "production", "--repos", "backend", "--tag", "prod")
	h.run(t, "var", "XYZ", "backend", "A=prod", "--workflow", "production")

	h.run(t, "audit", "XYZ", "backend", "--workflow", "production")
	out := h.out.String()

	if strings.Count(out, "v") == 0 {
		t.Fatalf("output = %q, want production's own history", out)
	}
	if strings.Contains(out, "v2") {
		t.Errorf("output = %q, want only production's single version — timelines must not interleave", out)
	}
}

func TestAuditOfASharedBag(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend")
	h.run(t, "add-workflow", "XYZ", "local", "--repos", "backend")

	g, _ := h.app.Store.GroupByName("XYZ")
	wf, _ := h.app.Store.WorkflowByName(g.ID, "local")
	sg, _ := h.app.Store.CreateSharedGroup(wf.ID, "db")
	h.app.Store.ReplaceSharedGroupVars(sg.ID, map[string]string{"_PRIZM_DB_URL": "postgres://a/db"})
	h.app.Store.ReplaceSharedGroupVars(sg.ID, map[string]string{"_PRIZM_DB_URL": "postgres://b/db"})

	if err := h.run(t, "audit", "XYZ", "--workflow", "local", "--bag", "db"); err != nil {
		t.Fatalf("audit error = %v", err)
	}
	if !strings.Contains(h.out.String(), "v2") {
		t.Errorf("output = %q, want the bag's two versions", h.out.String())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run TestAudit`
Expected: FAIL — unknown command `audit`.

- [ ] **Step 3: Implement the shared diff renderer**

Create `internal/cli/renderdiff.go`:

```go
package cli

import (
	"fmt"
	"io"

	"github.com/troglodytto/prizm/internal/sharedfile"
)

// renderDiff writes a key-level diff. This is the single diff renderer in prizm,
// used by audit, the carousel and shared-sync, so drift always reads the same.
func renderDiff(w io.Writer, d sharedfile.Diff, indent string) {
	if d.Empty() {
		fmt.Fprintf(w, "%s%s\n", indent, style.Detail("(no change)"))
		return
	}

	for _, key := range d.Added {
		fmt.Fprintf(w, "%s%s\n", indent, style.Row(style.Add, key, ""))
	}
	for _, c := range d.Changed {
		fmt.Fprintf(w, "%s%s\n", indent, style.Row(style.Change, c.Key, c.From+" → "+c.To))
	}
	for _, key := range d.Removed {
		fmt.Fprintf(w, "%s%s\n", indent, style.Row(style.Remove, key, ""))
	}
}
```

- [ ] **Step 4: Implement the command**

Create `internal/cli/audit.go`:

```go
package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/troglodytto/prizm/internal/audit"
	"github.com/troglodytto/prizm/internal/store"
)

func newAuditCmd(app *App) *cobra.Command {
	var (
		workflow string
		bag      string
		version  int
		browse   bool
	)

	cmd := &cobra.Command{
		Use:   "audit [group] [repo]",
		Short: "Step through the history of a repo's or a shared bag's variables",
		Long: "History is scoped per (repo, workflow), so two workflows touching the\n" +
			"same repo keep separate timelines. Use --bag to read a shared bag's\n" +
			"history instead.",
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			scope, label, err := auditScope(app, args, workflow, bag)
			if err != nil {
				return err
			}

			history, err := audit.Load(app.Store, scope)
			if err != nil {
				return err
			}
			if history.Len() == 0 {
				fmt.Fprintf(app.Out, "no history for %s yet\n", label)
				return nil
			}

			if version > 0 {
				v, ok := history.At(version)
				if !ok {
					return fmt.Errorf("no version %d for %s — there are %d", version, label, history.Len())
				}
				printVersion(app, history, v)
				return nil
			}

			if browse && app.canBrowseHistory() {
				return browseHistory(app, label, history)
			}

			fmt.Fprintf(app.Out, "%s\n", label)
			for i := history.Len() - 1; i >= 0; i-- {
				v := history.Versions[i]
				fmt.Fprintf(app.Out, "  v%-3d %-10s %-12s %s\n",
					v.Number, audit.Since(v.CreatedAt, app.Now()), v.Source, v.Note)
			}
			fmt.Fprintf(app.Out, "\nsee one version: prizm audit ... --version N\n")
			return nil
		},
	}

	cmd.Flags().StringVar(&workflow, "workflow", "", "workflow whose timeline to read")
	cmd.Flags().StringVar(&bag, "bag", "", "read a shared bag's timeline instead of a repo's")
	cmd.Flags().IntVar(&version, "version", 0, "show one version's diff")
	cmd.Flags().BoolVar(&browse, "browse", true, "step through versions interactively when a terminal is available")
	return cmd
}

func printVersion(app *App, history audit.History, v audit.Version) {
	fmt.Fprintf(app.Out, "v%d  %s  %s  %s\n",
		v.Number, audit.Since(v.CreatedAt, app.Now()), v.Source, v.Note)
	renderDiff(app.Out, history.DiffAt(v.Number-1), "  ")
}

// auditScope resolves which timeline to read, and a label naming it.
func auditScope(app *App, args []string, workflow, bag string) (store.Scope, string, error) {
	if bag != "" {
		g, rest, err := app.splitGroup(args, 0)
		if err != nil {
			return store.Scope{}, "", err
		}
		_ = rest

		if workflow == "" {
			return store.Scope{}, "", fmt.Errorf("--bag needs --workflow, since bags are scoped to a workflow")
		}
		wf, err := app.Store.WorkflowByName(g.ID, workflow)
		if err != nil {
			return store.Scope{}, "", fmt.Errorf("no such workflow %q in group %s", workflow, g.Name)
		}
		sg, err := app.Store.SharedGroupByName(wf.ID, bag)
		if err != nil {
			return store.Scope{}, "", fmt.Errorf("no such shared bag %q in %s/%s", bag, g.Name, wf.Name)
		}
		return store.SharedGroupScope(sg.ID), fmt.Sprintf("%s/%s/%s", g.Name, wf.Name, bag), nil
	}

	g, repo, _, err := app.splitGroupRepo(args, 0)
	if err != nil {
		return store.Scope{}, "", err
	}

	wf, err := resolveAuditWorkflow(app, g, repo, workflow)
	if err != nil {
		return store.Scope{}, "", err
	}
	return store.WorkflowRepoScope(wf.ID, repo.ID),
		fmt.Sprintf("%s/%s (%s)", g.Name, repo.Name, wf.Name), nil
}

// resolveAuditWorkflow picks the workflow: the named one, or the one the repo
// is currently applied to.
func resolveAuditWorkflow(app *App, g store.Group, repo store.Repo, name string) (store.Workflow, error) {
	if name != "" {
		wf, err := app.Store.WorkflowByName(g.ID, name)
		if err != nil {
			return store.Workflow{}, fmt.Errorf("no such workflow %q in group %s", name, g.Name)
		}
		return wf, nil
	}

	applied, err := app.Store.AppliedFor(g.ID)
	if err != nil {
		return store.Workflow{}, err
	}
	state, ok := applied[repo.ID]
	if !ok {
		return store.Workflow{}, fmt.Errorf(
			"%s is not applied to any workflow, so prizm cannot tell which timeline you mean — pass --workflow", repo.Name)
	}

	workflows, err := app.Store.ListWorkflows(g.ID)
	if err != nil {
		return store.Workflow{}, err
	}
	for _, w := range workflows {
		if w.ID == state.WorkflowID {
			return w, nil
		}
	}
	return store.Workflow{}, fmt.Errorf("%s is linked to a workflow that no longer exists", repo.Name)
}
```

Register it: `newAuditCmd(app),`. For this task, stub the carousel hooks so the package compiles:

```go
func (a *App) canBrowseHistory() bool { return false }

func browseHistory(app *App, label string, history audit.History) error { return nil }
```

Task 3 replaces both.

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/cli/ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/
git commit -m "feat(cli): audit — version list and per-version diffs"
```

---

### Task 3: The version carousel

**Files:**
- Create: `internal/tui/carousel.go`, `internal/tui/carousel_test.go`
- Modify: `internal/cli/audit.go` (replace the stubs)
- Test: `internal/cli/audit_browse_test.go`

**Interfaces:**
- Produces:
  - `tui.Frame` struct: `Header string`, `Body string`.
  - `tui.Carousel(title string, frames []Frame) error` — left/right through pre-rendered frames.
  - `App.Carousel` — injectable, defaulting to `tui.Carousel`.

The spec asked for exactly this: press left and right to move between versions, with the diff from the previous version rendered per frame. Frames are **pre-rendered strings** rather than a callback, which keeps the model trivial and means the diff colouring lives in one place with every other diff in prizm.

- [ ] **Step 1: Write the failing test**

Create `internal/tui/carousel_test.go`:

```go
package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func frames() []Frame {
	return []Frame{
		{Header: "v1  5d ago  import", Body: "+ A"},
		{Header: "v2  1d ago  var", Body: "+ B"},
		{Header: "v3  2h ago  sync", Body: "~ A  1 → 9"},
	}
}

func TestCarouselStartsOnTheNewestFrame(t *testing.T) {
	m := newCarouselModel("History", frames())

	if m.index != 2 {
		t.Errorf("index = %d, want the newest frame (2)", m.index)
	}
}

func TestCarouselLeftGoesBackInTime(t *testing.T) {
	m := newCarouselModel("History", frames())
	m = m.update(tea.KeyMsg{Type: tea.KeyLeft})

	if m.index != 1 {
		t.Errorf("index = %d, want 1", m.index)
	}
}

func TestCarouselRightGoesForward(t *testing.T) {
	m := newCarouselModel("History", frames())
	m = m.update(tea.KeyMsg{Type: tea.KeyLeft})
	m = m.update(tea.KeyMsg{Type: tea.KeyRight})

	if m.index != 2 {
		t.Errorf("index = %d, want 2", m.index)
	}
}

func TestCarouselStopsAtBothEnds(t *testing.T) {
	m := newCarouselModel("History", frames())

	for i := 0; i < 10; i++ {
		m = m.update(tea.KeyMsg{Type: tea.KeyLeft})
	}
	if m.index != 0 {
		t.Errorf("index = %d after paging left, want 0", m.index)
	}

	for i := 0; i < 10; i++ {
		m = m.update(tea.KeyMsg{Type: tea.KeyRight})
	}
	if m.index != 2 {
		t.Errorf("index = %d after paging right, want 2", m.index)
	}
}

func TestCarouselHomeAndEnd(t *testing.T) {
	m := newCarouselModel("History", frames())

	m = m.update(key('g'))
	if m.index != 0 {
		t.Errorf("index = %d after g, want the oldest", m.index)
	}

	m = m.update(key('G'))
	if m.index != 2 {
		t.Errorf("index = %d after G, want the newest", m.index)
	}
}

func TestCarouselEscapeQuits(t *testing.T) {
	m := newCarouselModel("History", frames())
	m = m.update(tea.KeyMsg{Type: tea.KeyEsc})

	if !m.done {
		t.Error("escape did not close the carousel")
	}
}

func TestCarouselViewShowsPositionAndFrame(t *testing.T) {
	m := newCarouselModel("History", frames())
	view := m.View()

	for _, want := range []string{"History", "v3", "~ A", "3/3"} {
		if !strings.Contains(view, want) {
			t.Errorf("View() missing %q:\n%s", want, view)
		}
	}
}

func TestCarouselWithOneFrame(t *testing.T) {
	m := newCarouselModel("History", frames()[:1])
	m = m.update(tea.KeyMsg{Type: tea.KeyLeft})
	m = m.update(tea.KeyMsg{Type: tea.KeyRight})

	if m.index != 0 {
		t.Errorf("index = %d, want 0 for a single frame", m.index)
	}
}

func TestCarouselWithNoFramesIsDone(t *testing.T) {
	if m := newCarouselModel("History", nil); !m.done {
		t.Error("an empty carousel is not done")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/ -run TestCarousel`
Expected: FAIL — `undefined: newCarouselModel`.

- [ ] **Step 3: Implement the carousel**

Create `internal/tui/carousel.go`:

```go
package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// Frame is one pre-rendered page of a carousel.
type Frame struct {
	Header string
	Body   string
}

type carouselModel struct {
	title  string
	frames []Frame
	index  int
	done   bool
}

func newCarouselModel(title string, frames []Frame) carouselModel {
	// Start on the newest, which is what someone opening a history wants first.
	return carouselModel{
		title:  title,
		frames: frames,
		index:  max(0, len(frames)-1),
		done:   len(frames) == 0,
	}
}

func (m carouselModel) Init() tea.Cmd { return nil }

func (m carouselModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next := m.update(msg)
	if next.done {
		return next, tea.Quit
	}
	return next, nil
}

func (m carouselModel) update(msg tea.Msg) carouselModel {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m
	}

	switch keyMsg.Type {
	case tea.KeyCtrlC, tea.KeyEsc, tea.KeyEnter:
		m.done = true
	case tea.KeyLeft:
		m = m.move(-1)
	case tea.KeyRight:
		m = m.move(1)
	case tea.KeyRunes:
		switch keyMsg.Runes[0] {
		case 'h':
			m = m.move(-1)
		case 'l':
			m = m.move(1)
		case 'g':
			m.index = 0
		case 'G':
			m.index = len(m.frames) - 1
		case 'q':
			m.done = true
		}
	}
	return m
}

func (m carouselModel) move(delta int) carouselModel {
	next := m.index + delta
	if next < 0 {
		next = 0
	}
	if next > len(m.frames)-1 {
		next = len(m.frames) - 1
	}
	m.index = next
	return m
}

func (m carouselModel) View() string {
	if len(m.frames) == 0 {
		return T.Frame.Render(T.Dim.Render("no history"))
	}

	frame := m.frames[m.index]

	var b strings.Builder
	b.WriteString(T.Title.Render(m.title) + "\n\n")
	b.WriteString(T.Selected.Render(frame.Header) + "\n\n")
	b.WriteString(frame.Body)
	b.WriteString("\n" + T.Help.Render(fmt.Sprintf(
		"←→ version   g/G ends   esc close        %d/%d", m.index+1, len(m.frames))))

	return T.Frame.Render(b.String())
}

// Carousel steps through pre-rendered frames.
func Carousel(title string, frames []Frame) error {
	_, err := run(newCarouselModel(title, frames))
	return err
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
```

- [ ] **Step 4: Wire it into `audit`**

Add `Carousel func(title string, frames []tui.Frame) error` to `App`, default it in `Execute`, and replace the two stubs in `internal/cli/audit.go`:

```go
func (a *App) canBrowseHistory() bool {
	return a.Carousel != nil && (tui.Available() || a.pickerInjected)
}

// browseHistory renders every version as a frame and hands them to the carousel.
func browseHistory(app *App, label string, history audit.History) error {
	frames := make([]tui.Frame, 0, history.Len())

	for i, v := range history.Versions {
		var body strings.Builder
		renderDiff(&body, history.DiffAt(i), "  ")

		header := fmt.Sprintf("v%d  %s  %s", v.Number, audit.Since(v.CreatedAt, app.Now()), v.Source)
		if v.Note != "" {
			header += "  " + v.Note
		}
		frames = append(frames, tui.Frame{Header: header, Body: body.String()})
	}
	return app.Carousel(label, frames)
}
```

Add `"strings"` and the `tui` import.

- [ ] **Step 5: Add a command-level test**

Create `internal/cli/audit_browse_test.go` with a test that injects `app.Carousel`, runs `audit`, and asserts it received one frame per version with the newest last and the v1 frame showing additions only.

- [ ] **Step 6: Run the suite**

Run: `go test ./... `
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/tui/ internal/cli/
git commit -m "feat(audit): left/right version carousel"
```

---

### Task 4: `--restore`

**Files:**
- Modify: `internal/store/vars.go` (`ReplaceWorkflowRepoVars`), `internal/cli/audit.go`
- Test: `internal/cli/restore_test.go`

**Interfaces:**
- Produces:
  - `store.(*Store).ReplaceWorkflowRepoVars(workflowID, repoID int64, vars map[string]string, source, note string) error`
  - `prizm audit ... --restore N`

Restore is cheap precisely because snapshots store complete maps: it is "write this old map as the current one", reusing the same write path as everything else. It is also **forward-only** — restoring v2 produces a new v5 whose content equals v2's, tagged `restore`, so the history shows that a rollback happened rather than hiding it.

- [ ] **Step 1: Write the failing test**

Create `internal/cli/restore_test.go`:

```go
package cli

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/troglodytto/prizm/internal/store"
)

func TestRestoreBringsBackAnOldMap(t *testing.T) {
	h := newHarness(t)
	h.seedHistory(t) // v1 {A:1}, v2 {A:1,B:2}, v3 {A:9}

	if err := h.run(t, "audit", "XYZ", "backend", "--workflow", "local", "--restore", "2"); err != nil {
		t.Fatalf("restore error = %v", err)
	}

	g, _ := h.app.Store.GroupByName("XYZ")
	wf, _ := h.app.Store.WorkflowByName(g.ID, "local")
	repo, _ := h.app.Store.RepoByName(g.ID, "backend")

	got, _ := h.app.Store.WorkflowRepoVars(wf.ID, repo.ID)
	if diff := cmp.Diff(map[string]string{"A": "1", "B": "2"}, got); diff != "" {
		t.Errorf("restored vars mismatch (-want +got):\n%s", diff)
	}
}

func TestRestoreIsForwardOnly(t *testing.T) {
	h := newHarness(t)
	h.seedHistory(t)
	h.run(t, "audit", "XYZ", "backend", "--workflow", "local", "--restore", "2")

	g, _ := h.app.Store.GroupByName("XYZ")
	wf, _ := h.app.Store.WorkflowByName(g.ID, "local")
	repo, _ := h.app.Store.RepoByName(g.ID, "backend")

	snaps, _ := h.app.Store.ListSnapshots(store.WorkflowRepoScope(wf.ID, repo.ID))
	if len(snaps) != 4 {
		t.Errorf("history has %d versions, want 4 — a restore adds a version, it does not rewrite them", len(snaps))
	}
	if snaps[0].Source != store.SourceRestore {
		t.Errorf("newest source = %q, want %q", snaps[0].Source, store.SourceRestore)
	}
}

func TestRestoreConfirmsFirst(t *testing.T) {
	h := newHarness(t)
	h.seedHistory(t)

	var prompted bool
	h.app.Confirm = func(string) (bool, error) {
		prompted = true
		return false, nil
	}

	h.run(t, "audit", "XYZ", "backend", "--workflow", "local", "--restore", "1")
	if !prompted {
		t.Error("restore did not confirm before overwriting")
	}

	g, _ := h.app.Store.GroupByName("XYZ")
	wf, _ := h.app.Store.WorkflowByName(g.ID, "local")
	repo, _ := h.app.Store.RepoByName(g.ID, "backend")
	got, _ := h.app.Store.WorkflowRepoVars(wf.ID, repo.ID)
	if got["A"] != "9" {
		t.Errorf("A = %q, want the current value after declining", got["A"])
	}
}

func TestRestoreRejectsAnUnknownVersion(t *testing.T) {
	h := newHarness(t)
	h.seedHistory(t)

	err := h.run(t, "audit", "XYZ", "backend", "--workflow", "local", "--restore", "99")
	if err == nil || !strings.Contains(err.Error(), "99") {
		t.Errorf("error = %v, want it to name the bad version", err)
	}
}

func TestRestoreTellsYouToReapply(t *testing.T) {
	h := newHarness(t)
	h.seedHistory(t)

	h.run(t, "audit", "XYZ", "backend", "--workflow", "local", "--restore", "2")
	if !strings.Contains(h.out.String(), "up") {
		t.Errorf("output = %q, want it to point at re-applying", h.out.String())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run TestRestore`
Expected: FAIL — unknown flag `--restore`.

- [ ] **Step 3: Add the replace-and-snapshot write path**

Append to `internal/store/vars.go`:

```go
// ReplaceWorkflowRepoVars makes a (workflow, repo) layer exactly vars, and
// records the result as a new version. Used by restore.
func (s *Store) ReplaceWorkflowRepoVars(workflowID, repoID int64, vars map[string]string, source, note string) error {
	for key := range vars {
		if err := checkKey(key); err != nil {
			return err
		}
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(
		`DELETE FROM workflow_repo_vars WHERE workflow_id = ? AND repo_id = ?`, workflowID, repoID,
	); err != nil {
		return err
	}

	now := time.Now().Unix()
	for key, value := range vars {
		blob, err := s.cipher.Encrypt(value)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`
			INSERT INTO workflow_repo_vars(workflow_id, repo_id, key, value, updated_at)
			VALUES (?, ?, ?, ?, ?)`, workflowID, repoID, key, blob, now); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	return s.snapshotWorkflowRepo(workflowID, repoID, source, note)
}
```

- [ ] **Step 4: Add the flag**

In `internal/cli/audit.go`, add `restore int` and its flag, and handle it before the listing branch:

```go
			if restore > 0 {
				v, ok := history.At(restore)
				if !ok {
					return fmt.Errorf("no version %d for %s — there are %d", restore, label, history.Len())
				}
				return restoreVersion(app, scope, label, v)
			}
```

and implement:

```go
// restoreVersion writes an old version's map back as the current state. It is
// forward-only: the restore itself becomes the newest version.
func restoreVersion(app *App, scope store.Scope, label string, v audit.Version) error {
	fmt.Fprintf(app.Out, "restore %s to v%d (%s, %s)\n",
		label, v.Number, v.Source, audit.Since(v.CreatedAt, app.Now()))

	ok, err := app.Confirm("This replaces the current values. Continue? [y/N] ")
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(app.Out, "cancelled")
		return nil
	}

	note := fmt.Sprintf("restored v%d", v.Number)
	switch scope.Kind {
	case store.ScopeWorkflowRepo:
		err = app.Store.ReplaceWorkflowRepoVars(scope.A, scope.B, v.Vars, store.SourceRestore, note)
	case store.ScopeSharedGroup:
		err = app.Store.ReplaceSharedGroupVarsWithSource(scope.A, v.Vars, store.SourceRestore, note)
	default:
		return fmt.Errorf("cannot restore scope kind %q", scope.Kind)
	}
	if err != nil {
		return err
	}

	fmt.Fprintf(app.Out, "restored — run `prizm up` to write the change into the repo\n")
	return nil
}
```

Note the shared-bag branch does **not** rewrite the bag's file here; that would silently overwrite an edit the user may be mid-way through. `up` will report the divergence, and `shared-sync` resolves it — the same rule as everywhere else in prizm.

- [ ] **Step 5: Run the suite**

Run: `go test ./... && go build ./...`
Expected: PASS and a clean build.

- [ ] **Step 6: Commit**

```bash
git add internal/store/ internal/cli/
git commit -m "feat(audit): restore an earlier version as a new version"
```

---

## Phase 4 Self-Review

**Spec coverage.** Per-key diffs rather than per-line → Task 1, reusing `sharedfile.Compare`. Scope per `(repo, workflow)` so timelines do not interleave → Tasks 1–2, with a test. Snapshot-source tagging shown in the listing → Task 2. Left/right carousel with the diff against the previous version → Task 3. `--restore` as a near-free consequence of full snapshots → Task 4. No retention logic, per the spec's explicit reasoning → recorded in Global Constraints.

**Placeholder scan.** Two command-level tests (Task 3 Step 5, and the browse assertions) are described rather than written out, because both are five-line injections over harnesses defined in full earlier in this plan. Everything else carries complete code.

**Type consistency.** `audit.History.At` is 1-based (user-facing) while `DiffAt` is 0-based (index into `Versions`); the call sites convert with `v.Number-1` and both behaviours have tests. `renderDiff` writes to an `io.Writer`, so the same function serves stdout in Task 2 and a `strings.Builder` in Task 3 — one diff renderer, one appearance.

**Watch during execution:**

- `Load` reverses the store's newest-first ordering while numbering. Getting the reversal wrong makes `v1` the newest and every diff read backwards; `TestLoadNumbersVersionsOldestFirst` and `TestDiffAtFirstVersionIsAllAdditions` are the guards.
- Task 2 ships deliberate stubs for `canBrowseHistory` and `browseHistory`, replaced in Task 3. Do not leave them if Task 3 is deferred — `--browse` would silently do nothing.
