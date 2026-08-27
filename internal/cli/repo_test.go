package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLooksLikePath(t *testing.T) {
	paths := []string{".", "..", "./auth", "../auth", "~/code/auth", "/abs/auth", "code/auth"}
	for _, p := range paths {
		if !looksLikePath(p) {
			t.Errorf("looksLikePath(%q) = false, want true", p)
		}
	}

	names := []string{"auth", "acme-auth", "enterprise_search", "svc.v2"}
	for _, n := range names {
		if looksLikePath(n) {
			t.Errorf("looksLikePath(%q) = true, want false", n)
		}
	}
}

// The reported case: `.` is the directory, not the repo's name.
func TestAddRepoDotMeansHereAndInfersTheName(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "acme")

	dir := filepath.Join(t.TempDir(), "auth")
	os.MkdirAll(dir, 0o755)
	h.cwd = dir

	if err := h.run(t, "add-repo", "acme", "."); err != nil {
		t.Fatalf("add-repo . error = %v", err)
	}

	g, _ := h.app.Store.GroupByName("acme")
	repo, err := h.app.Store.RepoByName(g.ID, "auth")
	if err != nil {
		t.Fatalf(`expected a repo named "auth" inferred from the directory: %v`, err)
	}
	if repo.Path != dir {
		t.Errorf("Path = %q, want %q", repo.Path, dir)
	}
}

func TestAddRepoWithNoSecondArgumentInfersBoth(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "acme")

	dir := filepath.Join(t.TempDir(), "platform")
	os.MkdirAll(dir, 0o755)
	h.cwd = dir

	if err := h.run(t, "add-repo", "acme"); err != nil {
		t.Fatalf("add-repo error = %v", err)
	}

	g, _ := h.app.Store.GroupByName("acme")
	if _, err := h.app.Store.RepoByName(g.ID, "platform"); err != nil {
		t.Fatalf(`expected a repo named "platform": %v`, err)
	}
}

func TestAddRepoRelativePathInfersName(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "acme")

	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "sso"), 0o755)
	h.cwd = root

	if err := h.run(t, "add-repo", "acme", "./sso"); err != nil {
		t.Fatalf("add-repo ./sso error = %v", err)
	}

	g, _ := h.app.Store.GroupByName("acme")
	repo, err := h.app.Store.RepoByName(g.ID, "sso")
	if err != nil {
		t.Fatalf(`expected a repo named "sso": %v`, err)
	}
	if repo.Path != filepath.Join(root, "sso") {
		t.Errorf("Path = %q, want %q", repo.Path, filepath.Join(root, "sso"))
	}
}

func TestAddRepoAbsolutePathInfersName(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "acme")

	dir := filepath.Join(t.TempDir(), "search-svc")
	os.MkdirAll(dir, 0o755)

	if err := h.run(t, "add-repo", "acme", dir); err != nil {
		t.Fatalf("add-repo <abs> error = %v", err)
	}

	g, _ := h.app.Store.GroupByName("acme")
	if _, err := h.app.Store.RepoByName(g.ID, "search-svc"); err != nil {
		t.Fatalf(`expected a repo named "search-svc": %v`, err)
	}
}

// A bare word is still a name, as before.
func TestAddRepoBareWordIsStillAName(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "acme")

	dir := filepath.Join(t.TempDir(), "some-checkout")
	os.MkdirAll(dir, 0o755)
	h.cwd = dir

	if err := h.run(t, "add-repo", "acme", "auth"); err != nil {
		t.Fatalf("add-repo auth error = %v", err)
	}

	g, _ := h.app.Store.GroupByName("acme")
	repo, err := h.app.Store.RepoByName(g.ID, "auth")
	if err != nil {
		t.Fatalf(`expected a repo named "auth", not "some-checkout": %v`, err)
	}
	if repo.Path != dir {
		t.Errorf("Path = %q, want the cwd %q", repo.Path, dir)
	}
}

func TestAddRepoNameFlagOverridesInference(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "acme")

	dir := filepath.Join(t.TempDir(), "acme-auth")
	os.MkdirAll(dir, 0o755)

	if err := h.run(t, "add-repo", "acme", dir, "--name", "auth"); err != nil {
		t.Fatalf("add-repo --name error = %v", err)
	}

	g, _ := h.app.Store.GroupByName("acme")
	if _, err := h.app.Store.RepoByName(g.ID, "auth"); err != nil {
		t.Fatalf(`expected the --name value to win: %v`, err)
	}
}

func TestAddRepoRejectsPathGivenTwice(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "acme")

	err := h.run(t, "add-repo", "acme", ".", "--path", "/tmp")
	if err == nil || !strings.Contains(err.Error(), "twice") {
		t.Errorf("error = %v, want a complaint about the path being given twice", err)
	}
}

func TestAddRepoRejectsAMissingDirectory(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "acme")

	gone := filepath.Join(t.TempDir(), "nope")
	if err := h.run(t, "add-repo", "acme", gone); err == nil || !strings.Contains(err.Error(), gone) {
		t.Errorf("error = %v, want it to name the missing directory", err)
	}
}

func TestAddRepoUnusableDirectoryNameAsksForOne(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "acme")

	dir := filepath.Join(t.TempDir(), "my project")
	os.MkdirAll(dir, 0o755)

	err := h.run(t, "add-repo", "acme", dir)
	if err == nil || !strings.Contains(err.Error(), "cannot infer a name") {
		t.Fatalf("error = %v, want a request for an explicit name", err)
	}
	if !strings.Contains(h.help(), "add-repo") {
		t.Error("an unusable inferred name should show add-repo's help")
	}
}

// The uniqueness rule, stated as a test: per group, not global.
func TestRepoNamesAreUniquePerGroupNotGlobally(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "acme")
	h.run(t, "init", "abxy")

	one, two := t.TempDir(), t.TempDir()

	if err := h.run(t, "add-repo", "acme", "auth", "--path", one); err != nil {
		t.Fatalf("acme/auth error = %v", err)
	}
	if err := h.run(t, "add-repo", "abxy", "auth", "--path", two); err != nil {
		t.Fatalf("abxy/auth should be allowed: %v", err)
	}

	err := h.run(t, "add-repo", "abxy", "auth", "--path", one)
	if err == nil {
		t.Fatal("a second abxy/auth was allowed")
	}
	if !strings.Contains(err.Error(), "abxy") {
		t.Errorf("error = %q, want it to name the group it clashed in", err)
	}
}
