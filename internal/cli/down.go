package cli

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/troglodytto/prizm/internal/compose"
	"github.com/troglodytto/prizm/internal/tui"
)

func newDownCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:               "down [group] <workflow>",
		ValidArgsFunction: positions(app, compGroupOrWorkflow, compWorkflow),
		Short:             "Stop the services a workflow brought up",
		Long: "The inverse of `up`, for containers only. Env files are left exactly\n" +
			"as they are: they describe how a repo is configured, not whether\n" +
			"anything is running, and clearing them would leave the repo in a\n" +
			"state no workflow describes.\n\n" +
			"Switching workflows does not stop the previous one's services either.\n" +
			"Stopping something you did not ask to stop is the more expensive\n" +
			"mistake, so teardown stays explicit.",
		Args: usageArgs(cobra.RangeArgs(1, 2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			g, wf, err := app.groupWorkflow(args)
			if err != nil {
				return err
			}
			return takeDown(app, g, wf)
		},
	}
}

// withSpinner runs slow work with feedback, bounded by the compose timeout.
//
// Everything it wraps shells out to docker, where the wait is long enough
// that silence reads as a hang.
func (a *App) withSpinner(label string, work func(context.Context) (string, error)) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), compose.Timeout)
	defer cancel()

	return tui.Spin(ctx, label, work)
}
