// Package secret encrypts small secrets (SMTP passwords, webhook URLs) at
// rest with AES-256-GCM. The key is derived from SECRET_KEY.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
)

const prefix = "v1:"

// ErrNoKey means SECRET_KEY isn't configured, so secrets can't be stored.
var ErrNoKey = errors.New("SECRET_KEY is not set; secrets can't be stored")

// Box seals and opens secrets.
type Box struct{ aead cipher.AEAD }

// New derives a key from any non-empty string (use 32+ random bytes).
// It returns nil when key is empty.
func New(key string) *Box {
	if key == "" {
		return nil
	}
	sum := sha256.Sum256([]byte("drix-secret-v1:" + key))
	block, _ := aes.NewCipher(sum[:])
	aead, _ := cipher.NewGCM(block)
	return &Box{aead: aead}
}

// Seal encrypts plaintext. A nil Box returns ErrNoKey.
func (b *Box) Seal(plain string) (string, error) {
	if b == nil {
		return "", ErrNoKey
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ct := b.aead.Seal(nonce, nonce, []byte(plain), nil)
	return prefix + base64.RawStdEncoding.EncodeToString(ct), nil
}

// Open decrypts a value produced by Seal.
func (b *Box) Open(sealed string) (string, error) {
	if b == nil {
		return "", ErrNoKey
	}
	if !strings.HasPrefix(sealed, prefix) {
		return "", errors.New("not an encrypted value")
	}
	raw, err := base64.RawStdEncoding.DecodeString(sealed[len(prefix):])
	if err != nil || len(raw) < b.aead.NonceSize() {
		return "", errors.New("corrupt encrypted value")
	}
	n := b.aead.NonceSize()
	plain, err := b.aead.Open(nil, raw[:n], raw[n:], nil)
	if err != nil {
		return "", errors.New("can't decrypt: SECRET_KEY changed?")
	}
	return string(plain), nil
}
