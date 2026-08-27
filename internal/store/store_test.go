package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/troglodytto/prizm/internal/crypto"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()

	s, err := Open(filepath.Join(t.TempDir(), "prizm.db"), crypto.Plaintext{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func newEncryptedTestStore(t *testing.T) *Store {
	t.Helper()

	key := make([]byte, crypto.KeySize)
	for i := range key {
		key[i] = byte(i)
	}
	c, err := crypto.NewAESGCM(key)
	if err != nil {
		t.Fatalf("NewAESGCM() error = %v", err)
	}

	s, err := Open(filepath.Join(t.TempDir(), "prizm.db"), c)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpenCreatesDatabaseFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prizm.db")

	s, err := Open(path, crypto.Plaintext{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer s.Close()

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("database file not created: %v", err)
	}
}

func TestOpenSetsOwnerOnlyPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prizm.db")

	s, err := Open(path, crypto.Plaintext{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer s.Close()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("db mode = %04o, want 0600", perm)
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prizm.db")

	s1, err := Open(path, crypto.Plaintext{})
	if err != nil {
		t.Fatalf("first Open() error = %v", err)
	}
	s1.Close()

	s2, err := Open(path, crypto.Plaintext{})
	if err != nil {
		t.Fatalf("second Open() error = %v (schema not idempotent?)", err)
	}
	s2.Close()
}

func TestOpenEnablesForeignKeys(t *testing.T) {
	s := newTestStore(t)

	var on int
	if err := s.db.QueryRow("PRAGMA foreign_keys").Scan(&on); err != nil {
		t.Fatalf("PRAGMA foreign_keys: %v", err)
	}
	if on != 1 {
		t.Error("foreign_keys pragma is off; cascading deletes will not work")
	}
}

func TestSchemaCreatesAllTables(t *testing.T) {
	s := newTestStore(t)

	want := []string{
		"groups", "repos", "workflows", "workflow_repos", "group_vars",
		"repo_vars", "shared_groups", "shared_group_repos",
		"shared_group_vars", "workflow_repo_vars", "applied",
	}

	for _, table := range want {
		var name string
		if err := s.db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table,
		).Scan(&name); err != nil {
			t.Errorf("table %q missing: %v", table, err)
		}
	}
}
