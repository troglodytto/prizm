package apply

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fixture struct {
	builtPath  string
	repoPath   string
	linkPath   string
	backupPath string
}

func newFixture(t *testing.T) fixture {
	t.Helper()

	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}

	return fixture{
		builtPath: filepath.Join(root, "built", "XYZ", "local", "backend.env"),
		repoPath:  repoPath,
		linkPath:  filepath.Join(repoPath, ".env"),
		// Deliberately outside repoPath: tests that assert the repo directory
		// stays clean would pass trivially if backups landed inside it.
		backupPath: filepath.Join(root, "backups", "XYZ", "backend__local__.env__20260827-143000"),
	}
}

func TestApplyWritesBuiltFileAndSymlinks(t *testing.T) {
	f := newFixture(t)

	res, err := Apply(f.builtPath, "A=1\n", f.repoPath, ".env", f.backupPath)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if res.LinkPath != f.linkPath {
		t.Errorf("LinkPath = %q, want %q", res.LinkPath, f.linkPath)
	}
	if res.BackedUpTo != "" {
		t.Errorf("BackedUpTo = %q, want empty", res.BackedUpTo)
	}

	got, err := os.ReadFile(f.builtPath)
	if err != nil {
		t.Fatalf("reading built file: %v", err)
	}
	if string(got) != "A=1\n" {
		t.Errorf("built content = %q, want %q", got, "A=1\n")
	}

	info, err := os.Lstat(f.linkPath)
	if err != nil {
		t.Fatalf("lstat link: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("target is not a symlink")
	}

	dest, err := os.Readlink(f.linkPath)
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if dest != f.builtPath {
		t.Errorf("symlink points at %q, want %q", dest, f.builtPath)
	}
}

func TestApplyBuiltFileIsOwnerOnly(t *testing.T) {
	f := newFixture(t)

	if _, err := Apply(f.builtPath, "SECRET=x\n", f.repoPath, ".env", f.backupPath); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	info, err := os.Stat(f.builtPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("built file mode = %04o, want 0600", perm)
	}
}

// The displaced file goes exactly where the caller says, which is outside the
// repo. Nothing timestamped is left beside the user's env file.
func TestApplyBacksUpAnExistingRegularFileToTheGivenPath(t *testing.T) {
	f := newFixture(t)
	if err := os.WriteFile(f.linkPath, []byte("PRECIOUS=keepme\n"), 0o644); err != nil {
		t.Fatalf("seeding .env: %v", err)
	}
	want := filepath.Join(t.TempDir(), "backups", "XYZ", "backend__local__.env__20260827-143000")

	res, err := Apply(f.builtPath, "A=1\n", f.repoPath, ".env", want)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if res.BackedUpTo != want {
		t.Errorf("BackedUpTo = %q, want %q", res.BackedUpTo, want)
	}

	backup, err := os.ReadFile(res.BackedUpTo)
	if err != nil {
		t.Fatalf("reading backup: %v", err)
	}
	if string(backup) != "PRECIOUS=keepme\n" {
		t.Errorf("backup content = %q, want the original file", backup)
	}

	entries, err := os.ReadDir(f.repoPath)
	if err != nil {
		t.Fatalf("reading repo dir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != ".env" {
			t.Errorf("repo holds %q; a backup must not be left beside the env file", e.Name())
		}
	}
}

// An empty backup path means the caller already holds this content — sync has
// absorbed it into prizm — so the displaced file is dropped rather than kept.
func TestApplyDiscardsTheDisplacedFileWhenGivenNoBackupPath(t *testing.T) {
	f := newFixture(t)
	if err := os.WriteFile(f.linkPath, []byte("ALREADY=inprizm\n"), 0o644); err != nil {
		t.Fatalf("seeding .env: %v", err)
	}

	res, err := Apply(f.builtPath, "A=1\n", f.repoPath, ".env", "")
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if res.BackedUpTo != "" {
		t.Errorf("BackedUpTo = %q, want empty — no backup was asked for", res.BackedUpTo)
	}

	entries, err := os.ReadDir(f.repoPath)
	if err != nil {
		t.Fatalf("reading repo dir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != ".env" {
			t.Errorf("repo holds %q; nothing should be left behind", e.Name())
		}
	}

	// The link must still be correct — discarding the old file is not licence
	// to leave the repo without one.
	got, err := os.ReadFile(f.linkPath)
	if err != nil {
		t.Fatalf("reading .env: %v", err)
	}
	if string(got) != "A=1\n" {
		t.Errorf(".env = %q, want the built content", got)
	}
}

func TestApplyReplacesAnExistingSymlinkWithoutBackup(t *testing.T) {
	f := newFixture(t)
	other := filepath.Join(t.TempDir(), "other.env")
	os.WriteFile(other, []byte("OLD=1\n"), 0o600)
	if err := os.Symlink(other, f.linkPath); err != nil {
		t.Fatalf("seeding symlink: %v", err)
	}

	res, err := Apply(f.builtPath, "A=1\n", f.repoPath, ".env", f.backupPath)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if res.BackedUpTo != "" {
		t.Errorf("BackedUpTo = %q, want empty — a symlink needs no backup", res.BackedUpTo)
	}

	if dest, _ := os.Readlink(f.linkPath); dest != f.builtPath {
		t.Errorf("symlink points at %q, want %q", dest, f.builtPath)
	}
}

func TestApplyIsIdempotent(t *testing.T) {
	f := newFixture(t)

	for i := 0; i < 3; i++ {
		res, err := Apply(f.builtPath, "A=1\n", f.repoPath, ".env", f.backupPath)
		if err != nil {
			t.Fatalf("Apply() run %d error = %v", i, err)
		}
		if res.BackedUpTo != "" {
			t.Errorf("run %d backed up %q; re-applying must not accumulate backups", i, res.BackedUpTo)
		}
	}

	entries, _ := os.ReadDir(f.repoPath)
	if len(entries) != 1 {
		t.Errorf("repo dir has %d entries, want exactly 1 (.env)", len(entries))
	}
}

func TestApplyHonoursACustomEnvFileName(t *testing.T) {
	f := newFixture(t)

	res, err := Apply(f.builtPath, "A=1\n", f.repoPath, ".env.local", f.backupPath)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	want := filepath.Join(f.repoPath, ".env.local")
	if res.LinkPath != want {
		t.Errorf("LinkPath = %q, want %q", res.LinkPath, want)
	}
	if _, err := os.Lstat(want); err != nil {
		t.Errorf("expected %q to exist: %v", want, err)
	}
}

func TestApplyErrorsWhenRepoPathIsMissing(t *testing.T) {
	f := newFixture(t)
	missing := filepath.Join(t.TempDir(), "gone")

	_, err := Apply(f.builtPath, "A=1\n", missing, ".env", f.backupPath)
	if err == nil {
		t.Fatal("Apply(missing repo) error = nil, want error")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Errorf("error = %q, want it to name the missing path", err)
	}
}

// A second-resolution timestamp plus os.Rename meant a second apply inside
// one second destroyed the first backup — the safety mechanism deleting the
// thing it was protecting. The stamp now comes from the caller, so the
// collision is the same shape: the same path handed over twice.
func TestASecondBackupDoesNotDestroyTheFirst(t *testing.T) {
	f := newFixture(t)

	if err := os.WriteFile(f.linkPath, []byte("PRECIOUS=one\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := Apply(f.builtPath, "PORT=1\n", f.repoPath, ".env", f.backupPath); err != nil {
		t.Fatalf("first apply: %v", err)
	}

	// The user puts a real file back, then applies again in the same second —
	// so the caller computes the identical backup path.
	if err := os.Remove(f.linkPath); err != nil {
		t.Fatalf("remove link: %v", err)
	}
	if err := os.WriteFile(f.linkPath, []byte("PRECIOUS=two\n"), 0o600); err != nil {
		t.Fatalf("reseed: %v", err)
	}
	if _, err := Apply(f.builtPath, "PORT=2\n", f.repoPath, ".env", f.backupPath); err != nil {
		t.Fatalf("second apply: %v", err)
	}

	backupDir := filepath.Dir(f.backupPath)
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("reading backup dir: %v", err)
	}
	if len(entries) != 2 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("got %d backup(s) %v, want 2 — the first must survive", len(entries), names)
	}

	var seen []string
	for _, e := range entries {
		raw, _ := os.ReadFile(filepath.Join(backupDir, e.Name()))
		seen = append(seen, string(raw))
	}
	joined := strings.Join(seen, "")
	for _, want := range []string{"PRECIOUS=one", "PRECIOUS=two"} {
		if !strings.Contains(joined, want) {
			t.Errorf("backups = %v, want one containing %q", seen, want)
		}
	}
}
