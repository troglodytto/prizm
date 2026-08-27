package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/troglodytto/prizm/internal/compose"
	"github.com/troglodytto/prizm/internal/store"
	"github.com/troglodytto/prizm/internal/style"
)

func newDockerCmd(app *App) *cobra.Command {
	var (
		composePath string
		services    string
		detach      bool
	)

	cmd := &cobra.Command{
		Use:               "docker [group] <workflow>",
		ValidArgsFunction: positions(app, compGroupOrWorkflow, compWorkflow),
		Short:             "Attach a compose stack to a workflow",
		Long: "Some workflows need more than env files — a database, a tunnel, a\n" +
			"local queue. Attach a compose file to the workflow and `prizm up`\n" +
			"brings those services up after the env files are written.\n\n" +
			"Services are scoped to the workflow with a compose project name, so\n" +
			"two workflows can point at the same compose file without adopting\n" +
			"each other's containers.\n\n" +
			"With no flags, shows what is attached.",
		Args: usageArgs(cobra.RangeArgs(1, 2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			g, wf, err := app.groupWorkflow(args)
			if err != nil {
				return err
			}

			switch {
			case detach:
				if composePath != "" || services != "" {
					return errUsage("--detach removes the stack; drop the other flags")
				}
				return detachStack(app, g, wf)
			case composePath != "":
				return attachStack(app, g, wf, composePath, splitList(services))
			case services != "":
				return errUsage("--services needs --compose: there is no file to take them from")
			default:
				return showStack(app, g, wf)
			}
		},
	}

	cmd.Flags().StringVar(&composePath, "compose", "", "path to the compose file")
	cmd.Flags().StringVar(&services, "services", "", "comma-separated services to bring up (default: all)")
	cmd.Flags().BoolVar(&detach, "detach", false, "remove the stack from this workflow")
	return cmd
}

func attachStack(app *App, g store.Group, wf store.Workflow, path string, services []string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	// The path is stored, so a typo would only surface at the next `up` —
	// long after the person who made it has moved on.
	if _, err := os.Stat(abs); err != nil {
		return fmt.Errorf("no compose file at %s", abs)
	}

	if err := app.Store.SetDockerStack(wf.ID, abs, services); err != nil {
		return err
	}

	app.result(style.OK, g.Name+"/"+wf.Name, "compose stack attached")
	col := style.WidthOf(stackFields)
	app.field(col, "file", abs)
	app.field(col, "services", listOrAll(services))
	app.field(col, "project", compose.ProjectName(g.Name, wf.Name))
	return nil
}

func detachStack(app *App, g store.Group, wf store.Workflow) error {
	if _, err := app.Store.DockerStackFor(wf.ID); err != nil {
		if errors.Is(err, store.ErrNoStack) {
			app.result(style.Same, wf.Name, "no compose stack attached")
			return nil
		}
		return err
	}

	if err := app.Store.DeleteDockerStack(wf.ID); err != nil {
		return err
	}

	app.result(style.OK, g.Name+"/"+wf.Name, "compose stack removed")
	app.hint("containers already running are left alone — run `prizm down` first to stop them")
	return nil
}

func showStack(app *App, g store.Group, wf store.Workflow) error {
	stack, err := app.Store.DockerStackFor(wf.ID)
	if errors.Is(err, store.ErrNoStack) {
		app.hint("%s has no compose stack — attach one with `prizm docker %s %s --compose <file>`",
			wf.Name, g.Name, wf.Name)
		return nil
	}
	if err != nil {
		return err
	}

	app.heading("%s/%s", g.Name, wf.Name)
	col := style.WidthOf(stackFields)
	app.field(col, "file", stack.ComposePath)
	app.field(col, "services", listOrAll(stack.Services))
	app.field(col, "project", compose.ProjectName(g.Name, wf.Name))

	running, out, err := runningServices(app, g, wf, stack)
	if err != nil {
		// Docker being unreachable is not a reason to fail a listing; say so
		// where the answer would have been.
		app.field(col, "running", "unknown — "+dockerReason(err, out))
		return nil
	}
	app.field(col, "running", listOrNone(running))
	return nil
}

// bringUp starts a workflow's services. It is deliberately best-effort and
// reported separately from the env files: Docker not running must never make
// a successful env write look like a failure.
func bringUp(app *App, g store.Group, wf store.Workflow) {
	stack, err := app.Store.DockerStackFor(wf.ID)
	if err != nil {
		return // no stack, or a store failure the env write already survived
	}
	if app.Docker == nil {
		return
	}

	app.blank()
	label := listOrAll(stack.Services)

	out, err := app.withSpinner("starting "+label, func(ctx context.Context) (string, error) {
		return compose.Up(ctx, app.Docker, composeStack(g, wf, stack))
	})
	if err != nil {
		app.result(style.Fail, label, dockerReason(err, out))
		if detail := dockerDetail(out); detail != "" {
			app.detail("  %s", detail)
		}
		app.hint("the env files were written — this only affects the services")
		return
	}

	app.result(style.OK, label, "running")
}

// takeDown stops a workflow's services.
func takeDown(app *App, g store.Group, wf store.Workflow) error {
	stack, err := app.Store.DockerStackFor(wf.ID)
	if errors.Is(err, store.ErrNoStack) {
		app.hint("%s has no compose stack — nothing to stop", wf.Name)
		return nil
	}
	if err != nil {
		return err
	}

	label := listOrAll(stack.Services)
	out, err := app.withSpinner("stopping "+label, func(ctx context.Context) (string, error) {
		return compose.Down(ctx, app.Docker, composeStack(g, wf, stack))
	})
	if err != nil {
		// Cobra prints the returned error after RunE, so anything written
		// here lands above it and reads backwards. Fold the detail in.
		reason := dockerReason(err, out)
		if detail := dockerDetail(out); detail != "" {
			reason += "\n  " + style.Detail(detail)
		}
		return errors.New(reason)
	}

	app.result(style.OK, g.Name+"/"+wf.Name, label+" stopped")
	return nil
}

// runningServices also returns docker's raw output, so a failure can be
// explained in docker's own words rather than prizm's.
func runningServices(app *App, g store.Group, wf store.Workflow, stack store.DockerStack) ([]string, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), compose.Timeout)
	defer cancel()
	return compose.Running(ctx, app.Docker, composeStack(g, wf, stack))
}

func composeStack(g store.Group, wf store.Workflow, stack store.DockerStack) compose.Stack {
	return compose.Stack{
		ComposePath: stack.ComposePath,
		Services:    stack.Services,
		Project:     compose.ProjectName(g.Name, wf.Name),
	}
}

// dockerReason turns a compose failure into one line.
//
// It prefers docker's own first line of output over the wrapped error: the
// error says which command failed, which the user can already see, while the
// output says *why* — "daemon not running", "no such service", "port in use".
// Echoing the command line instead is how a tool sends someone to debug
// prizm when the answer was that Docker Desktop was closed.
func dockerReason(err error, out string) string {
	if errors.Is(err, compose.ErrNoDocker) {
		return "docker is not installed or not on PATH"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "docker timed out after " + compose.Timeout.String()
	}
	line := firstLine(out)
	if line == "" {
		return err.Error()
	}

	// Docker's daemon-unreachable message is three clauses and a socket
	// path. It is the single most common failure, and at that length it
	// wraps into a wall that hides the one word that matters.
	if isDaemonDown(line) {
		return "the docker daemon is not running"
	}
	return line
}

// isDaemonDown recognises the daemon-unreachable message. Matching on text is
// unlovely, but it is the only signal the CLI gives, and being wrong only
// costs a slightly longer error line.
func isDaemonDown(line string) bool {
	lower := strings.ToLower(line)
	return strings.Contains(lower, "docker daemon") ||
		strings.Contains(lower, "docker api") ||
		strings.Contains(lower, "docker.sock")
}

// dockerDetail is the full text, printed under the short reason when it adds
// something — the socket path is what you need to fix the daemon case.
func dockerDetail(out string) string {
	line := firstLine(out)
	if line == "" || !isDaemonDown(line) {
		return ""
	}
	return line
}

// firstLine picks the first non-empty line, which is where docker puts the
// failure. Progress chatter ("Container x  Creating") is filtered out so a
// failure part-way through a startup still reports the failure.
func firstLine(out string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Container ") || strings.HasPrefix(line, "Network ") {
			continue
		}
		return line
	}
	return ""
}

func splitList(csv string) []string {
	var out []string
	for _, part := range strings.Split(csv, ",") {
		if name := strings.TrimSpace(part); name != "" {
			out = append(out, name)
		}
	}
	return out
}

func listOrAll(names []string) string {
	if len(names) == 0 {
		return "all services"
	}
	return strings.Join(names, ", ")
}

func listOrNone(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

// stackFields is the label column for a stack listing, measured once so the
// values line up the way every other listing does.
var stackFields = []string{"file", "services", "project", "running"}
