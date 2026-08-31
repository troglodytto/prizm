// Package config resolves the on-disk locations prizm uses.
package config

import (
	"errors"
	"os"
	"path/filepath"
)

const appName = "prizm"

// DataDir is the root directory for all prizm state.
func DataDir() (string, error) {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, appName), nil
	}

	home := os.Getenv("HOME")
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", errors.New("cannot determine home directory: set HOME or XDG_DATA_HOME")
		}
	}
	return filepath.Join(home, ".local", "share", appName), nil
}

// DBPath is the SQLite database file.
func DBPath() (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, appName+".db"), nil
}

// BuiltPath is the resolved env file prizm generates for one (group, workflow, repo).
func BuiltPath(group, workflow, repo string) (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "built", group, workflow, repo+".env"), nil
}

// BackupStamp is the timestamp format in a displaced env file's name.
const BackupStamp = "20060102-150405"

// BackupPath is where a displaced env file is kept, for one
// (group, workflow, repo, env file) at a moment in time.
//
// Backups live under prizm's own directory rather than beside the file they
// came from: a repo is somewhere you read and commit, and scattering
// timestamped copies of your secrets through it is prizm's mess to keep, not
// yours. The name carries what the location used to say — which repo, which
// workflow, which file — so one flat directory per group stays greppable.
func BackupPath(group, workflow, repo, envFile, stamp string) (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	// envFile may be nested (`envs/qastage.env`); only its name identifies it,
	// and keeping the separator would nest directories under backups/.
	name := repo + "__" + workflow + "__" + filepath.Base(envFile) + "__" + stamp
	return filepath.Join(dir, "backups", group, name), nil
}

// EnsureDir creates dir and its parents with owner-only permissions.
func EnsureDir(dir string) error {
	return os.MkdirAll(dir, 0o700)
}
