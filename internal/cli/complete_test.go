package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/spf13/cobra"
)

// complete drives the real command tree's completion the way a shell does.
func (h *harness) complete(t *testing.T, args ...string) []string {
	t.Helper()

	root := NewRootCmd(h.app)
	root.SetOut(h.out)
	root.SetErr(h.err)
	root.SetArgs(append([]string{cobra.ShellCompRequestCmd}, args...))

	h.out.Reset()
	if err := root.Execute(); err != nil {
		t.Fatalf("__complete %v error = %v", args, err)
	}

	var out []string
	for _, line := range strings.Split(h.out.String(), "\n") {
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		out = append(out, strings.SplitN(line, "\t", 2)[0])
	}
	return out
}

func (h *harness) seedForCompletion(t *testing.T) {
	t.Helper()

	h.run(t, "init", "acme")
	h.run(t, "add-repo", "acme", "auth")
	h.run(t, "add-repo", "acme", "backend")
	h.run(t, "add-workflow", "acme", "local", "--tag", "local", "--repos", "auth,backend")
	h.run(t, "add-workflow", "acme", "staging", "--tag", "qa", "--repos", "auth,backend")
	h.run(t, "var", "acme", "auth", "PORT=1", "TOKEN=2")
}

// Every command that takes a group completes one in its first slot.
func TestCompletionOffersGroupsInTheFirstSlot(t *testing.T) {
	h := newHarness(t)
	h.seedForCompletion(t)

	for _, cmd := range []string{"ls", "status", "rm", "rename", "edit-workflow", "repair", "var", "import", "shared-sync", "shared-ls", "global", "pick", "up"} {
		got := h.complete(t, cmd, "")
		if len(got) == 0 || got[0] != "acme" {
			t.Errorf("`prizm %s <TAB>` = %v, want the group names", cmd, got)
		}
	}
}

// The second slot is scoped to whichever group was named.
func TestCompletionScopesTheSecondSlotToTheGroup(t *testing.T) {
	h := newHarness(t)
	h.seedForCompletion(t)

	workflows := []string{"local", "staging"}
	repos := []string{"auth", "backend"}

	for _, tt := range []struct {
		cmd  string
		want []string
	}{
		{"edit-workflow", workflows},
		{"up", workflows},
		{"shared-sync", workflows},
		{"repair", repos},
		{"var", repos},
		{"import", repos},
	} {
		if diff := cmp.Diff(tt.want, h.complete(t, tt.cmd, "acme", "")); diff != "" {
			t.Errorf("`prizm %s acme <TAB>` mismatch (-want +got):\n%s", tt.cmd, diff)
		}
	}
}

// unset completes the variables actually set, so you need not remember them.
func TestCompletionOffersVariableKeysToUnset(t *testing.T) {
	h := newHarness(t)
	h.seedForCompletion(t)

	if diff := cmp.Diff([]string{"PORT", "TOKEN"}, h.complete(t, "unset", "acme", "auth", "")); diff != "" {
		t.Errorf("mismatch (-want +got):\n%s", diff)
	}
}

func TestCompletionFiltersByPrefix(t *testing.T) {
	h := newHarness(t)
	h.seedForCompletion(t)

	if diff := cmp.Diff([]string{"local"}, h.complete(t, "edit-workflow", "acme", "loc")); diff != "" {
		t.Errorf("mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"TOKEN"}, h.complete(t, "unset", "acme", "auth", "TO")); diff != "" {
		t.Errorf("mismatch (-want +got):\n%s", diff)
	}
}

func TestCompletionOffersFlagValues(t *testing.T) {
	h := newHarness(t)
	h.seedForCompletion(t)

	if diff := cmp.Diff([]string{"local", "staging"}, h.complete(t, "var", "acme", "auth", "--workflow", "")); diff != "" {
		t.Errorf("--workflow mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"auth", "backend"}, h.complete(t, "rm", "acme", "--repo", "")); diff != "" {
		t.Errorf("--repo mismatch (-want +got):\n%s", diff)
	}

	tags := h.complete(t, "edit-workflow", "acme", "local", "--tag", "")
	for _, want := range []string{"local", "prod", "qa"} {
		if !contains(tags, want) {
			t.Errorf("--tag = %v, want it to include %q", tags, want)
		}
	}
}

// A comma-separated list completes in place, offering only what is not
// already named.
func TestCompletionBuildsCommaSeparatedRepoLists(t *testing.T) {
	h := newHarness(t)
	h.seedForCompletion(t)

	if diff := cmp.Diff([]string{"auth", "backend"}, h.complete(t, "add-workflow", "acme", "x", "--repos", "")); diff != "" {
		t.Errorf("mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"auth,backend"}, h.complete(t, "add-workflow", "acme", "x", "--repos", "auth,")); diff != "" {
		t.Errorf("mismatch (-want +got):\n%s", diff)
	}
}

// The same bag name exists once per workflow; the user is choosing a name.
func TestCompletionDoesNotRepeatBagNames(t *testing.T) {
	h := newHarness(t)
	h.seedForCompletion(t)
	h.run(t, "shared-add", "acme", "local", "infra", "--repos", "auth")
	h.run(t, "shared-add", "acme", "staging", "infra", "--repos", "auth")

	if diff := cmp.Diff([]string{"infra"}, h.complete(t, "rm", "acme", "--bag", "")); diff != "" {
		t.Errorf("mismatch (-want +got):\n%s", diff)
	}
}

// Standing in a repo, the group is inferred rather than demanded.
func TestCompletionInfersTheGroupFromCwd(t *testing.T) {
	h := newHarness(t)
	beDir := h.repoDir(t, "backend")

	h.run(t, "init", "acme")
	h.run(t, "add-repo", "acme", "backend", "--path", beDir)
	h.run(t, "add-workflow", "acme", "local", "--repos", "backend")
	h.cwd = beDir

	if got := h.complete(t, "up", ""); !contains(got, "local") {
		t.Errorf("`prizm up <TAB>` inside a repo = %v, want its workflows too", got)
	}
}

// Completion must never run off the end of a command's arguments.
func TestCompletionStopsAfterTheLastSlot(t *testing.T) {
	h := newHarness(t)
	h.seedForCompletion(t)

	if got := h.complete(t, "up", "acme", "local", ""); len(got) != 0 {
		t.Errorf("`prizm up acme local <TAB>` = %v, want nothing left", got)
	}
}

func TestServiceCompletionReadsTheComposeFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stack.yml")
	yaml := "version: '3'\n" +
		"services:\n" +
		"  db:\n" +
		"    image: postgres\n" +
		"    ports: ['5432:5432']\n" +
		"  cache:\n" +
		"    image: redis\n" +
		"volumes:\n" +
		"  data: {}\n"
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("writing compose file: %v", err)
	}

	got, _ := completeServices(path, "")
	if len(got) != 2 || got[0] != "db" || got[1] != "cache" {
		t.Errorf("services = %v, want [db cache] — image/ports are settings, volumes is another block", got)
	}
}

func TestServiceCompletionKeepsWhatIsAlreadyTyped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stack.yml")
	if err := os.WriteFile(path, []byte("services:\n  db:\n  cache:\n"), 0o600); err != nil {
		t.Fatalf("writing compose file: %v", err)
	}

	// The shell replaces the whole word, so an earlier choice must come back
	// with each candidate or it is silently dropped.
	got, _ := completeServices(path, "db,")
	if len(got) != 1 || got[0] != "db,cache" {
		t.Errorf("services = %v, want [db,cache]", got)
	}
}

func TestServiceCompletionSurvivesAMissingFile(t *testing.T) {
	if got, _ := completeServices("/nope/stack.yml", ""); got != nil {
		t.Errorf("services = %v, want none rather than a crash", got)
	}
}
