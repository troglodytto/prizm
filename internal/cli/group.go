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
		Args:  cobra.ExactArgs(1),
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
		Args:    cobra.MaximumNArgs(1),
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

	fmt.Fprintln(app.Out, style.Heading(g.Name))

	fmt.Fprintln(app.Out, "  repos:")
	for _, r := range repos {
		fmt.Fprintf(app.Out, "    %-*s %s\n", style.NameWidth, r.Name, style.Detail(r.Path))
	}

	fmt.Fprintln(app.Out, "  workflows:")
	for _, w := range workflows {
		members, err := app.Store.WorkflowRepos(w.ID)
		if err != nil {
			return err
		}
		names := make([]string, 0, len(members))
		for _, m := range members {
			names = append(names, m.Name)
		}

		tag := ""
		if w.Tag != "" {
			tag = "  " + style.Tag(w.Tag)
		}
		fmt.Fprintf(app.Out, "    %-*s %s%s\n", style.NameWidth, w.Name, style.Detail(joinOrNone(names)), tag)
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
