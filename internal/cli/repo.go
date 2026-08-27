package cli

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/troglodytto/prizm/internal/style"
)

func newAddRepoCmd(app *App) *cobra.Command {
	var (
		path    string
		envFile string
	)

	cmd := &cobra.Command{
		Use:   "add-repo <group> <repo>",
		Short: "Register a repo in a group (defaults to the current directory)",
		Args:  usageArgs(cobra.ExactArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			g, err := app.mustGroup(args[0])
			if err != nil {
				return err
			}

			abs, err := resolvePath(app, path)
			if err != nil {
				return err
			}

			repo, err := app.Store.AddRepo(g.ID, args[1], abs, envFile)
			if err != nil {
				return err
			}

			fmt.Fprintln(app.Out, style.Row(style.OK, repo.Name,
				fmt.Sprintf("%s (%s)", repo.Path, repo.EnvFile)))
			return nil
		},
	}

	cmd.Flags().StringVar(&path, "path", "", "repo path (default: current directory)")
	cmd.Flags().StringVar(&envFile, "env-file", "", "env file name to link inside the repo (default: .env)")
	return cmd
}

// resolvePath turns a possibly-relative path into an absolute one. Repo paths
// are a stable contract, so they are always stored absolute.
func resolvePath(app *App, path string) (string, error) {
	if path == "" {
		cwd, err := app.Cwd()
		if err != nil {
			return "", fmt.Errorf("determining current directory: %w", err)
		}
		return cwd, nil
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}

	cwd, err := app.Cwd()
	if err != nil {
		return "", fmt.Errorf("determining current directory: %w", err)
	}
	return filepath.Join(cwd, path), nil
}
