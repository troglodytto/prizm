package resolve

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestExpandSimpleReference(t *testing.T) {
	got, err := Expand(map[string]string{"HOST": "localhost", "URL": "http://${HOST}:8080"})
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}

	want := map[string]string{"HOST": "localhost", "URL": "http://localhost:8080"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Expand() mismatch (-want +got):\n%s", diff)
	}
}

// The spec's shape: a public name pointing at internal plumbing that is itself
// derived from two more internal values.
func TestExpandNestedInternalDerivation(t *testing.T) {
	got, err := Expand(map[string]string{
		"_PRIZM_DB_USER": "svc_app",
		"_PRIZM_DB_PASS": "hunter2",
		"_PRIZM_DB_URL":  "postgres://${_PRIZM_DB_USER}:${_PRIZM_DB_PASS}@localhost:5432/app",
		"DB_URL":         "${_PRIZM_DB_URL}",
	})
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}
	if want := "postgres://svc_app:hunter2@localhost:5432/app"; got["DB_URL"] != want {
		t.Errorf("DB_URL = %q, want %q", got["DB_URL"], want)
	}
}

func TestExpandChains(t *testing.T) {
	got, err := Expand(map[string]string{
		"USER": "u",
		"URL":  "postgres://${USER}@h/db",
		"DSN":  "${URL}?sslmode=disable",
	})
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}
	if want := "postgres://u@h/db?sslmode=disable"; got["DSN"] != want {
		t.Errorf("DSN = %q, want %q", got["DSN"], want)
	}
}

func TestExpandMultipleReferencesInOneValue(t *testing.T) {
	got, err := Expand(map[string]string{"A": "1", "B": "2", "SUM": "${A}-${B}-${A}"})
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}
	if got["SUM"] != "1-2-1" {
		t.Errorf("SUM = %q, want %q", got["SUM"], "1-2-1")
	}
}

// Passwords and regexes are full of $. Only ${...} is a reference.
func TestExpandLeavesBareDollarAlone(t *testing.T) {
	in := map[string]string{"PASS": "p$ssw0rd$", "REGEX": "^foo$", "COST": "$100"}

	got, err := Expand(in)
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}
	if diff := cmp.Diff(in, got); diff != "" {
		t.Errorf("Expand() mismatch (-want +got):\n%s", diff)
	}
}

func TestExpandEscapedDollarBrace(t *testing.T) {
	got, err := Expand(map[string]string{"TEMPLATE": "literal $${NOT_A_REF} here"})
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}
	if want := "literal ${NOT_A_REF} here"; got["TEMPLATE"] != want {
		t.Errorf("TEMPLATE = %q, want %q", got["TEMPLATE"], want)
	}
}

func TestExpandUnresolvedReferenceIsAnError(t *testing.T) {
	_, err := Expand(map[string]string{"DB_URL": "postgres://${_PRIZM_DB_USER}@h/db"})
	if !errors.Is(err, ErrUnresolved) {
		t.Fatalf("Expand() error = %v, want ErrUnresolved", err)
	}
	if !strings.Contains(err.Error(), "_PRIZM_DB_USER") || !strings.Contains(err.Error(), "DB_URL") {
		t.Errorf("error = %q, want it to name both the referencing key and the missing one", err)
	}
}

func TestExpandDetectsCycles(t *testing.T) {
	if _, err := Expand(map[string]string{"A": "${B}", "B": "${A}"}); !errors.Is(err, ErrCycle) {
		t.Fatalf("Expand() error = %v, want ErrCycle", err)
	}
}

func TestExpandDetectsSelfReference(t *testing.T) {
	if _, err := Expand(map[string]string{"A": "${A}"}); !errors.Is(err, ErrCycle) {
		t.Errorf("Expand() error = %v, want ErrCycle", err)
	}
}

func TestExpandIsDeterministic(t *testing.T) {
	in := map[string]string{"A": "1", "B": "${A}", "C": "${B}", "D": "${C}"}

	first, err := Expand(in)
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}
	for i := 0; i < 20; i++ {
		got, err := Expand(in)
		if err != nil {
			t.Fatalf("Expand() error = %v", err)
		}
		if diff := cmp.Diff(first, got); diff != "" {
			t.Fatalf("Expand() not deterministic on run %d (-first +got):\n%s", i, diff)
		}
	}
}

// The output file must be opaque: no trace of the derivation chain.
func TestEmitDropsInternalKeys(t *testing.T) {
	got := Emit(map[string]string{
		"_PRIZM_DB_USER": "svc_app",
		"_PRIZM_DB_PASS": "hunter2",
		"_PRIZM_DB_URL":  "postgres://svc_app:hunter2@h/db",
		"DB_URL":         "postgres://svc_app:hunter2@h/db",
		"PORT":           "8080",
	})

	want := map[string]string{"DB_URL": "postgres://svc_app:hunter2@h/db", "PORT": "8080"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Emit() mismatch (-want +got):\n%s", diff)
	}
}
