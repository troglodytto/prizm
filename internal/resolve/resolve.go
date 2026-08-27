package resolve

import (
	"fmt"

	"github.com/troglodytto/prizm/internal/store"
)

// ForRepo merges a repo's layers. The result still contains ${...} templates;
// call Expand to resolve them and Emit to drop the internal plumbing.
func ForRepo(s *store.Store, wf store.Workflow, repo store.Repo) (map[string]string, error) {
	layers, err := ForRepoLayers(s, wf, repo)
	if err != nil {
		return nil, err
	}
	return Merge(layers), nil
}

// ForRepoLayers assembles a repo's variable layers, lowest precedence first,
// without merging them.
//
// Reconciliation needs the layers intact: knowing that a key changed is
// useless without knowing which layer owns it, because that is what decides
// where the change has to be written back.
func ForRepoLayers(s *store.Store, wf store.Workflow, repo store.Repo) ([]Layer, error) {
	// Layer 0: facts about the whole group. Lowest precedence, so any layer
	// above can contradict one without anything being unwired first.
	groupVars, err := s.GroupVars(repo.GroupID)
	if err != nil {
		return nil, fmt.Errorf("reading group vars for %q: %w", repo.Name, err)
	}
	layers := []Layer{{Name: "group", Kind: LayerGroup, GroupID: repo.GroupID, Vars: groupVars}}

	repoVars, err := s.RepoVars(repo.ID)
	if err != nil {
		return nil, fmt.Errorf("reading repo-shared vars for %q: %w", repo.Name, err)
	}
	layers = append(layers, Layer{Name: "repo-shared", Kind: LayerRepoShared, Vars: repoVars})

	sharedGroups, err := s.SharedGroupsForRepo(wf.ID, repo.ID)
	if err != nil {
		return nil, fmt.Errorf("reading shared groups for %q: %w", repo.Name, err)
	}
	for _, sg := range sharedGroups {
		vars, err := s.SharedGroupVars(sg.ID)
		if err != nil {
			return nil, fmt.Errorf("reading shared group %q: %w", sg.Name, err)
		}
		layers = append(layers, Layer{
			Name:          "shared:" + sg.Name,
			Kind:          LayerSharedGroup,
			SharedGroupID: sg.ID,
			Vars:          vars,
		})
	}

	specific, err := s.WorkflowRepoVars(wf.ID, repo.ID)
	if err != nil {
		return nil, fmt.Errorf("reading %s/%s vars: %w", wf.Name, repo.Name, err)
	}
	layers = append(layers, Layer{
		Name: wf.Name + "+" + repo.Name,
		Kind: LayerWorkflowRepo,
		Vars: specific,
	})

	return layers, nil
}
