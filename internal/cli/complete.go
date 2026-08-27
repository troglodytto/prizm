package cli

import (
	"strings"

	"github.com/spf13/cobra"
)

// Positional completion.
//
// Almost every prizm command reads the same way — an optional group, then
// something scoped to it — so completion is expressed as one completer per
// argument slot rather than a switch in each command.

// completeFn offers candidates for one positional slot. args holds the
// positionals already typed.
type completeFn func(a *App, args []string, toComplete string) ([]string, cobra.ShellCompDirective)

// noFiles is the directive for a slot that is not a path.
const noFiles = cobra.ShellCompDirectiveNoFileComp

// positions builds a ValidArgsFunction from one completer per slot. A slot
// past the end offers nothing, which is what stops completion running off the
// end of a command.
func positions(app *App, slots ...completeFn) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) >= len(slots) {
			return nil, noFiles
		}
		return slots[len(args)](app, args, toComplete)
	}
}

// compGroup offers group names, ranked by relevance to the current directory.
func compGroup(a *App, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return a.completeGroups(toComplete)
}

// compWorkflow offers the workflows of whichever group is in play.
func compWorkflow(a *App, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return a.completeWorkflows(a.scopeGroup(args), toComplete)
}

// compRepo offers the repos of whichever group is in play.
func compRepo(a *App, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return a.completeRepos(a.scopeGroup(args), toComplete)
}

// compBag offers the shared bags of a group's workflow.
func compBag(a *App, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	group := a.scopeGroup(args)

	workflow := ""
	if rest := a.scopeRest(args); len(rest) > 0 {
		workflow = rest[0]
	}
	return a.completeBags(group, workflow, toComplete)
}

// compGroupOrWorkflow offers groups and, when standing inside a repo, that
// group's workflows too — because `prizm up local` is valid there.
func compGroupOrWorkflow(a *App, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	out, directive := a.completeGroups(toComplete)

	if group, ok := a.inferredGroupName(); ok {
		workflows, _ := a.completeWorkflows(group, toComplete)
		out = append(out, workflows...)
	}
	return out, directive
}

// compFiles hands the slot back to the shell's own path completion.
func compFiles(*App, []string, string) ([]string, cobra.ShellCompDirective) {
	return nil, cobra.ShellCompDirectiveDefault
}

// compNone offers nothing — for a slot the user must invent, like a new name.
func compNone(*App, []string, string) ([]string, cobra.ShellCompDirective) {
	return nil, noFiles
}

// scopeGroup works out which group the arguments are talking about: the first
// one if it names a group, otherwise whichever contains the current directory.
// It mirrors what splitGroup does at run time, so completion offers exactly
// what the command would accept.
func (a *App) scopeGroup(args []string) string {
	if len(args) > 0 {
		if _, err := a.Store.GroupByName(args[0]); err == nil {
			return args[0]
		}
	}

	name, _ := a.inferredGroupName()
	return name
}

// scopeRest is the positionals after the group, however it was determined.
func (a *App) scopeRest(args []string) []string {
	if len(args) > 0 {
		if _, err := a.Store.GroupByName(args[0]); err == nil {
			return args[1:]
		}
	}
	return args
}

// completeBags offers a workflow's shared bag names.
func (a *App) completeBags(group, workflow, toComplete string) ([]string, cobra.ShellCompDirective) {
	bags, err := a.Store.AllSharedGroups()
	if err != nil {
		return nil, noFiles
	}

	// Without a workflow filter the same bag name appears once per workflow,
	// so offer each name once — the user is choosing a name, not a row.
	seen := map[string]bool{}

	var names []string
	for _, b := range bags {
		if b.GroupName != group {
			continue
		}
		if workflow != "" && b.WorkflowName != workflow {
			continue
		}
		if seen[b.Name] {
			continue
		}
		seen[b.Name] = true
		names = append(names, b.Name)
	}
	sortStrings(names)
	return withPrefix(names, toComplete), noFiles
}

// completeVarKeys offers the variable names already set on a repo, so `unset`
// can complete what is actually there rather than making you remember it.
func (a *App) completeVarKeys(group, repo, workflow, toComplete string) ([]string, cobra.ShellCompDirective) {
	g, err := a.Store.GroupByName(group)
	if err != nil {
		return nil, noFiles
	}

	r, err := a.Store.RepoByName(g.ID, repo)
	if err != nil {
		return nil, noFiles
	}

	var vars map[string]string
	if workflow == "" {
		vars, err = a.Store.RepoVars(r.ID)
	} else {
		wf, wfErr := a.Store.WorkflowByName(g.ID, workflow)
		if wfErr != nil {
			return nil, noFiles
		}
		vars, err = a.Store.WorkflowRepoVars(wf.ID, r.ID)
	}
	if err != nil {
		return nil, noFiles
	}

	names := make([]string, 0, len(vars))
	for k := range vars {
		names = append(names, k)
	}
	sortStrings(names)
	return withPrefix(names, toComplete), noFiles
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// completeTags offers the tags already in use, plus the ones prizm treats as
// meaningful, so guardrails are easy to apply consistently.
func (a *App) completeTags(toComplete string) ([]string, cobra.ShellCompDirective) {
	seen := map[string]bool{"prod": true, "qa": true, "local": true}

	groups, err := a.Store.ListGroups()
	if err == nil {
		for _, g := range groups {
			workflows, wfErr := a.Store.ListWorkflows(g.ID)
			if wfErr != nil {
				continue
			}
			for _, w := range workflows {
				if w.Tag != "" {
					seen[w.Tag] = true
				}
			}
		}
	}

	names := make([]string, 0, len(seen))
	for t := range seen {
		names = append(names, t)
	}
	sortStrings(names)
	return withPrefix(names, toComplete), noFiles
}

// completeRepoList completes a comma-separated repo list in place, so
// --repos auth,<TAB> offers the repos not yet named.
func (a *App) completeRepoList(args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	g, err := a.Store.GroupByName(a.scopeGroup(args))
	if err != nil {
		return nil, noFiles
	}

	repos, err := a.Store.ListRepos(g.ID)
	if err != nil {
		return nil, noFiles
	}

	prefix, last := "", toComplete
	if i := strings.LastIndex(toComplete, ","); i >= 0 {
		prefix, last = toComplete[:i+1], toComplete[i+1:]
	}

	already := make(map[string]bool)
	for _, n := range strings.Split(prefix, ",") {
		already[n] = true
	}

	var out []string
	for _, r := range repos {
		if !already[r.Name] && strings.HasPrefix(r.Name, last) {
			out = append(out, prefix+r.Name)
		}
	}
	// The candidates are whole values, so the shell must not add a space and
	// end the list mid-way through it.
	return out, noFiles | cobra.ShellCompDirectiveNoSpace
}

// registerScopedFlags wires the flag-value completers a command declares.
func registerScopedFlags(app *App, cmd *cobra.Command) {
	if cmd.Flag("workflow") != nil {
		_ = cmd.RegisterFlagCompletionFunc("workflow",
			func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
				return app.completeWorkflows(app.scopeGroup(args), toComplete)
			})
	}
	if cmd.Flag("repo") != nil {
		_ = cmd.RegisterFlagCompletionFunc("repo",
			func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
				return app.completeRepos(app.scopeGroup(args), toComplete)
			})
	}
	if cmd.Flag("repos") != nil {
		_ = cmd.RegisterFlagCompletionFunc("repos",
			func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
				return app.completeRepoList(args, toComplete)
			})
	}
	if cmd.Flag("tag") != nil {
		_ = cmd.RegisterFlagCompletionFunc("tag",
			func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
				return app.completeTags(toComplete)
			})
	}
	if cmd.Flag("bag") != nil {
		_ = cmd.RegisterFlagCompletionFunc("bag",
			func(c *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
				// A bag belongs to one workflow, so if --workflow is already
				// on the line, only that workflow's bags are candidates.
				return app.completeBags(app.scopeGroup(args), flagValue(c, "workflow"), toComplete)
			})
	}
}

// flagValue reads a flag already typed on the line, or "" if it is absent.
func flagValue(cmd *cobra.Command, name string) string {
	if f := cmd.Flag(name); f != nil {
		return f.Value.String()
	}
	return ""
}
