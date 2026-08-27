package crypto

import "sync"

// Lazy defers building the real cipher until a value is actually encrypted or
// decrypted.
//
// The keychain is the one dependency prizm cannot supply for itself, and it is
// absent on exactly the machines where people first try a tool: a container, a
// CI runner, a headless server over SSH. Resolving it eagerly meant `prizm
// --version` and `prizm --help` failed with a keychain error on those boxes,
// which reads as "this tool is broken" rather than "this command needed a
// secret and there is nowhere to keep one".
//
// Commands that never touch a value now never ask.
type Lazy struct {
	once    sync.Once
	resolve func() (Cipher, error)
	cipher  Cipher
	err     error
}

// NewLazy wraps a cipher constructor, calling it at most once and only when
// something is actually encrypted or decrypted.
func NewLazy(resolve func() (Cipher, error)) *Lazy {
	return &Lazy{resolve: resolve}
}

// FromKeyring is the real thing: the OS keychain, opened on first use.
func FromKeyring() *Lazy {
	return NewLazy(func() (Cipher, error) {
		key, err := LoadOrCreateKey()
		if err != nil {
			return nil, err
		}
		return NewAESGCM(key)
	})
}

func (l *Lazy) get() (Cipher, error) {
	l.once.Do(func() { l.cipher, l.err = l.resolve() })
	return l.cipher, l.err
}

// Encrypt resolves the cipher, then encrypts.
func (l *Lazy) Encrypt(plaintext string) ([]byte, error) {
	c, err := l.get()
	if err != nil {
		return nil, err
	}
	return c.Encrypt(plaintext)
}

// Decrypt resolves the cipher, then decrypts.
func (l *Lazy) Decrypt(blob []byte) (string, error) {
	c, err := l.get()
	if err != nil {
		return "", err
	}
	return c.Decrypt(blob)
}
