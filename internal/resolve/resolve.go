package resolve

import (
	"fmt"

	"github.com/troglodytto/prizm/internal/store"
)

// ForRepo assembles a repo's variable layers, lowest precedence first, and
// merges them. The result still contains ${...} templates; call Expand to
// resolve them and Emit to drop the internal plumbing.
func ForRepo(s *store.Store, wf store.Workflow, repo store.Repo) (map[string]string, error) {
	// Layer 0: facts about the whole group. Lowest precedence, so any layer
	// above can contradict one without anything being unwired first.
	groupVars, err := s.GroupVars(repo.GroupID)
	if err != nil {
		return nil, fmt.Errorf("reading group vars for %q: %w", repo.Name, err)
	}
	layers := []Layer{{Name: "group", Vars: groupVars}}

	repoVars, err := s.RepoVars(repo.ID)
	if err != nil {
		return nil, fmt.Errorf("reading repo-shared vars for %q: %w", repo.Name, err)
	}
	layers = append(layers, Layer{Name: "repo-shared", Vars: repoVars})

	sharedGroups, err := s.SharedGroupsForRepo(wf.ID, repo.ID)
	if err != nil {
		return nil, fmt.Errorf("reading shared groups for %q: %w", repo.Name, err)
	}
	for _, sg := range sharedGroups {
		vars, err := s.SharedGroupVars(sg.ID)
		if err != nil {
			return nil, fmt.Errorf("reading shared group %q: %w", sg.Name, err)
		}
		layers = append(layers, Layer{Name: "shared:" + sg.Name, Vars: vars})
	}

	specific, err := s.WorkflowRepoVars(wf.ID, repo.ID)
	if err != nil {
		return nil, fmt.Errorf("reading %s/%s vars: %w", wf.Name, repo.Name, err)
	}
	layers = append(layers, Layer{Name: wf.Name + "+" + repo.Name, Vars: specific})

	return Merge(layers), nil
}
