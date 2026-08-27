package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func (h *harness) sharedFixture(t *testing.T) (beDir, authDir, file string) {
	t.Helper()

	beDir, authDir = h.repoDir(t, "backend"), h.repoDir(t, "auth")
	file = filepath.Join(t.TempDir(), "db.env")

	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend", "--path", beDir)
	h.run(t, "add-repo", "XYZ", "auth", "--path", authDir)
	h.run(t, "add-workflow", "XYZ", "local")

	if err := h.run(t, "shared-add", "XYZ", "local", "db", "--repos", "backend,auth", "--file", file); err != nil {
		t.Fatalf("shared-add error = %v", err)
	}
	return beDir, authDir, file
}

func (h *harness) bagVars(t *testing.T) map[string]string {
	t.Helper()

	g, _ := h.app.Store.GroupByName("XYZ")
	wf, _ := h.app.Store.WorkflowByName(g.ID, "local")
	sg, _ := h.app.Store.SharedGroupByName(wf.ID, "db")

	vars, err := h.app.Store.SharedGroupVars(sg.ID)
	if err != nil {
		t.Fatalf("SharedGroupVars() error = %v", err)
	}
	return vars
}

func TestSharedAddCreatesBagAndFile(t *testing.T) {
	h := newHarness(t)
	_, _, file := h.sharedFixture(t)

	body, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("bag file not created: %v", err)
	}
	// Members render in sorted order, like Render sorts keys: the file must be
	// byte-identical for the same bag no matter what order --repos was given in.
	if !strings.Contains(string(body), "# prizm:repos auth,backend") {
		t.Errorf("file = %q, want the repos header in sorted order", body)
	}

	info, _ := os.Stat(file)
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("bag file mode = %04o, want 0600 — it holds plaintext secrets", perm)
	}
}

func TestSharedSyncLoadsEditedFile(t *testing.T) {
	h := newHarness(t)
	_, _, file := h.sharedFixture(t)

	os.WriteFile(file, []byte(
		"# prizm:repos backend,auth\n\n"+
			"_PRIZM_DB_USER=svc_app\n"+
			"_PRIZM_DB_URL=postgres://${_PRIZM_DB_USER}@h/db\n"), 0o600)

	if err := h.run(t, "shared-sync", "XYZ", "local", "db"); err != nil {
		t.Fatalf("shared-sync error = %v", err)
	}

	want := map[string]string{
		"_PRIZM_DB_USER": "svc_app",
		"_PRIZM_DB_URL":  "postgres://${_PRIZM_DB_USER}@h/db",
	}
	if diff := cmp.Diff(want, h.bagVars(t)); diff != "" {
		t.Errorf("bag vars mismatch (-want +got):\n%s", diff)
	}
}

func TestSharedSyncIsAFullReplace(t *testing.T) {
	h := newHarness(t)
	_, _, file := h.sharedFixture(t)

	os.WriteFile(file, []byte("# prizm:repos backend,auth\n\nA=1\nB=2\n"), 0o600)
	h.run(t, "shared-sync", "XYZ", "local", "db")

	os.WriteFile(file, []byte("# prizm:repos backend,auth\n\nA=1\n"), 0o600)
	if err := h.run(t, "shared-sync", "XYZ", "local", "db"); err != nil {
		t.Fatalf("shared-sync error = %v", err)
	}

	if _, still := h.bagVars(t)["B"]; still {
		t.Error("B survived; sync must be a full replace so the file stays authoritative")
	}
}

func TestSharedSyncReconcilesMembershipFromHeader(t *testing.T) {
	h := newHarness(t)
	_, _, file := h.sharedFixture(t)

	os.WriteFile(file, []byte("# prizm:repos backend\n\nA=1\n"), 0o600)
	if err := h.run(t, "shared-sync", "XYZ", "local", "db"); err != nil {
		t.Fatalf("shared-sync error = %v", err)
	}

	g, _ := h.app.Store.GroupByName("XYZ")
	wf, _ := h.app.Store.WorkflowByName(g.ID, "local")
	sg, _ := h.app.Store.SharedGroupByName(wf.ID, "db")

	repos, _ := h.app.Store.SharedGroupRepos(sg.ID)
	if len(repos) != 1 || repos[0].Name != "backend" {
		t.Errorf("members = %+v, want only backend after the header changed", repos)
	}
}

func TestSharedSyncShowsDiffAndRespectsDeclining(t *testing.T) {
	h := newHarness(t)
	_, _, file := h.sharedFixture(t)

	var prompted bool
	h.app.Confirm = func(string) (bool, error) {
		prompted = true
		return false, nil
	}

	os.WriteFile(file, []byte("# prizm:repos backend,auth\n\nA=1\n"), 0o600)
	h.run(t, "shared-sync", "XYZ", "local", "db")

	if !prompted {
		t.Error("shared-sync did not ask before writing")
	}
	if !strings.Contains(h.out.String(), "A") {
		t.Errorf("output = %q, want the key-level diff", h.out.String())
	}
	if got := h.bagVars(t); len(got) != 0 {
		t.Errorf("bag = %v, want unchanged after declining", got)
	}
}

func TestSharedSyncNoChangesIsQuietAndSucceeds(t *testing.T) {
	h := newHarness(t)
	_, _, file := h.sharedFixture(t)

	os.WriteFile(file, []byte("# prizm:repos backend,auth\n\nA=1\n"), 0o600)
	h.run(t, "shared-sync", "XYZ", "local", "db")

	h.app.Confirm = func(string) (bool, error) {
		t.Error("Confirm was called when nothing changed")
		return false, nil
	}
	if err := h.run(t, "shared-sync", "XYZ", "local", "db"); err != nil {
		t.Fatalf("second shared-sync error = %v", err)
	}
	if !strings.Contains(h.out.String(), "up to date") {
		t.Errorf("output = %q, want an up-to-date message", h.out.String())
	}
}

func TestSharedSyncRejectsUnknownRepoInHeader(t *testing.T) {
	h := newHarness(t)
	_, _, file := h.sharedFixture(t)

	os.WriteFile(file, []byte("# prizm:repos backend,ghost\n\nA=1\n"), 0o600)

	err := h.run(t, "shared-sync", "XYZ", "local", "db")
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error = %v, want it to name the unknown repo", err)
	}
}

func TestSharedLsShowsBagsAndFiles(t *testing.T) {
	h := newHarness(t)
	_, _, file := h.sharedFixture(t)

	if err := h.run(t, "shared-ls", "XYZ"); err != nil {
		t.Fatalf("shared-ls error = %v", err)
	}
	out := h.out.String()
	if !strings.Contains(out, "db") || !strings.Contains(out, file) {
		t.Errorf("output = %q, want the bag name and its file", out)
	}
}

// The full loop: edit a file, sync it, and have `up` produce derived values.
func TestSharedBagFeedsUpEndToEnd(t *testing.T) {
	h := newHarness(t)
	beDir, authDir, file := h.sharedFixture(t)

	os.WriteFile(file, []byte(
		"# prizm:repos backend,auth\n\n"+
			"_PRIZM_DB_USER=svc_app\n"+
			"_PRIZM_DB_PASS=hunter2\n"+
			"_PRIZM_DB_URL=postgres://${_PRIZM_DB_USER}:${_PRIZM_DB_PASS}@localhost:5432/app\n"), 0o600)

	if err := h.run(t, "shared-sync", "XYZ", "local", "db"); err != nil {
		t.Fatalf("shared-sync error = %v", err)
	}
	h.run(t, "var", "XYZ", "backend", "DB_URL=${_PRIZM_DB_URL}", "--workflow", "local")
	h.run(t, "var", "XYZ", "auth", "DATABASE_URL=${_PRIZM_DB_URL}", "--workflow", "local")

	if err := h.run(t, "up", "XYZ", "local"); err != nil {
		t.Fatalf("up error = %v\nout: %s", err, h.out.String())
	}

	dsn := "postgres://svc_app:hunter2@localhost:5432/app"
	for dir, want := range map[string]string{
		beDir:   "DB_URL=" + dsn + "\n" + wfStamp,
		authDir: "DATABASE_URL=" + dsn + "\n" + wfStamp,
	} {
		got, err := os.ReadFile(filepath.Join(dir, ".env"))
		if err != nil {
			t.Fatalf("reading env: %v", err)
		}
		if string(got) != want {
			t.Errorf("%s/.env = %q, want %q", dir, got, want)
		}
	}
}

func TestSharedAddKeepsAFileThatIsAlreadyThere(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "k")
	h.run(t, "add-repo", "k", "auth", "--path", h.repoDir(t, "auth"))
	h.run(t, "add-workflow", "k", "local", "--repos", "auth")

	// Someone wrote the bag file first — by hand, or from a script moments
	// earlier. Creating the bag must not be what destroys it.
	path := filepath.Join(t.TempDir(), "infra.env")
	const content = "# prizm:repos auth\n_PRIZM_DB=postgres://real/data\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing bag file: %v", err)
	}

	if err := h.run(t, "shared-add", "k", "local", "infra", "--repos", "auth", "--file", path); err != nil {
		t.Fatalf("shared-add: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if string(got) != content {
		t.Errorf("file = %q, want it untouched (%q)", got, content)
	}
	if !strings.Contains(h.out.String(), "kept the file") {
		t.Errorf("output = %q, want it to say the file was kept", h.out.String())
	}
}

func TestSharedAddStillWritesATemplateWhenThereIsNoFile(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "k")
	h.run(t, "add-repo", "k", "auth", "--path", h.repoDir(t, "auth"))
	h.run(t, "add-workflow", "k", "local", "--repos", "auth")

	path := filepath.Join(t.TempDir(), "infra.env")
	if err := h.run(t, "shared-add", "k", "local", "infra", "--repos", "auth", "--file", path); err != nil {
		t.Fatalf("shared-add: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected a template to be created: %v", err)
	}
	if !strings.Contains(string(got), "prizm:repos") {
		t.Errorf("template = %q, want the repos header", got)
	}
}
