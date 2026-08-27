package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/troglodytto/prizm/internal/store"
	"github.com/troglodytto/prizm/internal/style"
)

func newAddWorkflowCmd(app *App) *cobra.Command {
	var (
		repoList string
		tag      string
	)

	cmd := &cobra.Command{
		Use:               "add-workflow <group> <workflow>",
		ValidArgsFunction: positions(app, compGroup, compNone),
		Short:             "Define a workflow: a named bundle of repos",
		Long: "Without --repos the workflow covers every repo currently in the group.\n" +
			"Pass --repos to cover an explicit subset, e.g. a frontend-only workflow.",
		Args: usageArgs(cobra.ExactArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			g, err := app.mustGroup(args[0])
			if err != nil {
				return err
			}

			repoIDs, err := chooseRepos(app, g, repoList,
				fmt.Sprintf("Repos covered by %s/%s", g.Name, args[1]))
			if err != nil {
				return quietUserCancel(app, err)
			}

			wf, err := app.Store.AddWorkflow(g.ID, args[1], tag, repoIDs)
			if err != nil {
				return err
			}

			app.result(style.OK, wf.Name, fmt.Sprintf("workflow covering %d repo(s)", len(repoIDs)))
			return nil
		},
	}

	cmd.Flags().StringVar(&repoList, "repos", "", "comma-separated repo names (default: every repo in the group)")
	cmd.Flags().StringVar(&tag, "tag", "", "guardrail tag, e.g. prod/qa/local")
	return cmd
}

// resolveRepoIDs turns a comma-separated repo list into IDs, defaulting to
// every repo in the group when the list is empty.
func resolveRepoIDs(app *App, g store.Group, list string) ([]int64, error) {
	if strings.TrimSpace(list) == "" {
		repos, err := app.Store.ListRepos(g.ID)
		if err != nil {
			return nil, err
		}
		ids := make([]int64, 0, len(repos))
		for _, r := range repos {
			ids = append(ids, r.ID)
		}
		return ids, nil
	}

	var ids []int64
	for _, name := range strings.Split(list, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}

		repo, err := app.Store.RepoByName(g.ID, name)
		if err != nil {
			return nil, fmt.Errorf("no such repo %q in group %s", name, g.Name)
		}
		ids = append(ids, repo.ID)
	}
	return ids, nil
}
