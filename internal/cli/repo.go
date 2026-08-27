package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/troglodytto/prizm/internal/store"
	"github.com/troglodytto/prizm/internal/style"
)

func newAddRepoCmd(app *App) *cobra.Command {
	var (
		path    string
		name    string
		envFile string
	)

	cmd := &cobra.Command{
		Use:   "add-repo <group> [name|path]",
		Short: "Register a repo in a group",
		Long: "The second argument is a path when it looks like one (`.`, `./x`, `../x`,\n" +
			"`~/x`, or anything containing a slash) and a name otherwise.\n\n" +
			"  prizm add-repo acme               # this directory, name from it\n" +
			"  prizm add-repo acme .             # the same thing, explicitly\n" +
			"  prizm add-repo acme ~/code/auth   # that directory, name 'auth'\n" +
			"  prizm add-repo acme auth          # this directory, named 'auth'\n\n" +
			"An inferred name comes from the directory, or from the git remote when\n" +
			"the directory name is not a usable identifier. Names must be unique\n" +
			"within a group, but two groups may each have their own 'auth'.",
		Args: usageArgs(cobra.RangeArgs(1, 2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			g, err := app.mustGroup(args[0])
			if err != nil {
				return err
			}

			if len(args) == 2 {
				if looksLikePath(args[1]) {
					if path != "" {
						return errUsage("path given twice: %q and --path %q", args[1], path)
					}
					path = args[1]
				} else {
					if name != "" {
						return errUsage("name given twice: %q and --name %q", args[1], name)
					}
					name = args[1]
				}
			}

			abs, err := resolvePath(app, path)
			if err != nil {
				return err
			}
			if info, statErr := os.Stat(abs); statErr != nil || !info.IsDir() {
				return fmt.Errorf("%s is not an existing directory", abs)
			}

			if name == "" {
				name, err = inferRepoName(abs)
				if err != nil {
					return err
				}
			}

			repo, err := app.Store.AddRepo(g.ID, name, abs, envFile)
			if err != nil {
				return describeAddRepoError(g, name, err)
			}

			app.result(style.OK, repo.Name, fmt.Sprintf("%s (%s)", repo.Path, repo.EnvFile))
			return nil
		},
	}

	cmd.Flags().StringVar(&path, "path", "", "repo path (default: current directory)")
	cmd.Flags().StringVar(&name, "name", "", "repo name (default: inferred from the directory)")
	cmd.Flags().StringVar(&envFile, "env-file", "", "env file name to link inside the repo (default: .env)")
	return cmd
}

// looksLikePath reports whether an argument is meant as a directory rather
// than a repo name. Repo names are identifiers and can never contain a slash,
// so the two are unambiguous.
func looksLikePath(arg string) bool {
	switch arg {
	case ".", "..":
		return true
	}
	return strings.ContainsRune(arg, '/') || strings.HasPrefix(arg, "~")
}

// inferRepoName derives a repo name from its directory: the directory's own
// name normally, falling back to the git remote when that is not a usable
// identifier — a checkout in "my project" still has a sane remote.
func inferRepoName(abs string) (string, error) {
	if base := filepath.Base(abs); store.ValidName(base) {
		return base, nil
	}

	if remote, ok := gitRemoteName(abs); ok {
		return remote, nil
	}

	return "", errUsage(
		"cannot infer a name from %s — pass one, for example `prizm add-repo <group> <name> --path %s`",
		abs, abs)
}

// gitRemoteName returns the repository name from origin's URL, if there is one.
func gitRemoteName(dir string) (string, bool) {
	out, err := exec.Command("git", "-C", dir, "remote", "get-url", "origin").Output()
	if err != nil {
		return "", false
	}

	url := strings.TrimSpace(string(out))
	url = strings.TrimSuffix(url, ".git")
	url = strings.TrimSuffix(url, "/")

	// Handles both git@host:owner/repo and https://host/owner/repo.
	if i := strings.LastIndexAny(url, "/:"); i >= 0 {
		url = url[i+1:]
	}

	if !store.ValidName(url) {
		return "", false
	}
	return url, true
}

// describeAddRepoError names the group on a duplicate, since the same repo
// name in a different group is perfectly legal.
func describeAddRepoError(g store.Group, name string, err error) error {
	if errors.Is(err, store.ErrExists) {
		return fmt.Errorf("group %s already has a repo named %q (other groups may have their own)", g.Name, name)
	}
	return err
}

// resolvePath turns a possibly-relative path into an absolute one. Repo paths
// are a stable contract, so they are always stored absolute.
func resolvePath(app *App, path string) (string, error) {
	cwd, err := app.Cwd()
	if err != nil {
		return "", fmt.Errorf("determining current directory: %w", err)
	}

	switch {
	case path == "":
		return cwd, nil
	case path == "~" || strings.HasPrefix(path, "~/"):
		// Shells expand this before we see it; a quoted argument does not.
		home, hErr := os.UserHomeDir()
		if hErr != nil {
			return "", fmt.Errorf("expanding %q: %w", path, hErr)
		}
		return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(path, "~"), "/")), nil
	case filepath.IsAbs(path):
		return filepath.Clean(path), nil
	default:
		return filepath.Join(cwd, path), nil
	}
}
