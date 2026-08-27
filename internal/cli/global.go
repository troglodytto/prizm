package cli

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"

	"github.com/troglodytto/prizm/internal/config"
	"github.com/troglodytto/prizm/internal/sharedfile"
	"github.com/troglodytto/prizm/internal/store"
	"github.com/troglodytto/prizm/internal/style"
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
			"Run `prizm shared-sync` afterwards to load your edits.",
		Args: usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			g, _, err := app.splitGroup(args, 0)
			if err != nil {
				return err
			}

			path, err := ensureGlobalFile(app, g)
			if err != nil {
				return err
			}

			editor := os.Getenv("EDITOR")
			if editor == "" {
				editor = "vi"
			}

			ed := exec.Command(editor, path)
			ed.Stdin, ed.Stdout, ed.Stderr = os.Stdin, os.Stdout, os.Stderr
			if err := ed.Run(); err != nil {
				return fmt.Errorf("running %s: %w", editor, err)
			}

			app.hint("run `prizm shared-sync` to apply your edits")
			return nil
		},
	}
}

// ensureGlobalFile materialises a group's global file if it does not exist yet.
func ensureGlobalFile(app *App, g store.Group) (string, error) {
	path, err := config.GlobalPath(g.Name)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}

	if err := config.EnsureDir(filepathDir(path)); err != nil {
		return "", err
	}

	vars, err := app.Store.GroupVars(g.ID)
	if err != nil {
		return "", err
	}

	header := "# Shared across every workflow in " + g.Name + ".\n" +
		"# Values here are defaults: any workflow or repo can override one.\n\n"
	if err := os.WriteFile(path, []byte(header+sharedfile.Render(nil, vars)), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// syncGlobal reconciles a group's global file into the database.
func syncGlobal(app *App, g store.Group, yes bool) error {
	path, err := config.GlobalPath(g.Name)
	if err != nil {
		return err
	}

	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil // a group need not have any global variables
	}
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	_, incoming, _, err := sharedfile.Parse(string(raw))
	if err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}

	current, err := app.Store.GroupVars(g.ID)
	if err != nil {
		return err
	}

	diff := sharedfile.Compare(current, incoming)
	if diff.Empty() {
		app.result(style.Same, g.Name+" (global)", "up to date")
		return nil
	}

	app.sayf("%s %s", style.Heading(g.Name+" (global)"), style.Detail("← "+path))
	renderVarDiff(app, diff)

	if !yes {
		ok, err := app.Confirm("Apply? [y/N] ")
		if err != nil {
			return err
		}
		if !ok {
			app.result(style.Warn, g.Name+" (global)", "skipped")
			return nil
		}
	}

	if err := app.Store.ReplaceGroupVars(g.ID, incoming); err != nil {
		return err
	}
	app.result(style.OK, g.Name+" (global)", "synced")
	return nil
}

// filepathDir is filepath.Dir, kept local so this file needs one import fewer.
func filepathDir(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[:i]
		}
	}
	return "."
}

// rewriteGlobalFile re-materialises a group's global file after prizm changes
// one of its values, so the file and the database do not disagree.
func rewriteGlobalFile(app *App, groupID int64) error {
	groups, err := app.Store.ListGroups()
	if err != nil {
		return err
	}

	for _, g := range groups {
		if g.ID != groupID {
			continue
		}

		path, err := config.GlobalPath(g.Name)
		if err != nil {
			return err
		}
		if _, statErr := os.Stat(path); statErr != nil {
			return nil // no file to keep in step
		}

		vars, err := app.Store.GroupVars(g.ID)
		if err != nil {
			return err
		}

		header := "# Shared across every workflow in " + g.Name + ".\n" +
			"# Values here are defaults: any workflow or repo can override one.\n\n"
		return os.WriteFile(path, []byte(header+sharedfile.Render(nil, vars)), 0o600)
	}
	return nil
}
