package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// SharedGroupRef is a shared bag with the names needed to address it.
type SharedGroupRef struct {
	SharedGroup
	GroupName    string
	WorkflowName string
}

// SetSharedGroupFile points a bag at the file that backs it.
func (s *Store) SetSharedGroupFile(id int64, path string) error {
	_, err := s.db.Exec(`UPDATE shared_groups SET file_path = ? WHERE id = ?`, path, id)
	return err
}

// AllSharedGroups returns every bag in the database with its addressing names.
func (s *Store) AllSharedGroups() ([]SharedGroupRef, error) {
	rows, err := s.db.Query(`
		SELECT sg.id, sg.workflow_id, sg.name, sg.file_path, g.name, w.name
		FROM shared_groups sg
		JOIN workflows w ON w.id = sg.workflow_id
		JOIN "groups"  g ON g.id = w.group_id
		ORDER BY g.name, w.name, sg.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SharedGroupRef
	for rows.Next() {
		var ref SharedGroupRef
		if err := rows.Scan(
			&ref.ID, &ref.WorkflowID, &ref.Name, &ref.FilePath,
			&ref.GroupName, &ref.WorkflowName,
		); err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}

// SharedGroupRepos returns a bag's member repos, ordered by name.
func (s *Store) SharedGroupRepos(id int64) ([]Repo, error) {
	rows, err := s.db.Query(`
		SELECT r.id, r.group_id, r.name, r.path, r.env_file
		FROM repos r
		JOIN shared_group_repos sgr ON sgr.repo_id = r.id
		WHERE sgr.shared_group_id = ?
		ORDER BY r.name`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Repo
	for rows.Next() {
		var r Repo
		if err := rows.Scan(&r.ID, &r.GroupID, &r.Name, &r.Path, &r.EnvFile); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ReplaceSharedGroupVars makes the bag's variables exactly vars.
//
// The file is authoritative, so a key absent from it is deleted rather than
// merged. That is what makes the diff honest.
func (s *Store) ReplaceSharedGroupVars(id int64, vars map[string]string) error {
	for key := range vars {
		if err := checkKey(key); err != nil {
			return err
		}
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM shared_group_vars WHERE shared_group_id = ?`, id); err != nil {
		return err
	}

	now := time.Now().Unix()
	for key, value := range vars {
		blob, err := s.cipher.Encrypt(value)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(
			`INSERT INTO shared_group_vars(shared_group_id, key, value, updated_at) VALUES (?, ?, ?, ?)`,
			id, key, blob, now,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ReplaceSharedGroupRepos makes the bag's membership exactly repoIDs.
func (s *Store) ReplaceSharedGroupRepos(id int64, repoIDs []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM shared_group_repos WHERE shared_group_id = ?`, id); err != nil {
		return err
	}
	for _, repoID := range repoIDs {
		if _, err := tx.Exec(
			`INSERT INTO shared_group_repos(shared_group_id, repo_id) VALUES (?, ?)`, id, repoID,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SharedGroupByID looks a bag up by its primary key.
func (s *Store) SharedGroupByID(id int64) (SharedGroup, error) {
	var sg SharedGroup
	err := s.db.QueryRow(
		`SELECT id, workflow_id, name, file_path FROM shared_groups WHERE id = ?`, id,
	).Scan(&sg.ID, &sg.WorkflowID, &sg.Name, &sg.FilePath)

	if errors.Is(err, sql.ErrNoRows) {
		return SharedGroup{}, fmt.Errorf("shared group %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return SharedGroup{}, err
	}
	return sg, nil
}
