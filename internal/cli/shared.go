package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/troglodytto/prizm/internal/config"
	"github.com/troglodytto/prizm/internal/sharedfile"
	"github.com/troglodytto/prizm/internal/store"
	"github.com/troglodytto/prizm/internal/style"
)

func newSharedAddCmd(app *App) *cobra.Command {
	var (
		repoList string
		file     string
	)

	cmd := &cobra.Command{
		Use:               "shared-add [group] <workflow> <name>",
		ValidArgsFunction: positions(app, compGroup, compWorkflow, compNone),
		Short:             "Create a file-backed shared variable bag",
		Long: "The bag is backed by a real .env file you edit directly. Run\n" +
			"`prizm shared-sync` afterwards to reconcile your edits into prizm.",
		Args: usageArgs(cobra.RangeArgs(2, 3)),
		RunE: func(cmd *cobra.Command, args []string) error {
			g, rest, err := app.splitGroup(args, 2)
			if err != nil {
				return err
			}

			wf, err := app.Store.WorkflowByName(g.ID, rest[0])
			if err != nil {
				return fmt.Errorf("no such workflow %q in group %s", rest[0], g.Name)
			}
			name := rest[1]

			repoIDs, err := chooseRepos(app, g, repoList,
				fmt.Sprintf("Repos in shared bag %s/%s/%s", g.Name, wf.Name, name))
			if err != nil {
				return quietUserCancel(app, err)
			}

			path, err := bagPath(app, g.Name, wf.Name, name, file)
			if err != nil {
				return err
			}

			sg, err := app.Store.CreateSharedGroup(wf.ID, name)
			if err != nil {
				return err
			}
			if err := app.Store.SetSharedGroupFile(sg.ID, path); err != nil {
				return err
			}
			if err := app.Store.ReplaceSharedGroupRepos(sg.ID, repoIDs); err != nil {
				return err
			}
			if err := writeBagFile(app, sg.ID, path); err != nil {
				return err
			}

			app.result(style.OK, name, fmt.Sprintf("shared bag in %s/%s", g.Name, wf.Name))
			app.detail("  edit: %s", path)
			app.hint("  then: prizm shared-sync")
			return nil
		},
	}

	cmd.Flags().StringVar(&repoList, "repos", "", "comma-separated repos that receive this bag (default: all in the group)")
	cmd.Flags().StringVar(&file, "file", "", "back the bag with this file (default: inside prizm's data directory)")
	return cmd
}

func newSharedEditCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:               "shared-edit [group] <workflow> <name>",
		ValidArgsFunction: positions(app, compGroup, compWorkflow, compBag),
		Short:             "Open a shared bag's file in $EDITOR",
		Args:              usageArgs(cobra.RangeArgs(2, 3)),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, _, sg, err := resolveBag(app, args)
			if err != nil {
				return err
			}

			editor := os.Getenv("EDITOR")
			if editor == "" {
				editor = "vi"
			}

			ed := exec.Command(editor, sg.FilePath)
			ed.Stdin, ed.Stdout, ed.Stderr = os.Stdin, os.Stdout, os.Stderr
			if err := ed.Run(); err != nil {
				return fmt.Errorf("running %s: %w", editor, err)
			}

			app.hint("run `prizm shared-sync` to apply your edits")
			return nil
		},
	}
}

func newSharedLsCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:               "shared-ls [group]",
		ValidArgsFunction: positions(app, compGroup),
		Short:             "List shared bags and the files backing them",
		Args:              usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			bags, err := app.Store.AllSharedGroups()
			if err != nil {
				return err
			}

			filter := ""
			if len(args) == 1 {
				g, err := app.mustGroup(args[0])
				if err != nil {
					return err
				}
				filter = g.Name
			}

			shown := 0
			for _, bag := range bags {
				if filter != "" && bag.GroupName != filter {
					continue
				}

				repos, err := app.Store.SharedGroupRepos(bag.ID)
				if err != nil {
					return err
				}
				names := make([]string, 0, len(repos))
				for _, r := range repos {
					names = append(names, r.Name)
				}

				app.sayf("%s %s", style.Heading(bag.GroupName+"/"+bag.WorkflowName+"/"+bag.Name), style.Detail(joinOrNone(names)))
				app.detail("  %s", bag.FilePath)
				shown++
			}

			if shown == 0 {
				app.hint("no shared bags yet — run `prizm shared-add <group> <workflow> <name>`")
			}
			return nil
		},
	}
}

func newSharedSyncCmd(app *App) *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:               "shared-sync [group] [workflow] [name]",
		ValidArgsFunction: positions(app, compGroup, compWorkflow, compBag),
		Short:             "Reconcile shared bag files into prizm",
		Long: "The file is the source of truth: a key removed from it is removed from\n" +
			"the bag. Nothing is written without confirmation.",
		Args: usageArgs(cobra.MaximumNArgs(3)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := syncAllGlobals(app, args, yes); err != nil {
				return err
			}

			bags, err := selectBags(app, args)
			if err != nil {
				return err
			}
			if len(bags) == 0 {
				app.hint("no shared bags to sync")
				return nil
			}

			for _, bag := range bags {
				if err := syncBag(app, bag, yes); err != nil {
					return err
				}
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&yes, "yes", false, "apply without confirmation")
	return cmd
}

// syncBag reconciles one bag's file into the database.
func syncBag(app *App, bag store.SharedGroupRef, yes bool) error {
	label := fmt.Sprintf("%s/%s/%s", bag.GroupName, bag.WorkflowName, bag.Name)

	raw, err := os.ReadFile(bag.FilePath)
	if err != nil {
		return fmt.Errorf("reading %s for %s: %w", bag.FilePath, label, err)
	}

	repoNames, incoming, hasHeader, err := sharedfile.Parse(string(raw))
	if err != nil {
		return fmt.Errorf("parsing %s: %w", bag.FilePath, err)
	}

	current, err := app.Store.SharedGroupVars(bag.ID)
	if err != nil {
		return err
	}
	diff := sharedfile.Compare(current, incoming)

	repoIDs, membershipDiff, err := membershipChange(app, bag, repoNames, hasHeader)
	if err != nil {
		return err
	}

	if diff.Empty() && membershipDiff == "" {
		app.result(style.Same, bag.Name, "up to date")
		return nil
	}

	app.sayf("%s %s", style.Heading(label), style.Detail("← "+bag.FilePath))
	renderVarDiff(app, diff)
	if membershipDiff != "" {
		app.say(strings.TrimRight(membershipDiff, "\n"))
	}

	if !yes {
		ok, err := app.Confirm("Apply? [y/N] ")
		if err != nil {
			return err
		}
		if !ok {
			app.result(style.Warn, bag.Name, "skipped")
			return nil
		}
	}

	defer app.snapshot(store.SharedGroupScope(bag.ID), store.SourceSharedSync, filepath.Base(bag.FilePath))
	if err := app.Store.ReplaceSharedGroupVars(bag.ID, incoming); err != nil {
		return err
	}
	if hasHeader {
		if err := app.Store.ReplaceSharedGroupRepos(bag.ID, repoIDs); err != nil {
			return err
		}
	}

	app.result(style.OK, bag.Name, "synced")
	return nil
}

// renderVarDiff prints a key-level diff. Shared by bags and the group file so
// every reconciliation in prizm reads the same.
func renderVarDiff(app *App, diff sharedfile.Diff) {
	for _, key := range diff.Added {
		app.say("  " + style.Row(style.Add, key, ""))
	}
	for _, c := range diff.Changed {
		app.say("  " + style.Row(style.Change, c.Key, c.From+" → "+c.To))
	}
	for _, key := range diff.Removed {
		app.say("  " + style.Row(style.Remove, key, ""))
	}
}

// membershipChange resolves the header's repo names and describes any change.
// Membership is only touched when the file states it.
func membershipChange(app *App, bag store.SharedGroupRef, repoNames []string, hasHeader bool) ([]int64, string, error) {
	if !hasHeader {
		return nil, "", nil
	}

	g, err := app.mustGroup(bag.GroupName)
	if err != nil {
		return nil, "", err
	}

	repoIDs := make([]int64, 0, len(repoNames))
	for _, name := range repoNames {
		repo, err := app.Store.RepoByName(g.ID, name)
		if err != nil {
			return nil, "", fmt.Errorf("%s: header names repo %q, which is not in group %s",
				bag.FilePath, name, g.Name)
		}
		repoIDs = append(repoIDs, repo.ID)
	}

	members, err := app.Store.SharedGroupRepos(bag.ID)
	if err != nil {
		return nil, "", err
	}
	existing := make([]string, 0, len(members))
	for _, m := range members {
		existing = append(existing, m.Name)
	}

	// Compare as sets: the header lists repos in the author's order, while the
	// store returns them sorted, so an ordered comparison reports phantom
	// changes on every sync.
	if sameSet(existing, repoNames) {
		return repoIDs, "", nil
	}
	return repoIDs, "  " + style.Row(style.Change, "repos",
		joinOrNone(existing)+" → "+joinOrNone(repoNames)) + "\n", nil
}

// sameSet reports whether two name lists contain the same names, ignoring order.
func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	sortedA := append([]string(nil), a...)
	sortedB := append([]string(nil), b...)
	sort.Strings(sortedA)
	sort.Strings(sortedB)

	for i := range sortedA {
		if sortedA[i] != sortedB[i] {
			return false
		}
	}
	return true
}

// syncAllGlobals reconciles the group file for whichever groups this
// invocation covers.
func syncAllGlobals(app *App, args []string, yes bool) error {
	if len(args) > 0 {
		g, _, err := app.splitGroup(args, len(args)-1)
		if err != nil {
			return err
		}
		return syncGlobal(app, g, yes)
	}

	groups, err := app.Store.ListGroups()
	if err != nil {
		return err
	}
	for _, g := range groups {
		if err := syncGlobal(app, g, yes); err != nil {
			return err
		}
	}
	return nil
}

// selectBags narrows the bags to sync from optional group/workflow/name args.
func selectBags(app *App, args []string) ([]store.SharedGroupRef, error) {
	all, err := app.Store.AllSharedGroups()
	if err != nil {
		return nil, err
	}
	if len(args) == 0 {
		return all, nil
	}

	g, rest, err := app.splitGroup(args, len(args)-1)
	if err != nil {
		return nil, err
	}

	var out []store.SharedGroupRef
	for _, bag := range all {
		if bag.GroupName != g.Name {
			continue
		}
		if len(rest) >= 1 && bag.WorkflowName != rest[0] {
			continue
		}
		if len(rest) >= 2 && bag.Name != rest[1] {
			continue
		}
		out = append(out, bag)
	}
	return out, nil
}

// resolveBag resolves exactly one bag from [group] <workflow> <name>.
func resolveBag(app *App, args []string) (store.Group, store.Workflow, store.SharedGroup, error) {
	g, rest, err := app.splitGroup(args, 2)
	if err != nil {
		return store.Group{}, store.Workflow{}, store.SharedGroup{}, err
	}

	wf, err := app.Store.WorkflowByName(g.ID, rest[0])
	if err != nil {
		return store.Group{}, store.Workflow{}, store.SharedGroup{},
			fmt.Errorf("no such workflow %q in group %s", rest[0], g.Name)
	}

	sg, err := app.Store.SharedGroupByName(wf.ID, rest[1])
	if err != nil {
		return store.Group{}, store.Workflow{}, store.SharedGroup{},
			fmt.Errorf("no such shared bag %q in %s/%s", rest[1], g.Name, wf.Name)
	}
	return g, wf, sg, nil
}

// bagPath decides where a bag's file lives.
func bagPath(app *App, group, workflow, name, override string) (string, error) {
	if override != "" {
		return resolvePath(app, override)
	}

	dir, err := config.DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "shared", group, workflow, name+".env"), nil
}

// writeBagFile materialises a bag's current contents as editable text.
func writeBagFile(app *App, bagID int64, path string) error {
	if err := config.EnsureDir(filepath.Dir(path)); err != nil {
		return err
	}

	vars, err := app.Store.SharedGroupVars(bagID)
	if err != nil {
		return err
	}
	repos, err := app.Store.SharedGroupRepos(bagID)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(repos))
	for _, r := range repos {
		names = append(names, r.Name)
	}

	// 0600: this file holds plaintext secrets while the DB copy is encrypted.
	return os.WriteFile(path, []byte(sharedfile.Render(names, vars)), 0o600)
}
