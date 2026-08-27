package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Repo is a named checkout at a fixed absolute path.
type Repo struct {
	ID      int64
	GroupID int64
	Name    string
	Path    string
	EnvFile string
}

// DefaultEnvFile is the file prizm symlinks inside a repo when none is given.
const DefaultEnvFile = ".env"

// AddRepo registers a repo in a group. path must already be absolute: repo
// paths are a stable contract, changed only by `prizm repair`.
func (s *Store) AddRepo(groupID int64, name, path, envFile string) (Repo, error) {
	if envFile == "" {
		envFile = DefaultEnvFile
	}

	res, err := s.db.Exec(
		`INSERT INTO repos(group_id, name, path, env_file, created_at) VALUES (?, ?, ?, ?, ?)`,
		groupID, name, path, envFile, time.Now().Unix(),
	)
	if err != nil {
		if isUniqueViolation(err) {
			return Repo{}, fmt.Errorf("repo %q: %w", name, ErrExists)
		}
		return Repo{}, err
	}

	id, err := res.LastInsertId()
	if err != nil {
		return Repo{}, err
	}
	return Repo{ID: id, GroupID: groupID, Name: name, Path: path, EnvFile: envFile}, nil
}

// RepoByName looks a repo up within a group.
func (s *Store) RepoByName(groupID int64, name string) (Repo, error) {
	var r Repo
	err := s.db.QueryRow(
		`SELECT id, group_id, name, path, env_file FROM repos WHERE group_id = ? AND name = ?`,
		groupID, name,
	).Scan(&r.ID, &r.GroupID, &r.Name, &r.Path, &r.EnvFile)

	if errors.Is(err, sql.ErrNoRows) {
		return Repo{}, fmt.Errorf("repo %q: %w", name, ErrNotFound)
	}
	if err != nil {
		return Repo{}, err
	}
	return r, nil
}

// ListRepos returns a group's repos ordered by name.
func (s *Store) ListRepos(groupID int64) ([]Repo, error) {
	rows, err := s.db.Query(
		`SELECT id, group_id, name, path, env_file FROM repos WHERE group_id = ? ORDER BY name`,
		groupID,
	)
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

// RepoPathsByGroup maps every group name to its repo paths in one query.
// Completion uses this, so it must stay a single round trip.
func (s *Store) RepoPathsByGroup() (map[string][]string, error) {
	rows, err := s.db.Query(`
		SELECT g.name, r.path
		FROM "groups" g
		JOIN repos r ON r.group_id = g.id
		ORDER BY g.name, r.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string][]string)
	for rows.Next() {
		var group, path string
		if err := rows.Scan(&group, &path); err != nil {
			return nil, err
		}
		out[group] = append(out[group], path)
	}
	return out, rows.Err()
}
