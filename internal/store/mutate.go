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
	return s.deleteWithHistory(
		`DELETE FROM "groups" WHERE id = ?`, id,
		// Every timeline that belongs to this group: its own, its repos'
		// wiring, its workflows' bags, and each repo+workflow pair.
		`scope_kind = 'group' AND scope_a = ?`,
		`scope_kind = 'repo' AND scope_a IN (SELECT id FROM repos WHERE group_id = ?)`,
		`scope_kind = 'shared_group' AND scope_a IN (
			SELECT sg.id FROM shared_groups sg
			JOIN workflows w ON w.id = sg.workflow_id WHERE w.group_id = ?)`,
		`scope_kind = 'workflow_repo' AND scope_a IN (SELECT id FROM workflows WHERE group_id = ?)`,
	)
}

// DeleteRepo removes a repo, its variables, and its membership everywhere.
func (s *Store) DeleteRepo(id int64) error {
	return s.deleteWithHistory(`DELETE FROM repos WHERE id = ?`, id,
		`scope_kind = 'repo' AND scope_a = ?`,
		`scope_kind = 'workflow_repo' AND scope_b = ?`,
	)
}

// DeleteWorkflow removes a workflow, its bags and its variables.
func (s *Store) DeleteWorkflow(id int64) error {
	return s.deleteWithHistory(`DELETE FROM workflows WHERE id = ?`, id,
		`scope_kind = 'workflow_repo' AND scope_a = ?`,
		`scope_kind = 'shared_group' AND scope_a IN (
			SELECT id FROM shared_groups WHERE workflow_id = ?)`,
	)
}

// DeleteSharedGroup removes a shared bag and its variables.
func (s *Store) DeleteSharedGroup(id int64) error {
	return s.deleteWithHistory(`DELETE FROM shared_groups WHERE id = ?`, id,
		`scope_kind = 'shared_group' AND scope_a = ?`,
	)
}

// deleteWithHistory removes a row and the snapshot timelines addressed to it,
// in one transaction.
//
// Snapshots are addressed by (kind, id) rather than by a foreign key, so
// nothing cascades them away. That would be a slow leak on its own; what makes
// it a disclosure is that SQLite reissues a freed rowid to the next row
// inserted. Delete a repo and create another, and the new one inherits the old
// one's id — and with it, an audit trail holding the deleted repo's values in
// full. `audit --restore` would then write them into a repo that never had
// them.
func (s *Store) deleteWithHistory(del string, id int64, scopes ...string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, where := range scopes {
		if _, err := tx.Exec(
			`DELETE FROM snapshot_vars WHERE snapshot_id IN (
				SELECT id FROM snapshots WHERE `+where+`)`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM snapshots WHERE `+where, id); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(del, id); err != nil {
		return err
	}
	return tx.Commit()
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

// SetWorkflowTag changes a workflow's guardrail tag. An empty tag clears it.
func (s *Store) SetWorkflowTag(id int64, tag string) error {
	_, err := s.db.Exec(`UPDATE workflows SET tag = ? WHERE id = ?`, tag, id)
	return err
}

// ReplaceWorkflowRepos makes a workflow's membership exactly repoIDs.
//
// Variables for a repo that drops out are deliberately kept: removing a repo
// from a workflow is usually a change of scope, not a decision to throw away
// its configuration, and adding it back should not mean retyping everything.
// `prizm unset` is how you actually discard values.
func (s *Store) ReplaceWorkflowRepos(id int64, repoIDs []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM workflow_repos WHERE workflow_id = ?`, id); err != nil {
		return err
	}
	for _, repoID := range repoIDs {
		if _, err := tx.Exec(
			`INSERT INTO workflow_repos(workflow_id, repo_id) VALUES (?, ?)`, id, repoID,
		); err != nil {
			return fmt.Errorf("adding repo %d to the workflow: %w", repoID, err)
		}
	}
	return tx.Commit()
}
