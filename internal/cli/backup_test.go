package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// backupsFor lists the backup file names prizm has kept for a group.
func backupsFor(t *testing.T, group string) []string {
	t.Helper()

	dir := filepath.Join(os.Getenv("XDG_DATA_HOME"), "prizm", "backups", group)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("reading backup dir: %v", err)
	}

	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// strayBackupsIn reports anything in a repo that looks like prizm's leavings.
func strayBackupsIn(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading repo dir: %v", err)
	}

	var stray []string
	for _, e := range entries {
		if e.Name() != ".env" {
			stray = append(stray, e.Name())
		}
	}
	return stray
}

// backupFixture is a group with one repo already applied, so the repo holds a
// symlink that a hand-edit can then displace.
func (h *harness) backupFixture(t *testing.T) string {
	t.Helper()

	repo := h.repoDir(t, "backend")
	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend", "--path", repo)
	h.run(t, "add-workflow", "XYZ", "local")
	h.run(t, "var", "XYZ", "backend", "--workflow", "local", "PORT=4000")

	if err := h.run(t, "up", "XYZ", "local"); err != nil {
		t.Fatalf("first up: %v", err)
	}
	return repo
}

// `up` discards a hand-edit, so the displaced file is the user's only copy and
// must be kept — under prizm's own directory, never beside the env file.
func TestUpKeepsTheDisplacedFileUnderTheDataDir(t *testing.T) {
	h := newHarness(t)
	repo := h.backupFixture(t)

	// A hand-edit that replaces the symlink with a real file, as an editor does.
	os.Remove(filepath.Join(repo, ".env"))
	if err := os.WriteFile(filepath.Join(repo, ".env"), []byte("PORT=9999\n"), 0o600); err != nil {
		t.Fatalf("hand-edit: %v", err)
	}

	if err := h.run(t, "up", "XYZ", "local"); err != nil {
		t.Fatalf("second up: %v", err)
	}

	backups := backupsFor(t, "XYZ")
	if len(backups) != 1 {
		t.Fatalf("backups = %v, want exactly 1 — `up` discards the edit, so it is the only copy", backups)
	}
	if want := "backend__local__.env__"; backups[0][:len(want)] != want {
		t.Errorf("backup name = %q, want it to start with %q", backups[0], want)
	}
	if stray := strayBackupsIn(t, repo); stray != nil {
		t.Errorf("repo holds %v; nothing may be left beside the env file", stray)
	}
}

// `sync` reads the hand-edit into prizm before regenerating the file, so the
// displaced copy is redundant the moment it is written. Keeping one is the
// clutter this exists to stop.
func TestSyncKeepsNoBackupWhenEveryEditWasAbsorbed(t *testing.T) {
	h := newHarness(t)
	repo := h.backupFixture(t)

	// An editor changing one value leaves the rest of the file, prizm's own
	// PRIZM_WORKFLOW stamp included.
	os.Remove(filepath.Join(repo, ".env"))
	if err := os.WriteFile(filepath.Join(repo, ".env"), []byte("PORT=9999\n"+wfStamp), 0o600); err != nil {
		t.Fatalf("hand-edit: %v", err)
	}

	if err := h.run(t, "sync", "XYZ", "backend", "--yes"); err != nil {
		t.Fatalf("sync: %v", err)
	}

	// The edit is safe in prizm...
	g, _ := h.app.Store.GroupByName("XYZ")
	wf, _ := h.app.Store.WorkflowByName(g.ID, "local")
	r, _ := h.app.Store.RepoByName(g.ID, "backend")
	vars, err := h.app.Store.WorkflowRepoVars(wf.ID, r.ID)
	if err != nil {
		t.Fatalf("WorkflowRepoVars: %v", err)
	}
	if vars["PORT"] != "9999" {
		t.Fatalf("PORT = %q, want 9999 — sync must absorb the edit before we judge the backup", vars["PORT"])
	}

	// ...so no copy of it was kept anywhere.
	if backups := backupsFor(t, "XYZ"); backups != nil {
		t.Errorf("backups = %v, want none — sync already holds this content", backups)
	}
	if stray := strayBackupsIn(t, repo); stray != nil {
		t.Errorf("repo holds %v, want none", stray)
	}
}

// A wholesale rewrite drops prizm's own PRIZM_WORKFLOW stamp, which sync
// classifies as an unattributable removal and skips. Nothing of the user's is
// in that key — prizm regenerates it — so it must not resurrect the backup
// this change exists to remove.
func TestSyncKeepsNoBackupWhenTheOnlySkipRemovesNothing(t *testing.T) {
	h := newHarness(t)
	repo := h.backupFixture(t)

	os.Remove(filepath.Join(repo, ".env"))
	if err := os.WriteFile(filepath.Join(repo, ".env"), []byte("PORT=9999\n"), 0o600); err != nil {
		t.Fatalf("hand-edit: %v", err)
	}

	if err := h.run(t, "sync", "XYZ", "backend", "--yes"); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if !strings.Contains(h.out.String(), "skipped") {
		t.Fatalf("expected a skipped item in:\n%s", h.out.String())
	}

	if backups := backupsFor(t, "XYZ"); backups != nil {
		t.Errorf("backups = %v, want none — the skipped key holds no value to lose", backups)
	}
}

// A skipped edit that does hold a value is the opposite case: this rewrite is
// about to undo it, and the file is the only place it exists.
func TestSyncKeepsTheBackupWhenASkippedEditHoldsAValue(t *testing.T) {
	h := newHarness(t)
	repo := h.repoDir(t, "backend")
	bag := filepath.Join(t.TempDir(), "shared.env")

	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend", "--path", repo)
	h.run(t, "add-workflow", "XYZ", "local")
	h.run(t, "shared-add", "XYZ", "local", "addr", "--repos", "backend", "--file", bag)
	h.run(t, "var", "XYZ", "backend", "--workflow", "local", "HOST=${_PRIZM_HOST}")

	if err := os.WriteFile(bag, []byte("# prizm:repos backend\n\n_PRIZM_HOST=one\n"), 0o600); err != nil {
		t.Fatalf("seeding bag: %v", err)
	}
	h.run(t, "shared-sync", "XYZ", "local", "addr", "--yes")

	if err := h.run(t, "up", "XYZ", "local"); err != nil {
		t.Fatalf("up: %v", err)
	}

	// Editing a value that came from a shared bag is the ambiguous case: prizm
	// will not guess between changing the bag and pinning it here, so a
	// non-interactive sync skips it.
	os.Remove(filepath.Join(repo, ".env"))
	if err := os.WriteFile(filepath.Join(repo, ".env"), []byte("HOST=two\n"+wfStamp), 0o600); err != nil {
		t.Fatalf("hand-edit: %v", err)
	}

	if err := h.run(t, "sync", "XYZ", "backend", "--yes"); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if !strings.Contains(h.out.String(), "skipped") {
		t.Fatalf("expected the edit to be skipped in:\n%s", h.out.String())
	}

	backups := backupsFor(t, "XYZ")
	if len(backups) != 1 {
		t.Fatalf("backups = %v, want exactly 1 — the skipped edit exists nowhere else", backups)
	}
}
