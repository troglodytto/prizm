package sharedfile

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestRenderIncludesReposHeader(t *testing.T) {
	got := Render([]string{"backend", "auth", "ai"}, map[string]string{"_PRIZM_DB_USER": "svc"})

	if !strings.HasPrefix(got, "# prizm:repos backend,auth,ai\n") {
		t.Errorf("Render() = %q, want it to start with the repos header", got)
	}
	if !strings.Contains(got, "_PRIZM_DB_USER=svc") {
		t.Errorf("Render() = %q, want the variable body", got)
	}
}

func TestRenderWithoutReposOmitsHeader(t *testing.T) {
	if got := Render(nil, map[string]string{"A": "1"}); strings.Contains(got, "prizm:repos") {
		t.Errorf("Render() = %q, want no header when there are no repos", got)
	}
}

func TestParseReadsHeaderAndVars(t *testing.T) {
	repos, vars, hasHeader, err := Parse(
		"# prizm:repos backend, auth ,ai\n\n_PRIZM_DB_USER=svc\n_PRIZM_DB_URL=postgres://${_PRIZM_DB_USER}@h/db\n")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if !hasHeader {
		t.Error("hasHeader = false, want true")
	}
	if diff := cmp.Diff([]string{"backend", "auth", "ai"}, repos); diff != "" {
		t.Errorf("repos mismatch (-want +got):\n%s", diff)
	}

	want := map[string]string{
		"_PRIZM_DB_USER": "svc",
		"_PRIZM_DB_URL":  "postgres://${_PRIZM_DB_USER}@h/db",
	}
	if diff := cmp.Diff(want, vars); diff != "" {
		t.Errorf("vars mismatch (-want +got):\n%s", diff)
	}
}

func TestParseWithoutHeader(t *testing.T) {
	repos, vars, hasHeader, err := Parse("A=1\n")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if hasHeader {
		t.Error("hasHeader = true, want false")
	}
	if len(repos) != 0 {
		t.Errorf("repos = %v, want none", repos)
	}
	if vars["A"] != "1" {
		t.Errorf("vars = %v, want A=1", vars)
	}
}

func TestParseIgnoresOrdinaryComments(t *testing.T) {
	_, vars, hasHeader, err := Parse("# just a note\nA=1\n")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if hasHeader {
		t.Error("an ordinary comment was mistaken for the prizm header")
	}
	if vars["A"] != "1" {
		t.Errorf("vars = %v, want A=1", vars)
	}
}

func TestParseEmptyReposHeader(t *testing.T) {
	repos, _, hasHeader, err := Parse("# prizm:repos\nA=1\n")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if !hasHeader {
		t.Error("hasHeader = false, want true for an explicitly empty audience")
	}
	if len(repos) != 0 {
		t.Errorf("repos = %v, want none", repos)
	}
}

func TestRenderParseRoundTrip(t *testing.T) {
	repos := []string{"backend", "auth"}
	vars := map[string]string{"_PRIZM_A": "1", "_PRIZM_URL": "postgres://${_PRIZM_A}@h/db"}

	gotRepos, gotVars, _, err := Parse(Render(repos, vars))
	if err != nil {
		t.Fatalf("Parse(Render(...)) error = %v", err)
	}
	if diff := cmp.Diff(repos, gotRepos); diff != "" {
		t.Errorf("repos mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(vars, gotVars); diff != "" {
		t.Errorf("vars mismatch (-want +got):\n%s", diff)
	}
}

func TestCompareDetectsEveryKindOfChange(t *testing.T) {
	got := Compare(
		map[string]string{"KEEP": "same", "CHANGE": "old", "GONE": "x"},
		map[string]string{"KEEP": "same", "CHANGE": "new", "NEW": "y"},
	)

	if diff := cmp.Diff([]string{"NEW"}, got.Added); diff != "" {
		t.Errorf("Added mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"GONE"}, got.Removed); diff != "" {
		t.Errorf("Removed mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]Change{{Key: "CHANGE", From: "old", To: "new"}}, got.Changed); diff != "" {
		t.Errorf("Changed mismatch (-want +got):\n%s", diff)
	}
}

// Reordering keys is not a change. This is why prizm diffs maps, not text.
func TestCompareIsKeyLevelNotLineLevel(t *testing.T) {
	got := Compare(map[string]string{"A": "1", "B": "2"}, map[string]string{"B": "2", "A": "1"})
	if !got.Empty() {
		t.Errorf("Compare() = %+v, want empty — key order must not register as drift", got)
	}
}

func TestCompareResultsAreSorted(t *testing.T) {
	got := Compare(map[string]string{}, map[string]string{"Z": "1", "A": "2", "M": "3"})
	if diff := cmp.Diff([]string{"A", "M", "Z"}, got.Added); diff != "" {
		t.Errorf("Added mismatch (-want +got):\n%s", diff)
	}
}
