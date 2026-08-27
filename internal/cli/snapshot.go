package cli

import (
	"fmt"
	"time"

	"github.com/troglodytto/prizm/internal/store"
)

// now is the clock, swapped out in tests so snapshot timelines are stable.
var now = time.Now

// snapshot records the current contents of a scope as a new version.
//
// It runs *after* a write, not before: the timeline is a list of states the
// configuration has actually been in, so restoring version N means "put it
// back exactly as it was", with no replaying of deltas.
//
// A failure here never fails the command. History is a safety net; refusing
// to set a variable because its backup could not be filed would be the net
// causing the fall.
func (a *App) snapshot(scope store.Scope, source, note string) {
	vars, err := a.scopeVars(scope)
	if err != nil {
		return
	}
	//nolint:errcheck // deliberately advisory, see doc comment
	a.Store.RecordSnapshot(scope, vars, source, note, now())
}

// scopeVars reads whatever layer a scope addresses.
func (a *App) scopeVars(scope store.Scope) (map[string]string, error) {
	switch scope.Kind {
	case store.ScopeGroup:
		return a.Store.GroupVars(scope.A)
	case store.ScopeRepo:
		return a.Store.RepoVars(scope.A)
	case store.ScopeSharedGroup:
		return a.Store.SharedGroupVars(scope.A)
	case store.ScopeWorkflowRepo:
		return a.Store.WorkflowRepoVars(scope.A, scope.B)
	default:
		return nil, fmt.Errorf("unknown scope %q", scope.Kind)
	}
}

// varScope picks the timeline a `prizm var`-shaped write belongs to.
func varScope(wf store.Workflow, repo store.Repo, scoped bool) store.Scope {
	if scoped {
		return store.WorkflowRepoScope(wf.ID, repo.ID)
	}
	return store.RepoScope(repo.ID)
}
