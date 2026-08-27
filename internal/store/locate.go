package store

import (
	"fmt"
	"path/filepath"
	"strings"
)

// RepoForPath returns the registered repo containing path, along with its
// group. When repos are nested, the deepest one wins. Returns ErrNotFound when
// path is outside every registered repo.
func (s *Store) RepoForPath(path string) (Repo, Group, error) {
	path = filepath.Clean(path)

	rows, err := s.db.Query(`
		SELECT r.id, r.group_id, r.name, r.path, r.env_file,
		       g.id, g.name, g.use_count, g.last_used_at
		FROM repos r
		JOIN "groups" g ON g.id = r.group_id`)
	if err != nil {
		return Repo{}, Group{}, err
	}
	defer rows.Close()

	var (
		bestRepo  Repo
		bestGroup Group
		bestLen   int
	)
	for rows.Next() {
		var (
			r        Repo
			g        Group
			lastUsed int64
		)
		if err := rows.Scan(
			&r.ID, &r.GroupID, &r.Name, &r.Path, &r.EnvFile,
			&g.ID, &g.Name, &g.UseCount, &lastUsed,
		); err != nil {
			return Repo{}, Group{}, err
		}

		clean := filepath.Clean(r.Path)
		if !pathContains(clean, path) {
			continue
		}
		if len(clean) > bestLen {
			bestLen, bestRepo, bestGroup = len(clean), r, g
		}
	}
	if err := rows.Err(); err != nil {
		return Repo{}, Group{}, err
	}

	if bestLen == 0 {
		return Repo{}, Group{}, fmt.Errorf("no registered repo contains %s: %w", path, ErrNotFound)
	}
	return bestRepo, bestGroup, nil
}

// pathContains compares whole path segments, so /code/x does not contain
// /code/x-old.
func pathContains(dir, child string) bool {
	return child == dir || strings.HasPrefix(child, dir+string(filepath.Separator))
}
