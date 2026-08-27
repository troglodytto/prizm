package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestParseAssignment(t *testing.T) {
	tests := []struct {
		in        string
		wantKey   string
		wantValue string
		wantErr   bool
	}{
		{in: "A=1", wantKey: "A", wantValue: "1"},
		{in: "DSN=postgres://u:p@h/db?a=b", wantKey: "DSN", wantValue: "postgres://u:p@h/db?a=b"},
		{in: "EMPTY=", wantKey: "EMPTY", wantValue: ""},
		{in: "_PRIZM_PASS=hunter2", wantKey: "_PRIZM_PASS", wantValue: "hunter2"},
		{in: "TEMPLATE=${_PRIZM_DB_URL}", wantKey: "TEMPLATE", wantValue: "${_PRIZM_DB_URL}"},
		{in: "NOEQUALS", wantErr: true},
		{in: "=novalue", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			key, value, err := parseAssignment(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseAssignment(%q) error = nil, want error", tt.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseAssignment(%q) error = %v", tt.in, err)
			}
			if key != tt.wantKey || value != tt.wantValue {
				t.Errorf("parseAssignment(%q) = (%q, %q), want (%q, %q)", tt.in, key, value, tt.wantKey, tt.wantValue)
			}
		})
	}
}

func (h *harness) seedGroup(t *testing.T) {
	t.Helper()

	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend")
	h.run(t, "add-repo", "XYZ", "frontend")
	h.run(t, "add-workflow", "XYZ", "local")
}

func TestVarSetsRepoSharedLayer(t *testing.T) {
	h := newHarness(t)
	h.seedGroup(t)

	if err := h.run(t, "var", "XYZ", "backend", "LOG_LEVEL=debug"); err != nil {
		t.Fatalf("var error = %v", err)
	}

	g, _ := h.app.Store.GroupByName("XYZ")
	repo, _ := h.app.Store.RepoByName(g.ID, "backend")
	got, _ := h.app.Store.RepoVars(repo.ID)
	if diff := cmp.Diff(map[string]string{"LOG_LEVEL": "debug"}, got); diff != "" {
		t.Errorf("RepoVars() mismatch (-want +got):\n%s", diff)
	}
}

func TestVarSetsWorkflowLayerWithFlag(t *testing.T) {
	h := newHarness(t)
	h.seedGroup(t)

	if err := h.run(t, "var", "XYZ", "backend", "PORT=8080", "--workflow", "local"); err != nil {
		t.Fatalf("var error = %v", err)
	}

	g, _ := h.app.Store.GroupByName("XYZ")
	repo, _ := h.app.Store.RepoByName(g.ID, "backend")
	wf, _ := h.app.Store.WorkflowByName(g.ID, "local")

	got, _ := h.app.Store.WorkflowRepoVars(wf.ID, repo.ID)
	if diff := cmp.Diff(map[string]string{"PORT": "8080"}, got); diff != "" {
		t.Errorf("WorkflowRepoVars() mismatch (-want +got):\n%s", diff)
	}
	if shared, _ := h.app.Store.RepoVars(repo.ID); len(shared) != 0 {
		t.Errorf("repo-shared layer = %v, want empty — --workflow must scope the write", shared)
	}
}

func TestVarSetsSeveralAssignmentsAtOnce(t *testing.T) {
	h := newHarness(t)
	h.seedGroup(t)

	if err := h.run(t, "var", "XYZ", "backend", "A=1", "B=2", "C=3"); err != nil {
		t.Fatalf("var error = %v", err)
	}

	g, _ := h.app.Store.GroupByName("XYZ")
	repo, _ := h.app.Store.RepoByName(g.ID, "backend")
	if got, _ := h.app.Store.RepoVars(repo.ID); len(got) != 3 {
		t.Errorf("RepoVars() = %v, want 3 entries", got)
	}
}

func TestVarStoresTemplateVerbatim(t *testing.T) {
	h := newHarness(t)
	h.seedGroup(t)

	if err := h.run(t, "var", "XYZ", "backend", "DB_URL=${_PRIZM_DB_URL}"); err != nil {
		t.Fatalf("var error = %v", err)
	}

	g, _ := h.app.Store.GroupByName("XYZ")
	repo, _ := h.app.Store.RepoByName(g.ID, "backend")
	got, _ := h.app.Store.RepoVars(repo.ID)
	if got["DB_URL"] != "${_PRIZM_DB_URL}" {
		t.Errorf("DB_URL = %q, want the literal template", got["DB_URL"])
	}
}

func TestVarRejectsUnknownRepo(t *testing.T) {
	h := newHarness(t)
	h.seedGroup(t)

	if err := h.run(t, "var", "XYZ", "ghost", "A=1"); err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error = %v, want it to name the unknown repo", err)
	}
}

func TestVarRejectsUnknownWorkflow(t *testing.T) {
	h := newHarness(t)
	h.seedGroup(t)

	if err := h.run(t, "var", "XYZ", "backend", "A=1", "--workflow", "ghost"); err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error = %v, want it to name the unknown workflow", err)
	}
}

func TestImportLoadsAnEnvFile(t *testing.T) {
	h := newHarness(t)
	h.seedGroup(t)

	path := filepath.Join(t.TempDir(), ".env.local")
	body := "# a comment\nexport PORT=8080\nDSN=\"postgres://u:p@h/db\"\n\nDEBUG=true\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	if err := h.run(t, "import", "XYZ", "backend", path, "--workflow", "local"); err != nil {
		t.Fatalf("import error = %v", err)
	}

	g, _ := h.app.Store.GroupByName("XYZ")
	repo, _ := h.app.Store.RepoByName(g.ID, "backend")
	wf, _ := h.app.Store.WorkflowByName(g.ID, "local")

	got, _ := h.app.Store.WorkflowRepoVars(wf.ID, repo.ID)
	want := map[string]string{"PORT": "8080", "DSN": "postgres://u:p@h/db", "DEBUG": "true"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("imported vars mismatch (-want +got):\n%s", diff)
	}
}

func TestImportReportsHowManyVarsItLoaded(t *testing.T) {
	h := newHarness(t)
	h.seedGroup(t)

	path := filepath.Join(t.TempDir(), ".env")
	os.WriteFile(path, []byte("A=1\nB=2\n"), 0o600)

	if err := h.run(t, "import", "XYZ", "backend", path); err != nil {
		t.Fatalf("import error = %v", err)
	}
	if !strings.Contains(h.out.String(), "2") {
		t.Errorf("output = %q, want it to report the count", h.out.String())
	}
}

func TestImportMissingFile(t *testing.T) {
	h := newHarness(t)
	h.seedGroup(t)

	if err := h.run(t, "import", "XYZ", "backend", filepath.Join(t.TempDir(), "nope.env")); err == nil {
		t.Fatal("import of a missing file error = nil, want error")
	}
}
