package crypto

import (
	"errors"
	"testing"
)

func TestLazyDoesNotResolveUntilUsed(t *testing.T) {
	calls := 0
	l := NewLazy(func() (Cipher, error) {
		calls++
		return Plaintext{}, nil
	})

	// Constructing it is what `prizm --version` does. Nothing should have
	// asked the keychain for anything yet.
	if calls != 0 {
		t.Fatalf("resolve ran %d times before any value was touched, want 0", calls)
	}

	if _, err := l.Encrypt("x"); err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if calls != 1 {
		t.Errorf("resolve ran %d times, want 1", calls)
	}
}

func TestLazyResolvesOnlyOnce(t *testing.T) {
	calls := 0
	l := NewLazy(func() (Cipher, error) {
		calls++
		return Plaintext{}, nil
	})

	for i := 0; i < 5; i++ {
		blob, err := l.Encrypt("x")
		if err != nil {
			t.Fatalf("encrypt: %v", err)
		}
		if _, err := l.Decrypt(blob); err != nil {
			t.Fatalf("decrypt: %v", err)
		}
	}

	if calls != 1 {
		t.Errorf("resolve ran %d times, want 1 — a keychain prompt per variable would be unusable", calls)
	}
}

func TestLazySurfacesTheResolveFailure(t *testing.T) {
	boom := errors.New("no secret service")
	l := NewLazy(func() (Cipher, error) { return nil, boom })

	if _, err := l.Encrypt("x"); !errors.Is(err, boom) {
		t.Errorf("encrypt err = %v, want the keychain failure", err)
	}
	if _, err := l.Decrypt([]byte("x")); !errors.Is(err, boom) {
		t.Errorf("decrypt err = %v, want the keychain failure", err)
	}
}

func TestLazyRoundTripsThroughTheRealCipher(t *testing.T) {
	key := make([]byte, KeySize)
	l := NewLazy(func() (Cipher, error) { return NewAESGCM(key) })

	blob, err := l.Encrypt("hunter2")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	got, err := l.Decrypt(blob)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if got != "hunter2" {
		t.Errorf("round trip = %q, want hunter2", got)
	}
}
