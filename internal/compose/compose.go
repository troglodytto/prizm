// Package compose drives `docker compose` for a workflow's services.
//
// It shells out to the CLI rather than using Docker's Go SDK: it behaves
// exactly like what a person would type, which is what makes it debuggable
// when it breaks, and it is a fraction of the code.
package compose

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// ErrNoDocker means the docker CLI is not installed or not on PATH.
var ErrNoDocker = errors.New("docker is not available")

// Runner executes a compose command. Swapped out in tests, and the reason
// nothing here needs a live daemon to be covered.
type Runner interface {
	Run(ctx context.Context, args ...string) (string, error)
}

// CLI is the real runner.
type CLI struct{}

// Run executes `docker <args>` and returns its combined output. Compose puts
// its progress on stderr, so a failure with no stdout still explains itself.
func (CLI) Run(ctx context.Context, args ...string) (string, error) {
	path, err := exec.LookPath("docker")
	if err != nil {
		return "", ErrNoDocker
	}

	var buf bytes.Buffer
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Stdout, cmd.Stderr = &buf, &buf

	if err := cmd.Run(); err != nil {
		return buf.String(), fmt.Errorf("docker %s: %w", strings.Join(args, " "), err)
	}
	return buf.String(), nil
}

// Stack is a compose file and the subset of services to act on.
type Stack struct {
	ComposePath string
	Services    []string
	Project     string // --project-name, so two workflows never share containers
}

// Timeout bounds a compose call. Pulling an image the first time is slow, but
// an unbounded wait would hang `up` with no way to tell whether anything is
// happening.
const Timeout = 5 * time.Minute

// Up starts the stack's services in the background.
func Up(ctx context.Context, r Runner, s Stack) (string, error) {
	args := append(s.base(), "up", "-d", "--remove-orphans")
	return r.Run(ctx, append(args, s.Services...)...)
}

// Down stops the stack's services.
//
// Named services are stopped rather than removed: `docker compose down`
// takes the whole project with it, which would also stop services a
// different workflow put up from the same file.
func Down(ctx context.Context, r Runner, s Stack) (string, error) {
	if len(s.Services) == 0 {
		return r.Run(ctx, append(s.base(), "down")...)
	}
	args := append(s.base(), "stop")
	return r.Run(ctx, append(args, s.Services...)...)
}

// Running lists the stack's services that are currently up. The raw output
// comes back too, so a caller can report docker's own explanation on failure.
func Running(ctx context.Context, r Runner, s Stack) ([]string, string, error) {
	out, err := r.Run(ctx, append(s.base(), "ps", "--services", "--filter", "status=running")...)
	if err != nil {
		return nil, out, err
	}

	var names []string
	for _, line := range strings.Split(out, "\n") {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		// A workflow that named a subset only cares about that subset.
		if len(s.Services) > 0 && !contains(s.Services, name) {
			continue
		}
		names = append(names, name)
	}
	return names, out, nil
}

// base is the argument prefix every call shares.
func (s Stack) base() []string {
	args := []string{"compose", "-f", s.ComposePath}
	if s.Project != "" {
		args = append(args, "--project-name", s.Project)
	}
	return args
}

func contains(haystack []string, needle string) bool {
	for _, v := range haystack {
		if v == needle {
			return true
		}
	}
	return false
}

// ProjectName derives a stable, compose-legal project name for a workflow.
//
// Scoping by group and workflow is what keeps `local` and `staging` from
// adopting each other's containers when they point at the same compose file.
func ProjectName(group, workflow string) string {
	return "prizm-" + sanitise(group) + "-" + sanitise(workflow)
}

// sanitise reduces a name to what compose accepts: lowercase alphanumerics,
// dashes and underscores.
func sanitise(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return b.String()
}
