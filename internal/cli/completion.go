package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/troglodytto/prizm/internal/rank"
)

// completeGroups returns every group name, ordered by relevance to the current
// directory.
//
// KeepOrder is not optional: bash and zsh alphabetise candidates by default,
// which would silently discard the directory-relevance ranking — the exact
// thing the ordering exists to provide.
//
// This runs on every Tab press, synchronously, so it touches plaintext
// metadata only: no decryption, no keychain, no file walking.
func (a *App) completeGroups(toComplete string) ([]string, cobra.ShellCompDirective) {
	directive := cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveKeepOrder

	groups, err := a.Store.ListGroups()
	if err != nil || len(groups) == 0 {
		return nil, directive
	}

	paths, err := a.Store.RepoPathsByGroup()
	if err != nil {
		return nil, directive
	}

	cwd, err := a.Cwd()
	if err != nil {
		cwd = ""
	}

	candidates := make([]rank.Candidate, 0, len(groups))
	for _, g := range groups {
		candidates = append(candidates, rank.Candidate{
			Name:       g.Name,
			Paths:      paths[g.Name],
			UseCount:   g.UseCount,
			LastUsedAt: g.LastUsedAt,
		})
	}

	return withPrefix(rank.Rank(candidates, cwd, a.Now()), toComplete), directive
}

// completeWorkflows returns one group's workflow names.
func (a *App) completeWorkflows(group, toComplete string) ([]string, cobra.ShellCompDirective) {
	directive := cobra.ShellCompDirectiveNoFileComp

	g, err := a.Store.GroupByName(group)
	if err != nil {
		return nil, directive
	}

	workflows, err := a.Store.ListWorkflows(g.ID)
	if err != nil {
		return nil, directive
	}

	names := make([]string, 0, len(workflows))
	for _, w := range workflows {
		names = append(names, w.Name)
	}
	return withPrefix(names, toComplete), directive
}

// completeRepos returns one group's repo names.
func (a *App) completeRepos(group, toComplete string) ([]string, cobra.ShellCompDirective) {
	directive := cobra.ShellCompDirectiveNoFileComp

	g, err := a.Store.GroupByName(group)
	if err != nil {
		return nil, directive
	}

	repos, err := a.Store.ListRepos(g.ID)
	if err != nil {
		return nil, directive
	}

	names := make([]string, 0, len(repos))
	for _, r := range repos {
		names = append(names, r.Name)
	}
	return withPrefix(names, toComplete), directive
}

// inferredGroupName is the group containing the current directory, if any.
func (a *App) inferredGroupName() (string, bool) {
	cwd, err := a.Cwd()
	if err != nil {
		return "", false
	}

	_, g, err := a.Store.RepoForPath(cwd)
	if err != nil {
		return "", false
	}
	return g.Name, true
}

// completeRoot handles `prizm <TAB>`: the groups, ranked by relevance to the
// current directory, then the workflows of the group we are standing in.
//
// Verbs are deliberately not added here — cobra emits its own subcommands
// before calling this, and adding them again would duplicate every one. That
// also means a bare Tab lists the verbs first and these after; cobra does not
// expose a way to reorder the two. Typing any prefix disambiguates
// immediately, and `prizm` with no arguments opens the picker instead.
func (a *App) completeRoot(_ *cobra.Command, toComplete string) ([]string, cobra.ShellCompDirective) {
	out, directive := a.completeGroups(toComplete)

	if group, ok := a.inferredGroupName(); ok {
		workflows, _ := a.completeWorkflows(group, toComplete)
		out = append(out, workflows...)
	}
	return withPrefix(out, toComplete), directive
}

// completeGroupThenWorkflow is the positional completer for `up`: a group
// first, or — inside a repo — a workflow directly.
func (a *App) completeGroupThenWorkflow(args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	switch len(args) {
	case 0:
		out, directive := a.completeGroups(toComplete)
		if group, ok := a.inferredGroupName(); ok {
			workflows, _ := a.completeWorkflows(group, toComplete)
			out = append(out, workflows...)
		}
		return out, directive
	case 1:
		return a.completeWorkflows(args[0], toComplete)
	default:
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
}

// withPrefix keeps only candidates the user's partial word could become,
// preserving order.
func withPrefix(candidates []string, toComplete string) []string {
	if toComplete == "" {
		return candidates
	}

	out := make([]string, 0, len(candidates))
	for _, c := range candidates {
		if strings.HasPrefix(c, toComplete) {
			out = append(out, c)
		}
	}
	return out
}
