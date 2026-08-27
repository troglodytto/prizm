package crypto

import (
	"bytes"
	"testing"
)

func testKey() []byte {
	k := make([]byte, KeySize)
	for i := range k {
		k[i] = byte(i)
	}
	return k
}

func TestAESGCMRoundTrip(t *testing.T) {
	c, err := NewAESGCM(testKey())
	if err != nil {
		t.Fatalf("NewAESGCM() error = %v", err)
	}

	for _, want := range []string{"", "hello", "postgres://u:p@h/db", "multi\nline\tvalue"} {
		blob, err := c.Encrypt(want)
		if err != nil {
			t.Fatalf("Encrypt(%q) error = %v", want, err)
		}
		got, err := c.Decrypt(blob)
		if err != nil {
			t.Fatalf("Decrypt() error = %v", err)
		}
		if got != want {
			t.Errorf("round trip = %q, want %q", got, want)
		}
	}
}

func TestAESGCMCiphertextIsNotPlaintext(t *testing.T) {
	c, _ := NewAESGCM(testKey())

	blob, err := c.Encrypt("SUPERSECRET")
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	if bytes.Contains(blob, []byte("SUPERSECRET")) {
		t.Error("ciphertext contains the plaintext")
	}
}

func TestAESGCMNonceIsRandom(t *testing.T) {
	c, _ := NewAESGCM(testKey())

	a, _ := c.Encrypt("same")
	b, _ := c.Encrypt("same")
	if bytes.Equal(a, b) {
		t.Error("two encryptions of the same plaintext produced identical blobs; nonce is not random")
	}
}

func TestAESGCMRejectsBadKeyLength(t *testing.T) {
	if _, err := NewAESGCM([]byte("short")); err == nil {
		t.Error("NewAESGCM(short key) error = nil, want error")
	}
}

func TestAESGCMRejectsTamperedBlob(t *testing.T) {
	c, _ := NewAESGCM(testKey())

	blob, _ := c.Encrypt("value")
	blob[len(blob)-1] ^= 0xFF

	if _, err := c.Decrypt(blob); err == nil {
		t.Error("Decrypt(tampered) error = nil, want authentication failure")
	}
}

func TestAESGCMRejectsTruncatedBlob(t *testing.T) {
	c, _ := NewAESGCM(testKey())

	if _, err := c.Decrypt([]byte{1, 2, 3}); err == nil {
		t.Error("Decrypt(truncated) error = nil, want error")
	}
}

func TestPlaintextCipherRoundTrip(t *testing.T) {
	var c Cipher = Plaintext{}

	blob, err := c.Encrypt("value")
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	got, err := c.Decrypt(blob)
	if err != nil {
		t.Fatalf("Decrypt() error = %v", err)
	}
	if got != "value" {
		t.Errorf("round trip = %q, want %q", got, "value")
	}
}
