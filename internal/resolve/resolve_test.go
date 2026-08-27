package resolve

import (
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/troglodytto/prizm/internal/crypto"
	"github.com/troglodytto/prizm/internal/store"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()

	s, err := store.Open(filepath.Join(t.TempDir(), "prizm.db"), crypto.Plaintext{})
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// envFor runs the full pipeline the way `up` will.
func envFor(t *testing.T, s *store.Store, wf store.Workflow, repo store.Repo) map[string]string {
	t.Helper()

	vars, err := ForRepo(s, wf, repo)
	if err != nil {
		t.Fatalf("ForRepo(%s) error = %v", repo.Name, err)
	}
	expanded, err := Expand(vars)
	if err != nil {
		t.Fatalf("Expand(%s) error = %v", repo.Name, err)
	}
	return Emit(expanded)
}

// The spec's worked example: backend, auth and ai share derived DB credentials
// and expose them under their own names; frontend is in the same workflow but
// not in the shared bag, so it sees none of it.
func TestForRepoAssemblesAllThreeLayers(t *testing.T) {
	s := newStore(t)
	g, _ := s.CreateGroup("XYZ")
	fe, _ := s.AddRepo(g.ID, "frontend", "/code/frontend", "")
	be, _ := s.AddRepo(g.ID, "backend", "/code/backend", "")
	auth, _ := s.AddRepo(g.ID, "auth", "/code/auth", "")
	wf, _ := s.AddWorkflow(g.ID, "local", "local", []int64{fe.ID, be.ID, auth.ID})

	s.SetRepoVar(be.ID, "LOG_LEVEL", "debug")

	sg, _ := s.CreateSharedGroup(wf.ID, "db")
	s.AddSharedGroupRepo(sg.ID, be.ID)
	s.AddSharedGroupRepo(sg.ID, auth.ID)
	s.SetSharedGroupVar(sg.ID, "_PRIZM_DB_USER", "svc_app")
	s.SetSharedGroupVar(sg.ID, "_PRIZM_DB_PASS", "hunter2")
	s.SetSharedGroupVar(sg.ID, "_PRIZM_DB_URL", "postgres://${_PRIZM_DB_USER}:${_PRIZM_DB_PASS}@localhost:5432/app")

	s.SetWorkflowRepoVar(wf.ID, be.ID, "PORT", "8080")
	s.SetWorkflowRepoVar(wf.ID, be.ID, "DB_URL", "${_PRIZM_DB_URL}")
	s.SetWorkflowRepoVar(wf.ID, auth.ID, "DATABASE_URL", "${_PRIZM_DB_URL}")
	s.SetWorkflowRepoVar(wf.ID, fe.ID, "API_URL", "http://localhost:8080")

	dsn := "postgres://svc_app:hunter2@localhost:5432/app"
	want := map[string]map[string]string{
		"backend":  {"LOG_LEVEL": "debug", "PORT": "8080", "DB_URL": dsn},
		"auth":     {"DATABASE_URL": dsn},
		"frontend": {"API_URL": "http://localhost:8080"},
	}

	for _, repo := range []store.Repo{be, auth, fe} {
		if diff := cmp.Diff(want[repo.Name], envFor(t, s, wf, repo)); diff != "" {
			t.Errorf("%s env mismatch (-want +got):\n%s", repo.Name, diff)
		}
	}
}

// A repo overriding one input of a shared template changes only its own
// expansion; the stored template is untouched for everyone else.
func TestForRepoOverrideOfTemplateInput(t *testing.T) {
	s := newStore(t)
	g, _ := s.CreateGroup("XYZ")
	be, _ := s.AddRepo(g.ID, "backend", "/code/backend", "")
	auth, _ := s.AddRepo(g.ID, "auth", "/code/auth", "")
	wf, _ := s.AddWorkflow(g.ID, "local", "", []int64{be.ID, auth.ID})

	sg, _ := s.CreateSharedGroup(wf.ID, "db")
	s.AddSharedGroupRepo(sg.ID, be.ID)
	s.AddSharedGroupRepo(sg.ID, auth.ID)
	s.SetSharedGroupVar(sg.ID, "_PRIZM_DB_USER", "svc_app")
	s.SetSharedGroupVar(sg.ID, "_PRIZM_DB_URL", "postgres://${_PRIZM_DB_USER}@h/db")

	s.SetWorkflowRepoVar(wf.ID, be.ID, "DB_URL", "${_PRIZM_DB_URL}")
	s.SetWorkflowRepoVar(wf.ID, auth.ID, "DB_URL", "${_PRIZM_DB_URL}")
	s.SetWorkflowRepoVar(wf.ID, be.ID, "_PRIZM_DB_USER", "svc_backend")

	if got := envFor(t, s, wf, be)["DB_URL"]; got != "postgres://svc_backend@h/db" {
		t.Errorf("backend DB_URL = %q, want the overridden user", got)
	}
	if got := envFor(t, s, wf, auth)["DB_URL"]; got != "postgres://svc_app@h/db" {
		t.Errorf("auth DB_URL = %q, want the shared user", got)
	}
	if _, leaked := envFor(t, s, wf, be)["_PRIZM_DB_USER"]; leaked {
		t.Error("internal var leaked into backend's emitted env")
	}
}

// sync and audit compare templates, so ForRepo must not expand.
func TestForRepoReturnsTemplatesNotExpansions(t *testing.T) {
	s := newStore(t)
	g, _ := s.CreateGroup("XYZ")
	r, _ := s.AddRepo(g.ID, "backend", "/code/backend", "")
	wf, _ := s.AddWorkflow(g.ID, "local", "", []int64{r.ID})

	s.SetWorkflowRepoVar(wf.ID, r.ID, "_PRIZM_HOST", "h")
	s.SetWorkflowRepoVar(wf.ID, r.ID, "URL", "http://${_PRIZM_HOST}")

	vars, err := ForRepo(s, wf, r)
	if err != nil {
		t.Fatalf("ForRepo() error = %v", err)
	}
	if vars["URL"] != "http://${_PRIZM_HOST}" {
		t.Errorf("URL = %q, want the unexpanded template", vars["URL"])
	}
}

func TestForRepoWithNoVarsReturnsEmpty(t *testing.T) {
	s := newStore(t)
	g, _ := s.CreateGroup("XYZ")
	r, _ := s.AddRepo(g.ID, "solo", "/code/solo", "")
	wf, _ := s.AddWorkflow(g.ID, "local", "", []int64{r.ID})

	vars, err := ForRepo(s, wf, r)
	if err != nil {
		t.Fatalf("ForRepo() error = %v", err)
	}
	if len(vars) != 0 {
		t.Errorf("ForRepo() = %v, want empty", vars)
	}
}
