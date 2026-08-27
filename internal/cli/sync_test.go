package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/troglodytto/prizm/internal/tui"
)

// editApplied rewrites the file a repo's symlink points at, which is what
// hand-editing a managed .env actually does.
func editApplied(t *testing.T, repoDir, content string) {
	t.Helper()

	dest, err := os.Readlink(filepath.Join(repoDir, ".env"))
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if err := os.WriteFile(dest, []byte(content), 0o600); err != nil {
		t.Fatalf("writing edit: %v", err)
	}
}

func (h *harness) seedSync(t *testing.T) (authDir, backendDir string) {
	t.Helper()

	authDir, backendDir = h.repoDir(t, "auth"), h.repoDir(t, "backend")

	h.run(t, "init", "k")
	h.run(t, "add-repo", "k", "auth", "--path", authDir)
	h.run(t, "add-repo", "k", "backend", "--path", backendDir)
	h.run(t, "add-workflow", "k", "local", "--repos", "auth,backend")

	g, _ := h.app.Store.GroupByName("k")
	wf, _ := h.app.Store.WorkflowByName(g.ID, "local")
	auth, _ := h.app.Store.RepoByName(g.ID, "auth")
	backend, _ := h.app.Store.RepoByName(g.ID, "backend")

	sg, _ := h.app.Store.CreateSharedGroup(wf.ID, "db")
	h.app.Store.AddSharedGroupRepo(sg.ID, auth.ID)
	h.app.Store.AddSharedGroupRepo(sg.ID, backend.ID)
	h.app.Store.SetSharedGroupVar(sg.ID, "_PRIZM_DB_URL", "postgres://old/app")
	h.app.Store.SetSharedGroupVar(sg.ID, "SHARED", "one")

	h.run(t, "var", "k", "auth", "PORT=4000", "DB_URL=${_PRIZM_DB_URL}", "--workflow", "local")
	h.run(t, "var", "k", "backend", "DB_URL=${_PRIZM_DB_URL}", "--workflow", "local")
	h.run(t, "up", "k", "local")
	return authDir, backendDir
}

func (h *harness) varsOf(t *testing.T, repo string) map[string]string {
	t.Helper()

	g, _ := h.app.Store.GroupByName("k")
	wf, _ := h.app.Store.WorkflowByName(g.ID, "local")
	r, _ := h.app.Store.RepoByName(g.ID, repo)

	vars, err := h.app.Store.WorkflowRepoVars(wf.ID, r.ID)
	if err != nil {
		t.Fatalf("WorkflowRepoVars() error = %v", err)
	}
	return vars
}

func TestSyncWritesAnEditBackToTheRepoLayer(t *testing.T) {
	h := newHarness(t)
	authDir, _ := h.seedSync(t)

	editApplied(t, authDir, "PORT=9999\nDB_URL=postgres://old/app\nSHARED=one\n")
	if err := h.run(t, "sync", "k", "auth", "--yes"); err != nil {
		t.Fatalf("sync error = %v", err)
	}

	if got := h.varsOf(t, "auth")["PORT"]; got != "9999" {
		t.Errorf("PORT = %q, want the edited value written back", got)
	}
}

func TestSyncAddsANewKey(t *testing.T) {
	h := newHarness(t)
	authDir, _ := h.seedSync(t)

	editApplied(t, authDir, "PORT=4000\nDB_URL=postgres://old/app\nSHARED=one\nBRAND_NEW=x\n")
	h.run(t, "sync", "k", "auth", "--yes")

	if got := h.varsOf(t, "auth")["BRAND_NEW"]; got != "x" {
		t.Errorf("BRAND_NEW = %q, want it captured", got)
	}
}

// A shared literal propagates — and the bag's file must be kept in step, or
// the next shared-sync reads the stale file and silently reverts this.
func TestSyncPropagatesASharedLiteralAndRewritesItsFile(t *testing.T) {
	h := newHarness(t)
	authDir, backendDir := h.seedSync(t)

	editApplied(t, authDir, "PORT=4000\nDB_URL=postgres://old/app\nSHARED=two\n")
	h.run(t, "sync", "k", "auth", "--yes")
	h.run(t, "up", "k", "local")

	body, _ := os.ReadFile(filepath.Join(backendDir, ".env"))
	if !strings.Contains(string(body), "SHARED=two") {
		t.Errorf("backend/.env = %q, want the shared change to have reached it", body)
	}

	g, _ := h.app.Store.GroupByName("k")
	wf, _ := h.app.Store.WorkflowByName(g.ID, "local")
	sg, _ := h.app.Store.SharedGroupByName(wf.ID, "db")
	if sg.FilePath != "" {
		file, _ := os.ReadFile(sg.FilePath)
		if !strings.Contains(string(file), "SHARED=two") {
			t.Errorf("bag file = %q, want it kept in step with the database", file)
		}
	}
}

// The core refusal: an edited derived value has two legitimate meanings, so
// prizm must not pick one.
func TestSyncSkipsAmbiguousDerivedValues(t *testing.T) {
	h := newHarness(t)
	authDir, _ := h.seedSync(t)

	editApplied(t, authDir, "PORT=4000\nDB_URL=postgres://NEW/app\nSHARED=one\n")
	if err := h.run(t, "sync", "k", "auth", "--yes"); err != nil {
		t.Fatalf("sync error = %v", err)
	}

	out := h.out.String()
	if !strings.Contains(out, "_PRIZM_DB_URL") || !strings.Contains(out, "skipped") {
		t.Errorf("output = %q, want it to name the reference and say it was skipped", out)
	}

	g, _ := h.app.Store.GroupByName("k")
	wf, _ := h.app.Store.WorkflowByName(g.ID, "local")
	sg, _ := h.app.Store.SharedGroupByName(wf.ID, "db")
	vars, _ := h.app.Store.SharedGroupVars(sg.ID)
	if vars["_PRIZM_DB_URL"] != "postgres://old/app" {
		t.Errorf("shared value = %q, want it untouched", vars["_PRIZM_DB_URL"])
	}
}

func TestSyncPinResolvesTheAmbiguityLocally(t *testing.T) {
	h := newHarness(t)
	authDir, _ := h.seedSync(t)

	editApplied(t, authDir, "PORT=4000\nDB_URL=postgres://PINNED/app\nSHARED=one\n")
	if err := h.run(t, "sync", "k", "auth", "--yes", "--pin"); err != nil {
		t.Fatalf("sync --pin error = %v", err)
	}

	if got := h.varsOf(t, "auth")["DB_URL"]; got != "postgres://PINNED/app" {
		t.Errorf("auth DB_URL = %q, want the pinned literal", got)
	}
	if got := h.varsOf(t, "backend")["DB_URL"]; got != "${_PRIZM_DB_URL}" {
		t.Errorf("backend DB_URL = %q, want it still tracking the shared value", got)
	}
}

// Choosing "update the shared value" moves it for every consumer.
func TestSyncInteractiveUpdateShared(t *testing.T) {
	h := newHarness(t)
	authDir, _ := h.seedSync(t)

	h.app.pickerInjected = true
	h.app.Resolve = func(_ string, rows []tui.ResolveRow) ([]int, error) {
		out := make([]int, len(rows))
		return out, nil // index 0 = the first choice, "update the shared value"
	}

	editApplied(t, authDir, "PORT=4000\nDB_URL=postgres://MOVED/app\nSHARED=one\n")
	if err := h.run(t, "sync", "k", "auth"); err != nil {
		t.Fatalf("sync error = %v", err)
	}

	g, _ := h.app.Store.GroupByName("k")
	wf, _ := h.app.Store.WorkflowByName(g.ID, "local")
	sg, _ := h.app.Store.SharedGroupByName(wf.ID, "db")

	vars, _ := h.app.Store.SharedGroupVars(sg.ID)
	if vars["_PRIZM_DB_URL"] != "postgres://MOVED/app" {
		t.Errorf("shared value = %q, want the update applied", vars["_PRIZM_DB_URL"])
	}
	if got := h.varsOf(t, "auth")["DB_URL"]; got != "${_PRIZM_DB_URL}" {
		t.Errorf("auth DB_URL = %q, want it still tracking the shared value", got)
	}
}

// Cancelling the resolver changes nothing.
func TestSyncCancellationChangesNothing(t *testing.T) {
	h := newHarness(t)
	authDir, _ := h.seedSync(t)

	h.app.pickerInjected = true
	h.app.Resolve = func(string, []tui.ResolveRow) ([]int, error) { return nil, tui.ErrCancelled }

	editApplied(t, authDir, "PORT=9999\nDB_URL=postgres://old/app\nSHARED=one\n")
	if err := h.run(t, "sync", "k", "auth"); err != nil {
		t.Errorf("cancelling returned %v, want nil", err)
	}
	if got := h.varsOf(t, "auth")["PORT"]; got != "4000" {
		t.Errorf("PORT = %q, want it unchanged after cancelling", got)
	}
}

// Sync regenerates the file, so a skipped edit is undone on disk.
func TestSyncRegeneratesTheFile(t *testing.T) {
	h := newHarness(t)
	authDir, _ := h.seedSync(t)

	editApplied(t, authDir, "\n\nPORT=9999\n\nDB_URL=postgres://old/app\nSHARED=one\n")
	h.run(t, "sync", "k", "auth", "--yes")

	body, _ := os.ReadFile(filepath.Join(authDir, ".env"))
	if strings.Contains(string(body), "\n\n") {
		t.Errorf("file = %q, want it regenerated cleanly from prizm", body)
	}
	if !strings.Contains(string(body), "PORT=9999") {
		t.Errorf("file = %q, want the reconciled value", body)
	}
}

func TestSyncCleanRepoSaysNothingToDo(t *testing.T) {
	h := newHarness(t)
	h.seedSync(t)

	if err := h.run(t, "sync", "k"); err != nil {
		t.Fatalf("sync error = %v", err)
	}
	if !strings.Contains(h.out.String(), "nothing to reconcile") {
		t.Errorf("output = %q, want a nothing-to-do message", h.out.String())
	}
}

// sync used to regenerate a shared file from the database, which destroyed
// anything written but not yet loaded with shared-sync — silently, while
// reporting success.
func TestSyncKeepsUnsyncedEditsInASharedFile(t *testing.T) {
	h := newHarness(t)
	fixedClock(t)
	authDir, _ := h.seedSync(t)
	h.run(t, "up", "k", "local")

	g, _ := h.app.Store.GroupByName("k")
	wf, _ := h.app.Store.WorkflowByName(g.ID, "local")
	bag, _ := h.app.Store.SharedGroupByName(wf.ID, "db")

	// The fixture's bag has no backing file; give it one.
	bagPath := filepath.Join(t.TempDir(), "db.env")
	if err := h.app.Store.SetSharedGroupFile(bag.ID, bagPath); err != nil {
		t.Fatalf("SetSharedGroupFile: %v", err)
	}
	bag.FilePath = bagPath

	// What the user has on disk: a synced key, plus a comment and a key they
	// have written but not yet loaded.
	const handWritten = "# prizm:repos auth,backend\n" +
		"SHARED=one\n" +
		"\n" +
		"# rotate me in Q3\n" +
		"NEW_KEY=notyetsynced\n"
	if err := os.WriteFile(bag.FilePath, []byte(handWritten), 0o600); err != nil {
		t.Fatalf("writing bag file: %v", err)
	}

	// A hand-edit to the repo drives sync to update the shared value.
	editApplied(t, authDir, "PORT=4000\nSHARED=two\n")
	if err := h.run(t, "sync", "k", "auth", "--yes"); err != nil {
		t.Fatalf("sync: %v", err)
	}

	raw, err := os.ReadFile(bag.FilePath)
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	got := string(raw)

	if !strings.Contains(got, "SHARED=two") {
		t.Errorf("the synced key was not updated:\n%s", got)
	}
	for _, want := range []string{"NEW_KEY=notyetsynced", "# rotate me in Q3", "# prizm:repos"} {
		if !strings.Contains(got, want) {
			t.Errorf("sync destroyed %q:\n%s", want, got)
		}
	}
}
