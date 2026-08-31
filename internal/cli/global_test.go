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

	// Create an empty global.env file to simulate the stale file that would
	// have been created by the old `prizm global` workflow.
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
}
