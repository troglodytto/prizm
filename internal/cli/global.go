package cli

import (
	"github.com/spf13/cobra"

	"github.com/troglodytto/prizm/internal/store"
)

func newGlobalCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:               "global [group]",
		ValidArgsFunction: positions(app, compGroup),
		Short:             "Edit the variables shared across every workflow in a group",
		Long: "Group-global variables are facts about the whole group — a shared\n" +
			"database cluster's username, an AWS account — true in every workflow\n" +
			"and every repo.\n\n" +
			"They are the lowest layer, so they are defaults rather than bindings:\n" +
			"when a value stops being universal, any layer above simply overrides\n" +
			"it and nothing has to be unwired first.\n\n" +
			"What you save is applied immediately. This is the same layer as\n" +
			"`prizm edit --global`.",
		Args: usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			g, _, err := app.splitGroup(args, 0)
			if err != nil {
				return err
			}
			return editScope(app, store.GroupScope(g.ID), g.Name+" (global)")
		},
	}
}
