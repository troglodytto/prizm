package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/troglodytto/prizm/internal/store"
	"github.com/troglodytto/prizm/internal/style"
)

func newEditWorkflowCmd(app *App) *cobra.Command {
	var (
		name     string
		tag      string
		repoList string
	)

	cmd := &cobra.Command{
		Use:   "edit-workflow [group] <workflow>",
		Short: "Change a workflow's name, tag or repos",
		Long: "With no flags and a terminal, opens the repo checkbox list with the\n" +
			"current members ticked.\n\n" +
			"  prizm edit-workflow acme local                    pick repos\n" +
			"  prizm edit-workflow acme local --tag qa           retag it\n" +
			"  prizm edit-workflow acme local --repos auth,ai    set members\n" +
			"  prizm edit-workflow acme local --tag ''           clear the tag\n\n" +
			"Dropping a repo keeps its variables: that is a change of scope, not a\n" +
			"decision to discard configuration. Use `prizm unset` to remove values.",
		Args: usageArgs(cobra.RangeArgs(1, 2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			g, rest, err := app.splitGroup(args, 1)
			if err != nil {
				return err
			}

			wf, err := app.Store.WorkflowByName(g.ID, rest[0])
			if err != nil {
				return fmt.Errorf("no such workflow %q in group %s", rest[0], g.Name)
			}

			touchedTag := cmd.Flags().Changed("tag")
			touchedRepos := cmd.Flags().Changed("repos")
			var changes []string

			if name != "" {
				if err := app.Store.RenameWorkflow(wf.ID, name); err != nil {
					return err
				}
				changes = append(changes, "renamed from "+wf.Name)
				wf.Name = name
			}

			if touchedTag {
				if err := app.Store.SetWorkflowTag(wf.ID, tag); err != nil {
					return err
				}
				if tag == "" {
					changes = append(changes, "tag cleared")
				} else {
					changes = append(changes, "tag "+style.Tag(tag))
				}
			}

			// No flags at all means the repo set is what you came to change.
			if touchedRepos || (name == "" && !touchedTag) {
				ids, names, err := chooseWorkflowRepos(app, g, wf, repoList)
				if err != nil {
					return quietUserCancel(app, err)
				}
				if err := app.Store.ReplaceWorkflowRepos(wf.ID, ids); err != nil {
					return err
				}
				changes = append(changes, plural(len(ids), "repo")+": "+joinOrNone(names))
			}

			app.result(style.OK, wf.Name, strings.Join(changes, " · "))
			app.hint("run `prizm %s %s` to apply the change", g.Name, wf.Name)
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "rename the workflow")
	cmd.Flags().StringVar(&tag, "tag", "", "set the guardrail tag; pass '' to clear it")
	cmd.Flags().StringVar(&repoList, "repos", "", "comma-separated repos the workflow covers")
	return cmd
}

// chooseWorkflowRepos resolves the new member set: an explicit --repos, or a
// checkbox list preselected with whoever is in it now.
func chooseWorkflowRepos(app *App, g store.Group, wf store.Workflow, list string) ([]int64, []string, error) {
	repos, err := app.Store.ListRepos(g.ID)
	if err != nil {
		return nil, nil, err
	}

	byName := make(map[string]int64, len(repos))
	for _, r := range repos {
		byName[r.Name] = r.ID
	}

	if list != "" {
		ids, err := resolveRepoIDs(app, g, list)
		if err != nil {
			return nil, nil, err
		}
		return ids, namesOf(repos, ids), nil
	}

	current, err := app.Store.WorkflowRepos(wf.ID)
	if err != nil {
		return nil, nil, err
	}
	currentNames := make([]string, 0, len(current))
	for _, r := range current {
		currentNames = append(currentNames, r.Name)
	}

	if !app.canPickMany() {
		return nil, nil, errUsage(
			"no terminal, so prizm cannot ask which repos %s should cover — pass --repos %s",
			wf.Name, strings.Join(currentNames, ","))
	}

	chosen, err := app.PickMany(
		fmt.Sprintf("Repos covered by %s/%s", g.Name, wf.Name),
		repoOptions(repos), currentNames)
	if err != nil {
		return nil, nil, cancelled(err)
	}

	ids := make([]int64, 0, len(chosen))
	for _, n := range chosen {
		ids = append(ids, byName[n])
	}
	return ids, chosen, nil
}

func namesOf(repos []store.Repo, ids []int64) []string {
	want := make(map[int64]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}

	var out []string
	for _, r := range repos {
		if want[r.ID] {
			out = append(out, r.Name)
		}
	}
	return out
}
