// Package cli is prizm's command tree.
package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/troglodytto/prizm/internal/config"
	"github.com/troglodytto/prizm/internal/crypto"
	"github.com/troglodytto/prizm/internal/store"
	"github.com/troglodytto/prizm/internal/tui"
)

// App carries everything the commands need. The clock and cwd are injected so
// every command is testable without touching the real environment.
type App struct {
	Store   *store.Store
	Out     io.Writer
	Err     io.Writer
	Now     func() time.Time
	Cwd     func() (string, error)
	Confirm func(prompt string) (bool, error)

	// Interactive surfaces. Injectable so every path is testable without a
	// terminal; pickerInjected is the test-only escape hatch and must never
	// be settable from a flag or the environment.
	PickOne  func(heading, context string, options []tui.Option) (string, error)
	PickMany func(title string, options []tui.Option, preselected []string) ([]string, error)

	pickerInjected bool
}

// NewRootCmd builds the whole command tree.
func NewRootCmd(app *App) *cobra.Command {
	root := &cobra.Command{
		Use:   "prizm",
		Short: "Share env files across repos, grouped by workflow",
		Long: "prizm applies a named workflow's environment to every repo it covers,\n" +
			"building each repo's env file from shared and per-repo variables.",
		Version: Version(),
		// Errors print themselves; Run decides whether help follows.
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.ArbitraryArgs,
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return app.completeRoot(cmd, toComplete)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return errUsage("unknown command or group %q", args[0])
			}
			// Bare `prizm` is help. The picker is `prizm pick`, because a
			// tool's front door should tell you what it does, not take over
			// the terminal before you have asked for anything.
			return cmd.Help()
		},
	}

	root.SetOut(app.Out)
	root.SetErr(app.Err)
	registerHelpStyling(root)

	var noTUI bool
	root.PersistentFlags().BoolVar(&noTUI, "no-tui", false, "never show interactive prompts")
	root.PersistentPreRun = func(*cobra.Command, []string) {
		if noTUI {
			tui.Disable()
		}
	}

	// A wrong flag is a usage problem, so it should show help too.
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return usageError{err: err}
	})

	root.AddCommand(
		newInitCmd(app),
		newAddRepoCmd(app),
		newAddWorkflowCmd(app),
		newEditWorkflowCmd(app),
		newLsCmd(app),
		newPickCmd(app),
		newVarCmd(app),
		newImportCmd(app),
		newUpCmd(app),
		newStatusCmd(app),
		newRepairCmd(app),
		newRenameCmd(app),
		newRemoveCmd(app),
		newUnsetCmd(app),
		newGlobalCmd(app),
		newSharedAddCmd(app),
		newSharedEditCmd(app),
		newSharedLsCmd(app),
		newSharedSyncCmd(app),
	)
	return root
}

// Execute wires real dependencies and returns a process exit code.
func Execute() int {
	key, err := crypto.LoadOrCreateKey()
	if err != nil {
		return fail(err)
	}

	cipher, err := crypto.NewAESGCM(key)
	if err != nil {
		return fail(err)
	}

	dbPath, err := config.DBPath()
	if err != nil {
		return fail(err)
	}
	if err := config.EnsureDir(filepath.Dir(dbPath)); err != nil {
		return fail(err)
	}

	s, err := store.Open(dbPath, cipher)
	if err != nil {
		return fail(err)
	}
	defer s.Close()

	app := &App{Store: s, Out: os.Stdout, Err: os.Stderr, Now: time.Now, Cwd: os.Getwd}
	app.Confirm = confirmOnStdin(app.Out)
	app.PickOne = tui.PickOne
	app.PickMany = tui.PickMany

	root := NewRootCmd(app)
	root.SetArgs(rewriteArgs(app, root, os.Args[1:]))

	if err := Run(root); err != nil {
		var shown errShown
		if !errors.As(err, &shown) {
			failLine(os.Stderr, err)
		}
		return 1
	}
	return 0
}

func fail(err error) int {
	failLine(os.Stderr, err)
	return 1
}

// rewriteArgs applies the group-first sugar using the live command tree and DB.
func rewriteArgs(app *App, root *cobra.Command, args []string) []string {
	return Rewrite(args, Resolver{
		IsCommand: func(name string) bool {
			if name == completePrefix || name == "help" || name == "completion" {
				return true
			}
			for _, c := range root.Commands() {
				if c.Name() == name || c.HasAlias(name) {
					return true
				}
			}
			return false
		},
		IsGroup: func(name string) bool {
			_, err := app.Store.GroupByName(name)
			return err == nil
		},
		InferGroup: func() (string, bool) {
			cwd, err := app.Cwd()
			if err != nil {
				return "", false
			}
			_, g, err := app.Store.RepoForPath(cwd)
			if err != nil {
				return "", false
			}
			return g.Name, true
		},
		IsWorkflow: func(group, name string) bool {
			g, err := app.Store.GroupByName(group)
			if err != nil {
				return false
			}
			_, err = app.Store.WorkflowByName(g.ID, name)
			return err == nil
		},
	})
}

// confirmOnStdin is the real prompt. Anything other than y/yes declines.
func confirmOnStdin(out io.Writer) func(string) (bool, error) {
	return func(prompt string) (bool, error) {
		promptLine(out, prompt)

		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return false, nil
		}

		answer := strings.ToLower(strings.TrimSpace(line))
		return answer == "y" || answer == "yes", nil
	}
}

// mustGroup resolves a group name or returns a user-facing error.
func (a *App) mustGroup(name string) (store.Group, error) {
	g, err := a.Store.GroupByName(name)
	if err != nil {
		return store.Group{}, fmt.Errorf("no such group %q — run `prizm init %s` first", name, name)
	}
	return g, nil
}
