package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/troglodytto/prizm/internal/store"
	"github.com/troglodytto/prizm/internal/tui"
)

// fixedClock makes snapshot timestamps deterministic and, more importantly,
// distinguishable — two versions written in the same test second would sort
// ambiguously.
func fixedClock(t *testing.T) func() {
	t.Helper()

	base := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	tick := 0
	original := now
	now = func() time.Time {
		tick++
		return base.Add(time.Duration(tick) * time.Minute)
	}
	t.Cleanup(func() { now = original })
	return func() { tick += 60 }
}

func (h *harness) seedAudit(t *testing.T) (store.Group, store.Repo) {
	t.Helper()

	h.run(t, "init", "k")
	h.run(t, "add-repo", "k", "auth", "--path", h.repoDir(t, "auth"))

	g, err := h.app.Store.GroupByName("k")
	if err != nil {
		t.Fatalf("group: %v", err)
	}
	repo, err := h.app.Store.RepoByName(g.ID, "auth")
	if err != nil {
		t.Fatalf("repo: %v", err)
	}
	return g, repo
}

func TestVarWritesRecordHistory(t *testing.T) {
	h := newHarness(t)
	fixedClock(t)
	_, repo := h.seedAudit(t)

	h.run(t, "var", "k", "auth", "PORT=4000")
	h.run(t, "var", "k", "auth", "PORT=9999")

	snaps, err := h.app.Store.ListSnapshots(store.RepoScope(repo.ID))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(snaps) != 2 {
		t.Fatalf("got %d versions, want 2 — every write records one", len(snaps))
	}
	if snaps[0].Source != store.SourceVar || !strings.Contains(snaps[0].Note, "PORT") {
		t.Errorf("newest = %q/%q, want the source and key that caused it", snaps[0].Source, snaps[0].Note)
	}
}

func TestIdenticalWriteRecordsNothing(t *testing.T) {
	h := newHarness(t)
	fixedClock(t)
	_, repo := h.seedAudit(t)

	h.run(t, "var", "k", "auth", "PORT=4000")
	h.run(t, "var", "k", "auth", "PORT=4000")

	snaps, _ := h.app.Store.ListSnapshots(store.RepoScope(repo.ID))
	if len(snaps) != 1 {
		t.Errorf("got %d versions, want 1 — a write that changes nothing is not history", len(snaps))
	}
}

func TestLayersHaveSeparateTimelines(t *testing.T) {
	h := newHarness(t)
	fixedClock(t)
	g, repo := h.seedAudit(t)

	h.run(t, "add-workflow", "k", "local", "--repos", "auth")
	h.run(t, "var", "k", "auth", "PORT=4000")
	h.run(t, "var", "k", "auth", "PORT=5000", "--workflow", "local")

	wf, _ := h.app.Store.WorkflowByName(g.ID, "local")

	shared, _ := h.app.Store.ListSnapshots(store.RepoScope(repo.ID))
	scoped, _ := h.app.Store.ListSnapshots(store.WorkflowRepoScope(wf.ID, repo.ID))

	if len(shared) != 1 || len(scoped) != 1 {
		t.Errorf("repo-shared=%d workflow=%d, want 1 each — an override is not an edit of the layer below",
			len(shared), len(scoped))
	}
}

func TestAuditListsNewestFirst(t *testing.T) {
	h := newHarness(t)
	fixedClock(t)
	h.seedAudit(t)

	h.run(t, "var", "k", "auth", "PORT=4000")
	h.run(t, "var", "k", "auth", "PORT=9999")

	if err := h.run(t, "audit", "k", "auth"); err != nil {
		t.Fatalf("audit: %v", err)
	}

	out := h.out.String()
	if !strings.Contains(out, "current") {
		t.Errorf("output = %q, want the live version marked", out)
	}
	if !strings.Contains(out, "PORT") {
		t.Errorf("output = %q, want the note naming the key", out)
	}
}

func TestAuditWithoutHistoryTeaches(t *testing.T) {
	h := newHarness(t)
	h.seedAudit(t)

	if err := h.run(t, "audit", "k", "auth"); err != nil {
		t.Fatalf("audit: %v", err)
	}
	if !strings.Contains(h.out.String(), "no history") {
		t.Errorf("output = %q, want a hint rather than an error", h.out.String())
	}
}

func TestRestorePutsTheOldValueBack(t *testing.T) {
	h := newHarness(t)
	fixedClock(t)
	_, repo := h.seedAudit(t)

	h.run(t, "var", "k", "auth", "PORT=4000", "KEEP=yes")
	h.run(t, "var", "k", "auth", "PORT=9999")
	h.run(t, "unset", "k", "auth", "KEEP")

	snaps, _ := h.app.Store.ListSnapshots(store.RepoScope(repo.ID))
	oldest := snaps[len(snaps)-1]

	// Pick the oldest version, which is what the carousel returns on ⏎.
	h.app.pickerInjected = true
	h.app.PickHistory = func(string, []tui.Version) (tui.Version, bool, error) {
		return tui.Version{ID: oldest.ID, When: "3m ago", Changes: []tui.Change{{Key: "PORT", Mark: '~'}}}, true, nil
	}

	if err := h.run(t, "audit", "k", "auth", "--restore"); err != nil {
		t.Fatalf("restore: %v", err)
	}

	vars, _ := h.app.Store.RepoVars(repo.ID)
	if vars["PORT"] != "4000" {
		t.Errorf("PORT = %q, want 4000 restored", vars["PORT"])
	}
	if vars["KEEP"] != "yes" {
		t.Errorf("KEEP = %q, want it back — restore replaces the layer, it does not merge", vars["KEEP"])
	}
}

func TestRestoreIsItselfUndoable(t *testing.T) {
	h := newHarness(t)
	fixedClock(t)
	_, repo := h.seedAudit(t)

	h.run(t, "var", "k", "auth", "PORT=4000")
	h.run(t, "var", "k", "auth", "PORT=9999")

	snaps, _ := h.app.Store.ListSnapshots(store.RepoScope(repo.ID))
	oldest := snaps[len(snaps)-1]

	h.app.pickerInjected = true
	h.app.PickHistory = func(string, []tui.Version) (tui.Version, bool, error) {
		return tui.Version{ID: oldest.ID, Changes: []tui.Change{{Key: "PORT", Mark: '~'}}}, true, nil
	}
	h.run(t, "audit", "k", "auth", "--restore")

	after, _ := h.app.Store.ListSnapshots(store.RepoScope(repo.ID))
	if len(after) != len(snaps)+1 {
		t.Fatalf("got %d versions, want %d — a restore must be undoable too", len(after), len(snaps)+1)
	}
	if after[0].Source != store.SourceRestore {
		t.Errorf("newest source = %q, want %q", after[0].Source, store.SourceRestore)
	}
}

func TestDiffDescribesWhatRestoringWouldDo(t *testing.T) {
	then := map[string]string{"GONE": "a", "SAME": "b", "MOVED": "old"}
	live := map[string]string{"SAME": "b", "MOVED": "new", "ADDED": "c"}

	got := map[string]tui.Change{}
	for _, c := range diffVars(then, live) {
		got[c.Key] = c
	}

	if len(got) != 3 {
		t.Fatalf("got %d changes, want 3 — SAME must not appear", len(got))
	}
	if got["GONE"].Mark != '+' {
		t.Errorf("GONE mark = %q, want '+' — restoring brings it back", got["GONE"].Mark)
	}
	if got["ADDED"].Mark != '-' {
		t.Errorf("ADDED mark = %q, want '-' — restoring removes it", got["ADDED"].Mark)
	}
	if got["MOVED"].Mark != '~' || got["MOVED"].From != "old" {
		t.Errorf("MOVED = %+v, want '~' with From=old", got["MOVED"])
	}
}

func TestAuditRefusesAmbiguousScope(t *testing.T) {
	h := newHarness(t)
	h.seedAudit(t)

	if err := h.run(t, "audit", "k", "--global", "--bag", "db"); err == nil {
		t.Error("want a refusal: --global and --bag name different layers")
	}
	if err := h.run(t, "audit", "k", "--bag", "db"); err == nil {
		t.Error("want a refusal: a bag needs the workflow it belongs to")
	}
}

func TestRestoreRefusesWithoutATerminal(t *testing.T) {
	h := newHarness(t)
	fixedClock(t)
	h.seedAudit(t)

	h.run(t, "var", "k", "auth", "PORT=4000")
	h.run(t, "var", "k", "auth", "PORT=9999")

	// No picker injected and no terminal: there is no way to know which
	// version was meant, and picking one would be a guess about data.
	h.app.PickHistory = nil
	err := h.run(t, "audit", "k", "auth", "--restore")
	if err == nil {
		t.Fatal("want a refusal rather than a guessed version")
	}
	if !strings.Contains(h.out.String(), "9999") && !strings.Contains(h.out.String(), "var") {
		t.Errorf("output = %q, want the timeline still printed so the run is not wasted", h.out.String())
	}
}
