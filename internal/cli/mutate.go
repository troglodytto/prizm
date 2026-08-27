package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/troglodytto/prizm/internal/store"
	"github.com/troglodytto/prizm/internal/style"
)

func newRenameCmd(app *App) *cobra.Command {
	var (
		repo     string
		workflow string
	)

	cmd := &cobra.Command{
		Use:   "rename [group] <new-name>",
		Short: "Rename a group, or one of its repos or workflows",
		Long: "Renames the group by default. Use --repo or --workflow to rename one\n" +
			"of its members instead.\n\n" +
			"  prizm rename acme platform            # the group\n" +
			"  prizm rename acme api --repo backend  # a repo\n" +
			"  prizm rename acme qa --workflow staging",
		Args: usageArgs(cobra.RangeArgs(1, 2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			g, rest, err := app.splitGroup(args, 1)
			if err != nil {
				return err
			}
			newName := rest[0]

			switch {
			case repo != "" && workflow != "":
				return errUsage("pass --repo or --workflow, not both")

			case repo != "":
				r, err := app.repoIn(g, repo)
				if err != nil {
					return err
				}
				if err := app.Store.RenameRepo(r.ID, newName); err != nil {
					return err
				}
				app.result(style.OK, newName, "renamed from "+repo)
				app.hint("run `prizm %s <workflow>` to re-link it under the new name", g.Name)

			case workflow != "":
				wf, err := app.Store.WorkflowByName(g.ID, workflow)
				if err != nil {
					return fmt.Errorf("no such workflow %q in group %s", workflow, g.Name)
				}
				if err := app.Store.RenameWorkflow(wf.ID, newName); err != nil {
					return err
				}
				app.result(style.OK, newName, "renamed from "+workflow)

			default:
				old := g.Name
				if err := app.Store.RenameGroup(g.ID, newName); err != nil {
					return err
				}
				app.result(style.OK, newName, "renamed from "+old)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&repo, "repo", "", "rename this repo instead of the group")
	cmd.Flags().StringVar(&workflow, "workflow", "", "rename this workflow instead of the group")
	return cmd
}

func newRemoveCmd(app *App) *cobra.Command {
	var (
		repo     string
		workflow string
		bag      string
		yes      bool
	)

	cmd := &cobra.Command{
		Use:     "rm [group]",
		Short:   "Remove a group, or one of its repos, workflows or bags",
		Aliases: []string{"remove"},
		Long: "Removes the whole group by default, with everything under it. Use a\n" +
			"flag to remove one member instead.\n\n" +
			"Env files already written are left alone: deleting configuration\n" +
			"should not reach into your repos.",
		Args: usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			g, _, err := app.splitGroup(args, 0)
			if err != nil {
				return err
			}

			switch {
			case repo != "":
				r, err := app.repoIn(g, repo)
				if err != nil {
					return err
				}
				if !confirmRemoval(app, yes, "repo "+g.Name+"/"+r.Name, "its variables and workflow membership") {
					return nil
				}
				if err := app.Store.DeleteRepo(r.ID); err != nil {
					return err
				}
				app.result(style.OK, r.Name, "removed")

			case workflow != "":
				wf, err := app.Store.WorkflowByName(g.ID, workflow)
				if err != nil {
					return fmt.Errorf("no such workflow %q in group %s", workflow, g.Name)
				}
				if !confirmRemoval(app, yes, "workflow "+g.Name+"/"+wf.Name, "its shared bags and variables") {
					return nil
				}
				if err := app.Store.DeleteWorkflow(wf.ID); err != nil {
					return err
				}
				app.result(style.OK, wf.Name, "removed")

			case bag != "":
				sg, err := findBag(app, g, bag)
				if err != nil {
					return err
				}
				if !confirmRemoval(app, yes, "shared bag "+bag, "its variables") {
					return nil
				}
				if err := app.Store.DeleteSharedGroup(sg.ID); err != nil {
					return err
				}
				app.result(style.OK, bag, "removed")

			default:
				repos, workflows, vars, err := app.Store.CountsFor(g.ID)
				if err != nil {
					return err
				}
				what := fmt.Sprintf("%s, %s and %s",
					plural(repos, "repo"), plural(workflows, "workflow"), plural(vars, "variable"))
				if !confirmRemoval(app, yes, "group "+g.Name, what) {
					return nil
				}
				if err := app.Store.DeleteGroup(g.ID); err != nil {
					return err
				}
				app.result(style.OK, g.Name, "removed")
			}

			app.hint("env files already written were left in place")
			return nil
		},
	}

	cmd.Flags().StringVar(&repo, "repo", "", "remove this repo instead of the group")
	cmd.Flags().StringVar(&workflow, "workflow", "", "remove this workflow instead of the group")
	cmd.Flags().StringVar(&bag, "bag", "", "remove this shared bag instead of the group")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation")
	return cmd
}

func newUnsetCmd(app *App) *cobra.Command {
	var (
		workflow string
		global   bool
	)

	cmd := &cobra.Command{
		Use:   "unset [group] [repo] KEY [KEY...]",
		Short: "Remove variables, mirroring `prizm var`",
		Args:  usageArgs(cobra.MinimumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if global {
				g, keys, err := app.splitGroupByLookup(args)
				if err != nil {
					return err
				}
				for _, key := range keys {
					if err := app.Store.DeleteGroupVar(g.ID, key); err != nil {
						return err
					}
				}
				app.result(style.OK, g.Name, fmt.Sprintf("%s removed (global)", plural(len(keys), "variable")))
				return nil
			}

			g, repo, keys, err := app.splitGroupRepoByLookup(args)
			if err != nil {
				return err
			}

			wf, err := workflowIn(app, g, workflow)
			if err != nil {
				return err
			}

			for _, key := range keys {
				if workflow == "" {
					err = app.Store.DeleteRepoVar(repo.ID, key)
				} else {
					err = app.Store.DeleteWorkflowRepoVar(wf.ID, repo.ID, key)
				}
				if err != nil {
					return err
				}
			}

			app.result(style.OK, repo.Name,
				fmt.Sprintf("%s removed%s", plural(len(keys), "variable"), scopeSuffix(workflow)))
			return nil
		},
	}

	cmd.Flags().StringVar(&workflow, "workflow", "", "remove from one workflow's layer")
	cmd.Flags().BoolVar(&global, "global", false, "remove from the group-global layer")
	return cmd
}

// confirmRemoval states what is about to go, then asks. Deletion is the one
// place where being slightly annoying is correct.
func confirmRemoval(app *App, yes bool, subject, contents string) bool {
	if yes {
		return true
	}

	app.say(style.Alert("This removes "+subject) + style.Detail(", along with "+contents+"."))

	ok, err := app.Confirm("Continue? [y/N] ")
	if err != nil || !ok {
		app.result(style.Warn, subject, "kept")
		return false
	}
	return true
}

// findBag locates a bag by name across a group's workflows.
func findBag(app *App, g store.Group, name string) (store.SharedGroup, error) {
	bags, err := app.Store.AllSharedGroups()
	if err != nil {
		return store.SharedGroup{}, err
	}

	var found []store.SharedGroupRef
	for _, b := range bags {
		if b.GroupName == g.Name && b.Name == name {
			found = append(found, b)
		}
	}

	switch len(found) {
	case 0:
		return store.SharedGroup{}, fmt.Errorf("no shared bag %q in group %s", name, g.Name)
	case 1:
		return found[0].SharedGroup, nil
	default:
		return store.SharedGroup{}, errUsage(
			"%q exists in several workflows in %s — remove the workflow instead, or delete its file",
			name, g.Name)
	}
}
