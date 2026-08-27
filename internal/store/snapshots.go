package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"time"
)

// ScopeKind names what a snapshot belongs to.
type ScopeKind string

const (
	// ScopeWorkflowRepo is one repo's variables within one workflow.
	ScopeWorkflowRepo ScopeKind = "workflow_repo"
	// ScopeRepo is one repo's variables across every workflow — the wiring.
	ScopeRepo ScopeKind = "repo"
	// ScopeSharedGroup is one shared bag's variables.
	ScopeSharedGroup ScopeKind = "shared_group"
	// ScopeGroup is the group-global layer.
	ScopeGroup ScopeKind = "group"
)

// Why a version exists. During an audit this is often more useful than the
// diff — "who touched this" answers more questions than "what changed".
const (
	SourceVar        = "var"
	SourceImport     = "import"
	SourceSync       = "sync"
	SourceSharedSync = "shared-sync"
	SourceRestore    = "restore"
	SourceEdit       = "edit"
	SourceUnset      = "unset"
)

// Scope addresses a snapshot timeline.
type Scope struct {
	Kind ScopeKind
	A    int64
	B    int64
}

// WorkflowRepoScope is the timeline for one repo inside one workflow.
func WorkflowRepoScope(workflowID, repoID int64) Scope {
	return Scope{Kind: ScopeWorkflowRepo, A: workflowID, B: repoID}
}

// RepoScope is the timeline for a repo's all-workflows layer.
func RepoScope(id int64) Scope { return Scope{Kind: ScopeRepo, A: id} }

// SharedGroupScope is the timeline for one shared bag.
func SharedGroupScope(id int64) Scope { return Scope{Kind: ScopeSharedGroup, A: id} }

// GroupScope is the timeline for a group's global layer.
func GroupScope(id int64) Scope { return Scope{Kind: ScopeGroup, A: id} }

// Snapshot is one recorded version of a scope's variables.
type Snapshot struct {
	ID        int64
	Scope     Scope
	Source    string
	Note      string
	CreatedAt time.Time
}

// RecordSnapshot stores vars as a new version, unless it is identical to the
// previous one. Reports whether it wrote.
func (s *Store) RecordSnapshot(scope Scope, vars map[string]string, source, note string, now time.Time) (bool, error) {
	hash := hashVars(vars)

	var last string
	err := s.db.QueryRow(`
		SELECT content_hash FROM snapshots
		WHERE scope_kind = ? AND scope_a = ? AND scope_b = ?
		ORDER BY created_at DESC, id DESC LIMIT 1`,
		string(scope.Kind), scope.A, scope.B,
	).Scan(&last)

	switch {
	case err == nil && last == hash:
		return false, nil
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return false, err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	res, err := tx.Exec(`
		INSERT INTO snapshots(scope_kind, scope_a, scope_b, content_hash, source, note, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		string(scope.Kind), scope.A, scope.B, hash, source, note, now.Unix())
	if err != nil {
		return false, err
	}

	id, err := res.LastInsertId()
	if err != nil {
		return false, err
	}

	for key, value := range vars {
		blob, encErr := s.cipher.Encrypt(value)
		if encErr != nil {
			return false, encErr
		}
		if _, err := tx.Exec(
			`INSERT INTO snapshot_vars(snapshot_id, key, value) VALUES (?, ?, ?)`, id, key, blob,
		); err != nil {
			return false, err
		}
	}

	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// ListSnapshots returns a scope's versions, newest first.
func (s *Store) ListSnapshots(scope Scope) ([]Snapshot, error) {
	rows, err := s.db.Query(`
		SELECT id, source, note, created_at FROM snapshots
		WHERE scope_kind = ? AND scope_a = ? AND scope_b = ?
		ORDER BY created_at DESC, id DESC`,
		string(scope.Kind), scope.A, scope.B)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Snapshot
	for rows.Next() {
		var (
			snap    Snapshot
			created int64
		)
		if err := rows.Scan(&snap.ID, &snap.Source, &snap.Note, &created); err != nil {
			return nil, err
		}
		snap.Scope = scope
		snap.CreatedAt = time.Unix(created, 0)
		out = append(out, snap)
	}
	return out, rows.Err()
}

// SnapshotVars returns one version's variables, decrypted.
func (s *Store) SnapshotVars(id int64) (map[string]string, error) {
	return s.queryVars(`SELECT key, value FROM snapshot_vars WHERE snapshot_id = ?`, id)
}

// hashVars fingerprints a variable map independently of key order. Lengths are
// included so {"AB":"C"} and {"A":"BC"} cannot collide.
func hashVars(vars map[string]string) string {
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	h := sha256.New()
	for _, k := range keys {
		fmt.Fprintf(h, "%d:%s=%d:%s\n", len(k), k, len(vars[k]), vars[k])
	}
	return hex.EncodeToString(h.Sum(nil))
}
