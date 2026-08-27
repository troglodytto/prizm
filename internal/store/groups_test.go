package store

import (
	"errors"
	"testing"
	"time"
)

func TestCreateAndGetGroup(t *testing.T) {
	s := newTestStore(t)

	created, err := s.CreateGroup("XYZ")
	if err != nil {
		t.Fatalf("CreateGroup() error = %v", err)
	}
	if created.ID == 0 {
		t.Error("CreateGroup() returned zero ID")
	}
	if created.Name != "XYZ" {
		t.Errorf("Name = %q, want %q", created.Name, "XYZ")
	}

	got, err := s.GroupByName("XYZ")
	if err != nil {
		t.Fatalf("GroupByName() error = %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("GroupByName().ID = %d, want %d", got.ID, created.ID)
	}
}

func TestCreateGroupRejectsDuplicate(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.CreateGroup("XYZ"); err != nil {
		t.Fatalf("first CreateGroup() error = %v", err)
	}
	if _, err := s.CreateGroup("XYZ"); !errors.Is(err, ErrExists) {
		t.Errorf("second CreateGroup() error = %v, want ErrExists", err)
	}
}

func TestGroupByNameMissing(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.GroupByName("nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GroupByName(missing) error = %v, want ErrNotFound", err)
	}
}

func TestListGroupsSortedByName(t *testing.T) {
	s := newTestStore(t)
	for _, n := range []string{"zeta", "alpha", "mid"} {
		if _, err := s.CreateGroup(n); err != nil {
			t.Fatalf("CreateGroup(%q) error = %v", n, err)
		}
	}

	groups, err := s.ListGroups()
	if err != nil {
		t.Fatalf("ListGroups() error = %v", err)
	}

	want := []string{"alpha", "mid", "zeta"}
	if len(groups) != len(want) {
		t.Fatalf("ListGroups() returned %d groups, want %d", len(groups), len(want))
	}
	for i, w := range want {
		if groups[i].Name != w {
			t.Errorf("groups[%d].Name = %q, want %q", i, groups[i].Name, w)
		}
	}
}

func TestTouchGroupUpdatesFrecency(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")
	now := time.Unix(1700000000, 0)

	if err := s.TouchGroup(g.ID, now); err != nil {
		t.Fatalf("TouchGroup() error = %v", err)
	}
	if err := s.TouchGroup(g.ID, now.Add(time.Hour)); err != nil {
		t.Fatalf("second TouchGroup() error = %v", err)
	}

	got, err := s.GroupByName("XYZ")
	if err != nil {
		t.Fatalf("GroupByName() error = %v", err)
	}
	if got.UseCount != 2 {
		t.Errorf("UseCount = %d, want 2", got.UseCount)
	}
	if want := now.Add(time.Hour).Unix(); got.LastUsedAt.Unix() != want {
		t.Errorf("LastUsedAt = %d, want %d", got.LastUsedAt.Unix(), want)
	}
}
