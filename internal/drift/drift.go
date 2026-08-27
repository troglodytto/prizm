// Package drift compares what is on disk against what prizm would write now.
//
// Link state and content state are tracked separately on purpose: a repo can
// be correctly linked but content-drifted (someone edited the file), or
// content-identical but linked to another workflow's build (someone ran a
// different apply elsewhere). Collapsing those into one status would lose
// exactly the information `status` exists to show.
package drift

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/troglodytto/prizm/internal/envfile"
	"github.com/troglodytto/prizm/internal/sharedfile"
	"github.com/troglodytto/prizm/internal/store"
)

// LinkState describes the repo's env file as a filesystem object.
type LinkState int

const (
	// NoFile means the workflow has never been applied here.
	NoFile LinkState = iota
	// Managed means the symlink points at the build prizm expects.
	Managed
	// ManagedElsewhere means it is a symlink to a different build — usually
	// another workflow was applied more recently.
	ManagedElsewhere
	// Unmanaged means a real file sits where prizm's symlink should be.
	Unmanaged
	// PathMissing means the repo directory is gone.
	PathMissing
	// Unresolvable means the repo's variables could not be resolved, so
	// there is nothing to compare the file against. Distinct from NoFile:
	// the file may be perfectly intact.
	Unresolvable
)

func (l LinkState) String() string {
	switch l {
	case Unresolvable:
		return "cannot resolve"
	case NoFile:
		return "not applied"
	case Managed:
		return "linked"
	case ManagedElsewhere:
		return "linked elsewhere"
	case Unmanaged:
		return "unmanaged file"
	case PathMissing:
		return "path missing"
	}
	return "unknown"
}

// Report is one repo's state relative to a workflow.
type Report struct {
	Repo     store.Repo
	Link     LinkState
	LinkDest string
	Diff     sharedfile.Diff
	// Err carries why a repo is Unresolvable. Without it the reason — a
	// reference cycle, an undefined ${…} — was known only to `up`.
	Err error
}

// InSync reports whether nothing needs doing.
func (r Report) InSync() bool { return r.Link == Managed && r.Diff.Empty() }

// Changes counts the drifted keys.
func (r Report) Changes() int {
	return len(r.Diff.Added) + len(r.Diff.Removed) + len(r.Diff.Changed)
}

// Inspect compares repo's env file against expected, the map `up` would write.
func Inspect(repo store.Repo, expected map[string]string, expectedBuilt string) (Report, error) {
	report := Report{Repo: repo}

	if info, err := os.Stat(repo.Path); err != nil || !info.IsDir() {
		report.Link = PathMissing
		return report, nil
	}

	target := filepath.Join(repo.Path, repo.EnvFile)

	info, err := os.Lstat(target)
	if os.IsNotExist(err) {
		report.Link = NoFile
		return report, nil
	}
	if err != nil {
		return Report{}, fmt.Errorf("inspecting %s: %w", target, err)
	}

	if info.Mode()&os.ModeSymlink != 0 {
		dest, err := os.Readlink(target)
		if err != nil {
			return Report{}, fmt.Errorf("reading link %s: %w", target, err)
		}
		report.LinkDest = dest

		report.Link = ManagedElsewhere
		if dest == expectedBuilt {
			report.Link = Managed
		}
	} else {
		report.Link = Unmanaged
	}

	// Read through the link. A dangling symlink counts as not applied.
	raw, err := os.ReadFile(target)
	if os.IsNotExist(err) {
		report.Link = NoFile
		return report, nil
	}
	if err != nil {
		return Report{}, fmt.Errorf("reading %s: %w", target, err)
	}

	onDisk, err := envfile.Parse(string(raw))
	if err != nil {
		return Report{}, fmt.Errorf("parsing %s: %w", target, err)
	}

	report.Diff = sharedfile.Compare(expected, onDisk)
	return report, nil
}
