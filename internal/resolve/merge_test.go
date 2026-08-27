package resolve

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestMergePrecedenceLaterLayerWins(t *testing.T) {
	got := Merge([]Layer{
		{Name: "repo-shared", Vars: map[string]string{"A": "1", "B": "repo"}},
		{Name: "shared:db", Vars: map[string]string{"B": "shared", "C": "3"}},
		{Name: "workflow+repo", Vars: map[string]string{"C": "specific"}},
	})

	want := map[string]string{"A": "1", "B": "shared", "C": "specific"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Merge() mismatch (-want +got):\n%s", diff)
	}
}

func TestMergeEmptyLayers(t *testing.T) {
	if got := Merge(nil); len(got) != 0 {
		t.Errorf("Merge(nil) = %v, want empty map", got)
	}
}

func TestMergeDoesNotMutateInputs(t *testing.T) {
	first := map[string]string{"A": "1"}
	Merge([]Layer{{Name: "a", Vars: first}, {Name: "b", Vars: map[string]string{"A": "2"}}})

	if first["A"] != "1" {
		t.Errorf("input layer was mutated: A = %q, want %q", first["A"], "1")
	}
}

func TestIsInternal(t *testing.T) {
	tests := map[string]bool{
		"_PRIZM_DB_URL": true,
		"_PRIZM_":       true,
		"DB_URL":        false,
		"PRIZM_DB_URL":  false,
		"_DB_URL":       false,
		"_prizm_db_url": false, // the prefix is case-sensitive
	}

	for key, want := range tests {
		if got := IsInternal(key); got != want {
			t.Errorf("IsInternal(%q) = %v, want %v", key, got, want)
		}
	}
}
