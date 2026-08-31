package cli

import "testing"

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
