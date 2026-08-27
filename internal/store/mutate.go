package store

import "fmt"

// Rename and delete. Everything a user can create, they can undo — a typo in
// a repo name should not be permanent.
//
// Cascading deletes are already declared in the schema, so removing a group
// takes its repos, workflows, bags and variables with it.

// RenameGroup changes a group's name.
func (s *Store) RenameGroup(id int64, name string) error {
	return s.rename("group", `UPDATE "groups" SET name = ? WHERE id = ?`, name, id)
}

// RenameRepo changes a repo's name within its group.
func (s *Store) RenameRepo(id int64, name string) error {
	return s.rename("repo", `UPDATE repos SET name = ? WHERE id = ?`, name, id)
}

// RenameWorkflow changes a workflow's name within its group.
func (s *Store) RenameWorkflow(id int64, name string) error {
	if IsReservedName(name) {
		return fmt.Errorf("workflow %q: %w (it is a prizm command)", name, ErrReservedName)
	}
	return s.rename("workflow", `UPDATE workflows SET name = ? WHERE id = ?`, name, id)
}

func (s *Store) rename(kind, query, name string, id int64) error {
	if err := checkName(kind, name); err != nil {
		return err
	}

	if _, err := s.db.Exec(query, name, id); err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%s %q: %w", kind, name, ErrExists)
		}
		return err
	}
	return nil
}

// DeleteGroup removes a group and everything under it.
func (s *Store) DeleteGroup(id int64) error {
	_, err := s.db.Exec(`DELETE FROM "groups" WHERE id = ?`, id)
	return err
}

// DeleteRepo removes a repo, its variables, and its membership everywhere.
func (s *Store) DeleteRepo(id int64) error {
	_, err := s.db.Exec(`DELETE FROM repos WHERE id = ?`, id)
	return err
}

// DeleteWorkflow removes a workflow, its bags and its variables.
func (s *Store) DeleteWorkflow(id int64) error {
	_, err := s.db.Exec(`DELETE FROM workflows WHERE id = ?`, id)
	return err
}

// DeleteSharedGroup removes a shared bag and its variables.
func (s *Store) DeleteSharedGroup(id int64) error {
	_, err := s.db.Exec(`DELETE FROM shared_groups WHERE id = ?`, id)
	return err
}

// DeleteRepoVar removes a key from the repo-shared layer.
func (s *Store) DeleteRepoVar(repoID int64, key string) error {
	_, err := s.db.Exec(`DELETE FROM repo_vars WHERE repo_id = ? AND key = ?`, repoID, key)
	return err
}

// DeleteWorkflowRepoVar removes a key from the repo+workflow layer.
func (s *Store) DeleteWorkflowRepoVar(workflowID, repoID int64, key string) error {
	_, err := s.db.Exec(
		`DELETE FROM workflow_repo_vars WHERE workflow_id = ? AND repo_id = ? AND key = ?`,
		workflowID, repoID, key)
	return err
}

// DeleteGroupVar removes a key from the group-global layer.
func (s *Store) DeleteGroupVar(groupID int64, key string) error {
	_, err := s.db.Exec(`DELETE FROM group_vars WHERE group_id = ? AND key = ?`, groupID, key)
	return err
}

// CountsFor reports what a group contains, for a confirmation prompt.
func (s *Store) CountsFor(groupID int64) (repos, workflows, vars int, err error) {
	row := s.db.QueryRow(`
		SELECT
			(SELECT COUNT(*) FROM repos     WHERE group_id = ?),
			(SELECT COUNT(*) FROM workflows WHERE group_id = ?),
			(SELECT COUNT(*) FROM repo_vars rv JOIN repos r ON r.id = rv.repo_id WHERE r.group_id = ?)
			+ (SELECT COUNT(*) FROM group_vars WHERE group_id = ?)`,
		groupID, groupID, groupID, groupID)

	err = row.Scan(&repos, &workflows, &vars)
	return repos, workflows, vars, err
}

// UpdateRepoPath re-points a repo whose checkout moved. Repo paths are a
// stable contract; this is the one command that may change one.
func (s *Store) UpdateRepoPath(id int64, path string) error {
	_, err := s.db.Exec(`UPDATE repos SET path = ? WHERE id = ?`, path, id)
	return err
}
