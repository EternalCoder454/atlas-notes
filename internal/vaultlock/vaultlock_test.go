package vaultlock

import (
	"bytes"
	"strings"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	cfg, key, err := New("correct horse battery staple")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	secret := []byte("# Private\n\nSomething nobody else should read.\n")
	sealed, err := Seal(key, secret)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if bytes.Contains(sealed, []byte("Private")) {
		t.Fatal("the plaintext is visible in the sealed bytes")
	}
	got, err := Open(key, sealed)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(got, secret) {
		t.Fatalf("round trip changed the content: %q", got)
	}
	// The same key has to come back from the same password.
	again, err := cfg.Unlock("correct horse battery staple")
	if err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	if !bytes.Equal(key, again) {
		t.Fatal("unlocking with the right password derived a different key")
	}
	if _, err := Open(again, sealed); err != nil {
		t.Fatalf("a key derived again could not open the note: %v", err)
	}
}

func TestWrongPassword(t *testing.T) {
	cfg, _, err := New("the right one")
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "the wrong one", "the right one ", "THE RIGHT ONE"} {
		if _, err := cfg.Unlock(bad); err != ErrWrongPassword {
			t.Errorf("Unlock(%q) = %v, want ErrWrongPassword", bad, err)
		}
	}
}

// TestTamperedNoteFails is the property that separates encryption from a flag
// on a file: editing the bytes must not produce readable content.
func TestTamperedNoteFails(t *testing.T) {
	_, key, err := New("password")
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := Seal(key, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	for i := range sealed {
		bad := bytes.Clone(sealed)
		bad[i] ^= 0x01
		if _, err := Open(key, bad); err == nil {
			t.Fatalf("a note with byte %d flipped still opened", i)
		}
	}
	// Truncation, too.
	for _, n := range []int{0, 1, 23, 24, len(sealed) - 1} {
		if _, err := Open(key, sealed[:n]); err == nil {
			t.Errorf("a note truncated to %d bytes still opened", n)
		}
	}
}

// TestNoncesDiffer guards the one mistake that would break the cipher: sealing
// twice under one key must not reuse a nonce.
func TestNoncesDiffer(t *testing.T) {
	_, key, err := New("password")
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool, 200)
	for i := 0; i < 200; i++ {
		sealed, err := Seal(key, []byte("the same plaintext every time"))
		if err != nil {
			t.Fatal(err)
		}
		nonce := string(sealed[:24])
		if seen[nonce] {
			t.Fatal("a nonce repeated")
		}
		seen[nonce] = true
	}
}

// TestSaltsDiffer: two vaults with the same password must not share a key, or
// one cracked password would open every Atlas Notes vault that used it.
func TestSaltsDiffer(t *testing.T) {
	a, keyA, err := New("same password")
	if err != nil {
		t.Fatal(err)
	}
	b, keyB, err := New("same password")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a.Salt, b.Salt) {
		t.Fatal("two vaults were given the same salt")
	}
	if bytes.Equal(keyA, keyB) {
		t.Fatal("two vaults with one password derived the same key")
	}
	if _, err := b.Unlock("same password"); err != nil {
		t.Fatalf("the second vault would not open: %v", err)
	}
}

func TestEmptyPasswordRefused(t *testing.T) {
	if _, _, err := New(""); err == nil {
		t.Fatal("an empty password was accepted")
	}
}

func TestConfigRoundTrip(t *testing.T) {
	cfg, key, err := New("password")
	if err != nil {
		t.Fatal(err)
	}
	data, err := cfg.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("password")) {
		t.Fatal("the password appears in the stored configuration")
	}
	back, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got, err := back.Unlock("password")
	if err != nil {
		t.Fatalf("Unlock after reload: %v", err)
	}
	if !bytes.Equal(got, key) {
		t.Fatal("reloading the configuration changed the key")
	}
}

// TestParseRejectsBadConfig covers the lock file being truncated, hand-edited
// or written by something else. None of these may reach the KDF, which panics
// on zero parameters.
func TestParseRejectsBadConfig(t *testing.T) {
	cases := map[string]string{
		"empty":            ``,
		"not json":         `not json at all`,
		"no version":       `{"salt":"AAAAAAAAAAAAAAAAAAAAAA==","time":3,"memory":65536,"threads":4,"verifier":"AAAA"}`,
		"future version":   `{"version":99,"salt":"AAAAAAAAAAAAAAAAAAAAAA==","time":3,"memory":65536,"threads":4,"verifier":"AAAA"}`,
		"short salt":       `{"version":1,"salt":"AAAA","time":3,"memory":65536,"threads":4,"verifier":"AAAA"}`,
		"zero time":        `{"version":1,"salt":"AAAAAAAAAAAAAAAAAAAAAA==","time":0,"memory":65536,"threads":4,"verifier":"AAAA"}`,
		"zero memory":      `{"version":1,"salt":"AAAAAAAAAAAAAAAAAAAAAA==","time":3,"memory":0,"threads":4,"verifier":"AAAA"}`,
		"zero threads":     `{"version":1,"salt":"AAAAAAAAAAAAAAAAAAAAAA==","time":3,"memory":65536,"threads":0,"verifier":"AAAA"}`,
		"missing verifier": `{"version":1,"salt":"AAAAAAAAAAAAAAAAAAAAAA==","time":3,"memory":65536,"threads":4}`,
	}
	for name, data := range cases {
		if _, err := Parse([]byte(data)); err == nil {
			t.Errorf("%s: Parse accepted it", name)
		}
	}
}

// TestUnlockOnBadConfigDoesNotPanic: Parse is the gate, but Unlock must hold on
// its own, because a Config can also be built in code.
func TestUnlockOnBadConfigDoesNotPanic(t *testing.T) {
	for name, cfg := range map[string]*Config{
		"zero value": {},
		"nil":        nil,
		"no salt":    {Version: 1, Time: 3, Memory: 1024, Threads: 1, Verifier: []byte("x")},
	} {
		if _, err := cfg.Unlock("password"); err == nil {
			t.Errorf("%s: Unlock succeeded", name)
		}
	}
}

// TestLongAndOddPasswords: a password comes from a text field and can be
// anything a keyboard produces.
func TestLongAndOddPasswords(t *testing.T) {
	for _, pw := range []string{
		strings.Repeat("a", 4096),
		"emoji 🔐 and accents éàü",
		"  leading and trailing  ",
		"tab\tand\nnewline",
		"\x00embedded null",
	} {
		cfg, key, err := New(pw)
		if err != nil {
			t.Fatalf("New(%q): %v", pw, err)
		}
		got, err := cfg.Unlock(pw)
		if err != nil {
			t.Errorf("Unlock(%q): %v", pw, err)
			continue
		}
		if !bytes.Equal(got, key) {
			t.Errorf("Unlock(%q) derived a different key", pw)
		}
	}
}
