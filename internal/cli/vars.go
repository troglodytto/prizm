package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/troglodytto/prizm/internal/envfile"
	"github.com/troglodytto/prizm/internal/store"
	"github.com/troglodytto/prizm/internal/style"
)

func newVarCmd(app *App) *cobra.Command {
	var (
		workflow string
		global   bool
	)

	cmd := &cobra.Command{
		Use:               "var [group] [repo] KEY=VALUE [KEY=VALUE...]",
		ValidArgsFunction: positions(app, compGroup, compRepo, compNone),
		Short:             "Set variables on a repo",
		Long: "Without --workflow the variables apply in every workflow that touches\n" +
			"this repo. With --workflow they apply only there, and win over both the\n" +
			"repo-shared layer and any shared bag.\n\n" +
			"Values are stored verbatim: ${OTHER_VAR} references are expanded at `up`\n" +
			"time, not here. Keys starting with _PRIZM_ are internal — usable in\n" +
			"templates, never written to the repo's env file.\n\n" +
			"--global writes to the group-global layer instead: true for every repo\n" +
			"in every workflow, and the lowest-precedence layer there is.",
		Args: usageArgs(cobra.MinimumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if global {
				if workflow != "" {
					return errUsage("--global and --workflow name different layers — pick one")
				}
				return setGlobalVars(app, args)
			}

			g, repo, assignments, err := app.splitGroupRepo(args, countAssignments(args))
			if err != nil {
				return err
			}

			wf, err := workflowIn(app, g, workflow)
			if err != nil {
				return err
			}

			for _, assignment := range assignments {
				key, value, err := parseAssignment(assignment)
				if err != nil {
					return err
				}
				if err := writeVar(app, wf, repo, key, value, workflow != ""); err != nil {
					return err
				}
			}

			app.snapshot(varScope(wf, repo, workflow != ""), store.SourceVar, varNote(assignments))
			app.result(style.OK, repo.Name, fmt.Sprintf("%d variable(s) set%s", len(assignments), scopeSuffix(workflow)))
			return nil
		},
	}

	cmd.Flags().StringVar(&workflow, "workflow", "", "scope the variables to one workflow")
	cmd.Flags().BoolVar(&global, "global", false, "write to the group-global layer instead")
	return cmd
}

// setGlobalVars writes to layer 0 — the values true for every repo in every
// workflow. It mirrors `unset --global`, which already existed; having the
// removal but not the write made the layer feel like something only a file
// could produce.
func setGlobalVars(app *App, args []string) error {
	g, assignments, err := app.splitGroup(args, countAssignments(args))
	if err != nil {
		return err
	}
	if len(assignments) == 0 {
		return errUsage("nothing to set — pass at least one KEY=VALUE")
	}

	for _, assignment := range assignments {
		key, value, err := parseAssignment(assignment)
		if err != nil {
			return err
		}
		if err := app.Store.SetGroupVar(g.ID, key, value); err != nil {
			return err
		}
	}

	app.snapshot(store.GroupScope(g.ID), store.SourceVar, varNote(assignments))
	app.result(style.OK, g.Name, fmt.Sprintf("%d variable(s) set (global)", len(assignments)))
	app.hint("a group-global value is overridden by any bag or repo layer that names the same key")
	return nil
}

func newImportCmd(app *App) *cobra.Command {
	var workflow string

	cmd := &cobra.Command{
		Use:               "import [group] [repo] <file>",
		ValidArgsFunction: positions(app, compGroup, compRepo, compFiles),
		Short:             "Load an existing .env file into prizm",
		Long: "Most people already have .env.local files sitting in each repo; this is\n" +
			"how prizm gets populated the first time.",
		Args: usageArgs(cobra.RangeArgs(1, 3)),
		RunE: func(cmd *cobra.Command, args []string) error {
			g, repo, rest, err := app.splitGroupRepo(args, 1)
			if err != nil {
				return err
			}

			wf, err := workflowIn(app, g, workflow)
			if err != nil {
				return err
			}

			path := rest[0]
			raw, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("reading %s: %w", path, err)
			}
			vars, err := envfile.Parse(string(raw))
			if err != nil {
				return fmt.Errorf("parsing %s: %w", path, err)
			}

			for key, value := range vars {
				if err := writeVar(app, wf, repo, key, value, workflow != ""); err != nil {
					return err
				}
			}

			app.snapshot(varScope(wf, repo, workflow != ""), store.SourceImport, "import "+filepath.Base(path))
			app.result(style.OK, repo.Name, fmt.Sprintf("%d variable(s) imported%s", len(vars), scopeSuffix(workflow)))
			return nil
		},
	}

	cmd.Flags().StringVar(&workflow, "workflow", "", "scope the imported variables to one workflow")
	return cmd
}

// varNote summarises an edit for the history list: the keys touched, since
// values are the thing you are trying to recover and a preview of them here
// would put secrets in a listing that decrypts nothing else.
func varNote(assignments []string) string {
	keys := make([]string, 0, len(assignments))
	for _, a := range assignments {
		key, _, _ := strings.Cut(a, "=")
		keys = append(keys, key)
	}
	return summariseKeys(keys)
}

// unsetNote mirrors varNote for removals.
func unsetNote(keys []string) string {
	return "unset " + summariseKeys(keys)
}

// summariseKeys keeps a note to a readable width.
func summariseKeys(keys []string) string {
	if len(keys) > 3 {
		return fmt.Sprintf("%s +%d more", strings.Join(keys[:3], ", "), len(keys)-3)
	}
	return strings.Join(keys, ", ")
}

// writeVar puts a variable in the repo-shared layer, or the repo+workflow
// layer when a workflow was named.
func writeVar(app *App, wf store.Workflow, repo store.Repo, key, value string, scoped bool) error {
	if scoped {
		return app.Store.SetWorkflowRepoVar(wf.ID, repo.ID, key, value)
	}
	return app.Store.SetRepoVar(repo.ID, key, value)
}

// workflowIn resolves an optional workflow name within a group.
func workflowIn(app *App, g store.Group, name string) (store.Workflow, error) {
	if name == "" {
		return store.Workflow{}, nil
	}

	wf, err := app.Store.WorkflowByName(g.ID, name)
	if err != nil {
		return store.Workflow{}, fmt.Errorf("no such workflow %q in group %s", name, g.Name)
	}
	return wf, nil
}

// countAssignments returns how many trailing arguments look like KEY=VALUE.
// Everything before them is the optional group and repo, which is what lets
// `prizm var PORT=8080` work from inside a repo.
func countAssignments(args []string) int {
	n := 0
	for i := len(args) - 1; i >= 0; i-- {
		if !strings.Contains(args[i], "=") {
			break
		}
		n++
	}
	if n == 0 {
		return 1 // let parseAssignment produce the error message
	}
	return n
}

// parseAssignment splits KEY=VALUE. Only the first '=' separates, so values
// may contain any number of them.
func parseAssignment(arg string) (string, string, error) {
	key, value, found := strings.Cut(arg, "=")
	if !found {
		return "", "", fmt.Errorf("%q is not a KEY=VALUE assignment", arg)
	}

	key = strings.TrimSpace(key)
	if key == "" {
		return "", "", fmt.Errorf("%q has an empty key", arg)
	}
	return key, value, nil
}

func scopeSuffix(workflow string) string {
	if workflow == "" {
		return " (all workflows)"
	}
	return " (" + workflow + ")"
}
