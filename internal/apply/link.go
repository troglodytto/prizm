// Package apply writes a resolved env file and points a repo at it.
package apply

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/troglodytto/prizm/internal/config"
)

// backupStamp is the suffix format for a displaced env file.
const backupStamp = "20060102-150405"

// Result describes what Apply did, for reporting back to the user.
type Result struct {
	BuiltPath  string
	LinkPath   string
	BackedUpTo string // empty unless a real file was displaced
}

// Apply writes content to builtPath and points repoPath/envFile at it.
//
// Order matters: the build file is written first, any pre-existing real file
// is moved aside, and only then is the symlink swapped in atomically. There is
// never a moment when the repo has no env file.
func Apply(builtPath, content, repoPath, envFile string, now time.Time) (Result, error) {
	res := Result{BuiltPath: builtPath, LinkPath: filepath.Join(repoPath, envFile)}

	if info, err := os.Stat(repoPath); err != nil || !info.IsDir() {
		return Result{}, fmt.Errorf("repo path %s is missing or not a directory", repoPath)
	}

	if err := config.EnsureDir(filepath.Dir(builtPath)); err != nil {
		return Result{}, fmt.Errorf("creating build directory: %w", err)
	}
	if err := writeFileAtomic(builtPath, content); err != nil {
		return Result{}, fmt.Errorf("writing %s: %w", builtPath, err)
	}

	backup, err := preserveExisting(res.LinkPath, now)
	if err != nil {
		return Result{}, err
	}
	res.BackedUpTo = backup

	if err := symlinkAtomic(builtPath, res.LinkPath); err != nil {
		return Result{}, fmt.Errorf("linking %s: %w", res.LinkPath, err)
	}
	return res, nil
}

// writeFileAtomic writes via a temp file in the same directory, then renames.
func writeFileAtomic(path, content string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".prizm-tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// preserveExisting moves a real file out of the way and returns where it went.
// Symlinks are left for the rename to replace; absent targets are a no-op.
func preserveExisting(target string, now time.Time) (string, error) {
	info, err := os.Lstat(target)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("inspecting %s: %w", target, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", nil
	}

	// The stamp is second-resolution, and os.Rename replaces its destination
	// without complaint — so two applies inside one second had the second
	// backup silently destroy the first. The mechanism that exists to
	// preserve a file was the thing deleting it. Find a free name instead.
	backup, err := freeBackupPath(target, now)
	if err != nil {
		return "", err
	}
	if err := os.Rename(target, backup); err != nil {
		return "", fmt.Errorf("backing up %s: %w", target, err)
	}
	return backup, nil
}

// freeBackupPath returns a backup path that does not already exist, adding a
// numeric suffix when the timestamp alone collides.
func freeBackupPath(target string, now time.Time) (string, error) {
	base := fmt.Sprintf("%s.prizm-backup.%s", target, now.Format(backupStamp))
	if _, err := os.Lstat(base); os.IsNotExist(err) {
		return base, nil
	}

	for n := 2; n < 1000; n++ {
		candidate := fmt.Sprintf("%s.%d", base, n)
		if _, err := os.Lstat(candidate); os.IsNotExist(err) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("cannot find a free backup name beside %s", target)
}

// symlinkAtomic creates the link under a temp name, then renames it into place,
// so the repo never briefly lacks an env file.
func symlinkAtomic(dest, target string) error {
	tmp := filepath.Join(filepath.Dir(target), fmt.Sprintf(".prizm-link-%d", os.Getpid()))
	_ = os.Remove(tmp)

	if err := os.Symlink(dest, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
