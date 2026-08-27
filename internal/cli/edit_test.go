package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/troglodytto/prizm/internal/store"
)

// editWith stands in for $EDITOR, rewriting the buffer the way a person would.
func (h *harness) editWith(rewrite func(string) string) {
	h.app.EditFile = func(path string) error {
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(path, []byte(rewrite(string(raw))), 0o600)
	}
}

func TestEditSavesWhatYouWrote(t *testing.T) {
	h := newHarness(t)
	fixedClock(t)
	_, repo := h.seedAudit(t)

	h.run(t, "var", "k", "auth", "PORT=4000", "OLD=gone")
	h.editWith(func(string) string { return "PORT=9999\nNEW=added\n" })

	if err := h.run(t, "edit", "k", "auth"); err != nil {
		t.Fatalf("edit: %v", err)
	}

	vars, _ := h.app.Store.RepoVars(repo.ID)
	if vars["PORT"] != "9999" || vars["NEW"] != "added" {
		t.Errorf("vars = %v, want the edited contents", vars)
	}
	if _, still := vars["OLD"]; still {
		t.Error("OLD survived — a deleted line must remove the variable")
	}
}

func TestEditShowsTheLayerItIsEditing(t *testing.T) {
	h := newHarness(t)
	fixedClock(t)
	h.seedAudit(t)
	h.run(t, "var", "k", "auth", "PORT=4000")

	var seen string
	h.editWith(func(buf string) string {
		seen = buf
		return buf
	})
	h.run(t, "edit", "k", "auth")

	if !strings.Contains(seen, "PORT=4000") {
		t.Errorf("buffer = %q, want the current values", seen)
	}
	if !strings.Contains(seen, "k/auth") {
		t.Errorf("buffer = %q, want the layer named — the same keys live in four layers", seen)
	}
}

func TestEditWithNoChangesWritesNothing(t *testing.T) {
	h := newHarness(t)
	fixedClock(t)
	_, repo := h.seedAudit(t)

	h.run(t, "var", "k", "auth", "PORT=4000")
	before, _ := h.app.Store.ListSnapshots(store.RepoScope(repo.ID))

	h.editWith(func(buf string) string { return buf })
	if err := h.run(t, "edit", "k", "auth"); err != nil {
		t.Fatalf("edit: %v", err)
	}

	after, _ := h.app.Store.ListSnapshots(store.RepoScope(repo.ID))
	if len(after) != len(before) {
		t.Errorf("versions %d → %d, want no new one for an untouched buffer", len(before), len(after))
	}
	if !strings.Contains(h.out.String(), "unchanged") {
		t.Errorf("output = %q, want it to say nothing happened", h.out.String())
	}
}

func TestEditRefusesToWipeALayer(t *testing.T) {
	h := newHarness(t)
	fixedClock(t)
	_, repo := h.seedAudit(t)

	h.run(t, "var", "k", "auth", "PORT=4000")

	// An editor that failed to launch leaves an empty buffer; so does a
	// mis-typed quit. Neither should silently delete the layer.
	h.editWith(func(string) string { return "" })
	if err := h.run(t, "edit", "k", "auth"); err != nil {
		t.Fatalf("edit: %v", err)
	}

	vars, _ := h.app.Store.RepoVars(repo.ID)
	if vars["PORT"] != "4000" {
		t.Errorf("PORT = %q, want it kept — an empty buffer is not a deletion", vars["PORT"])
	}
}

func TestEditRejectsAnUnparseableBuffer(t *testing.T) {
	h := newHarness(t)
	fixedClock(t)
	_, repo := h.seedAudit(t)

	h.run(t, "var", "k", "auth", "PORT=4000")
	h.editWith(func(string) string { return "this is not an assignment\n" })

	if err := h.run(t, "edit", "k", "auth"); err == nil {
		t.Fatal("want an error naming the parse failure")
	}

	vars, _ := h.app.Store.RepoVars(repo.ID)
	if vars["PORT"] != "4000" {
		t.Errorf("PORT = %q, want the layer untouched when the edit did not parse", vars["PORT"])
	}
}

func TestEditRecordsHistory(t *testing.T) {
	h := newHarness(t)
	fixedClock(t)
	_, repo := h.seedAudit(t)

	h.run(t, "var", "k", "auth", "PORT=4000")
	h.editWith(func(string) string { return "PORT=9999\n" })
	h.run(t, "edit", "k", "auth")

	snaps, _ := h.app.Store.ListSnapshots(store.RepoScope(repo.ID))
	if len(snaps) != 2 || snaps[0].Source != store.SourceEdit {
		t.Errorf("newest = %d versions, source %q, want an edit version recorded",
			len(snaps), snaps[0].Source)
	}
}

func TestEditWithoutAnEditorRefuses(t *testing.T) {
	h := newHarness(t)
	h.seedAudit(t)
	h.run(t, "var", "k", "auth", "PORT=4000")

	h.app.EditFile = nil
	if err := h.run(t, "edit", "k", "auth"); err == nil {
		t.Error("want a refusal rather than a silent no-op")
	}
}
