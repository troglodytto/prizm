package apply

import (
	"errors"
	"testing"
)

func TestSecondAcquireFailsRatherThanWaiting(t *testing.T) {
	dir := t.TempDir()

	first, err := Acquire(dir)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer first.Release()

	// Same process, same fd table — flock is per open file description, so a
	// second Open genuinely contends the way a second process would.
	if _, err := Acquire(dir); !errors.Is(err, ErrLocked) {
		t.Errorf("second acquire = %v, want ErrLocked — a blocking wait would hide the race", err)
	}
}

func TestLockIsReusableAfterRelease(t *testing.T) {
	dir := t.TempDir()

	first, err := Acquire(dir)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}

	second, err := Acquire(dir)
	if err != nil {
		t.Fatalf("re-acquire after release: %v — a released lock must not stay held", err)
	}
	second.Release()
}

func TestReleasingTwiceIsSafe(t *testing.T) {
	dir := t.TempDir()

	lock, err := Acquire(dir)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("first release: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Errorf("second release: %v — deferred cleanup must be idempotent", err)
	}
}
