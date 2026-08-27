package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpWritesAndLinksEachRepo(t *testing.T) {
	h := newHarness(t)
	beDir := h.repoDir(t, "backend")
	feDir := h.repoDir(t, "frontend")

	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend", "--path", beDir)
	h.run(t, "add-repo", "XYZ", "frontend", "--path", feDir)
	h.run(t, "add-workflow", "XYZ", "local")
	h.run(t, "var", "XYZ", "backend", "PORT=8080", "--workflow", "local")
	h.run(t, "var", "XYZ", "frontend", "API_URL=http://localhost:8080", "--workflow", "local")

	if err := h.run(t, "up", "XYZ", "local"); err != nil {
		t.Fatalf("up error = %v\nout: %s", err, h.out.String())
	}

	for dir, want := range map[string]string{
		beDir: "PORT=8080\n",
		feDir: "API_URL=http://localhost:8080\n",
	} {
		link := filepath.Join(dir, ".env")
		info, err := os.Lstat(link)
		if err != nil {
			t.Fatalf("lstat %s: %v", link, err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			t.Errorf("%s is not a symlink", link)
		}
		got, err := os.ReadFile(link)
		if err != nil {
			t.Fatalf("reading %s: %v", link, err)
		}
		if string(got) != want {
			t.Errorf("%s content = %q, want %q", link, got, want)
		}
	}
}

// The end-to-end shape the whole design exists for.
func TestUpResolvesSharedDerivedValuesOpaquely(t *testing.T) {
	h := newHarness(t)
	beDir := h.repoDir(t, "backend")
	authDir := h.repoDir(t, "auth")

	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend", "--path", beDir)
	h.run(t, "add-repo", "XYZ", "auth", "--path", authDir)
	h.run(t, "add-workflow", "XYZ", "local")

	g, _ := h.app.Store.GroupByName("XYZ")
	wf, _ := h.app.Store.WorkflowByName(g.ID, "local")
	be, _ := h.app.Store.RepoByName(g.ID, "backend")
	auth, _ := h.app.Store.RepoByName(g.ID, "auth")

	sg, _ := h.app.Store.CreateSharedGroup(wf.ID, "db")
	h.app.Store.AddSharedGroupRepo(sg.ID, be.ID)
	h.app.Store.AddSharedGroupRepo(sg.ID, auth.ID)
	h.app.Store.SetSharedGroupVar(sg.ID, "_PRIZM_DB_USER", "svc_app")
	h.app.Store.SetSharedGroupVar(sg.ID, "_PRIZM_DB_PASS", "hunter2")
	h.app.Store.SetSharedGroupVar(sg.ID, "_PRIZM_DB_URL", "postgres://${_PRIZM_DB_USER}:${_PRIZM_DB_PASS}@localhost:5432/app")

	h.run(t, "var", "XYZ", "backend", "DB_URL=${_PRIZM_DB_URL}", "--workflow", "local")
	h.run(t, "var", "XYZ", "auth", "DATABASE_URL=${_PRIZM_DB_URL}", "--workflow", "local")

	if err := h.run(t, "up", "XYZ", "local"); err != nil {
		t.Fatalf("up error = %v\nout: %s", err, h.out.String())
	}

	dsn := "postgres://svc_app:hunter2@localhost:5432/app"
	for dir, want := range map[string]string{
		beDir:   "DB_URL=" + dsn + "\n",
		authDir: "DATABASE_URL=" + dsn + "\n",
	} {
		got, err := os.ReadFile(filepath.Join(dir, ".env"))
		if err != nil {
			t.Fatalf("reading env: %v", err)
		}
		if string(got) != want {
			t.Errorf("%s/.env = %q, want %q", dir, got, want)
		}
		if strings.Contains(string(got), "_PRIZM_") {
			t.Errorf("%s/.env leaked internal plumbing: %q", dir, got)
		}
	}
}

func TestUpSkipsFailingRepoAndContinues(t *testing.T) {
	h := newHarness(t)
	beDir := h.repoDir(t, "backend")
	feDir := h.repoDir(t, "frontend")

	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend", "--path", beDir)
	h.run(t, "add-repo", "XYZ", "frontend", "--path", feDir)
	h.run(t, "add-workflow", "XYZ", "local")
	h.run(t, "var", "XYZ", "backend", "DB_URL=postgres://${_PRIZM_MISSING}@h/db", "--workflow", "local")
	h.run(t, "var", "XYZ", "frontend", "API_URL=http://localhost", "--workflow", "local")

	if err := h.run(t, "up", "XYZ", "local"); err == nil {
		t.Fatal("up error = nil, want a non-zero result when a repo failed")
	}

	if _, err := os.Lstat(filepath.Join(feDir, ".env")); err != nil {
		t.Errorf("frontend was not applied: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(beDir, ".env")); !os.IsNotExist(err) {
		t.Error("backend .env exists; a failing repo must be left untouched")
	}

	out := h.out.String()
	if !strings.Contains(out, "_PRIZM_MISSING") || !strings.Contains(out, "backend") {
		t.Errorf("output = %q, want it to name the repo and the missing reference", out)
	}
}

func TestUpPreservesAnExistingRealEnvFile(t *testing.T) {
	h := newHarness(t)
	beDir := h.repoDir(t, "backend")
	os.WriteFile(filepath.Join(beDir, ".env"), []byte("PRECIOUS=keepme\n"), 0o600)

	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend", "--path", beDir)
	h.run(t, "add-workflow", "XYZ", "local")
	h.run(t, "var", "XYZ", "backend", "PORT=8080", "--workflow", "local")

	if err := h.run(t, "up", "XYZ", "local"); err != nil {
		t.Fatalf("up error = %v", err)
	}

	entries, _ := os.ReadDir(beDir)
	var found bool
	for _, e := range entries {
		if strings.Contains(e.Name(), ".prizm-backup.") {
			found = true
			if body, _ := os.ReadFile(filepath.Join(beDir, e.Name())); string(body) != "PRECIOUS=keepme\n" {
				t.Errorf("backup content = %q, want the original", body)
			}
		}
	}
	if !found {
		t.Error("no .prizm-backup file; the user's original .env was destroyed")
	}
}

func TestUpPromptsForProdTaggedWorkflow(t *testing.T) {
	h := newHarness(t)
	dir := h.repoDir(t, "backend")

	var prompted string
	h.app.Confirm = func(prompt string) (bool, error) {
		prompted = prompt
		return false, nil
	}

	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend", "--path", dir)
	h.run(t, "add-workflow", "XYZ", "production", "--tag", "prod")
	h.run(t, "var", "XYZ", "backend", "A=1", "--workflow", "production")

	if err := h.run(t, "up", "XYZ", "production"); err == nil {
		t.Fatal("up error = nil, want an aborted run after declining")
	}
	if !strings.Contains(prompted, "production") {
		t.Errorf("prompt = %q, want it to name the workflow", prompted)
	}
	if _, err := os.Lstat(filepath.Join(dir, ".env")); !os.IsNotExist(err) {
		t.Error("declining the prompt still applied the workflow")
	}
}

func TestUpProdPromptSkippedWithYes(t *testing.T) {
	h := newHarness(t)
	dir := h.repoDir(t, "backend")

	h.app.Confirm = func(string) (bool, error) {
		t.Error("Confirm was called despite --yes")
		return false, nil
	}

	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend", "--path", dir)
	h.run(t, "add-workflow", "XYZ", "production", "--tag", "prod")
	h.run(t, "var", "XYZ", "backend", "A=1", "--workflow", "production")

	if err := h.run(t, "up", "XYZ", "production", "--yes"); err != nil {
		t.Fatalf("up --yes error = %v", err)
	}
}

func TestUpDoesNotPromptForUntaggedWorkflow(t *testing.T) {
	h := newHarness(t)
	dir := h.repoDir(t, "backend")

	h.app.Confirm = func(string) (bool, error) {
		t.Error("Confirm was called for an untagged workflow")
		return false, nil
	}

	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend", "--path", dir)
	h.run(t, "add-workflow", "XYZ", "local")
	h.run(t, "var", "XYZ", "backend", "A=1", "--workflow", "local")

	if err := h.run(t, "up", "XYZ", "local"); err != nil {
		t.Fatalf("up error = %v", err)
	}
}

func TestUpIsIdempotent(t *testing.T) {
	h := newHarness(t)
	dir := h.repoDir(t, "backend")

	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend", "--path", dir)
	h.run(t, "add-workflow", "XYZ", "local")
	h.run(t, "var", "XYZ", "backend", "A=1", "--workflow", "local")

	for i := 0; i < 3; i++ {
		if err := h.run(t, "up", "XYZ", "local"); err != nil {
			t.Fatalf("up run %d error = %v", i, err)
		}
	}

	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("repo dir has %v, want only .env — repeated ups must not accumulate files", names)
	}
}

func TestUpRecordsWhatWasApplied(t *testing.T) {
	h := newHarness(t)
	dir := h.repoDir(t, "backend")

	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend", "--path", dir)
	h.run(t, "add-workflow", "XYZ", "local")
	h.run(t, "var", "XYZ", "backend", "A=1", "--workflow", "local")
	h.run(t, "up", "XYZ", "local")

	g, _ := h.app.Store.GroupByName("XYZ")
	if g.UseCount != 1 {
		t.Errorf("UseCount = %d, want 1 after one up", g.UseCount)
	}
}

func TestUpUnknownWorkflow(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "XYZ")

	if err := h.run(t, "up", "XYZ", "ghost"); err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error = %v, want it to name the unknown workflow", err)
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	h := newHarness(t)
	dir := h.repoDir(t, "auth")

	h.run(t, "init", "k")
	h.run(t, "add-repo", "k", "auth", "--path", dir)
	h.run(t, "add-workflow", "k", "local", "--repos", "auth")
	h.run(t, "var", "k", "auth", "PORT=4000")

	if err := h.run(t, "up", "k", "local", "--dry-run"); err != nil {
		t.Fatalf("dry run: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, ".env")); !os.IsNotExist(err) {
		t.Error(".env exists — a dry run must not write")
	}

	out := h.out.String()
	if !strings.Contains(out, "PORT") {
		t.Errorf("output = %q, want the keys that would be written", out)
	}
	if !strings.Contains(out, "dry run") {
		t.Errorf("output = %q, want it to say nothing was written", out)
	}
}

func TestDryRunOnAnAppliedRepoReportsNoChange(t *testing.T) {
	h := newHarness(t)
	dir := h.repoDir(t, "auth")

	h.run(t, "init", "k")
	h.run(t, "add-repo", "k", "auth", "--path", dir)
	h.run(t, "add-workflow", "k", "local", "--repos", "auth")
	h.run(t, "var", "k", "auth", "PORT=4000")
	h.run(t, "up", "k", "local")

	if err := h.run(t, "up", "k", "local", "--dry-run"); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !strings.Contains(h.out.String(), "already up to date") {
		t.Errorf("output = %q, want it to report no change", h.out.String())
	}
}

func TestDryRunReportsAFailingRepoWithoutApplyingAnything(t *testing.T) {
	h := newHarness(t)
	dir := h.repoDir(t, "auth")

	h.run(t, "init", "k")
	h.run(t, "add-repo", "k", "auth", "--path", dir)
	h.run(t, "add-workflow", "k", "local", "--repos", "auth")
	h.run(t, "var", "k", "auth", "BROKEN=${_PRIZM_MISSING}")

	if err := h.run(t, "up", "k", "local", "--dry-run"); err == nil {
		t.Fatal("want an error: the repo would fail to apply")
	}
	if _, err := os.Stat(filepath.Join(dir, ".env")); !os.IsNotExist(err) {
		t.Error(".env exists — a failed dry run must not write either")
	}
}
