package syncplan

import (
	"path/filepath"
	"testing"

	"github.com/troglodytto/prizm/internal/crypto"
	"github.com/troglodytto/prizm/internal/resolve"
	"github.com/troglodytto/prizm/internal/sharedfile"
	"github.com/troglodytto/prizm/internal/store"
)

type fixture struct {
	store  *store.Store
	wf     store.Workflow
	repo   store.Repo
	layers []resolve.Layer
}

func newFixture(t *testing.T) fixture {
	t.Helper()

	s, err := store.Open(filepath.Join(t.TempDir(), "prizm.db"), crypto.Plaintext{})
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { s.Close() })

	g, _ := s.CreateGroup("acme")
	be, _ := s.AddRepo(g.ID, "backend", "/code/backend", "")
	auth, _ := s.AddRepo(g.ID, "auth", "/code/auth", "")
	wf, _ := s.AddWorkflow(g.ID, "local", "", []int64{be.ID, auth.ID})

	s.SetGroupVar(g.ID, "_PRIZM_CLUSTER", "cluster.internal")
	s.SetGroupVar(g.ID, "REGION", "ap-south-1")

	sg, _ := s.CreateSharedGroup(wf.ID, "db")
	s.AddSharedGroupRepo(sg.ID, be.ID)
	s.AddSharedGroupRepo(sg.ID, auth.ID)
	s.SetSharedGroupVar(sg.ID, "_PRIZM_DB_URL", "postgres://old-host/app")
	s.SetSharedGroupVar(sg.ID, "SHARED_LITERAL", "shared-value")

	s.SetRepoVar(be.ID, "LOG_LEVEL", "debug")
	s.SetWorkflowRepoVar(wf.ID, be.ID, "PORT", "8080")
	s.SetWorkflowRepoVar(wf.ID, be.ID, "DB_URL", "${_PRIZM_DB_URL}")
	s.SetWorkflowRepoVar(wf.ID, be.ID, "COMPOSITE", "${_PRIZM_DB_URL}?sslmode=disable")

	layers, err := resolve.ForRepoLayers(s, wf, be)
	if err != nil {
		t.Fatalf("ForRepoLayers() error = %v", err)
	}
	return fixture{store: s, wf: wf, repo: be, layers: layers}
}

func buildOne(t *testing.T, f fixture, d sharedfile.Diff, pin bool) Item {
	t.Helper()

	plan, err := Build(f.store, f.wf, f.repo, f.layers, d, map[string]string{"BRAND_NEW": "value"}, pin)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(plan.Items) != 1 {
		t.Fatalf("Build() = %d items, want 1", len(plan.Items))
	}
	return plan.Items[0]
}

func changed(key, from, to string) sharedfile.Diff {
	return sharedfile.Diff{Changed: []sharedfile.Change{{Key: key, From: from, To: to}}}
}

func TestNewKeyGoesToTheRepoLayer(t *testing.T) {
	item := buildOne(t, newFixture(t), sharedfile.Diff{Added: []string{"BRAND_NEW"}}, false)

	if item.Action != WriteOwningLayer || item.Origin.Kind != resolve.LayerWorkflowRepo {
		t.Errorf("got %v/%v, want WriteOwningLayer on the repo+workflow layer", item.Action, item.Origin.Kind)
	}
	if item.To != "value" {
		t.Errorf("To = %q, want the value read from disk — a diff carries only names", item.To)
	}
}

func TestEditedRepoOwnedKeyGoesToItsLayer(t *testing.T) {
	f := newFixture(t)

	for _, tt := range []struct {
		key  string
		kind resolve.LayerKind
	}{
		{"PORT", resolve.LayerWorkflowRepo},
		{"LOG_LEVEL", resolve.LayerRepoShared},
	} {
		item := buildOne(t, f, changed(tt.key, "x", "y"), false)
		if item.Action != WriteOwningLayer {
			t.Errorf("%s: Action = %v, want WriteOwningLayer", tt.key, item.Action)
		}
		if item.Origin.Kind != tt.kind {
			t.Errorf("%s: Kind = %v, want %v", tt.key, item.Origin.Kind, tt.kind)
		}
	}
}

// A literal owned by a bag propagates — but the plan must name who else moves.
func TestEditedSharedLiteralNamesItsConsumers(t *testing.T) {
	item := buildOne(t, newFixture(t), changed("SHARED_LITERAL", "shared-value", "new"), false)

	if item.Action != WriteShared {
		t.Fatalf("Action = %v, want WriteShared", item.Action)
	}
	if len(item.Consumers) != 2 {
		t.Errorf("Consumers = %v, want both member repos named", item.Consumers)
	}
}

func TestEditedGroupLiteralIsShared(t *testing.T) {
	item := buildOne(t, newFixture(t), changed("REGION", "ap-south-1", "us-east-1"), false)

	if item.Action != WriteShared || item.Origin.Kind != resolve.LayerGroup {
		t.Errorf("got %v/%v, want WriteShared on the group layer", item.Action, item.Origin.Kind)
	}
}

// The core ambiguity: an edited derived value.
func TestEditedDerivedValueIsAmbiguous(t *testing.T) {
	item := buildOne(t, newFixture(t), changed("DB_URL", "postgres://old-host/app", "postgres://new-host/app"), false)

	if item.Action != Ambiguous {
		t.Fatalf("Action = %v, want Ambiguous", item.Action)
	}
	if item.RefName != "_PRIZM_DB_URL" {
		t.Errorf("RefName = %q, want the referenced variable", item.RefName)
	}
	if item.RefOrigin.SharedGroupID == 0 {
		t.Error("RefOrigin.SharedGroupID = 0; applying 'update the shared value' needs it")
	}
	if len(item.Consumers) != 2 {
		t.Errorf("Consumers = %v, want the repos an update would reach", item.Consumers)
	}
}

func TestPinResolvesAmbiguityToTheRepoLayer(t *testing.T) {
	item := buildOne(t, newFixture(t), changed("DB_URL", "a", "b"), true)

	if item.Action != WriteOwningLayer || item.Origin.Kind != resolve.LayerWorkflowRepo {
		t.Errorf("got %v/%v, want the pin written to this repo's own layer", item.Action, item.Origin.Kind)
	}
}

// A composite template cannot be inverted, so pinning is the only option.
func TestCompositeTemplateOffersOnlyPinning(t *testing.T) {
	item := buildOne(t, newFixture(t), changed("COMPOSITE", "a", "b"), false)

	if item.Action != Ambiguous {
		t.Fatalf("Action = %v, want Ambiguous", item.Action)
	}
	if item.RefName != "" {
		t.Errorf("RefName = %q, want empty — a composite template cannot be inverted", item.RefName)
	}
	if item.Reason == "" {
		t.Error("Reason is empty; the user needs to be told why")
	}

	choices := item.Choices()
	if len(choices) != 2 || choices[0] != DecidePin {
		t.Errorf("Choices() = %v, want pin then skip", choices)
	}
}

func TestDeletedRepoOwnedKeyIsADelete(t *testing.T) {
	item := buildOne(t, newFixture(t), sharedfile.Diff{Removed: []string{"PORT"}}, false)

	if item.Action != DeleteFromOwningLayer {
		t.Errorf("Action = %v, want DeleteFromOwningLayer", item.Action)
	}
}

// Deleting a shared key from one repo's file would take it from every repo.
func TestDeletedSharedKeyIsAmbiguous(t *testing.T) {
	f := newFixture(t)

	for _, key := range []string{"SHARED_LITERAL", "REGION"} {
		item := buildOne(t, f, sharedfile.Diff{Removed: []string{key}}, false)
		if item.Action != Ambiguous {
			t.Errorf("%s: Action = %v, want Ambiguous", key, item.Action)
		}
		if item.Reason == "" {
			t.Errorf("%s: Reason is empty", key)
		}
	}
}

func TestChoicesPerAction(t *testing.T) {
	for _, tt := range []struct {
		name string
		item Item
		want []Decision
	}{
		{"plain write", Item{Action: WriteOwningLayer}, []Decision{DecideApply, DecideSkip}},
		{"shared write", Item{Action: WriteShared}, []Decision{DecideApply, DecideSkip}},
		{"delete", Item{Action: DeleteFromOwningLayer}, []Decision{DecideApply, DecideSkip}},
		{"invertible derived", Item{Action: Ambiguous, RefName: "X"}, []Decision{DecideUpdateShared, DecidePin, DecideSkip}},
		{"composite", Item{Action: Ambiguous}, []Decision{DecidePin, DecideSkip}},
	} {
		got := tt.item.Choices()
		if len(got) != len(tt.want) {
			t.Errorf("%s: Choices() = %v, want %v", tt.name, got, tt.want)
			continue
		}
		for i := range tt.want {
			if got[i] != tt.want[i] {
				t.Errorf("%s: Choices() = %v, want %v", tt.name, got, tt.want)
				break
			}
		}
	}
}

func TestEmptyDiffMakesAnEmptyPlan(t *testing.T) {
	f := newFixture(t)

	plan, err := Build(f.store, f.wf, f.repo, f.layers, sharedfile.Diff{}, nil, false)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if !plan.Empty() {
		t.Errorf("Plan = %+v, want empty", plan)
	}
}
