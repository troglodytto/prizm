package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/troglodytto/prizm/internal/apply"
	"github.com/troglodytto/prizm/internal/config"
	"github.com/troglodytto/prizm/internal/envfile"
	"github.com/troglodytto/prizm/internal/resolve"
	"github.com/troglodytto/prizm/internal/sharedfile"
	"github.com/troglodytto/prizm/internal/store"
	"github.com/troglodytto/prizm/internal/style"
)

// prodTag is the guardrail tag that triggers a confirmation prompt.
const prodTag = "prod"

func newUpCmd(app *App) *cobra.Command {
	var yes, dryRun bool

	cmd := &cobra.Command{
		Use:   "up [group] <workflow>",
		Short: "Apply a workflow: build and link every covered repo's env file",
		Long: "Every covered repo gets its env file rebuilt and linked. Repos are\n" +
			"independent: one that fails is reported and skipped with its existing\n" +
			"file untouched, while the rest still apply.\n\n" +
			"--dry-run shows what each repo would gain, lose and change without\n" +
			"writing anything — worth running before a prod-tagged workflow.",
		Args:              usageArgs(cobra.RangeArgs(1, 2)),
		ValidArgsFunction: positions(app, compGroupOrWorkflow, compWorkflow),
		RunE: func(cmd *cobra.Command, args []string) error {
			g, rest, err := app.splitGroup(args, 1)
			if err != nil {
				return err
			}

			wf, err := app.Store.WorkflowByName(g.ID, rest[0])
			if err != nil {
				return fmt.Errorf("no such workflow %q in group %s", rest[0], g.Name)
			}

			if dryRun {
				return previewWorkflow(app, g, wf)
			}

			if wf.Tag == prodTag && !yes {
				ok, err := app.Confirm(fmt.Sprintf(
					"%s is tagged %s. Apply it? [y/N] ", style.Alert(g.Name+"/"+wf.Name), prodTag))
				if err != nil {
					return err
				}
				if !ok {
					app.result(style.Warn, wf.Name, "aborted")
					return errAborted
				}
			}

			return applyWorkflow(app, g, wf)
		},
	}

	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt for prod-tagged workflows")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would change without writing anything")
	return cmd
}

// previewWorkflow reports what `up` would do. It runs the same resolve and
// expand the real apply does — a preview built from a different code path
// would eventually disagree with it, which is worse than no preview.
func previewWorkflow(app *App, g store.Group, wf store.Workflow) error {
	repos, err := app.Store.WorkflowRepos(wf.ID)
	if err != nil {
		return err
	}
	if len(repos) == 0 {
		app.hint("workflow %s covers no repos", wf.Name)
		return nil
	}

	col := style.WidthOf(repoNames(repos))
	failed, changed := 0, 0

	for _, repo := range repos {
		diff, err := previewRepo(app, g, wf, repo)
		if err != nil {
			failed++
			app.row(col, style.Fail, repo.Name, err.Error())
			continue
		}

		if diff.Empty() {
			app.row(col, style.Same, repo.Name, "already up to date")
			continue
		}

		changed++
		app.row(col, style.Add, repo.Name, previewSummary(diff))
		for _, key := range diff.Added {
			app.detail("  + %s", key)
		}
		for _, key := range diff.Removed {
			app.detail("  - %s", key)
		}
		for _, key := range diff.Changed {
			app.detail("  ~ %s", key)
		}
	}

	app.blank()
	if failed > 0 {
		return fmt.Errorf("%d of %d repo(s) would fail", failed, len(repos))
	}
	if changed == 0 {
		app.hint("nothing to do — every repo already matches %s", wf.Name)
		return nil
	}
	app.hint("dry run — nothing was written; drop --dry-run to apply")
	return nil
}

// previewRepo computes what `up` would change for one repo.
//
// It deliberately does not reuse inspectRepo: that reports an unresolvable
// repo as "not applied" so a `status` listing still finishes, which is right
// there and wrong here. The point of a dry run is to find out that a repo
// would fail *before* running it for real, so the error propagates.
func previewRepo(app *App, g store.Group, wf store.Workflow, repo store.Repo) (sharedfile.Diff, error) {
	expected, err := buildEnv(app, wf, repo)
	if err != nil {
		return sharedfile.Diff{}, err
	}

	onDisk, err := readAppliedEnv(repo)
	if err != nil {
		// No file yet is not a failure: every key is simply an addition.
		if !os.IsNotExist(errors.Unwrap(err)) {
			return sharedfile.Diff{}, err
		}
		onDisk = map[string]string{}
	}

	return sharedfile.Compare(expected, onDisk), nil
}

// previewSummary counts the moves rather than repeating them, since the
// keys themselves are listed underneath.
func previewSummary(diff sharedfile.Diff) string {
	parts := make([]string, 0, 3)
	if n := len(diff.Added); n > 0 {
		parts = append(parts, fmt.Sprintf("+%d", n))
	}
	if n := len(diff.Removed); n > 0 {
		parts = append(parts, fmt.Sprintf("-%d", n))
	}
	if n := len(diff.Changed); n > 0 {
		parts = append(parts, fmt.Sprintf("~%d", n))
	}
	return strings.Join(parts, " ") + "   would be written"
}

// repoNames pulls the name column out of a repo list.
func repoNames(repos []store.Repo) []string {
	names := make([]string, 0, len(repos))
	for _, r := range repos {
		names = append(names, r.Name)
	}
	return names
}

// applyWorkflow applies one workflow to every repo it covers. Repos are
// independent: one that fails is reported and skipped, leaving its existing
// env file untouched, while every other repo still applies.
func applyWorkflow(app *App, g store.Group, wf store.Workflow) error {
	dir, err := config.DataDir()
	if err != nil {
		return err
	}
	lock, err := apply.Acquire(dir)
	if err != nil {
		if errors.Is(err, apply.ErrLocked) {
			return fmt.Errorf("another prizm apply is running — wait for it to finish")
		}
		return err
	}
	defer lock.Release()

	repos, err := app.Store.WorkflowRepos(wf.ID)
	if err != nil {
		return err
	}
	if len(repos) == 0 {
		app.hint("workflow %s covers no repos", wf.Name)
		return nil
	}

	col := style.WidthOf(repoNames(repos))

	failed := 0
	for _, repo := range repos {
		if err := applyRepo(app, g, wf, repo); err != nil {
			failed++
			app.row(col, style.Fail, repo.Name, err.Error())
			continue
		}
		app.row(col, style.OK, repo.Name, "set ("+wf.Name+")")
	}

	if err := app.Store.TouchGroup(g.ID, app.Now()); err != nil {
		return err
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d repo(s) failed", failed, len(repos))
	}
	return nil
}

// buildEnv is the single resolve path: merge the layers, expand the
// references, drop the internal keys. Both apply and preview go through it,
// so a dry run can never disagree with the run it is predicting.
func buildEnv(app *App, wf store.Workflow, repo store.Repo) (map[string]string, error) {
	templates, err := resolve.ForRepo(app.Store, wf, repo)
	if err != nil {
		return nil, err
	}

	expanded, err := resolve.Expand(templates)
	if err != nil {
		// Unresolved references and cycles are user errors; say so plainly.
		if errors.Is(err, resolve.ErrUnresolved) || errors.Is(err, resolve.ErrCycle) {
			return nil, fmt.Errorf("%v — %s left unchanged", err, repo.EnvFile)
		}
		return nil, err
	}

	return resolve.Emit(expanded), nil
}

// applyRepo resolves, expands and writes one repo's env file. Any failure
// leaves that repo's existing env file exactly as it was.
func applyRepo(app *App, g store.Group, wf store.Workflow, repo store.Repo) error {
	vars, err := buildEnv(app, wf, repo)
	if err != nil {
		return err
	}

	content := envfile.Render(vars)

	builtPath, err := config.BuiltPath(g.Name, wf.Name, repo.Name)
	if err != nil {
		return err
	}

	res, err := apply.Apply(builtPath, content, repo.Path, repo.EnvFile, app.Now())
	if err != nil {
		return err
	}
	if res.BackedUpTo != "" {
		app.detail("  backed up existing %s → %s", repo.EnvFile, res.BackedUpTo)
	}

	return app.Store.RecordApplied(repo.ID, wf.ID, res.BuiltPath, app.Now())
}
