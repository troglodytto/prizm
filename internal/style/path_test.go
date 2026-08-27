package style

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPathAbbreviatesHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got := Path(filepath.Join(home, "code", "frontend"))
	if got != "~/code/frontend" {
		t.Errorf("Path = %q, want ~/code/frontend", got)
	}
}

func TestPathLeavesOtherPathsAlone(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if got := Path("/opt/services/api"); got != "/opt/services/api" {
		t.Errorf("Path = %q, want it unchanged", got)
	}
}

func TestPathDoesNotAbbreviateASiblingPrefix(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// /home/bob must not become ~ob for a user whose home is /home/b.
	sibling := home + "-other/code"
	if got := Path(sibling); got != sibling {
		t.Errorf("Path = %q, want %q — the separator is what makes it a parent", got, sibling)
	}
}

func TestPathHandlesHomeItself(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if got := Path(home); got != "~" {
		t.Errorf("Path = %q, want ~", got)
	}
}

func TestPathSurvivesNoHome(t *testing.T) {
	t.Setenv("HOME", "")
	os.Unsetenv("HOME")

	if got := Path("/opt/api"); got != "/opt/api" {
		t.Errorf("Path = %q, want it unchanged when there is no home to strip", got)
	}
}
