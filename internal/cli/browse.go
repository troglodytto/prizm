package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/troglodytto/prizm/internal/rank"
	"github.com/troglodytto/prizm/internal/store"
	"github.com/troglodytto/prizm/internal/tui"
)

// newPickCmd is the interactive front door, and what the group-first sugar
// resolves to: `prizm XYZ` drills straight into XYZ's workflows.
func newPickCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:     "pick [group]",
		Short:   "Choose a workflow interactively and apply it",
		Aliases: []string{"browse"},
		Long: "Walks group → workflow → apply. Naming a group skips the first step,\n" +
			"which is what `prizm <group>` does.\n\n" +
			"Without a terminal it lists instead of prompting, so it stays usable\n" +
			"in a pipe or a script.",
		Args: usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			if !app.canPick() {
				if name == "" {
					return listGroups(app)
				}
				return listGroup(app, name)
			}
			return browse(app, name)
		},
	}
}

// canPick reports whether an interactive picker may be shown. A picker
// injected by a test counts, so the drill-down is testable without a terminal.
func (a *App) canPick() bool {
	return a.PickOne != nil && (tui.Available() || a.pickerInjected)
}

func (a *App) canPickMany() bool {
	return a.PickMany != nil && (tui.Available() || a.pickerInjected)
}

// browse drives group → workflow → apply. A named group skips the first step.
func browse(app *App, groupName string) error {
	if groupName == "" {
		chosen, err := pickGroup(app)
		if err != nil {
			return quietCancel(err)
		}
		if chosen == "" {
			return nil
		}
		groupName = chosen
	}

	g, err := app.mustGroup(groupName)
	if err != nil {
		return err
	}

	workflow, err := pickWorkflow(app, g)
	if err != nil {
		return quietCancel(err)
	}
	if workflow == "" {
		return nil
	}

	wf, err := app.Store.WorkflowByName(g.ID, workflow)
	if err != nil {
		return fmt.Errorf("no such workflow %q in group %s", workflow, g.Name)
	}
	return applyWorkflow(app, g, wf)
}

func pickGroup(app *App) (string, error) {
	groups, err := app.Store.ListGroups()
	if err != nil {
		return "", err
	}
	if len(groups) == 0 {
		app.hint("no groups yet — run `prizm init <name>`")
		return "", nil
	}
	if !app.canPick() {
		return "", listGroups(app)
	}

	// The same ordering completion uses, so the two never disagree.
	paths, err := app.Store.RepoPathsByGroup()
	if err != nil {
		return "", err
	}
	cwd, _ := app.Cwd()

	candidates := make([]rank.Candidate, 0, len(groups))
	for _, g := range groups {
		candidates = append(candidates, rank.Candidate{
			Name: g.Name, Paths: paths[g.Name], UseCount: g.UseCount, LastUsedAt: g.LastUsedAt,
		})
	}

	options := make([]tui.Option, 0, len(groups))
	for _, name := range rank.Rank(candidates, cwd, app.Now()) {
		options = append(options, tui.Option{
			Value: name,
			Label: name,
			Desc:  plural(len(paths[name]), "repo"),
		})
	}
	return app.PickOne("Select a group", options)
}

func pickWorkflow(app *App, g store.Group) (string, error) {
	workflows, err := app.Store.ListWorkflows(g.ID)
	if err != nil {
		return "", err
	}
	if len(workflows) == 0 {
		app.hint("%s has no workflows — run `prizm add-workflow %s <name>`", g.Name, g.Name)
		return "", nil
	}
	if !app.canPick() {
		return "", listGroup(app, g.Name)
	}

	options := make([]tui.Option, 0, len(workflows))
	for _, w := range workflows {
		repos, err := app.Store.WorkflowRepos(w.ID)
		if err != nil {
			return "", err
		}
		names := make([]string, 0, len(repos))
		for _, r := range repos {
			names = append(names, r.Name)
		}

		options = append(options, tui.Option{
			Value: w.Name,
			Label: w.Name,
			Desc:  joinOrNone(names),
			Tag:   w.Tag,
		})
	}
	return app.PickOne("Select a workflow — "+g.Name, options)
}

// chooseRepos resolves which repos a workflow or bag covers.
//
// An explicit --repos always wins. Otherwise, with a terminal, a checkbox list
// opens with everything ticked — so pressing Enter reproduces the
// non-interactive default of "all repos" exactly, and unticking is the
// deliberate act. Without a terminal the default is unchanged.
func chooseRepos(app *App, g store.Group, list, purpose string) ([]int64, error) {
	if list != "" {
		return resolveRepoIDs(app, g, list)
	}

	repos, err := app.Store.ListRepos(g.ID)
	if err != nil || len(repos) == 0 {
		return nil, err
	}

	ids := make([]int64, 0, len(repos))
	for _, r := range repos {
		ids = append(ids, r.ID)
	}
	if !app.canPickMany() {
		return ids, nil
	}

	options := make([]tui.Option, 0, len(repos))
	all := make([]string, 0, len(repos))
	byName := make(map[string]int64, len(repos))
	for _, r := range repos {
		options = append(options, tui.Option{Value: r.Name, Label: r.Name, Desc: r.Path})
		all = append(all, r.Name)
		byName[r.Name] = r.ID
	}

	chosen, err := app.PickMany(purpose, options, all)
	if err != nil {
		if errors.Is(err, tui.ErrCancelled) {
			return nil, errCancelledByUser
		}
		return nil, err
	}

	out := make([]int64, 0, len(chosen))
	for _, name := range chosen {
		out = append(out, byName[name])
	}
	return out, nil
}

// errCancelledByUser signals a clean abort up to the command boundary.
var errCancelledByUser = errors.New("cancelled by user")

// quietCancel turns a user abort into a clean exit.
func quietCancel(err error) error {
	if errors.Is(err, tui.ErrCancelled) || errors.Is(err, errCancelledByUser) {
		return nil
	}
	return err
}

// quietUserCancel turns an aborted prompt into a silent, successful exit.
func quietUserCancel(app *App, err error) error {
	if errors.Is(err, errCancelledByUser) {
		app.hint("cancelled")
		return nil
	}
	return err
}
