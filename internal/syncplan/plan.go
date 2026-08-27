// Package syncplan classifies hand-edits found in a repo's env file into
// actions that can be explained before any of them are applied.
//
// Knowing a key changed is the easy half. The hard half is where the change
// belongs, because a repo's env file is a flattened view of four layers and
// an edit to one line could mean four different things.
package syncplan

import (
	"fmt"
	"strings"

	"github.com/troglodytto/prizm/internal/resolve"
	"github.com/troglodytto/prizm/internal/sharedfile"
	"github.com/troglodytto/prizm/internal/store"
)

// Action is what sync would do about one edited key.
type Action int

const (
	// WriteOwningLayer writes to the layer that defined the key, or to the
	// repo+workflow layer when the key is new.
	WriteOwningLayer Action = iota
	// WriteShared writes to a shared bag or the group layer, changing every
	// repo that reads from it.
	WriteShared
	// DeleteFromOwningLayer removes a key its own layer defined.
	DeleteFromOwningLayer
	// Ambiguous means prizm will not guess. Skipped unless the user chooses.
	Ambiguous
)

// Item is one classified edit.
type Item struct {
	Key       string
	From      string
	To        string
	Action    Action
	Origin    resolve.Origin
	RefName   string         // set when the template is exactly ${RefName}
	RefOrigin resolve.Origin // where RefName lives, for DecideUpdateShared
	Consumers []string       // repos affected if something shared is written
	Reason    string         // why this needs a decision, in the user's terms
}

// Plan is everything sync would do for one repo.
type Plan struct {
	Repo     store.Repo
	Workflow store.Workflow
	Items    []Item
}

// Empty reports whether there is nothing to do.
func (p Plan) Empty() bool { return len(p.Items) == 0 }

// Decision is what the user chose for one item.
type Decision int

const (
	// DecideSkip leaves the edit alone.
	DecideSkip Decision = iota
	// DecideApply performs the item's classified action.
	DecideApply
	// DecideUpdateShared writes the new value to the referenced shared
	// variable, changing every repo that consumes it.
	DecideUpdateShared
	// DecidePin writes the expanded literal to this repo's own layer,
	// breaking its link to the shared value.
	DecidePin
)

// Label is the user-facing name of a decision.
func (d Decision) Label() string {
	switch d {
	case DecideApply:
		return "apply"
	case DecideUpdateShared:
		return "update the shared value"
	case DecidePin:
		return "pin this value here only"
	}
	return "skip"
}

// Choices returns the legal decisions for this item, best default first. The
// rules live here rather than in the interface so both the picker and the
// non-interactive path obey the same ones.
func (i Item) Choices() []Decision {
	if i.Action != Ambiguous {
		return []Decision{DecideApply, DecideSkip}
	}
	if i.RefName != "" {
		return []Decision{DecideUpdateShared, DecidePin, DecideSkip}
	}
	return []Decision{DecidePin, DecideSkip}
}

// Build classifies a drift diff. onDisk supplies the values behind it, since
// a diff carries only names for added keys. pin forces derived-value
// ambiguities to resolve as a literal on this repo's own layer.
func Build(s *store.Store, wf store.Workflow, repo store.Repo, layers []resolve.Layer, d sharedfile.Diff, onDisk map[string]string, pin bool) (Plan, error) {
	plan := Plan{Repo: repo, Workflow: wf}
	own := resolve.Origin{Layer: wf.Name + "+" + repo.Name, Kind: resolve.LayerWorkflowRepo}

	// Keys the human added: nothing else can own them.
	for _, key := range d.Added {
		plan.Items = append(plan.Items, Item{
			Key:    key,
			To:     onDisk[key],
			Action: WriteOwningLayer,
			Origin: own,
		})
	}

	for _, change := range d.Changed {
		item, err := classifyChange(s, repo, layers, change, own, pin)
		if err != nil {
			return Plan{}, err
		}
		plan.Items = append(plan.Items, item)
	}

	for _, key := range d.Removed {
		item, err := classifyRemoval(s, layers, key)
		if err != nil {
			return Plan{}, err
		}
		plan.Items = append(plan.Items, item)
	}

	return plan, nil
}

func classifyChange(s *store.Store, repo store.Repo, layers []resolve.Layer, c sharedfile.Change, own resolve.Origin, pin bool) (Item, error) {
	item := Item{Key: c.Key, From: c.From, To: c.To}

	origin, found := resolve.Attribute(layers, c.Key)
	if !found {
		item.Action, item.Origin = WriteOwningLayer, own
		return item, nil
	}
	item.Origin = origin

	// A derived value: the human edited an expansion, not the template. This
	// is the case prizm refuses to guess at, because "the shared thing moved"
	// and "only this repo is different" produce the identical edit.
	if resolve.IsTemplate(origin.Template) {
		if pin {
			item.Action, item.Origin = WriteOwningLayer, own
			return item, nil
		}

		item.Action = Ambiguous
		ref, invertible := resolve.SoleRef(origin.Template)
		if !invertible {
			item.Reason = fmt.Sprintf(
				"built from %s in %s, which prizm cannot take apart — edit that template, or pin a literal here",
				origin.Template, origin.Layer)
			return item, nil
		}

		item.RefName = ref
		if refOrigin, ok := resolve.Attribute(layers, ref); ok {
			item.RefOrigin = refOrigin

			consumers, err := sharedConsumers(s, refOrigin)
			if err != nil {
				return Item{}, err
			}
			item.Consumers = consumers
		}
		item.Reason = fmt.Sprintf("comes from ${%s} in %s", ref, item.RefOrigin.Layer)
		return item, nil
	}

	// A literal. Where it lives decides how far the change reaches.
	if origin.Kind == resolve.LayerSharedGroup || origin.Kind == resolve.LayerGroup {
		consumers, err := sharedConsumers(s, origin)
		if err != nil {
			return Item{}, err
		}
		item.Action, item.Consumers = WriteShared, consumers
		return item, nil
	}

	item.Action = WriteOwningLayer
	return item, nil
}

func classifyRemoval(s *store.Store, layers []resolve.Layer, key string) (Item, error) {
	item := Item{Key: key}

	origin, found := resolve.Attribute(layers, key)
	if !found {
		item.Action = Ambiguous
		item.Reason = "not defined in any layer — nothing to remove"
		return item, nil
	}
	item.Origin = origin

	// Deleting a shared value from one repo's file is not expressible as a
	// deletion: it would take it away from every other repo too.
	if origin.Kind == resolve.LayerSharedGroup || origin.Kind == resolve.LayerGroup {
		consumers, err := sharedConsumers(s, origin)
		if err != nil {
			return Item{}, err
		}

		item.Action, item.Consumers = Ambiguous, consumers
		item.Reason = fmt.Sprintf("defined in %s, which also feeds %s",
			origin.Layer, joinOr(consumers, "other repos"))
		return item, nil
	}

	item.Action = DeleteFromOwningLayer
	return item, nil
}

// sharedConsumers names the repos a shared write would reach.
func sharedConsumers(s *store.Store, origin resolve.Origin) ([]string, error) {
	if origin.Kind != resolve.LayerSharedGroup {
		return nil, nil // the group layer reaches everything; saying so is the caller's job
	}

	repos, err := s.SharedGroupRepos(origin.SharedGroupID)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(repos))
	for _, r := range repos {
		names = append(names, r.Name)
	}
	return names, nil
}

func joinOr(names []string, fallback string) string {
	if len(names) == 0 {
		return fallback
	}
	return strings.Join(names, ", ")
}
