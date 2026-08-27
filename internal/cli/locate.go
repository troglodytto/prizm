package cli

import (
	"fmt"

	"github.com/troglodytto/prizm/internal/store"
)

// splitGroup separates the group from the remaining positional arguments.
//
// The rule is positional count: want is how many positionals the command needs
// after the group, so want+1 arguments means the group was named and want
// means infer it from where we are standing. Nothing is guessed when it was
// stated.
func (a *App) splitGroup(args []string, want int) (store.Group, []string, error) {
	if len(args) > want {
		g, err := a.mustGroup(args[0])
		if err != nil {
			return store.Group{}, nil, err
		}
		return g, args[1:], nil
	}

	_, g, err := a.locate()
	if err != nil {
		return store.Group{}, nil, err
	}
	return g, args, nil
}

// splitGroupRepo separates an optional group and an optional repo from the
// remaining positionals. Anything not given is inferred from the current
// directory; anything given wins over inference.
func (a *App) splitGroupRepo(args []string, want int) (store.Group, store.Repo, []string, error) {
	switch {
	case len(args) > want+1: // group and repo both named
		g, err := a.mustGroup(args[0])
		if err != nil {
			return store.Group{}, store.Repo{}, nil, err
		}
		repo, err := a.repoIn(g, args[1])
		if err != nil {
			return store.Group{}, store.Repo{}, nil, err
		}
		return g, repo, args[2:], nil

	case len(args) > want: // repo named, group inferred
		_, g, err := a.locate()
		if err != nil {
			return store.Group{}, store.Repo{}, nil, err
		}
		repo, err := a.repoIn(g, args[0])
		if err != nil {
			return store.Group{}, store.Repo{}, nil, err
		}
		return g, repo, args[1:], nil

	default: // both inferred
		repo, g, err := a.locate()
		if err != nil {
			return store.Group{}, store.Repo{}, nil, err
		}
		return g, repo, args, nil
	}
}

func (a *App) repoIn(g store.Group, name string) (store.Repo, error) {
	repo, err := a.Store.RepoByName(g.ID, name)
	if err != nil {
		return store.Repo{}, fmt.Errorf("no such repo %q in group %s", name, g.Name)
	}
	return repo, nil
}

// locate finds the repo and group containing the current directory.
//
// Outside every registered repo with no group named, prizm cannot know which
// group was meant, and picking one would be a guess that writes files into
// repos. So it fails, and names the fix.
func (a *App) locate() (store.Repo, store.Group, error) {
	cwd, err := a.Cwd()
	if err != nil {
		return store.Repo{}, store.Group{}, fmt.Errorf("determining current directory: %w", err)
	}

	repo, g, err := a.Store.RepoForPath(cwd)
	if err != nil {
		return store.Repo{}, store.Group{}, errUsage(
			"not inside a registered repo, so prizm cannot tell which group you mean — "+
				"name it explicitly, or cd into one of the group's repos (cwd: %s)", cwd)
	}
	return repo, g, nil
}

// Some commands take a trailing list whose members are bare words —
// `prizm unset <group> <repo> KEY KEY` — so a positional count cannot tell a
// group name from a key. These resolve by lookup instead: consume a leading
// argument only if it actually names a group or a repo.

// splitGroupByLookup consumes a leading group name if there is one, otherwise
// infers the group from the current directory.
func (a *App) splitGroupByLookup(args []string) (store.Group, []string, error) {
	if len(args) > 1 {
		if g, err := a.Store.GroupByName(args[0]); err == nil {
			return g, args[1:], nil
		}
	}

	_, g, err := a.locate()
	if err != nil {
		return store.Group{}, nil, err
	}
	return g, args, nil
}

// splitGroupRepoByLookup consumes a leading group and repo name if present.
func (a *App) splitGroupRepoByLookup(args []string) (store.Group, store.Repo, []string, error) {
	g, rest, err := a.splitGroupByLookup(args)
	if err != nil {
		return store.Group{}, store.Repo{}, nil, err
	}

	if len(rest) > 1 {
		if repo, err := a.Store.RepoByName(g.ID, rest[0]); err == nil {
			return g, repo, rest[1:], nil
		}
	}

	repo, located, err := a.locate()
	if err != nil {
		return store.Group{}, store.Repo{}, nil, err
	}
	if located.ID != g.ID {
		return store.Group{}, store.Repo{}, nil, errUsage(
			"you are standing in %s, not %s — name the repo explicitly", located.Name, g.Name)
	}
	return g, repo, rest, nil
}

// groupWorkflow resolves the `[group] <workflow>` shape shared by up, down
// and docker, so all three infer the group from the working directory in
// exactly the same way.
func (a *App) groupWorkflow(args []string) (store.Group, store.Workflow, error) {
	g, rest, err := a.splitGroup(args, 1)
	if err != nil {
		return store.Group{}, store.Workflow{}, err
	}

	wf, err := a.Store.WorkflowByName(g.ID, rest[0])
	if err != nil {
		return store.Group{}, store.Workflow{}, fmt.Errorf("no such workflow %q in group %s", rest[0], g.Name)
	}
	return g, wf, nil
}
