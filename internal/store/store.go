// Package store is prizm's SQLite persistence layer.
//
// Variable values are encrypted; everything else is plaintext, so completion
// queries stay fast and never touch the cipher.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/troglodytto/prizm/internal/crypto"
)

// Sentinel errors returned by store methods.
var (
	ErrNotFound = errors.New("not found")
	ErrExists   = errors.New("already exists")
)

// Store is a handle on the prizm database.
type Store struct {
	db     *sql.DB
	cipher crypto.Cipher
}

// schema is idempotent so reopening an existing database is a no-op.
//
// "groups" is quoted throughout: SQLite treats GROUPS as a window-frame
// keyword. Timestamps are Unix seconds. Variable values are BLOBs holding
// ciphertext. ON DELETE CASCADE everywhere, so removing a group cannot
// orphan rows.
const schema = `
CREATE TABLE IF NOT EXISTS "groups" (
	id           INTEGER PRIMARY KEY,
	name         TEXT    NOT NULL UNIQUE,
	use_count    INTEGER NOT NULL DEFAULT 0,
	last_used_at INTEGER NOT NULL DEFAULT 0,
	created_at   INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS repos (
	id         INTEGER PRIMARY KEY,
	group_id   INTEGER NOT NULL REFERENCES "groups"(id) ON DELETE CASCADE,
	name       TEXT    NOT NULL,
	path       TEXT    NOT NULL,
	env_file   TEXT    NOT NULL DEFAULT '.env',
	created_at INTEGER NOT NULL,
	UNIQUE(group_id, name)
);
CREATE INDEX IF NOT EXISTS idx_repos_group ON repos(group_id);

CREATE TABLE IF NOT EXISTS workflows (
	id         INTEGER PRIMARY KEY,
	group_id   INTEGER NOT NULL REFERENCES "groups"(id) ON DELETE CASCADE,
	name       TEXT    NOT NULL,
	tag        TEXT    NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	UNIQUE(group_id, name)
);
CREATE INDEX IF NOT EXISTS idx_workflows_group ON workflows(group_id);

CREATE TABLE IF NOT EXISTS workflow_repos (
	workflow_id INTEGER NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
	repo_id     INTEGER NOT NULL REFERENCES repos(id)     ON DELETE CASCADE,
	PRIMARY KEY (workflow_id, repo_id)
);
CREATE INDEX IF NOT EXISTS idx_workflow_repos_repo ON workflow_repos(repo_id);

-- Layer 0: group-global. One fact about the whole group — a shared cluster's
-- username, an AWS account — true in every workflow and every repo, and
-- overridable by any layer above when it stops being true.
CREATE TABLE IF NOT EXISTS group_vars (
	group_id   INTEGER NOT NULL REFERENCES "groups"(id) ON DELETE CASCADE,
	key        TEXT    NOT NULL,
	value      BLOB    NOT NULL,
	updated_at INTEGER NOT NULL,
	PRIMARY KEY (group_id, key)
);

-- Layer 1: repo-shared, applies in every workflow that touches this repo.
CREATE TABLE IF NOT EXISTS repo_vars (
	repo_id    INTEGER NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
	key        TEXT    NOT NULL,
	value      BLOB    NOT NULL,
	updated_at INTEGER NOT NULL,
	PRIMARY KEY (repo_id, key)
);

-- Layer 2: a named bag of vars scoped to (workflow, repo subset).
CREATE TABLE IF NOT EXISTS shared_groups (
	id          INTEGER PRIMARY KEY,
	workflow_id INTEGER NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
	name        TEXT    NOT NULL,
	file_path   TEXT    NOT NULL DEFAULT '',
	created_at  INTEGER NOT NULL,
	UNIQUE(workflow_id, name)
);

CREATE TABLE IF NOT EXISTS shared_group_repos (
	shared_group_id INTEGER NOT NULL REFERENCES shared_groups(id) ON DELETE CASCADE,
	repo_id         INTEGER NOT NULL REFERENCES repos(id)         ON DELETE CASCADE,
	PRIMARY KEY (shared_group_id, repo_id)
);
CREATE INDEX IF NOT EXISTS idx_sgr_repo ON shared_group_repos(repo_id);

CREATE TABLE IF NOT EXISTS shared_group_vars (
	shared_group_id INTEGER NOT NULL REFERENCES shared_groups(id) ON DELETE CASCADE,
	key             TEXT    NOT NULL,
	value           BLOB    NOT NULL,
	updated_at      INTEGER NOT NULL,
	PRIMARY KEY (shared_group_id, key)
);

-- Layer 3: this repo, in this workflow. Highest precedence.
CREATE TABLE IF NOT EXISTS workflow_repo_vars (
	workflow_id INTEGER NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
	repo_id     INTEGER NOT NULL REFERENCES repos(id)     ON DELETE CASCADE,
	key         TEXT    NOT NULL,
	value       BLOB    NOT NULL,
	updated_at  INTEGER NOT NULL,
	PRIMARY KEY (workflow_id, repo_id, key)
);

-- Every variable write records the resulting state of its scope, so history
-- exists before anyone thinks to ask for it. Content is hashed so re-running
-- a command that changes nothing does not add a version.
CREATE TABLE IF NOT EXISTS snapshots (
	id           INTEGER PRIMARY KEY,
	scope_kind   TEXT    NOT NULL,
	scope_a      INTEGER NOT NULL,
	scope_b      INTEGER NOT NULL,
	content_hash TEXT    NOT NULL,
	source       TEXT    NOT NULL,
	note         TEXT    NOT NULL DEFAULT '',
	created_at   INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_snapshots_scope
	ON snapshots(scope_kind, scope_a, scope_b, created_at DESC);

CREATE TABLE IF NOT EXISTS snapshot_vars (
	snapshot_id INTEGER NOT NULL REFERENCES snapshots(id) ON DELETE CASCADE,
	key         TEXT    NOT NULL,
	value       BLOB    NOT NULL,
	PRIMARY KEY (snapshot_id, key)
);

-- What is currently linked where.
CREATE TABLE IF NOT EXISTS applied (
	repo_id     INTEGER PRIMARY KEY REFERENCES repos(id)     ON DELETE CASCADE,
	workflow_id INTEGER NOT NULL    REFERENCES workflows(id) ON DELETE CASCADE,
	built_path  TEXT    NOT NULL,
	applied_at  INTEGER NOT NULL
);
`

// Open opens (creating if needed) the prizm database at path.
func Open(path string, c crypto.Cipher) (*Store, error) {
	dsn := fmt.Sprintf(
		"file:%s?_foreign_keys=on&_busy_timeout=5000&_journal_mode=WAL&_synchronous=NORMAL",
		path,
	)

	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	// One connection: this is a single-user local CLI, and it removes any
	// chance of a per-connection pragma being missed.
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("opening database: %w", err)
	}

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("applying schema: %w", err)
	}

	if err := os.Chmod(path, 0o600); err != nil {
		db.Close()
		return nil, fmt.Errorf("securing database file: %w", err)
	}

	return &Store{db: db, cipher: c}, nil
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

// RecordApplied notes which workflow a repo is currently linked to. This is
// what `prizm status` reads in a later phase.
func (s *Store) RecordApplied(repoID, workflowID int64, builtPath string, now time.Time) error {
	_, err := s.db.Exec(`
		INSERT INTO applied(repo_id, workflow_id, built_path, applied_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(repo_id) DO UPDATE SET
			workflow_id = excluded.workflow_id,
			built_path  = excluded.built_path,
			applied_at  = excluded.applied_at`,
		repoID, workflowID, builtPath, now.Unix())
	return err
}

// Applied records which workflow a repo is currently linked to.
type Applied struct {
	RepoID     int64
	WorkflowID int64
	BuiltPath  string
	AppliedAt  time.Time
}

// AppliedFor returns the applied state of every repo in a group, keyed by repo ID.
func (s *Store) AppliedFor(groupID int64) (map[int64]Applied, error) {
	rows, err := s.db.Query(`
		SELECT a.repo_id, a.workflow_id, a.built_path, a.applied_at
		FROM applied a
		JOIN repos r ON r.id = a.repo_id
		WHERE r.group_id = ?`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[int64]Applied)
	for rows.Next() {
		var (
			a       Applied
			applied int64
		)
		if err := rows.Scan(&a.RepoID, &a.WorkflowID, &a.BuiltPath, &applied); err != nil {
			return nil, err
		}
		a.AppliedAt = time.Unix(applied, 0)
		out[a.RepoID] = a
	}
	return out, rows.Err()
}
