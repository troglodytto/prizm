package cli

import (
	"fmt"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/troglodytto/prizm/internal/store"
	"github.com/troglodytto/prizm/internal/style"
	"github.com/troglodytto/prizm/internal/tui"
)

func newAuditCmd(app *App) *cobra.Command {
	var (
		workflow string
		global   bool
		bag      string
		restore  bool
	)

	cmd := &cobra.Command{
		Use:               "audit [group] [repo]",
		ValidArgsFunction: positions(app, compGroup, compRepo),
		Short:             "Show a layer's history, and put it back",
		Long: "Every write prizm makes records the resulting state, so you can see what\n" +
			"a layer looked like an hour ago and restore it. Scrub the timeline with\n" +
			"←→; ⏎ restores the highlighted version.\n\n" +
			"Without --restore this only reads, so it is safe to run to answer\n" +
			"\"when did this change, and to what\".",
		Args: usageArgs(cobra.MaximumNArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			scope, label, err := auditScope(app, args, workflow, bag, global)
			if err != nil {
				return err
			}
			return auditTimeline(app, scope, label, restore)
		},
	}

	cmd.Flags().StringVar(&workflow, "workflow", "", "audit one workflow's layer for the repo")
	cmd.Flags().StringVar(&bag, "bag", "", "audit a shared bag (needs --workflow)")
	cmd.Flags().BoolVar(&global, "global", false, "audit the group-global layer")
	cmd.Flags().BoolVar(&restore, "restore", false, "allow restoring the version you pick")
	return cmd
}

// auditScope works out which timeline the flags name, refusing anything that
// could mean two different layers. A history command that quietly showed the
// wrong layer would be worse than one that showed nothing.
func auditScope(app *App, args []string, workflow, bag string, global bool) (store.Scope, string, error) {
	if global && bag != "" {
		return store.Scope{}, "", errUsage("--global and --bag name different layers — pick one")
	}

	if global {
		g, rest, err := app.splitGroup(args, 0)
		if err != nil {
			return store.Scope{}, "", err
		}
		if len(rest) > 0 {
			return store.Scope{}, "", errUsage("--global audits the whole group; drop %q", rest[0])
		}
		return store.GroupScope(g.ID), g.Name + " (global)", nil
	}

	if bag != "" {
		g, _, err := app.splitGroup(args, 0)
		if err != nil {
			return store.Scope{}, "", err
		}
		if workflow == "" {
			return store.Scope{}, "", errUsage("--bag needs --workflow: a bag belongs to one workflow")
		}
		wf, err := workflowIn(app, g, workflow)
		if err != nil {
			return store.Scope{}, "", err
		}
		sg, err := app.Store.SharedGroupByName(wf.ID, bag)
		if err != nil {
			return store.Scope{}, "", fmt.Errorf("no such bag %q in %s/%s", bag, g.Name, wf.Name)
		}
		return store.SharedGroupScope(sg.ID), g.Name + "/" + wf.Name + " · " + bag, nil
	}

	g, repo, rest, err := app.splitGroupRepo(args, 0)
	if err != nil {
		return store.Scope{}, "", err
	}
	if len(rest) > 0 {
		return store.Scope{}, "", errUsage("unexpected argument %q", rest[0])
	}

	if workflow == "" {
		return store.RepoScope(repo.ID), g.Name + "/" + repo.Name, nil
	}

	wf, err := workflowIn(app, g, workflow)
	if err != nil {
		return store.Scope{}, "", err
	}
	return store.WorkflowRepoScope(wf.ID, repo.ID), g.Name + "/" + repo.Name + " · " + wf.Name, nil
}

func auditTimeline(app *App, scope store.Scope, label string, restore bool) error {
	snaps, err := app.Store.ListSnapshots(scope)
	if err != nil {
		return err
	}
	if len(snaps) == 0 {
		app.hint("no history for %s yet — it is recorded from the next write onwards", label)
		return nil
	}

	live, err := app.scopeVars(scope)
	if err != nil {
		return err
	}

	versions, err := buildVersions(app, snaps, live)
	if err != nil {
		return err
	}

	if !restore {
		printTimeline(app, label, versions)
		return nil
	}

	// There is no sensible default version to restore, so with no terminal
	// this refuses rather than guessing which point in time was meant.
	if !app.canPickHistory() {
		printTimeline(app, label, versions)
		return errUsage("--restore needs a terminal to pick a version")
	}

	chosen, picked, err := app.PickHistory(label, versions)
	if err != nil {
		return err
	}
	if !picked {
		app.result(style.Warn, label, "cancelled")
		return nil
	}
	return restoreVersion(app, scope, label, chosen)
}

// canPickHistory reports whether the carousel can actually be shown.
func (a *App) canPickHistory() bool {
	return a.PickHistory != nil && (tui.Available() || a.pickerInjected)
}

// buildVersions turns snapshots into carousel entries, oldest first so the
// strip reads left-to-right like a timeline. Each one carries its diff
// against the live state, since that is what restoring it would do.
func buildVersions(app *App, snaps []store.Snapshot, live map[string]string) ([]tui.Version, error) {
	out := make([]tui.Version, 0, len(snaps))

	// ListSnapshots returns newest first; walk backwards.
	for i := len(snaps) - 1; i >= 0; i-- {
		snap := snaps[i]
		vars, err := app.Store.SnapshotVars(snap.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, tui.Version{
			ID:      snap.ID,
			When:    humanSince(now().Sub(snap.CreatedAt)),
			At:      stamp(snap.CreatedAt),
			Source:  snap.Source,
			Note:    snap.Note,
			Current: i == 0 && sameVars(vars, live),
			Changes: diffVars(vars, live),
		})
	}
	return out, nil
}

// diffVars describes what restoring `then` would do to `live`.
func diffVars(then, live map[string]string) []tui.Change {
	keys := map[string]bool{}
	for k := range then {
		keys[k] = true
	}
	for k := range live {
		keys[k] = true
	}

	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)

	var out []tui.Change
	for _, key := range sorted {
		was, hadThen := then[key]
		is, hasNow := live[key]
		switch {
		case hadThen && !hasNow:
			out = append(out, tui.Change{Key: key, Mark: '+', From: style.Secret(key, was)})
		case !hadThen && hasNow:
			out = append(out, tui.Change{Key: key, Mark: '-', To: style.Secret(key, is)})
		case was != is:
			out = append(out, tui.Change{
				Key: key, Mark: '~',
				From: style.Secret(key, was), To: style.Secret(key, is)})
		}
	}
	return out
}

func sameVars(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if other, ok := b[k]; !ok || other != v {
			return false
		}
	}
	return true
}

// printTimeline is the read-only view — the same information without a
// picker, so it works in a pipe and in a script.
func printTimeline(app *App, label string, versions []tui.Version) {
	app.heading("%s", label)

	whens := make([]string, 0, len(versions))
	for _, v := range versions {
		whens = append(whens, v.When)
	}
	col := style.WidthOf(whens)

	// Newest first when reading: the question is usually "what happened last".
	for i := len(versions) - 1; i >= 0; i-- {
		v := versions[i]
		// The absolute time earns its place here: three edits a minute apart
		// all read "just now", and an audit log that cannot order its own
		// entries is not an audit log.
		detail := v.At + " · " + v.Source
		if v.Note != "" {
			detail += " · " + v.Note
		}

		mark := style.Same
		if v.Current {
			mark = style.OK
			detail += " · current"
		}
		app.row(col, mark, v.When, detail)
	}

	app.blank()
	app.hint("run with --restore to scrub the timeline and put a version back")
}

func restoreVersion(app *App, scope store.Scope, label string, v tui.Version) error {
	if v.Current || len(v.Changes) == 0 {
		app.result(style.Same, label, "already in that state — nothing to restore")
		return nil
	}

	vars, err := app.Store.SnapshotVars(v.ID)
	if err != nil {
		return err
	}
	if err := app.replaceScopeVars(scope, vars); err != nil {
		return err
	}

	for _, c := range v.Changes {
		app.detail("%s %s", string(c.Mark), c.Key)
	}

	// The restore is itself a version, so an unwanted restore can be undone
	// the same way. History that only runs one direction is a trapdoor.
	app.snapshot(scope, store.SourceRestore, "restored "+v.When)
	app.result(style.OK, label, fmt.Sprintf("restored to %s · %s",
		v.When, plural(len(v.Changes), "key")))
	app.hint("run `prizm up` to write the change into the repos")
	return nil
}

// replaceScopeVars writes a whole layer, replacing what was there.
func (a *App) replaceScopeVars(scope store.Scope, vars map[string]string) error {
	switch scope.Kind {
	case store.ScopeGroup:
		return a.Store.ReplaceGroupVars(scope.A, vars)
	case store.ScopeRepo:
		return a.Store.ReplaceRepoVars(scope.A, vars)
	case store.ScopeSharedGroup:
		return a.Store.ReplaceSharedGroupVars(scope.A, vars)
	case store.ScopeWorkflowRepo:
		return a.Store.ReplaceWorkflowRepoVars(scope.A, scope.B, vars)
	default:
		return fmt.Errorf("unknown scope %q", scope.Kind)
	}
}

// stamp renders the wall-clock time, with the date once it stops being
// obvious which day is meant.
func stamp(t time.Time) string {
	age := now().Sub(t)
	switch {
	case age < time.Hour:
		// Several edits inside one minute is the normal shape of a fixing
		// session, and it is exactly then that you need to tell them apart.
		return t.Format("15:04:05")
	case age < 24*time.Hour:
		return t.Format("15:04")
	default:
		return t.Format("Jan 2 15:04")
	}
}

// humanSince renders an age at the coarsest useful unit. "3d ago" answers
// the question; "3d 4h 12m ago" makes you do arithmetic to compare two rows.
func humanSince(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
