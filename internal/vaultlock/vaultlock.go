// Package vaultlock is the cryptography behind password-protected notes. It
// knows nothing about notes, files or GTK: it turns a password into a key,
// and it seals and opens bytes with that key.
//
// What it guarantees, and what it does not. A sealed note is unreadable
// without the password, by Atlas Notes or by anything else — the bytes on disk
// are ciphertext, not compressed Markdown with a flag set. What it cannot do
// is help someone who forgets the password: there is no recovery path, because
// any recovery path is a second way in, and a second way in is the thing being
// protected against. The interface says so before it takes a password.
//
// Choices worth stating. Argon2id derives the key, because a password is low
// entropy and a fast hash would let anyone with the file guess billions of them
// a second; the parameters below cost real memory and time on purpose.
// XChaCha20-Poly1305 seals the bytes, because its nonce is large enough to pick
// at random without tracking a counter, and it authenticates: a tampered file
// fails to open rather than opening to garbage.
package vaultlock

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"
)

// Argon2id parameters. These cost about a tenth of a second and 64 MiB on a
// desktop of 2026 — unnoticeable when unlocking once, and ruinous for someone
// trying passwords in bulk. They are stored alongside the salt so a vault
// sealed today still opens after these numbers are raised.
const (
	defaultTime    = 3
	defaultMemory  = 64 * 1024 // KiB
	defaultThreads = 4
	keyLen         = chacha20poly1305.KeySize
	saltLen        = 16
)

// formatVersion is the on-disk shape of Config. It exists so a later change of
// cipher or KDF can be recognised rather than guessed at.
const formatVersion = 1

// verifierPlaintext is what Config.Verifier seals. Checking a password means
// opening it, which costs one Argon2id derivation and no note.
const verifierPlaintext = "atlas-notes/vaultlock"

// ErrWrongPassword is returned when a password does not open the vault. It is
// deliberately the same error whether the password is wrong or the verifier is
// damaged: telling those apart helps an attacker more than a user.
var ErrWrongPassword = errors.New("wrong password")

// Config is the public half of a locked vault: the salt and cost to derive the
// key, and a sealed blob to check a password against. It contains nothing
// secret and is safe to store in the clear.
type Config struct {
	Version  int    `json:"version"`
	Salt     []byte `json:"salt"`
	Time     uint32 `json:"time"`
	Memory   uint32 `json:"memory"`
	Threads  uint8  `json:"threads"`
	Verifier []byte `json:"verifier"`
}

// Key is a derived encryption key. It lives in memory for as long as the vault
// is unlocked and is never written anywhere.
type Key []byte

// New builds a fresh Config for a password and returns the key it derives, so
// the caller can seal with it without asking for the password twice.
func New(password string) (*Config, Key, error) {
	if password == "" {
		return nil, nil, errors.New("password is empty")
	}
	salt := make([]byte, saltLen)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, nil, err
	}
	cfg := &Config{
		Version: formatVersion,
		Salt:    salt,
		Time:    defaultTime,
		Memory:  defaultMemory,
		Threads: defaultThreads,
	}
	key := cfg.derive(password)
	verifier, err := Seal(key, []byte(verifierPlaintext))
	if err != nil {
		return nil, nil, err
	}
	cfg.Verifier = verifier
	return cfg, key, nil
}

// Unlock derives the key for a password and checks it against the verifier.
func (c *Config) Unlock(password string) (Key, error) {
	if err := c.valid(); err != nil {
		return nil, err
	}
	key := c.derive(password)
	plain, err := Open(key, c.Verifier)
	if err != nil || string(plain) != verifierPlaintext {
		return nil, ErrWrongPassword
	}
	return key, nil
}

// derive runs the KDF. It is only correct for a Config that has passed valid.
func (c *Config) derive(password string) Key {
	return argon2.IDKey([]byte(password), c.Salt, c.Time, c.Memory, c.Threads, keyLen)
}

// valid rejects a Config that would make the KDF panic or produce a key from
// nothing — a truncated, hand-edited or foreign lock file.
func (c *Config) valid() error {
	switch {
	case c == nil:
		return errors.New("vault lock: no configuration")
	case c.Version != formatVersion:
		return fmt.Errorf("vault lock: unsupported format version %d", c.Version)
	case len(c.Salt) < saltLen:
		return errors.New("vault lock: salt is missing or too short")
	case c.Time == 0 || c.Memory == 0 || c.Threads == 0:
		return errors.New("vault lock: key derivation parameters are missing")
	case len(c.Verifier) == 0:
		return errors.New("vault lock: verifier is missing")
	}
	return nil
}

// Marshal renders a Config as the JSON written beside the vault.
func (c *Config) Marshal() ([]byte, error) { return json.MarshalIndent(c, "", "  ") }

// Parse reads a Config back, rejecting one that could not be used.
func Parse(data []byte) (*Config, error) {
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	if err := c.valid(); err != nil {
		return nil, err
	}
	return &c, nil
}

// Seal encrypts plaintext, returning nonce || ciphertext || tag. The nonce is
// random: XChaCha20's is 192 bits, wide enough that repeating one by chance is
// not a risk worth keeping a counter for.
func Seal(key Key, plaintext []byte) ([]byte, error) {
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	// Appending to nonce puts the nonce in front of the ciphertext, which is
	// where Open expects it.
	return aead.Seal(nonce, nonce, plaintext, nil), nil
}

// Open decrypts what Seal produced. A wrong key and a tampered file are the
// same failure: the authentication tag does not check out.
func Open(key Key, blob []byte) ([]byte, error) {
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, err
	}
	if len(blob) < aead.NonceSize() {
		return nil, ErrWrongPassword
	}
	nonce, ciphertext := blob[:aead.NonceSize()], blob[aead.NonceSize():]
	plain, err := aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, ErrWrongPassword
	}
	return plain, nil
}
