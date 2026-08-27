package store

import (
	"testing"
	"time"
)

// SQLite reissues a freed rowid to the next row inserted, so a new repo can
// inherit a deleted one's id. If the deleted repo's snapshots survive, the new
// repo inherits its audit trail — and `audit --restore` writes a stranger's
// credentials into it.
func TestDeletingARepoTakesItsHistory(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("k")
	gone, _ := s.AddRepo(g.ID, "gone", "/tmp/gone", ".env")

	scope := RepoScope(gone.ID)
	if _, err := s.RecordSnapshot(scope,
		map[string]string{"STRIPE_SECRET": "sk_live_supersecret"},
		SourceVar, "set", time.Unix(1700000000, 0)); err != nil {
		t.Fatalf("RecordSnapshot: %v", err)
	}

	if err := s.DeleteRepo(gone.ID); err != nil {
		t.Fatalf("DeleteRepo: %v", err)
	}

	// The successor takes the freed id.
	next, err := s.AddRepo(g.ID, "next", "/tmp/next", ".env")
	if err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}
	if next.ID != gone.ID {
		t.Logf("id not reused (%d vs %d); the check below still has to hold", next.ID, gone.ID)
	}

	snaps, err := s.ListSnapshots(RepoScope(next.ID))
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(snaps) != 0 {
		vars, _ := s.SnapshotVars(snaps[0].ID)
		t.Fatalf("the new repo inherited %d snapshot(s) from the deleted one: %v", len(snaps), vars)
	}
}

func TestDeletingAWorkflowTakesItsHistory(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("k")
	r, _ := s.AddRepo(g.ID, "api", "/tmp/api", ".env")
	wf, _ := s.AddWorkflow(g.ID, "local", "", nil)

	if _, err := s.RecordSnapshot(WorkflowRepoScope(wf.ID, r.ID),
		map[string]string{"TOKEN": "secret"}, SourceVar, "set", time.Unix(1700000000, 0)); err != nil {
		t.Fatalf("RecordSnapshot: %v", err)
	}
	if err := s.DeleteWorkflow(wf.ID); err != nil {
		t.Fatalf("DeleteWorkflow: %v", err)
	}

	snaps, _ := s.ListSnapshots(WorkflowRepoScope(wf.ID, r.ID))
	if len(snaps) != 0 {
		t.Errorf("workflow deleted but %d snapshot(s) remain", len(snaps))
	}
}

func TestDeletingAGroupTakesEveryTimelineUnderIt(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("k")
	r, _ := s.AddRepo(g.ID, "api", "/tmp/api", ".env")
	wf, _ := s.AddWorkflow(g.ID, "local", "", nil)
	bag, _ := s.CreateSharedGroup(wf.ID, "infra")

	now := time.Unix(1700000000, 0)
	for _, sc := range []Scope{
		GroupScope(g.ID), RepoScope(r.ID),
		WorkflowRepoScope(wf.ID, r.ID), SharedGroupScope(bag.ID),
	} {
		if _, err := s.RecordSnapshot(sc, map[string]string{"K": "v"}, SourceVar, "set", now); err != nil {
			t.Fatalf("RecordSnapshot(%v): %v", sc.Kind, err)
		}
	}

	if err := s.DeleteGroup(g.ID); err != nil {
		t.Fatalf("DeleteGroup: %v", err)
	}

	for _, sc := range []Scope{
		GroupScope(g.ID), RepoScope(r.ID),
		WorkflowRepoScope(wf.ID, r.ID), SharedGroupScope(bag.ID),
	} {
		if snaps, _ := s.ListSnapshots(sc); len(snaps) != 0 {
			t.Errorf("%s timeline survived the group: %d snapshot(s)", sc.Kind, len(snaps))
		}
	}
}
