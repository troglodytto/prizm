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

// GlobalPath is the file backing a group's group-global variables.
func GlobalPath(group string) (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "shared", group, "global.env"), nil
}

// EnsureDir creates dir and its parents with owner-only permissions.
func EnsureDir(dir string) error {
	return os.MkdirAll(dir, 0o700)
}
