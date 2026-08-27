package rank

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

var now = time.Unix(1700000000, 0)

func TestRankPutsContainingGroupFirst(t *testing.T) {
	got := Rank([]Candidate{
		{Name: "alpha", Paths: []string{"/code/alpha"}, UseCount: 100, LastUsedAt: now},
		{Name: "xyz", Paths: []string{"/code/xyz/backend"}},
	}, "/code/xyz/backend/src", now)

	if diff := cmp.Diff([]string{"xyz", "alpha"}, got); diff != "" {
		t.Errorf("Rank() mismatch (-want +got):\n%s", diff)
	}
}

func TestRankNeverFilters(t *testing.T) {
	got := Rank([]Candidate{
		{Name: "a", Paths: []string{"/code/a"}},
		{Name: "b", Paths: []string{"/code/b"}},
		{Name: "c", Paths: []string{"/code/c"}},
	}, "/somewhere/else", now)

	if len(got) != 3 {
		t.Errorf("Rank() returned %d candidates, want all 3 — it must sort, not filter", len(got))
	}
}

func TestRankDeepestContainingPathWins(t *testing.T) {
	got := Rank([]Candidate{
		{Name: "outer", Paths: []string{"/code"}},
		{Name: "inner", Paths: []string{"/code/xyz/backend"}},
	}, "/code/xyz/backend/src", now)

	if got[0] != "inner" {
		t.Errorf("Rank()[0] = %q, want %q — the deeper match should win", got[0], "inner")
	}
}

func TestRankExactPathMatchCounts(t *testing.T) {
	got := Rank([]Candidate{
		{Name: "other", Paths: []string{"/code/other"}, UseCount: 50, LastUsedAt: now},
		{Name: "xyz", Paths: []string{"/code/xyz"}},
	}, "/code/xyz", now)

	if got[0] != "xyz" {
		t.Errorf("Rank()[0] = %q, want %q for an exact cwd match", got[0], "xyz")
	}
}

func TestRankParentOfRepoBeatsUnrelated(t *testing.T) {
	got := Rank([]Candidate{
		{Name: "unrelated", Paths: []string{"/elsewhere/x"}},
		{Name: "xyz", Paths: []string{"/code/xyz/backend"}},
	}, "/code/xyz", now)

	if got[0] != "xyz" {
		t.Errorf("Rank()[0] = %q, want %q — cwd is a parent of xyz's repo", got[0], "xyz")
	}
}

// /code/xyz-old must not count as being inside /code/xyz.
func TestRankSiblingDirectoryIsNotContainment(t *testing.T) {
	got := Rank([]Candidate{
		{Name: "xyz", Paths: []string{"/code/xyz"}},
		{Name: "recent", Paths: []string{"/elsewhere"}, UseCount: 10, LastUsedAt: now},
	}, "/code/xyz-old", now)

	if got[0] != "recent" {
		t.Errorf("Rank()[0] = %q, want %q — prefix match must respect path boundaries", got[0], "recent")
	}
}

func TestRankFrecencyOrdersUnrelatedGroups(t *testing.T) {
	got := Rank([]Candidate{
		{Name: "stale", Paths: []string{"/a"}, UseCount: 50, LastUsedAt: now.Add(-30 * 24 * time.Hour)},
		{Name: "hot", Paths: []string{"/b"}, UseCount: 5, LastUsedAt: now.Add(-10 * time.Minute)},
		{Name: "never", Paths: []string{"/c"}},
	}, "/somewhere/else", now)

	if diff := cmp.Diff([]string{"hot", "stale", "never"}, got); diff != "" {
		t.Errorf("Rank() mismatch (-want +got):\n%s", diff)
	}
}

func TestRankTiesBreakByName(t *testing.T) {
	got := Rank([]Candidate{
		{Name: "zeta", Paths: []string{"/z"}},
		{Name: "alpha", Paths: []string{"/a"}},
		{Name: "mid", Paths: []string{"/m"}},
	}, "/somewhere/else", now)

	if diff := cmp.Diff([]string{"alpha", "mid", "zeta"}, got); diff != "" {
		t.Errorf("Rank() mismatch (-want +got):\n%s", diff)
	}
}

func TestRankHandlesEmptyInput(t *testing.T) {
	if got := Rank(nil, "/anywhere", now); len(got) != 0 {
		t.Errorf("Rank(nil) = %v, want empty", got)
	}
}

func TestRankHandlesCandidateWithNoPaths(t *testing.T) {
	if diff := cmp.Diff([]string{"empty"}, Rank([]Candidate{{Name: "empty"}}, "/anywhere", now)); diff != "" {
		t.Errorf("Rank() mismatch (-want +got):\n%s", diff)
	}
}
