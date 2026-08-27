package apply

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// ErrLocked means another prizm process is mid-apply.
var ErrLocked = errors.New("another prizm apply is in progress")

// Lock is a held apply lock.
type Lock struct{ file *os.File }

// Acquire takes an exclusive, non-blocking lock for the duration of an apply.
//
// Applying a workflow is a sequence of writes across several repos, and two
// runs racing would interleave them: repo A from one workflow, repo B from
// another, with nothing in the result saying so. Failing the second run
// immediately is the honest answer — a blocking wait would hide the fact
// that two things were competing.
//
// The lock is advisory and process-scoped: it stops two prizm runs from
// overlapping, not a person editing a file by hand.
func Acquire(dir string) (*Lock, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}

	path := filepath.Join(dir, "apply.lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}

	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		file.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, ErrLocked
		}
		return nil, err
	}

	// The pid is for the human reading the lock file during a hang; nothing
	// reads it back, so a stale one is harmless. The flock is the mechanism.
	file.Truncate(0)
	fmt.Fprintf(file, "%d\n", os.Getpid())

	return &Lock{file: file}, nil
}

// Release drops the lock. The file stays behind: removing it would let a
// second process create and lock a fresh file at the same path while a third
// still holds the old one.
func (l *Lock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := unix.Flock(int(l.file.Fd()), unix.LOCK_UN)
	if closeErr := l.file.Close(); err == nil {
		err = closeErr
	}
	l.file = nil
	return err
}
