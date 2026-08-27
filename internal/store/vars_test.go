package store

import (
	"bytes"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestRepoVarsRoundTrip(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")
	r, _ := s.AddRepo(g.ID, "backend", "/code/backend", "")

	if err := s.SetRepoVar(r.ID, "LOG_LEVEL", "debug"); err != nil {
		t.Fatalf("SetRepoVar() error = %v", err)
	}

	got, err := s.RepoVars(r.ID)
	if err != nil {
		t.Fatalf("RepoVars() error = %v", err)
	}
	if diff := cmp.Diff(map[string]string{"LOG_LEVEL": "debug"}, got); diff != "" {
		t.Errorf("RepoVars() mismatch (-want +got):\n%s", diff)
	}
}

func TestSetRepoVarUpserts(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")
	r, _ := s.AddRepo(g.ID, "backend", "/code/backend", "")

	s.SetRepoVar(r.ID, "K", "first")
	if err := s.SetRepoVar(r.ID, "K", "second"); err != nil {
		t.Fatalf("second SetRepoVar() error = %v", err)
	}

	got, _ := s.RepoVars(r.ID)
	if got["K"] != "second" {
		t.Errorf("K = %q, want %q", got["K"], "second")
	}
}

func TestSetVarRejectsInvalidKeys(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")
	r, _ := s.AddRepo(g.ID, "backend", "/code/backend", "")

	for _, key := range []string{"", "1LEADING_DIGIT", "has-dash", "has space", "has=equals"} {
		if err := s.SetRepoVar(r.ID, key, "v"); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("SetRepoVar(%q) error = %v, want ErrInvalidKey", key, err)
		}
	}
}

func TestSetVarAcceptsInternalKeys(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")
	r, _ := s.AddRepo(g.ID, "backend", "/code/backend", "")

	if err := s.SetRepoVar(r.ID, "_PRIZM_DB_PASS", "hunter2"); err != nil {
		t.Errorf("SetRepoVar(_PRIZM_DB_PASS) error = %v, want nil", err)
	}
}

// The store is a dumb template holder: it must never expand anything.
func TestStoreKeepsTemplatesLiteral(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")
	r, _ := s.AddRepo(g.ID, "backend", "/code/backend", "")

	tmpl := "postgres://${_PRIZM_DB_USER}:${_PRIZM_DB_PASS}@h/db"
	s.SetRepoVar(r.ID, "_PRIZM_DB_URL", tmpl)

	got, _ := s.RepoVars(r.ID)
	if got["_PRIZM_DB_URL"] != tmpl {
		t.Errorf("_PRIZM_DB_URL = %q, want the literal template %q", got["_PRIZM_DB_URL"], tmpl)
	}
}

func TestWorkflowRepoVarsRoundTrip(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")
	r, _ := s.AddRepo(g.ID, "frontend", "/code/frontend", "")
	wf, _ := s.AddWorkflow(g.ID, "local", "", []int64{r.ID})

	if err := s.SetWorkflowRepoVar(wf.ID, r.ID, "PORT", "3000"); err != nil {
		t.Fatalf("SetWorkflowRepoVar() error = %v", err)
	}

	got, err := s.WorkflowRepoVars(wf.ID, r.ID)
	if err != nil {
		t.Fatalf("WorkflowRepoVars() error = %v", err)
	}
	if diff := cmp.Diff(map[string]string{"PORT": "3000"}, got); diff != "" {
		t.Errorf("WorkflowRepoVars() mismatch (-want +got):\n%s", diff)
	}
}

func TestWorkflowRepoVarsAreScopedPerWorkflow(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")
	r, _ := s.AddRepo(g.ID, "backend", "/code/backend", "")
	local, _ := s.AddWorkflow(g.ID, "local", "", []int64{r.ID})
	prod, _ := s.AddWorkflow(g.ID, "production", "prod", []int64{r.ID})

	s.SetWorkflowRepoVar(local.ID, r.ID, "API", "http://localhost:8080")
	s.SetWorkflowRepoVar(prod.ID, r.ID, "API", "https://api.example.com")

	gotLocal, _ := s.WorkflowRepoVars(local.ID, r.ID)
	gotProd, _ := s.WorkflowRepoVars(prod.ID, r.ID)

	if gotLocal["API"] != "http://localhost:8080" {
		t.Errorf("local API = %q", gotLocal["API"])
	}
	if gotProd["API"] != "https://api.example.com" {
		t.Errorf("prod API = %q", gotProd["API"])
	}
}

func TestSharedGroupMembershipAndVars(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")
	be, _ := s.AddRepo(g.ID, "backend", "/code/backend", "")
	auth, _ := s.AddRepo(g.ID, "auth", "/code/auth", "")
	fe, _ := s.AddRepo(g.ID, "frontend", "/code/frontend", "")
	wf, _ := s.AddWorkflow(g.ID, "local", "", []int64{be.ID, auth.ID, fe.ID})

	sg, err := s.CreateSharedGroup(wf.ID, "db")
	if err != nil {
		t.Fatalf("CreateSharedGroup() error = %v", err)
	}
	if err := s.AddSharedGroupRepo(sg.ID, be.ID); err != nil {
		t.Fatalf("AddSharedGroupRepo() error = %v", err)
	}
	s.AddSharedGroupRepo(sg.ID, auth.ID)

	s.SetSharedGroupVar(sg.ID, "_PRIZM_DB_USER", "svc_app")
	s.SetSharedGroupVar(sg.ID, "_PRIZM_DB_PASS", "hunter2")
	s.SetSharedGroupVar(sg.ID, "_PRIZM_DB_URL", "postgres://${_PRIZM_DB_USER}:${_PRIZM_DB_PASS}@localhost:5432/app")

	vars, err := s.SharedGroupVars(sg.ID)
	if err != nil {
		t.Fatalf("SharedGroupVars() error = %v", err)
	}
	if len(vars) != 3 {
		t.Errorf("SharedGroupVars() = %d entries, want 3", len(vars))
	}

	memberOf, err := s.SharedGroupsForRepo(wf.ID, be.ID)
	if err != nil {
		t.Fatalf("SharedGroupsForRepo() error = %v", err)
	}
	if len(memberOf) != 1 || memberOf[0].Name != "db" {
		t.Errorf("backend shared groups = %+v, want one named db", memberOf)
	}

	none, err := s.SharedGroupsForRepo(wf.ID, fe.ID)
	if err != nil {
		t.Fatalf("SharedGroupsForRepo(frontend) error = %v", err)
	}
	if len(none) != 0 {
		t.Errorf("frontend shared groups = %+v, want none", none)
	}
}

func TestSharedGroupsForRepoOrderedByName(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")
	r, _ := s.AddRepo(g.ID, "auth", "/code/auth", "")
	wf, _ := s.AddWorkflow(g.ID, "local", "", []int64{r.ID})

	for _, name := range []string{"zzz", "aaa", "mmm"} {
		sg, _ := s.CreateSharedGroup(wf.ID, name)
		s.AddSharedGroupRepo(sg.ID, r.ID)
	}

	got, err := s.SharedGroupsForRepo(wf.ID, r.ID)
	if err != nil {
		t.Fatalf("SharedGroupsForRepo() error = %v", err)
	}
	var names []string
	for _, sg := range got {
		names = append(names, sg.Name)
	}
	if diff := cmp.Diff([]string{"aaa", "mmm", "zzz"}, names); diff != "" {
		t.Errorf("order mismatch (-want +got):\n%s", diff)
	}
}

func TestCreateSharedGroupRejectsDuplicate(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")
	wf, _ := s.AddWorkflow(g.ID, "local", "", nil)

	s.CreateSharedGroup(wf.ID, "db")
	if _, err := s.CreateSharedGroup(wf.ID, "db"); !errors.Is(err, ErrExists) {
		t.Errorf("duplicate CreateSharedGroup() error = %v, want ErrExists", err)
	}
}

// The whole point of the crypto layer: a leaked DB file must not leak secrets.
func TestValuesAreEncryptedAtRest(t *testing.T) {
	s := newEncryptedTestStore(t)
	g, _ := s.CreateGroup("XYZ")
	r, _ := s.AddRepo(g.ID, "backend", "/code/backend", "")

	if err := s.SetRepoVar(r.ID, "_PRIZM_DB_PASS", "SUPERSECRET"); err != nil {
		t.Fatalf("SetRepoVar() error = %v", err)
	}

	var blob []byte
	if err := s.db.QueryRow(`SELECT value FROM repo_vars WHERE repo_id = ?`, r.ID).Scan(&blob); err != nil {
		t.Fatalf("reading raw value: %v", err)
	}
	if bytes.Contains(blob, []byte("SUPERSECRET")) {
		t.Error("value stored in plaintext")
	}

	got, err := s.RepoVars(r.ID)
	if err != nil {
		t.Fatalf("RepoVars() error = %v", err)
	}
	if got["_PRIZM_DB_PASS"] != "SUPERSECRET" {
		t.Errorf("decrypted value = %q, want %q", got["_PRIZM_DB_PASS"], "SUPERSECRET")
	}

	// Metadata must stay queryable in plaintext for fast completion.
	var name string
	if err := s.db.QueryRow(`SELECT name FROM repos WHERE id = ?`, r.ID).Scan(&name); err != nil {
		t.Fatalf("reading repo name: %v", err)
	}
	if name != "backend" {
		t.Errorf("repo name = %q, want plaintext %q", name, "backend")
	}
}
