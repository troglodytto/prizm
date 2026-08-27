package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/troglodytto/prizm/internal/crypto"
	"github.com/troglodytto/prizm/internal/store"
)

type harness struct {
	app *App
	out *bytes.Buffer
	err *bytes.Buffer
	cwd string
}

// stamp is what prizm appends to every generated file so a consumer can tell
// which workflow produced it. Tests that assert exact file content include it.
const wfStamp = "PRIZM_WORKFLOW=local\n"

func newHarness(t *testing.T) *harness {
	t.Helper()

	// Point every path prizm derives at a temp dir. Without this the suite
	// takes the real ~/.local/share/prizm/apply.lock and writes built env
	// files into the user's actual data directory — it left stray groups
	// there. A test must not be able to touch what it is testing.
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	s, err := store.Open(filepath.Join(t.TempDir(), "prizm.db"), crypto.Plaintext{})
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { s.Close() })

	h := &harness{out: &bytes.Buffer{}, err: &bytes.Buffer{}, cwd: t.TempDir()}
	h.app = &App{
		Store:   s,
		Out:     h.out,
		Err:     h.err,
		Now:     func() time.Time { return time.Unix(1700000000, 0) },
		Cwd:     func() (string, error) { return h.cwd, nil },
		Confirm: func(string) (bool, error) { return true, nil },
	}
	return h
}

// run executes one command line through the real cobra tree.
func (h *harness) run(t *testing.T, args ...string) error {
	t.Helper()

	h.out.Reset()
	h.err.Reset()

	cmd := NewRootCmd(h.app)
	cmd.SetOut(h.out)
	cmd.SetErr(h.err)
	cmd.SetArgs(args)
	return Run(cmd)
}

// repoDir makes a real directory for a repo to be linked into.
func (h *harness) repoDir(t *testing.T, name string) string {
	t.Helper()

	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	return dir
}

func TestInitCreatesGroup(t *testing.T) {
	h := newHarness(t)

	if err := h.run(t, "init", "XYZ"); err != nil {
		t.Fatalf("init error = %v", err)
	}
	if _, err := h.app.Store.GroupByName("XYZ"); err != nil {
		t.Fatalf("GroupByName() after init: %v", err)
	}
	if !strings.Contains(h.out.String(), "XYZ") {
		t.Errorf("output = %q, want it to mention the group", h.out.String())
	}
}

func TestInitRejectsDuplicateGroup(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "XYZ")

	if err := h.run(t, "init", "XYZ"); err == nil {
		t.Fatal("second init error = nil, want error")
	}
}

func TestAddRepoDefaultsToCwd(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "XYZ")

	if err := h.run(t, "add-repo", "XYZ", "backend"); err != nil {
		t.Fatalf("add-repo error = %v", err)
	}

	g, _ := h.app.Store.GroupByName("XYZ")
	repo, err := h.app.Store.RepoByName(g.ID, "backend")
	if err != nil {
		t.Fatalf("RepoByName() error = %v", err)
	}
	if repo.Path != h.cwd {
		t.Errorf("Path = %q, want cwd %q", repo.Path, h.cwd)
	}
	if repo.EnvFile != ".env" {
		t.Errorf("EnvFile = %q, want %q", repo.EnvFile, ".env")
	}
}

func TestAddRepoStoresAbsolutePath(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "XYZ")

	if err := h.run(t, "add-repo", "XYZ", "backend", "--path", "."); err != nil {
		t.Fatalf("add-repo error = %v", err)
	}

	g, _ := h.app.Store.GroupByName("XYZ")
	repo, _ := h.app.Store.RepoByName(g.ID, "backend")
	if !filepath.IsAbs(repo.Path) {
		t.Errorf("Path = %q, want absolute — paths are a stable contract", repo.Path)
	}
}

func TestAddRepoCustomEnvFile(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "XYZ")

	if err := h.run(t, "add-repo", "XYZ", "frontend", "--env-file", ".env.local"); err != nil {
		t.Fatalf("add-repo error = %v", err)
	}

	g, _ := h.app.Store.GroupByName("XYZ")
	repo, _ := h.app.Store.RepoByName(g.ID, "frontend")
	if repo.EnvFile != ".env.local" {
		t.Errorf("EnvFile = %q, want %q", repo.EnvFile, ".env.local")
	}
}

func TestAddRepoUnknownGroup(t *testing.T) {
	h := newHarness(t)

	err := h.run(t, "add-repo", "NOPE", "backend")
	if err == nil || !strings.Contains(err.Error(), "NOPE") {
		t.Errorf("error = %v, want it to name the group", err)
	}
}

func TestAddWorkflowWithRepos(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "frontend")
	h.run(t, "add-repo", "XYZ", "backend")
	h.run(t, "add-repo", "XYZ", "ai")

	if err := h.run(t, "add-workflow", "XYZ", "local", "--repos", "frontend,backend", "--tag", "local"); err != nil {
		t.Fatalf("add-workflow error = %v", err)
	}

	g, _ := h.app.Store.GroupByName("XYZ")
	wf, err := h.app.Store.WorkflowByName(g.ID, "local")
	if err != nil {
		t.Fatalf("WorkflowByName() error = %v", err)
	}
	repos, _ := h.app.Store.WorkflowRepos(wf.ID)
	if len(repos) != 2 {
		t.Errorf("workflow has %d repos, want 2", len(repos))
	}
	if wf.Tag != "local" {
		t.Errorf("Tag = %q, want %q", wf.Tag, "local")
	}
}

// The spec: defaulting to "all" is friendlier; explicit subsets are the
// exception, not the rule.
func TestAddWorkflowDefaultsToAllRepos(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "frontend")
	h.run(t, "add-repo", "XYZ", "backend")

	if err := h.run(t, "add-workflow", "XYZ", "full"); err != nil {
		t.Fatalf("add-workflow error = %v", err)
	}

	g, _ := h.app.Store.GroupByName("XYZ")
	wf, _ := h.app.Store.WorkflowByName(g.ID, "full")
	repos, _ := h.app.Store.WorkflowRepos(wf.ID)
	if len(repos) != 2 {
		t.Errorf("workflow has %d repos, want all 2", len(repos))
	}
}

func TestAddWorkflowRejectsUnknownRepo(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "frontend")

	err := h.run(t, "add-workflow", "XYZ", "local", "--repos", "frontend,ghost")
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error = %v, want it to name the unknown repo", err)
	}
}

func TestAddWorkflowRejectsReservedName(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "XYZ")

	err := h.run(t, "add-workflow", "XYZ", "status")
	if err == nil || !strings.Contains(err.Error(), "status") {
		t.Errorf("error = %v, want it to name the rejected word", err)
	}
}

func TestLsWithoutArgsListsGroups(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "XYZ")
	h.run(t, "init", "ABC")

	if err := h.run(t, "ls"); err != nil {
		t.Fatalf("ls error = %v", err)
	}
	out := h.out.String()
	if !strings.Contains(out, "XYZ") || !strings.Contains(out, "ABC") {
		t.Errorf("ls output = %q, want both groups", out)
	}
}

func TestLsGroupListsWorkflowsAndRepos(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend")
	h.run(t, "add-workflow", "XYZ", "local", "--tag", "local")

	if err := h.run(t, "ls", "XYZ"); err != nil {
		t.Fatalf("ls XYZ error = %v", err)
	}
	out := h.out.String()
	for _, want := range []string{"backend", "local"} {
		if !strings.Contains(out, want) {
			t.Errorf("ls XYZ output = %q, want it to mention %q", out, want)
		}
	}
}
