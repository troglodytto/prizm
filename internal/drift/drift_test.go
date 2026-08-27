package drift

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/troglodytto/prizm/internal/store"
)

type fixture struct {
	repo  store.Repo
	built string
}

func newFixture(t *testing.T) fixture {
	t.Helper()

	root := t.TempDir()
	repoPath := filepath.Join(root, "backend")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	built := filepath.Join(root, "built", "backend.env")
	if err := os.MkdirAll(filepath.Dir(built), 0o700); err != nil {
		t.Fatalf("mkdir built: %v", err)
	}

	return fixture{
		repo:  store.Repo{Name: "backend", Path: repoPath, EnvFile: ".env"},
		built: built,
	}
}

func (f fixture) link(t *testing.T, content string) {
	t.Helper()

	if err := os.WriteFile(f.built, []byte(content), 0o600); err != nil {
		t.Fatalf("writing built file: %v", err)
	}
	if err := os.Symlink(f.built, filepath.Join(f.repo.Path, ".env")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
}

func TestInspectInSync(t *testing.T) {
	f := newFixture(t)
	f.link(t, "A=1\nB=2\n")

	got, err := Inspect(f.repo, map[string]string{"A": "1", "B": "2"}, f.built)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if !got.InSync() {
		t.Errorf("InSync() = false, want true (link=%v diff=%+v)", got.Link, got.Diff)
	}
}

func TestInspectDetectsContentDrift(t *testing.T) {
	f := newFixture(t)
	f.link(t, "A=9\nNEW=x\n")

	got, err := Inspect(f.repo, map[string]string{"A": "1", "GONE": "y"}, f.built)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if got.Link != Managed {
		t.Errorf("Link = %v, want Managed", got.Link)
	}
	if got.InSync() {
		t.Fatal("InSync() = true, want false")
	}
	if got.Changes() != 3 {
		t.Errorf("Changes() = %d, want 3 (one changed, one added, one removed)", got.Changes())
	}
}

// Reordering keys is not drift.
func TestInspectIgnoresKeyOrder(t *testing.T) {
	f := newFixture(t)
	f.link(t, "B=2\nA=1\n")

	got, _ := Inspect(f.repo, map[string]string{"A": "1", "B": "2"}, f.built)
	if !got.InSync() {
		t.Errorf("InSync() = false; key order must not register as drift (%+v)", got.Diff)
	}
}

func TestInspectNoFile(t *testing.T) {
	f := newFixture(t)

	got, err := Inspect(f.repo, map[string]string{"A": "1"}, f.built)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if got.Link != NoFile {
		t.Errorf("Link = %v, want NoFile", got.Link)
	}
	if got.InSync() {
		t.Error("InSync() = true for an unapplied repo")
	}
}

func TestInspectUnmanagedRegularFile(t *testing.T) {
	f := newFixture(t)
	os.WriteFile(filepath.Join(f.repo.Path, ".env"), []byte("A=1\n"), 0o600)

	got, err := Inspect(f.repo, map[string]string{"A": "1"}, f.built)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if got.Link != Unmanaged {
		t.Errorf("Link = %v, want Unmanaged", got.Link)
	}
	if !got.Diff.Empty() {
		t.Errorf("Diff = %+v, want empty — the content happens to match", got.Diff)
	}
}

func TestInspectLinkedElsewhere(t *testing.T) {
	f := newFixture(t)
	other := filepath.Join(t.TempDir(), "production.env")
	os.WriteFile(other, []byte("A=1\n"), 0o600)
	os.Symlink(other, filepath.Join(f.repo.Path, ".env"))

	got, err := Inspect(f.repo, map[string]string{"A": "1"}, f.built)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if got.Link != ManagedElsewhere {
		t.Errorf("Link = %v, want ManagedElsewhere", got.Link)
	}
	if got.LinkDest != other {
		t.Errorf("LinkDest = %q, want %q", got.LinkDest, other)
	}
}

func TestInspectMissingRepoPath(t *testing.T) {
	repo := store.Repo{Name: "gone", Path: filepath.Join(t.TempDir(), "nope"), EnvFile: ".env"}

	got, err := Inspect(repo, map[string]string{"A": "1"}, "/tmp/built.env")
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if got.Link != PathMissing {
		t.Errorf("Link = %v, want PathMissing", got.Link)
	}
}

func TestInspectUnparseableFileIsAnError(t *testing.T) {
	f := newFixture(t)
	f.link(t, "this is not an env file\n")

	if _, err := Inspect(f.repo, map[string]string{}, f.built); err == nil {
		t.Error("Inspect() error = nil, want a parse error naming the problem")
	}
}
