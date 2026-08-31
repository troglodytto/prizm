package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDataDirUsesXDGDataHome(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/tmp/xdg-test")

	got, err := DataDir()
	if err != nil {
		t.Fatalf("DataDir() error = %v", err)
	}
	if want := "/tmp/xdg-test/prizm"; got != want {
		t.Errorf("DataDir() = %q, want %q", got, want)
	}
}

func TestDataDirFallsBackToHome(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("HOME", "/tmp/home-test")

	got, err := DataDir()
	if err != nil {
		t.Fatalf("DataDir() error = %v", err)
	}
	if want := "/tmp/home-test/.local/share/prizm"; got != want {
		t.Errorf("DataDir() = %q, want %q", got, want)
	}
}

func TestDBPath(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/tmp/xdg-test")

	got, err := DBPath()
	if err != nil {
		t.Fatalf("DBPath() error = %v", err)
	}
	if want := "/tmp/xdg-test/prizm/prizm.db"; got != want {
		t.Errorf("DBPath() = %q, want %q", got, want)
	}
}

func TestBuiltPath(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/tmp/xdg-test")

	got, err := BuiltPath("XYZ", "local", "backend")
	if err != nil {
		t.Fatalf("BuiltPath() error = %v", err)
	}
	if want := "/tmp/xdg-test/prizm/built/XYZ/local/backend.env"; got != want {
		t.Errorf("BuiltPath() = %q, want %q", got, want)
	}
}

// A displaced env file is named for what it was, not for where it sat: the
// repo it came from, the workflow that displaced it, and the file's own name.
func TestBackupPath(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/tmp/xdg-test")

	got, err := BackupPath("XYZ", "local", "backend", ".env", "20260827-143000")
	if err != nil {
		t.Fatalf("BackupPath() error = %v", err)
	}
	want := "/tmp/xdg-test/prizm/backups/XYZ/backend__local__.env__20260827-143000"
	if got != want {
		t.Errorf("BackupPath() = %q, want %q", got, want)
	}
}

// A nested env file name must not create directories under backups/ — the
// whole point is one flat, greppable directory per group.
func TestBackupPathFlattensANestedEnvFile(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/tmp/xdg-test")

	got, err := BackupPath("kroolo", "workos-qa", "ssm-tunnel-qa", "envs/qastage.env", "20260831-120647")
	if err != nil {
		t.Fatalf("BackupPath() error = %v", err)
	}
	want := "/tmp/xdg-test/prizm/backups/kroolo/ssm-tunnel-qa__workos-qa__qastage.env__20260831-120647"
	if got != want {
		t.Errorf("BackupPath() = %q, want %q", got, want)
	}
}

func TestEnsureDirCreates0700(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "b")

	if err := EnsureDir(dir); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("dir mode = %04o, want 0700", perm)
	}
}
