package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/troglodytto/prizm/internal/drift"
	"github.com/troglodytto/prizm/internal/envfile"
	"github.com/troglodytto/prizm/internal/resolve"
	"github.com/troglodytto/prizm/internal/store"
	"github.com/troglodytto/prizm/internal/style"
	"github.com/troglodytto/prizm/internal/syncplan"
	"github.com/troglodytto/prizm/internal/tui"
)

func newSyncCmd(app *App) *cobra.Command {
	var (
		yes bool
		pin bool
	)

	cmd := &cobra.Command{
		Use:   "sync [group] [repo]",
		Short: "Pull hand-edits from a repo's env file back into prizm",
		Long: "A repo's env file is a flattened view of four layers, so an edited line\n" +
			"could mean several different things. Each change is attributed to the\n" +
			"layer that defined it, and anything genuinely ambiguous is put to you\n" +
			"rather than guessed at.\n\n" +
			"Without a terminal, ambiguous changes are reported and skipped; --pin\n" +
			"resolves them as a literal on this repo alone.",
		Args:              usageArgs(cobra.MaximumNArgs(2)),
		ValidArgsFunction: positions(app, compGroup, compRepo),
		RunE: func(cmd *cobra.Command, args []string) error {
			g, repos, err := syncTargets(app, args)
			if err != nil {
				return err
			}

			applied, err := app.Store.AppliedFor(g.ID)
			if err != nil {
				return err
			}

			workflows, err := app.Store.ListWorkflows(g.ID)
			if err != nil {
				return err
			}
			byID := make(map[int64]store.Workflow, len(workflows))
			for _, w := range workflows {
				byID[w.ID] = w
			}

			// sync rewrites env files and re-points symlinks, so it needs
			// the same exclusive lock `up` takes. Without it, a concurrent
			// up interleaves with it on the same repos.
			lock, err := acquireApplyLock()
			if err != nil {
				return err
			}
			defer lock.Release()

			touched := 0
			for _, repo := range repos {
				state, ok := applied[repo.ID]
				if !ok {
					continue
				}

				done, err := syncRepo(app, g, byID[state.WorkflowID], repo, yes, pin)
				if err != nil {
					return err
				}
				if done {
					touched++
				}
			}

			if touched == 0 {
				app.hint("nothing to reconcile in %s", g.Name)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&yes, "yes", false, "apply without confirmation")
	cmd.Flags().BoolVar(&pin, "pin", false, "resolve derived-value edits as a literal on this repo only")
	return cmd
}

// syncTargets resolves which repos to reconcile.
func syncTargets(app *App, args []string) (store.Group, []store.Repo, error) {
	if len(args) == 2 {
		g, repo, _, err := app.splitGroupRepo(args, 0)
		if err != nil {
			return store.Group{}, nil, err
		}
		return g, []store.Repo{repo}, nil
	}

	g, rest, err := app.splitGroup(args, 0)
	if err != nil {
		return store.Group{}, nil, err
	}
	if len(rest) == 1 {
		repo, err := app.repoIn(g, rest[0])
		if err != nil {
			return store.Group{}, nil, err
		}
		return g, []store.Repo{repo}, nil
	}

	repos, err := app.Store.ListRepos(g.ID)
	return g, repos, err
}

// syncRepo reconciles one repo, reporting whether it had anything to do.
func syncRepo(app *App, g store.Group, wf store.Workflow, repo store.Repo, yes, pin bool) (bool, error) {
	if wf.ID == 0 {
		return false, nil
	}

	report, err := inspectRepo(app, g, wf, repo)
	if err != nil {
		return false, err
	}
	if report.Link == drift.PathMissing {
		app.result(style.Fail, repo.Name, "path missing — run `prizm repair`")
		return false, nil
	}
	// Nothing can be compared against a repo that will not resolve, but
	// "nothing to reconcile" is the wrong thing to say about it — that reads
	// as all clear when the configuration is broken.
	if report.Link == drift.Unresolvable {
		app.result(style.Fail, repo.Name, report.Err.Error())
		return true, nil
	}
	if report.Diff.Empty() {
		return false, nil
	}

	layers, err := resolve.ForRepoLayers(app.Store, wf, repo)
	if err != nil {
		return false, err
	}

	onDisk, err := readAppliedEnv(repo)
	if err != nil {
		return false, err
	}

	plan, err := syncplan.Build(app.Store, wf, repo, layers, report.Diff, onDisk, pin)
	if err != nil {
		return false, err
	}

	app.sayf("%s %s", style.Heading(g.Name+"/"+repo.Name),
		style.Detail("← "+repo.EnvFile+"  ("+wf.Name+")"))

	decisions, err := decide(app, plan, yes)
	if err != nil {
		return false, quietUserCancel(app, err)
	}

	if err := applyPlan(app, wf, repo, plan, decisions); err != nil {
		return false, err
	}

	// Regenerate the file so it matches prizm exactly again — sorted, with
	// internal values stripped and skipped edits undone.
	if _, err := applyRepo(app, g, wf, repo); err != nil {
		return false, err
	}

	app.result(style.OK, repo.Name, summarise(plan, decisions))
	return true, nil
}

// decide asks what to do about each item, or falls back to the
// non-interactive rules: actionable items apply, ambiguous ones are skipped.
func decide(app *App, plan syncplan.Plan, yes bool) (map[string]syncplan.Decision, error) {
	out := make(map[string]syncplan.Decision, len(plan.Items))

	if yes || !app.canResolve() {
		for _, item := range plan.Items {
			out[item.Key] = syncplan.DecideApply
			if item.Action == syncplan.Ambiguous {
				out[item.Key] = syncplan.DecideSkip
				app.say("  " + style.Row(style.Ask, item.Key, "skipped · "+item.Reason))
			} else {
				app.say("  " + style.Row(itemMark(item), item.Key, describe(item)))
			}
		}
		return out, nil
	}

	rows := make([]tui.ResolveRow, 0, len(plan.Items))
	for _, item := range plan.Items {
		choices := item.Choices()
		labels := make([]string, 0, len(choices))
		consequences := make([]string, 0, len(choices))

		// Only updating the shared value reaches other repos. Pinning and
		// skipping are, by definition, confined to this one.
		spread := ""
		if len(item.Consumers) > 1 {
			spread = "also changes " + strings.Join(others(item.Consumers, plan.Repo.Name), ", ")
		}

		for _, choice := range choices {
			labels = append(labels, choice.Label())
			if choice == syncplan.DecideUpdateShared {
				consequences = append(consequences, spread)
				continue
			}
			consequences = append(consequences, "")
		}

		rows = append(rows, tui.ResolveRow{
			Key:          item.Key,
			Detail:       describe(item),
			Note:         item.Reason,
			Choices:      labels,
			Consequences: consequences,
		})
	}

	chosen, err := app.Resolve("Reconcile "+plan.Repo.Name, rows)
	if err != nil {
		if errors.Is(err, tui.ErrCancelled) {
			return nil, errCancelledByUser
		}
		return nil, err
	}

	for i, item := range plan.Items {
		out[item.Key] = item.Choices()[chosen[i]]
	}
	return out, nil
}

// readAppliedEnv reads what is actually in the repo's env file right now.
func readAppliedEnv(repo store.Repo) (map[string]string, error) {
	raw, err := os.ReadFile(filepath.Join(repo.Path, repo.EnvFile))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", repo.EnvFile, err)
	}
	return envfile.Parse(string(raw))
}

// others drops the repo being synced from a consumer list: "also changes
// auth" while syncing auth is noise, and makes the real list harder to read.
func others(consumers []string, self string) []string {
	out := make([]string, 0, len(consumers))
	for _, name := range consumers {
		if name != self {
			out = append(out, name)
		}
	}
	return out
}

func itemMark(item syncplan.Item) style.Mark {
	switch item.Action {
	case syncplan.DeleteFromOwningLayer:
		return style.Remove
	case syncplan.Ambiguous:
		return style.Ask
	}
	if item.From == "" {
		return style.Add
	}
	return style.Change
}

func describe(item syncplan.Item) string {
	switch {
	case item.Action == syncplan.DeleteFromOwningLayer:
		return "removed from the file · " + item.Origin.Layer
	case item.From == "":
		return "added · " + style.Secret(item.Key, item.To)
	default:
		return style.Secret(item.Key, item.From) + " → " + style.Secret(item.Key, item.To)
	}
}

func summarise(plan syncplan.Plan, decisions map[string]syncplan.Decision) string {
	applied, skipped := 0, 0
	for _, item := range plan.Items {
		if decisions[item.Key] == syncplan.DecideSkip {
			skipped++
			continue
		}
		applied++
	}

	out := plural(applied, "change") + " reconciled"
	if skipped > 0 {
		out += fmt.Sprintf(", %d skipped", skipped)
	}
	return out
}

func applyPlan(app *App, wf store.Workflow, repo store.Repo, plan syncplan.Plan, decisions map[string]syncplan.Decision) error {
	// A sync can write across several layers at once. Snapshot each one it
	// could have touched afterwards; an untouched scope hashes identically to
	// its last version and records nothing.
	touched := map[store.Scope]bool{}
	defer func() {
		for scope := range touched {
			app.snapshot(scope, store.SourceSync, "sync "+repo.Name)
		}
	}()

	for _, item := range plan.Items {
		switch decisions[item.Key] {
		case syncplan.DecideSkip:
			continue

		case syncplan.DecidePin:
			touched[store.WorkflowRepoScope(wf.ID, repo.ID)] = true
			if err := app.Store.SetWorkflowRepoVar(wf.ID, repo.ID, item.Key, item.To); err != nil {
				return err
			}

		case syncplan.DecideUpdateShared:
			touched[sharedScope(item.RefOrigin)] = true
			if err := writeSharedValue(app, item.RefOrigin, item.RefName, item.To); err != nil {
				return err
			}

		case syncplan.DecideApply:
			touched[itemScope(wf, repo, item)] = true
			if err := applyItem(app, wf, repo, item); err != nil {
				return err
			}
		}
	}
	return nil
}

// itemScope names the timeline an applied item lands on.
func itemScope(wf store.Workflow, repo store.Repo, item syncplan.Item) store.Scope {
	if item.Origin.Kind == resolve.LayerRepoShared {
		return store.RepoScope(repo.ID)
	}
	return store.WorkflowRepoScope(wf.ID, repo.ID)
}

// sharedScope names the timeline behind a shared value.
func sharedScope(origin resolve.Origin) store.Scope {
	if origin.Kind == resolve.LayerGroup {
		return store.GroupScope(origin.GroupID)
	}
	return store.SharedGroupScope(origin.SharedGroupID)
}

func applyItem(app *App, wf store.Workflow, repo store.Repo, item syncplan.Item) error {
	switch item.Action {
	case syncplan.WriteOwningLayer:
		if item.Origin.Kind == resolve.LayerRepoShared {
			return app.Store.SetRepoVar(repo.ID, item.Key, item.To)
		}
		return app.Store.SetWorkflowRepoVar(wf.ID, repo.ID, item.Key, item.To)

	case syncplan.WriteShared:
		return writeSharedValue(app, item.Origin, item.Key, item.To)

	case syncplan.DeleteFromOwningLayer:
		if item.Origin.Kind == resolve.LayerRepoShared {
			return app.Store.DeleteRepoVar(repo.ID, item.Key)
		}
		return app.Store.DeleteWorkflowRepoVar(wf.ID, repo.ID, item.Key)
	}
	return nil
}

// writeSharedValue updates a group-global or shared-bag variable, keeping the
// bag's backing file in step. Without that the next `shared-sync` would read
// the stale file and quietly revert this.
func writeSharedValue(app *App, origin resolve.Origin, key, value string) error {
	if origin.Kind == resolve.LayerGroup {
		if err := app.Store.SetGroupVar(origin.GroupID, key, value); err != nil {
			return err
		}
		return rewriteGlobalFile(app, origin.GroupID)
	}

	if origin.SharedGroupID == 0 {
		return fmt.Errorf("%s: cannot update the shared value — it is not in a bag", key)
	}
	if err := app.Store.SetSharedGroupVar(origin.SharedGroupID, key, value); err != nil {
		return err
	}

	bag, err := app.Store.SharedGroupByID(origin.SharedGroupID)
	if err != nil {
		return err
	}
	if bag.FilePath == "" {
		return nil
	}
	return writeBagFile(app, bag.ID, bag.FilePath)
}

func (a *App) canResolve() bool {
	return a.Resolve != nil && (tui.Available() || a.pickerInjected)
}
