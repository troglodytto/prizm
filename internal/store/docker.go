package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

// DockerStack is the compose configuration attached to a workflow.
type DockerStack struct {
	WorkflowID  int64
	ComposePath string
	Services    []string // empty means the whole compose file
}

// ErrNoStack means the workflow has no compose stack attached.
var ErrNoStack = errors.New("no docker stack for this workflow")

// SetDockerStack attaches or replaces a workflow's compose stack.
func (s *Store) SetDockerStack(workflowID int64, composePath string, services []string) error {
	_, err := s.db.Exec(`
		INSERT INTO workflow_docker(workflow_id, compose_path, services, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(workflow_id) DO UPDATE SET
			compose_path = excluded.compose_path,
			services     = excluded.services,
			updated_at   = excluded.updated_at`,
		workflowID, composePath, strings.Join(services, ","), time.Now().Unix())
	return err
}

// DockerStackFor returns a workflow's stack, or ErrNoStack.
func (s *Store) DockerStackFor(workflowID int64) (DockerStack, error) {
	stack := DockerStack{WorkflowID: workflowID}

	var services string
	err := s.db.QueryRow(
		`SELECT compose_path, services FROM workflow_docker WHERE workflow_id = ?`, workflowID,
	).Scan(&stack.ComposePath, &services)

	if errors.Is(err, sql.ErrNoRows) {
		return DockerStack{}, ErrNoStack
	}
	if err != nil {
		return DockerStack{}, err
	}

	if services != "" {
		stack.Services = strings.Split(services, ",")
	}
	return stack, nil
}

// DeleteDockerStack detaches a workflow's stack.
func (s *Store) DeleteDockerStack(workflowID int64) error {
	_, err := s.db.Exec(`DELETE FROM workflow_docker WHERE workflow_id = ?`, workflowID)
	return err
}
