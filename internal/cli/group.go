package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/troglodytto/prizm/internal/style"
)

func newInitCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "init <group>",
		Short: "Create a new group",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			g, err := app.Store.CreateGroup(args[0])
			if err != nil {
				return err
			}
			fmt.Fprintln(app.Out, style.Row(style.OK, g.Name, "group created"))
			return nil
		},
	}
}

func newLsCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:     "ls [group]",
		Short:   "List groups, or one group's repos and workflows",
		Aliases: []string{"list"},
		Args:    usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return listGroups(app)
			}
			return listGroup(app, args[0])
		},
	}
}

func listGroups(app *App) error {
	groups, err := app.Store.ListGroups()
	if err != nil {
		return err
	}
	if len(groups) == 0 {
		fmt.Fprintln(app.Out, style.Hint("no groups yet — run `prizm init <name>`"))
		return nil
	}

	for _, g := range groups {
		fmt.Fprintln(app.Out, g.Name)
	}
	return nil
}

func listGroup(app *App, name string) error {
	g, err := app.mustGroup(name)
	if err != nil {
		return err
	}

	repos, err := app.Store.ListRepos(g.ID)
	if err != nil {
		return err
	}
	workflows, err := app.Store.ListWorkflows(g.ID)
	if err != nil {
		return err
	}

	names := make([]string, 0, len(repos)+len(workflows))
	for _, r := range repos {
		names = append(names, r.Name)
	}
	for _, w := range workflows {
		names = append(names, w.Name)
	}
	col := style.WidthOf(names)

	fmt.Fprintln(app.Out, style.Heading(g.Name))

	fmt.Fprintln(app.Out, "  repos:")
	for _, r := range repos {
		fmt.Fprintln(app.Out, col.Field(r.Name, r.Path))
	}

	fmt.Fprintln(app.Out, "  workflows:")
	for _, w := range workflows {
		members, err := app.Store.WorkflowRepos(w.ID)
		if err != nil {
			return err
		}
		memberNames := make([]string, 0, len(members))
		for _, m := range members {
			memberNames = append(memberNames, m.Name)
		}

		tag := ""
		if w.Tag != "" {
			tag = "  " + style.Tag(w.Tag)
		}
		fmt.Fprintln(app.Out, col.Field(w.Name, joinOrNone(memberNames))+tag)
	}
	return nil
}

func joinOrNone(names []string) string {
	if len(names) == 0 {
		return "(no repos)"
	}

	out := names[0]
	for _, n := range names[1:] {
		out += " " + n
	}
	return out
}
