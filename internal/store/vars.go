package store

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"time"
)

// ErrInvalidKey is returned for a key that is not a legal env-var name.
var ErrInvalidKey = errors.New("invalid variable key")

// keyPattern admits _PRIZM_-prefixed internal keys without a special case.
var keyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func checkKey(key string) error {
	if !keyPattern.MatchString(key) {
		return fmt.Errorf("%q: %w (must match [A-Za-z_][A-Za-z0-9_]*)", key, ErrInvalidKey)
	}
	return nil
}

// SharedGroup is a named bag of vars scoped to (workflow, repo subset).
type SharedGroup struct {
	ID         int64
	WorkflowID int64
	Name       string
	FilePath   string
}

// ---- Layer 1: repo-shared -------------------------------------------------

// SetRepoVar upserts a variable that applies in every workflow touching this repo.
func (s *Store) SetRepoVar(repoID int64, key, value string) error {
	if err := checkKey(key); err != nil {
		return err
	}

	blob, err := s.cipher.Encrypt(value)
	if err != nil {
		return err
	}

	_, err = s.db.Exec(`
		INSERT INTO repo_vars(repo_id, key, value, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(repo_id, key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		repoID, key, blob, time.Now().Unix())
	return err
}

// RepoVars returns the repo-shared layer, decrypted. Values are templates.
func (s *Store) RepoVars(repoID int64) (map[string]string, error) {
	return s.queryVars(`SELECT key, value FROM repo_vars WHERE repo_id = ?`, repoID)
}

// ---- Layer 2: workflow-scoped shared groups -------------------------------

// CreateSharedGroup creates a named shared-variable group inside a workflow.
func (s *Store) CreateSharedGroup(workflowID int64, name string) (SharedGroup, error) {
	if err := checkName("shared bag", name); err != nil {
		return SharedGroup{}, err
	}

	res, err := s.db.Exec(
		`INSERT INTO shared_groups(workflow_id, name, created_at) VALUES (?, ?, ?)`,
		workflowID, name, time.Now().Unix(),
	)
	if err != nil {
		if isUniqueViolation(err) {
			return SharedGroup{}, fmt.Errorf("shared group %q: %w", name, ErrExists)
		}
		return SharedGroup{}, err
	}

	id, err := res.LastInsertId()
	if err != nil {
		return SharedGroup{}, err
	}
	return SharedGroup{ID: id, WorkflowID: workflowID, Name: name}, nil
}

// SharedGroupByName looks a shared group up within a workflow.
func (s *Store) SharedGroupByName(workflowID int64, name string) (SharedGroup, error) {
	var sg SharedGroup
	err := s.db.QueryRow(
		`SELECT id, workflow_id, name, file_path FROM shared_groups WHERE workflow_id = ? AND name = ?`,
		workflowID, name,
	).Scan(&sg.ID, &sg.WorkflowID, &sg.Name, &sg.FilePath)

	if errors.Is(err, sql.ErrNoRows) {
		return SharedGroup{}, fmt.Errorf("shared group %q: %w", name, ErrNotFound)
	}
	if err != nil {
		return SharedGroup{}, err
	}
	return sg, nil
}

// AddSharedGroupRepo makes a repo a member of a shared group. Idempotent.
func (s *Store) AddSharedGroupRepo(sharedGroupID, repoID int64) error {
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO shared_group_repos(shared_group_id, repo_id) VALUES (?, ?)`,
		sharedGroupID, repoID,
	)
	return err
}

// SetSharedGroupVar upserts a variable in a shared group.
func (s *Store) SetSharedGroupVar(sharedGroupID int64, key, value string) error {
	if err := checkKey(key); err != nil {
		return err
	}

	blob, err := s.cipher.Encrypt(value)
	if err != nil {
		return err
	}

	_, err = s.db.Exec(`
		INSERT INTO shared_group_vars(shared_group_id, key, value, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(shared_group_id, key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		sharedGroupID, key, blob, time.Now().Unix())
	return err
}

// SharedGroupVars returns one shared group's variables, decrypted.
func (s *Store) SharedGroupVars(sharedGroupID int64) (map[string]string, error) {
	return s.queryVars(`SELECT key, value FROM shared_group_vars WHERE shared_group_id = ?`, sharedGroupID)
}

// SharedGroupsForRepo returns the shared groups this repo belongs to within a
// workflow, ordered by name. The order is deterministic so that two bags
// defining the same key resolve the same way every time: later name wins.
func (s *Store) SharedGroupsForRepo(workflowID, repoID int64) ([]SharedGroup, error) {
	rows, err := s.db.Query(`
		SELECT sg.id, sg.workflow_id, sg.name, sg.file_path
		FROM shared_groups sg
		JOIN shared_group_repos sgr ON sgr.shared_group_id = sg.id
		WHERE sg.workflow_id = ? AND sgr.repo_id = ?
		ORDER BY sg.name`, workflowID, repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SharedGroup
	for rows.Next() {
		var sg SharedGroup
		if err := rows.Scan(&sg.ID, &sg.WorkflowID, &sg.Name, &sg.FilePath); err != nil {
			return nil, err
		}
		out = append(out, sg)
	}
	return out, rows.Err()
}

// ---- Layer 3: repo + workflow specific ------------------------------------

// SetWorkflowRepoVar upserts the highest-precedence layer.
func (s *Store) SetWorkflowRepoVar(workflowID, repoID int64, key, value string) error {
	if err := checkKey(key); err != nil {
		return err
	}

	blob, err := s.cipher.Encrypt(value)
	if err != nil {
		return err
	}

	_, err = s.db.Exec(`
		INSERT INTO workflow_repo_vars(workflow_id, repo_id, key, value, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(workflow_id, repo_id, key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		workflowID, repoID, key, blob, time.Now().Unix())
	return err
}

// WorkflowRepoVars returns the (workflow, repo) layer, decrypted.
func (s *Store) WorkflowRepoVars(workflowID, repoID int64) (map[string]string, error) {
	return s.queryVars(
		`SELECT key, value FROM workflow_repo_vars WHERE workflow_id = ? AND repo_id = ?`,
		workflowID, repoID,
	)
}

// ---- shared helper --------------------------------------------------------

func (s *Store) queryVars(query string, args ...any) (map[string]string, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]string)
	for rows.Next() {
		var (
			key  string
			blob []byte
		)
		if err := rows.Scan(&key, &blob); err != nil {
			return nil, err
		}

		value, err := s.cipher.Decrypt(blob)
		if err != nil {
			return nil, fmt.Errorf("decrypting %q: %w", key, err)
		}
		out[key] = value
	}
	return out, rows.Err()
}
