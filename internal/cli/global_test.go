package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGlobalWritesStraightToTheDatabase(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "XYZ")

	// editWith stubs $EDITOR. The old implementation shelled out to
	// exec.Command instead, so it never saw this and wrote nothing.
	h.editWith(func(string) string { return "REGION=us-east-2\n" })

	if err := h.run(t, "global", "XYZ"); err != nil {
		t.Fatalf("global: %v", err)
	}

	g, err := h.app.Store.GroupByName("XYZ")
	if err != nil {
		t.Fatalf("GroupByName: %v", err)
	}
	vars, err := h.app.Store.GroupVars(g.ID)
	if err != nil {
		t.Fatalf("GroupVars: %v", err)
	}
	if vars["REGION"] != "us-east-2" {
		t.Errorf("REGION = %q, want %q — `global` must land in the database with no shared-sync",
			vars["REGION"], "us-east-2")
	}
}

// A bag sync must not touch the group layer. It used to: shared-sync called
// syncAllGlobals, which reconciled a global.env that no direct edit path ever
// rewrote — so the stale file's silence deleted every group variable.
func TestBagSyncLeavesGroupGlobalsAlone(t *testing.T) {
	h := newHarness(t)
	h.sharedFixture(t)

	if err := h.run(t, "var", "XYZ", "--global", "REGION=us-east-2"); err != nil {
		t.Fatalf("var --global: %v", err)
	}

	// This is a deliberate trip-wire, not a simulation of something harmless.
	// At HEAD nothing reads shared/<group>/global.env, so an empty file here
	// should have zero effect on the group layer; if a file-backed group
	// layer is ever reintroduced at this exact path, this fixture is what
	// catches it deleting REGION again. The path is hardcoded because
	// config.GlobalPath was removed along with the old layer — there is no
	// helper left to build it. Do not delete this fixture just because no
	// production code currently produces the file; that absence is the
	// point.
	dataDir := os.Getenv("XDG_DATA_HOME")
	if dataDir == "" {
		t.Fatal("XDG_DATA_HOME not set")
	}
	globalDir := filepath.Join(dataDir, "prizm", "shared", "XYZ")
	if err := os.MkdirAll(globalDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	globalPath := filepath.Join(globalDir, "global.env")
	if err := os.WriteFile(globalPath, []byte(""), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := h.run(t, "shared-sync", "XYZ", "local", "db", "--yes"); err != nil {
		t.Fatalf("shared-sync: %v", err)
	}

	g, err := h.app.Store.GroupByName("XYZ")
	if err != nil {
		t.Fatalf("GroupByName: %v", err)
	}
	vars, err := h.app.Store.GroupVars(g.ID)
	if err != nil {
		t.Fatalf("GroupVars: %v", err)
	}
	if vars["REGION"] != "us-east-2" {
		t.Errorf("REGION = %q after a bag sync, want %q — syncing one bag must not "+
			"reconcile the group layer", vars["REGION"], "us-east-2")
	}

	// The deleted syncAllGlobals also had a no-arg branch that looped every
	// group's bags. Cover the unscoped form too, so both ways of invoking
	// shared-sync are checked by this one test.
	if err := h.run(t, "shared-sync", "--yes"); err != nil {
		t.Fatalf("shared-sync (unscoped): %v", err)
	}

	vars, err = h.app.Store.GroupVars(g.ID)
	if err != nil {
		t.Fatalf("GroupVars: %v", err)
	}
	if vars["REGION"] != "us-east-2" {
		t.Errorf("REGION = %q after an unscoped shared-sync, want %q — syncing all bags "+
			"must not reconcile the group layer", vars["REGION"], "us-east-2")
	}
}
