package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/troglodytto/prizm/internal/config"
	"github.com/troglodytto/prizm/internal/drift"
	"github.com/troglodytto/prizm/internal/resolve"
	"github.com/troglodytto/prizm/internal/store"
	"github.com/troglodytto/prizm/internal/style"
)

func newStatusCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "status [group]",
		Short: "Show which workflow each repo is on, and whether it has drifted",
		Long: "After a few days of switching between workflows you stop remembering\n" +
			"which one each repo is actually sitting on. This answers that, and\n" +
			"flags any repo whose env file no longer matches what prizm would write.",
		Args: usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			g, _, err := app.splitGroup(args, 0)
			if err != nil {
				return err
			}
			return reportStatus(app, g)
		},
	}
}

func reportStatus(app *App, g store.Group) error {
	repos, err := app.Store.ListRepos(g.ID)
	if err != nil {
		return err
	}
	if len(repos) == 0 {
		app.hint("%s has no repos — run `prizm add-repo %s .`", g.Name, g.Name)
		return nil
	}

	applied, err := app.Store.AppliedFor(g.ID)
	if err != nil {
		return err
	}
	workflows, err := app.Store.ListWorkflows(g.ID)
	if err != nil {
		return err
	}
	byID := make(map[int64]store.Workflow, len(workflows))
	for _, w := range workflows {
		byID[w.ID] = w
	}

	names := make([]string, 0, len(repos))
	for _, r := range repos {
		names = append(names, r.Name)
	}
	col := style.WidthOf(names)

	app.heading("%s", g.Name)

	var drifted, missing, unapplied int
	for _, repo := range repos {
		state, ok := applied[repo.ID]
		if !ok {
			unapplied++
			app.row(col, style.Same, repo.Name, "not applied")
			continue
		}

		wf := byID[state.WorkflowID]
		report, err := inspectRepo(app, g, wf, repo)
		if err != nil {
			return err
		}

		mark, detail := statusOf(report, wf)
		switch {
		case report.Link == drift.PathMissing:
			missing++
		case !report.Diff.Empty():
			drifted++
		}
		app.say(col.Row(mark, repo.Name, detail) + tagSuffix(wf.Tag))
	}

	app.blank()
	switch {
	case missing > 0:
		app.hint("a repo's directory has moved — run `prizm repair %s <repo>`", g.Name)
	case drifted > 0:
		app.hint("drifted files were hand-edited; re-applying will overwrite them")
	case unapplied == len(repos):
		app.hint("nothing applied yet — run `prizm %s <workflow>`", g.Name)
	}
	return nil
}

// statusOf turns a drift report into the mark and text for one line.
func statusOf(report drift.Report, wf store.Workflow) (style.Mark, string) {
	switch {
	case report.Link == drift.PathMissing:
		return style.Fail, "path missing — " + report.Repo.Path

	case report.Link == drift.NoFile:
		return style.Warn, wf.Name + " · env file is gone"

	case report.Link == drift.Unmanaged:
		return style.Warn, wf.Name + " · a real file, not prizm's link"

	case report.Link == drift.ManagedElsewhere:
		return style.Warn, wf.Name + " · linked to another build"

	case !report.Diff.Empty():
		return style.Change, fmt.Sprintf("%s · %s", wf.Name, plural(report.Changes(), "local edit"))
	}
	return style.OK, wf.Name
}

func tagSuffix(tag string) string {
	if tag == "" {
		return ""
	}
	return "  " + style.Tag(tag)
}

// inspectRepo resolves what up would write for a repo, then compares it to disk.
func inspectRepo(app *App, g store.Group, wf store.Workflow, repo store.Repo) (drift.Report, error) {
	templates, err := resolve.ForRepo(app.Store, wf, repo)
	if err != nil {
		return drift.Report{}, err
	}

	// A repo that cannot resolve cannot be compared; report it as unapplied
	// rather than failing the whole listing.
	expanded, err := resolve.Expand(templates)
	if err != nil {
		return drift.Report{Repo: repo, Link: drift.NoFile}, nil
	}

	builtPath, err := config.BuiltPath(g.Name, wf.Name, repo.Name)
	if err != nil {
		return drift.Report{}, err
	}
	return drift.Inspect(repo, resolve.Emit(expanded), builtPath)
}
