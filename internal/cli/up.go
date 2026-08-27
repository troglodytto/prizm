package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/troglodytto/prizm/internal/apply"
	"github.com/troglodytto/prizm/internal/config"
	"github.com/troglodytto/prizm/internal/envfile"
	"github.com/troglodytto/prizm/internal/resolve"
	"github.com/troglodytto/prizm/internal/store"
	"github.com/troglodytto/prizm/internal/style"
)

// prodTag is the guardrail tag that triggers a confirmation prompt.
const prodTag = "prod"

func newUpCmd(app *App) *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:   "up [group] <workflow>",
		Short: "Apply a workflow: build and link every covered repo's env file",
		Args:  usageArgs(cobra.RangeArgs(1, 2)),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			return app.completeGroupThenWorkflow(args, toComplete)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			g, rest, err := app.splitGroup(args, 1)
			if err != nil {
				return err
			}

			wf, err := app.Store.WorkflowByName(g.ID, rest[0])
			if err != nil {
				return fmt.Errorf("no such workflow %q in group %s", rest[0], g.Name)
			}

			if wf.Tag == prodTag && !yes {
				ok, err := app.Confirm(fmt.Sprintf(
					"%s is tagged %s. Apply it? [y/N] ", style.Alert(g.Name+"/"+wf.Name), prodTag))
				if err != nil {
					return err
				}
				if !ok {
					return errors.New("aborted")
				}
			}

			return applyWorkflow(app, g, wf)
		},
	}

	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt for prod-tagged workflows")
	return cmd
}

// applyWorkflow applies one workflow to every repo it covers. Repos are
// independent: one that fails is reported and skipped, leaving its existing
// env file untouched, while every other repo still applies.
func applyWorkflow(app *App, g store.Group, wf store.Workflow) error {
	repos, err := app.Store.WorkflowRepos(wf.ID)
	if err != nil {
		return err
	}
	if len(repos) == 0 {
		fmt.Fprintln(app.Out, style.Hint("workflow "+wf.Name+" covers no repos"))
		return nil
	}

	names := make([]string, 0, len(repos))
	for _, r := range repos {
		names = append(names, r.Name)
	}
	col := style.WidthOf(names)

	failed := 0
	for _, repo := range repos {
		if err := applyRepo(app, g, wf, repo); err != nil {
			failed++
			fmt.Fprintln(app.Out, col.Row(style.Fail, repo.Name, err.Error()))
			continue
		}
		fmt.Fprintln(app.Out, col.Row(style.OK, repo.Name, "set ("+wf.Name+")"))
	}

	if err := app.Store.TouchGroup(g.ID, app.Now()); err != nil {
		return err
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d repo(s) failed", failed, len(repos))
	}
	return nil
}

// applyRepo resolves, expands and writes one repo's env file. Any failure
// leaves that repo's existing env file exactly as it was.
func applyRepo(app *App, g store.Group, wf store.Workflow, repo store.Repo) error {
	templates, err := resolve.ForRepo(app.Store, wf, repo)
	if err != nil {
		return err
	}

	expanded, err := resolve.Expand(templates)
	if err != nil {
		// Unresolved references and cycles are user errors; say so plainly.
		if errors.Is(err, resolve.ErrUnresolved) || errors.Is(err, resolve.ErrCycle) {
			return fmt.Errorf("%v — %s left unchanged", err, repo.EnvFile)
		}
		return err
	}

	content := envfile.Render(resolve.Emit(expanded))

	builtPath, err := config.BuiltPath(g.Name, wf.Name, repo.Name)
	if err != nil {
		return err
	}

	res, err := apply.Apply(builtPath, content, repo.Path, repo.EnvFile, app.Now())
	if err != nil {
		return err
	}
	if res.BackedUpTo != "" {
		fmt.Fprintln(app.Out, style.Detail("  backed up existing "+repo.EnvFile+" → "+res.BackedUpTo))
	}

	return app.Store.RecordApplied(repo.ID, wf.ID, res.BuiltPath, app.Now())
}
