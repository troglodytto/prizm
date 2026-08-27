package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Group is a top-level namespace owning repos and workflows.
type Group struct {
	ID         int64
	Name       string
	UseCount   int
	LastUsedAt time.Time
}

// CreateGroup registers a new group.
func (s *Store) CreateGroup(name string) (Group, error) {
	if err := checkName("group", name); err != nil {
		return Group{}, err
	}

	res, err := s.db.Exec(
		`INSERT INTO "groups"(name, created_at) VALUES (?, ?)`,
		name, time.Now().Unix(),
	)
	if err != nil {
		if isUniqueViolation(err) {
			return Group{}, fmt.Errorf("group %q: %w", name, ErrExists)
		}
		return Group{}, err
	}

	id, err := res.LastInsertId()
	if err != nil {
		return Group{}, err
	}
	return Group{ID: id, Name: name}, nil
}

// GroupByName looks a group up by its exact name.
func (s *Store) GroupByName(name string) (Group, error) {
	var (
		g        Group
		lastUsed int64
	)
	err := s.db.QueryRow(
		`SELECT id, name, use_count, last_used_at FROM "groups" WHERE name = ?`, name,
	).Scan(&g.ID, &g.Name, &g.UseCount, &lastUsed)

	if errors.Is(err, sql.ErrNoRows) {
		return Group{}, fmt.Errorf("group %q: %w", name, ErrNotFound)
	}
	if err != nil {
		return Group{}, err
	}

	g.LastUsedAt = time.Unix(lastUsed, 0)
	return g, nil
}

// ListGroups returns every group ordered by name. Display order is decided by
// the rank package, not here.
func (s *Store) ListGroups() ([]Group, error) {
	rows, err := s.db.Query(
		`SELECT id, name, use_count, last_used_at FROM "groups" ORDER BY name`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Group
	for rows.Next() {
		var (
			g        Group
			lastUsed int64
		)
		if err := rows.Scan(&g.ID, &g.Name, &g.UseCount, &lastUsed); err != nil {
			return nil, err
		}
		g.LastUsedAt = time.Unix(lastUsed, 0)
		out = append(out, g)
	}
	return out, rows.Err()
}

// TouchGroup records a use of the group, feeding frecency ranking.
func (s *Store) TouchGroup(id int64, now time.Time) error {
	_, err := s.db.Exec(
		`UPDATE "groups" SET use_count = use_count + 1, last_used_at = ? WHERE id = ?`,
		now.Unix(), id,
	)
	return err
}

// isUniqueViolation reports whether err is a SQLite UNIQUE constraint failure.
// Matching on message text keeps this driver-agnostic.
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
