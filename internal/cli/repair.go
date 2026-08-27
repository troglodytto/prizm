package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/troglodytto/prizm/internal/style"
)

func newRepairCmd(app *App) *cobra.Command {
	var path string

	cmd := &cobra.Command{
		Use:   "repair [group] <repo>",
		Short: "Re-point a repo whose checkout moved",
		Long: "Repo paths are a stable contract, so nothing else changes one. This is\n" +
			"the escape hatch for when a checkout genuinely moves.\n\n" +
			"Defaults to the current directory, so the usual form is to cd into the\n" +
			"repo's new home and run `prizm repair <group> <repo>`.",
		Args: usageArgs(cobra.RangeArgs(1, 2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			g, repo, _, err := app.splitGroupRepo(args, 0)
			if err != nil {
				return err
			}

			abs, err := resolvePath(app, path)
			if err != nil {
				return err
			}
			if info, statErr := os.Stat(abs); statErr != nil || !info.IsDir() {
				return fmt.Errorf("%s is not an existing directory", abs)
			}

			old := repo.Path
			if err := app.Store.UpdateRepoPath(repo.ID, abs); err != nil {
				return err
			}

			app.result(style.OK, repo.Name, abs)
			app.detail("  was %s", old)
			app.hint("run `prizm %s <workflow>` to re-link it", g.Name)
			return nil
		},
	}

	cmd.Flags().StringVar(&path, "path", "", "new repo path (default: current directory)")
	return cmd
}
