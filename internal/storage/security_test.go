package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// escapeNames are the shapes a note or folder name can take that would, if they
// were resolved literally, address a file outside the vault. They can come from
// the title field, the tree's rename prompt, or a vault synced from elsewhere.
var escapeNames = []string{
	"../escaped",
	"../../escaped",
	"sub/../../escaped",
	"a/../../b",
	"..",
	"../",
	"./../escaped",
	"/absolute",
	"//absolute",
	"  ../spaced  ",
}

// TestNotesStayInsideVault is the important one: whatever the name, nothing may
// be created, read, renamed or deleted outside the vault directory.
func TestNotesStayInsideVault(t *testing.T) {
	dir := t.TempDir()
	vault := filepath.Join(dir, "vault")
	s, err := Open(vault, filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// A file just outside the vault that must never be touched.
	outside := filepath.Join(dir, "escaped.md.zst")
	if err := os.WriteFile(outside, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, name := range escapeNames {
		if err := s.WriteNote(name, "payload"); err != nil {
			continue // refusing outright is also a correct outcome
		}
		abs := filepath.Join(vault, filepath.FromSlash(normalizeRel(name)))
		if !withinVault(vault, abs) {
			t.Errorf("WriteNote(%q) resolved outside the vault: %s", name, abs)
		}
	}

	if data, err := os.ReadFile(outside); err != nil || string(data) != "original" {
		t.Fatalf("a file outside the vault was overwritten: %v %q", err, data)
	}

	// Nothing should have been created next to the vault either.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		switch e.Name() {
		case "vault", "index.db", "index.db-wal", "index.db-shm", "escaped.md.zst":
		default:
			t.Errorf("unexpected entry created outside the vault: %s", e.Name())
		}
	}
}

func TestFoldersStayInsideVault(t *testing.T) {
	dir := t.TempDir()
	vault := filepath.Join(dir, "vault")
	s, err := Open(vault, filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	victim := filepath.Join(dir, "keep-me")
	if err := os.MkdirAll(victim, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, name := range escapeNames {
		s.CreateFolder(name)            // may fail; must not escape
		s.RenameFolder(name, "renamed") // ditto
		s.DeleteFolder(name)
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("a directory outside the vault was removed or moved: %v", err)
	}
	if err := s.DeleteFolder(""); err == nil {
		t.Error("DeleteFolder(\"\") must not delete the vault itself")
	}
	if _, err := os.Stat(vault); err != nil {
		t.Fatalf("the vault directory was deleted: %v", err)
	}
}

// TestNormalizeRelSanitizes documents exactly what the sanitizer does with the
// awkward inputs, since every path in the app goes through it.
func TestNormalizeRelSanitizes(t *testing.T) {
	cases := map[string]string{
		"Note":                  "Note",
		"Work/Note":             "Work/Note",
		"../escaped":            "escaped",
		"../../a/../b":          "a/b", // ".." is dropped, not resolved
		"/absolute":             "absolute",
		"//double//slash":       "double/slash",
		"./here":                "here",
		"..":                    "",
		"":                      "",
		"   ":                   "",
		"Note.md.zst":           "Note",
		"  Padded  /  Note  ":   "Padded/Note",
		"Work/../../../../root": "Work/root",
	}
	for in, want := range cases {
		if got := normalizeRel(in); got != want {
			t.Errorf("normalizeRel(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestReadNoteRejectsEscape makes sure reads are confined too: a crafted name
// must not be able to pull a file in from outside the vault.
func TestReadNoteRejectsEscape(t *testing.T) {
	dir := t.TempDir()
	vault := filepath.Join(dir, "vault")
	s, err := Open(vault, filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	secret := filepath.Join(dir, "secret.md.zst")
	if err := os.WriteFile(secret, s.enc.EncodeAll([]byte("top secret"), nil), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := s.ReadNote("../secret"); err == nil && strings.Contains(got, "top secret") {
		t.Error("ReadNote read a file from outside the vault")
	}
}

// TestCorruptNoteIsSurvivable makes sure a damaged or foreign file in the vault
// is an error, not a crash: vaults get synced, restored from backups, and
// edited by other tools.
func TestCorruptNoteIsSurvivable(t *testing.T) {
	dir := t.TempDir()
	vault := filepath.Join(dir, "vault")
	s, err := Open(vault, filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.WriteNote("Good", "# Fine\n"); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string][]byte{
		"Truncated": {0x28, 0xb5, 0x2f, 0xfd}, // a zstd magic number and nothing else
		"Garbage":   []byte("this is not compressed at all"),
	} {
		if err := os.WriteFile(filepath.Join(vault, name+noteExt), body, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ReadNote(name); err == nil {
			t.Errorf("ReadNote(%q) accepted a corrupt file", name)
		}
	}

	// A zero-length file is not corrupt, it is an empty note — opening it
	// should show an empty document rather than fail.
	if err := os.WriteFile(filepath.Join(vault, "Empty"+noteExt), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := s.ReadNote("Empty"); err != nil || got != "" {
		t.Errorf("ReadNote(empty file) = %q, %v; want empty content and no error", got, err)
	}

	// Reindex must still complete and keep the healthy note.
	if err := s.Reindex(); err != nil {
		t.Fatalf("Reindex over a vault with corrupt files: %v", err)
	}
	if _, err := s.ReadNote("Good"); err != nil {
		t.Errorf("healthy note unreadable after reindex: %v", err)
	}
}

// TestDecompressionBombIsBounded checks the cap on how large a note may expand
// to. Without it, a few kilobytes of hostile input in a synced vault turn into
// an out-of-memory kill.
func TestDecompressionBombIsBounded(t *testing.T) {
	dir := t.TempDir()
	vault := filepath.Join(dir, "vault")
	s, err := Open(vault, filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// 512 MiB of zeros compresses to a few hundred bytes.
	bomb := s.enc.EncodeAll(make([]byte, 512<<20), nil)
	t.Logf("bomb: %d bytes on disk, %d MiB decompressed", len(bomb), 512)
	if err := os.WriteFile(filepath.Join(vault, "Bomb"+noteExt), bomb, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadNote("Bomb")
	if err == nil {
		t.Fatalf("a %d MiB note was decompressed in full (%d bytes read)", 512, len(got))
	}
	t.Logf("rejected as expected: %v", err)
}
