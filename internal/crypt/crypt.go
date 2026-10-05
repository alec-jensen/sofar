// Package crypt seals secrets (the SimpleFIN access URL, TOTP secrets) with
// AES-256-GCM. spec: S17 security.
package crypt

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
)

// Box seals and opens strings with one 32-byte key.
type Box struct {
	aead cipher.AEAD
	key  []byte
}

// New returns a Box for a 32-byte key.
func New(key []byte) (*Box, error) {
	if len(key) != 32 {
		return nil, errors.New("encryption key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead, key: append([]byte(nil), key...)}, nil
}

// Seal encrypts raw and returns base64 (nonce followed by ciphertext).
func (b *Box) Seal(raw string) string {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		panic(err)
	}
	return base64.StdEncoding.EncodeToString(b.aead.Seal(nonce, nonce, []byte(raw), nil))
}

// Unseal reverses Seal.
func (b *Box) Unseal(sealed string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(sealed)
	n := b.aead.NonceSize()
	if err != nil || len(raw) < n {
		return "", errors.New("invalid encrypted value")
	}
	plain, err := b.aead.Open(nil, raw[:n], raw[n:], nil)
	return string(plain), err
}

// MAC returns a keyed hash of value for the given purpose. Used to store
// high-entropy secrets (recovery codes) so a database leak alone cannot be
// brute-forced offline.
func (b *Box) MAC(purpose, value string) string {
	m := hmac.New(sha256.New, b.key)
	m.Write([]byte(purpose))
	m.Write([]byte{0})
	m.Write([]byte(value))
	return hex.EncodeToString(m.Sum(nil))
}

// RandomToken returns 24 random bytes as unpadded base64url.
func RandomToken() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// HashToken is the stored form of a session token.
func HashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
