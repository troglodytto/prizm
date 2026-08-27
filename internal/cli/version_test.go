package cli

import (
	"strings"
	"testing"
)

func TestVersionPrefersTheLdflagsValue(t *testing.T) {
	original := version
	t.Cleanup(func() { version = original })

	version = "v1.2.3"
	if got := Version(); got != "v1.2.3" {
		t.Errorf("Version() = %q, want the injected value", got)
	}
}

// Without an injected value it must still say something useful rather than
// an empty string — a binary that cannot identify itself is a support problem.
func TestVersionFallsBackToBuildInfo(t *testing.T) {
	original := version
	t.Cleanup(func() { version = original })

	version = ""
	got := Version()

	if got == "" {
		t.Fatal("Version() = empty, want a fallback")
	}
	if strings.HasPrefix(got, "dev") {
		return // a working-tree build; the shape we expect under `go test`
	}
	if !strings.HasPrefix(got, "v") {
		t.Errorf("Version() = %q, want either a dev string or a vN.N.N module version", got)
	}
}

func TestRootCommandReportsTheVersion(t *testing.T) {
	h := newHarness(t)

	if err := h.run(t, "--version"); err != nil {
		t.Fatalf("--version error = %v", err)
	}
	if out := h.out.String(); !strings.Contains(out, "prizm") || !strings.Contains(out, Version()) {
		t.Errorf("--version output = %q, want the name and the version", out)
	}
}
