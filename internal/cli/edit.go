package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/troglodytto/prizm/internal/envfile"
	"github.com/troglodytto/prizm/internal/store"
	"github.com/troglodytto/prizm/internal/style"
)

func newEditCmd(app *App) *cobra.Command {
	var (
		workflow string
		global   bool
		bag      string
	)

	cmd := &cobra.Command{
		Use:               "edit [group] [repo]",
		ValidArgsFunction: positions(app, compGroup, compRepo),
		Short:             "Open a layer in $EDITOR and save what you write back",
		Long: "Setting a dozen variables one `prizm var` at a time is tedious, and\n" +
			"reading a layer back to check it is worse. This opens the layer as an\n" +
			"env file, and whatever you save replaces it.\n\n" +
			"The same scope flags as `prizm audit`: --workflow for one workflow's\n" +
			"layer, --bag for a shared bag, --global for the group layer.\n\n" +
			"Leaving the file unchanged, or emptying it entirely, cancels — an\n" +
			"editor that failed to open should not wipe a layer.",
		Args: usageArgs(cobra.MaximumNArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			scope, label, err := auditScope(app, args, workflow, bag, global)
			if err != nil {
				return err
			}
			return editScope(app, scope, label)
		},
	}

	cmd.Flags().StringVar(&workflow, "workflow", "", "edit one workflow's layer for the repo")
	cmd.Flags().StringVar(&bag, "bag", "", "edit a shared bag (needs --workflow)")
	cmd.Flags().BoolVar(&global, "global", false, "edit the group-global layer")
	return cmd
}

func editScope(app *App, scope store.Scope, label string) error {
	before, err := app.scopeVars(scope)
	if err != nil {
		return err
	}

	edited, err := app.editVars(label, before)
	if err != nil {
		return err
	}

	switch {
	case edited == nil:
		app.result(style.Same, label, "unchanged")
		return nil
	case len(edited) == 0 && len(before) > 0:
		// An empty buffer is far more often a failed editor or a mistaken
		// :q!-then-save than a deliberate request to delete every variable.
		app.result(style.Warn, label, "empty file — nothing removed")
		app.hint("to clear a layer, use `prizm unset` on the keys you mean")
		return nil
	}

	if err := app.replaceScopeVars(scope, edited); err != nil {
		return err
	}

	for _, change := range diffVars(edited, before) {
		app.detail("%s %s", string(change.Mark), change.Key)
	}

	app.snapshot(scope, store.SourceEdit, "edit")
	app.result(style.OK, label, fmt.Sprintf("%s now set", plural(len(edited), "variable")))
	app.hint("run `prizm up` to write the change into the repos")
	return nil
}

// editVars round-trips a layer through $EDITOR. A nil result means the user
// changed nothing, which is distinct from an empty one.
func (a *App) editVars(label string, vars map[string]string) (map[string]string, error) {
	if a.EditFile == nil {
		return nil, errUsage("editing needs a terminal and $EDITOR")
	}

	original := editorBuffer(label, vars)

	dir, err := os.MkdirTemp("", "prizm-edit-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	// The .env extension is what gives the editor its syntax highlighting.
	path := filepath.Join(dir, "prizm.env")
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		return nil, err
	}

	if err := a.EditFile(path); err != nil {
		return nil, err
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if string(raw) == original {
		return nil, nil
	}

	edited, err := envfile.Parse(string(raw))
	if err != nil {
		return nil, fmt.Errorf("your edit did not parse: %w", err)
	}
	if edited == nil {
		edited = map[string]string{}
	}
	return edited, nil
}

// editorBuffer renders the layer with a header explaining the rules, since
// the editor is the one place the user sees a layer whole.
func editorBuffer(label string, vars map[string]string) string {
	var b strings.Builder
	b.WriteString("# " + label + "\n")
	b.WriteString("#\n")
	b.WriteString("# Save to replace this layer with exactly what is below.\n")
	b.WriteString("# Delete a line to remove that variable. Quit without saving to cancel.\n")
	b.WriteString("#\n")
	b.WriteString("# ${OTHER} references are expanded at `up` time, not here.\n")
	b.WriteString("# _PRIZM_ keys are internal: usable in templates, never written to a repo.\n")
	b.WriteString("\n")

	if len(vars) == 0 {
		b.WriteString("# this layer is empty\n")
		return b.String()
	}
	b.WriteString(envfile.Render(vars))
	return b.String()
}

// launchEditor runs $EDITOR against a path, wired to the real terminal so a
// full-screen editor behaves normally.
func launchEditor(path string) error {
	name := firstNonEmpty(os.Getenv("VISUAL"), os.Getenv("EDITOR"), "vi")

	// $EDITOR is conventionally a command line, not a bare binary — "code -w"
	// and "emacsclient -nw" are both common.
	fields := strings.Fields(name)
	cmd := exec.Command(fields[0], append(fields[1:], path)...) //nolint:gosec // the user's own $EDITOR
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stderr, os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("running %s: %w", name, err)
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
