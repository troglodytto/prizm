package store

import (
	"errors"
	"testing"
)

func TestValidName(t *testing.T) {
	valid := []string{"auth", "acme-auth", "enterprise_search", "svc.v2", "a", "web3"}
	for _, n := range valid {
		if !ValidName(n) {
			t.Errorf("ValidName(%q) = false, want true", n)
		}
	}

	// Every one of these would either escape the data directory or be
	// untypeable as a command argument.
	invalid := []string{"", ".", "..", "../..", "a/b", "/abs", "has space", "-leading", ".hidden", "semi;colon", "star*"}
	for _, n := range invalid {
		if ValidName(n) {
			t.Errorf("ValidName(%q) = true, want false", n)
		}
	}
}

func TestCreateGroupRejectsUnsafeNames(t *testing.T) {
	s := newTestStore(t)

	for _, n := range []string{"../../escaped", "a/b", "has space", ".."} {
		if _, err := s.CreateGroup(n); !errors.Is(err, ErrInvalidName) {
			t.Errorf("CreateGroup(%q) error = %v, want ErrInvalidName", n, err)
		}
	}
}

func TestAddRepoRejectsUnsafeNames(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")

	for _, n := range []string{"../escape", "a/b", "my repo"} {
		if _, err := s.AddRepo(g.ID, n, "/tmp", ""); !errors.Is(err, ErrInvalidName) {
			t.Errorf("AddRepo(%q) error = %v, want ErrInvalidName", n, err)
		}
	}
}

func TestAddWorkflowRejectsUnsafeNames(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")

	if _, err := s.AddWorkflow(g.ID, "../x", "", nil); !errors.Is(err, ErrInvalidName) {
		t.Errorf("AddWorkflow(unsafe) error = %v, want ErrInvalidName", err)
	}
}

func TestCreateSharedGroupRejectsUnsafeNames(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")
	wf, _ := s.AddWorkflow(g.ID, "local", "", nil)

	if _, err := s.CreateSharedGroup(wf.ID, "../x"); !errors.Is(err, ErrInvalidName) {
		t.Errorf("CreateSharedGroup(unsafe) error = %v, want ErrInvalidName", err)
	}
}
