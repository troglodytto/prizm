package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrReservedName is returned when a workflow name collides with a prizm verb.
var ErrReservedName = errors.New("name is reserved")

// reservedNames are the words that can appear where a verb is expected in
// `prizm <group> <word>`. A workflow named "status" would be permanently
// unreachable in that form, so it is rejected at creation.
//
// Verbs planned for later phases are included deliberately: data created
// today must not block them.
var reservedNames = map[string]bool{
	"up": true, "down": true, "ls": true, "list": true, "status": true,
	"init": true, "add-repo": true, "add-workflow": true, "var": true,
	"import": true, "edit": true, "sync": true, "audit": true,
	"docker": true, "docker-add": true, "docker-ls": true, "repair": true,
	"shared-add": true, "shared-edit": true, "shared-ls": true, "shared-sync": true,
	"global": true, "browse": true, "rename": true, "rm": true, "remove": true, "unset": true, "completion": true, "help": true, "version": true,
}

// IsReservedName reports whether name may not be used as a workflow name.
func IsReservedName(name string) bool { return reservedNames[name] }

// Workflow is a named bundle of repos plus an optional guardrail tag.
type Workflow struct {
	ID      int64
	GroupID int64
	Name    string
	Tag     string
}

// AddWorkflow creates a workflow and its repo membership atomically.
func (s *Store) AddWorkflow(groupID int64, name, tag string, repoIDs []int64) (Workflow, error) {
	if err := checkName("workflow", name); err != nil {
		return Workflow{}, err
	}
	if IsReservedName(name) {
		return Workflow{}, fmt.Errorf("workflow %q: %w (it is a prizm command)", name, ErrReservedName)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return Workflow{}, err
	}
	defer tx.Rollback()

	res, err := tx.Exec(
		`INSERT INTO workflows(group_id, name, tag, created_at) VALUES (?, ?, ?, ?)`,
		groupID, name, tag, time.Now().Unix(),
	)
	if err != nil {
		if isUniqueViolation(err) {
			return Workflow{}, fmt.Errorf("workflow %q: %w", name, ErrExists)
		}
		return Workflow{}, err
	}

	id, err := res.LastInsertId()
	if err != nil {
		return Workflow{}, err
	}

	for _, repoID := range repoIDs {
		if _, err := tx.Exec(
			`INSERT INTO workflow_repos(workflow_id, repo_id) VALUES (?, ?)`, id, repoID,
		); err != nil {
			return Workflow{}, fmt.Errorf("adding repo %d to workflow %q: %w", repoID, name, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return Workflow{}, err
	}
	return Workflow{ID: id, GroupID: groupID, Name: name, Tag: tag}, nil
}

// WorkflowByName looks a workflow up within a group.
func (s *Store) WorkflowByName(groupID int64, name string) (Workflow, error) {
	var w Workflow
	err := s.db.QueryRow(
		`SELECT id, group_id, name, tag FROM workflows WHERE group_id = ? AND name = ?`,
		groupID, name,
	).Scan(&w.ID, &w.GroupID, &w.Name, &w.Tag)

	if errors.Is(err, sql.ErrNoRows) {
		return Workflow{}, fmt.Errorf("workflow %q: %w", name, ErrNotFound)
	}
	if err != nil {
		return Workflow{}, err
	}
	return w, nil
}

// ListWorkflows returns a group's workflows ordered by name.
func (s *Store) ListWorkflows(groupID int64) ([]Workflow, error) {
	rows, err := s.db.Query(
		`SELECT id, group_id, name, tag FROM workflows WHERE group_id = ? ORDER BY name`,
		groupID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Workflow
	for rows.Next() {
		var w Workflow
		if err := rows.Scan(&w.ID, &w.GroupID, &w.Name, &w.Tag); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// WorkflowRepos returns the repos this workflow touches, ordered by name.
func (s *Store) WorkflowRepos(workflowID int64) ([]Repo, error) {
	rows, err := s.db.Query(`
		SELECT r.id, r.group_id, r.name, r.path, r.env_file
		FROM repos r
		JOIN workflow_repos wr ON wr.repo_id = r.id
		WHERE wr.workflow_id = ?
		ORDER BY r.name`, workflowID)
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
