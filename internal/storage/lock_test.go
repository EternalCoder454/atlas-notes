package storage

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testPassword = "a password nobody would guess"

// lockedStore is a store with a password already set and the vault unlocked.
func lockedStore(t *testing.T) *Store {
	t.Helper()
	s := testStore(t)
	if err := s.SetPassword(testPassword); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	return s
}

// grep reports whether needle appears anywhere under the vault, in any file.
// It is how the tests check the claim that matters: that locking a note takes
// its content off the disk, rather than hiding it in the interface.
func grep(t *testing.T, vault, needle string) string {
	t.Helper()
	var found string
	err := filepath.Walk(vault, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || found != "" {
			return err
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		if bytes.Contains(data, []byte(needle)) {
			found = p
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

func TestLockNoteRemovesPlaintext(t *testing.T) {
	s := lockedStore(t)
	const secret = "the account number is 12345"
	if err := s.WriteNote("Private", "# Private\n\n"+secret+"\n"); err != nil {
		t.Fatal(err)
	}
	if at := grep(t, s.VaultPath, secret); at == "" {
		t.Fatal("the note was not readable on disk before locking; the test proves nothing")
	}
	if err := s.LockNote("Private"); err != nil {
		t.Fatalf("LockNote: %v", err)
	}
	if at := grep(t, s.VaultPath, secret); at != "" {
		t.Errorf("the secret is still readable on disk at %s", at)
	}
	if _, err := os.Stat(filepath.Join(s.VaultPath, "Private"+noteExt)); !os.IsNotExist(err) {
		t.Error("the unencrypted file is still there")
	}
	if _, err := os.Stat(filepath.Join(s.VaultPath, "Private"+lockedExt)); err != nil {
		t.Errorf("the encrypted file is missing: %v", err)
	}
	if !s.IsNoteLocked("Private") {
		t.Error("the note does not report itself as locked")
	}
}

func TestLockedNoteNeedsThePassword(t *testing.T) {
	s := lockedStore(t)
	const body = "# Private\n\nthe account number is 12345\n"
	if err := s.WriteNote("Private", body); err != nil {
		t.Fatal(err)
	}
	if err := s.LockNote("Private"); err != nil {
		t.Fatal(err)
	}

	// Unlocked: readable.
	got, err := s.ReadNote("Private")
	if err != nil {
		t.Fatalf("ReadNote while unlocked: %v", err)
	}
	if got != body {
		t.Fatalf("content changed through the lock: %q", got)
	}

	// Locked: not readable, and the reason is the one the interface acts on.
	s.Lock()
	if _, err := s.ReadNote("Private"); err != ErrLocked {
		t.Fatalf("ReadNote while locked = %v, want ErrLocked", err)
	}
	if err := s.Unlock("not the password"); err == nil {
		t.Fatal("the wrong password unlocked the vault")
	}
	if _, err := s.ReadNote("Private"); err != ErrLocked {
		t.Fatal("a failed unlock left the vault open")
	}
	if err := s.Unlock(testPassword); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	if got, err := s.ReadNote("Private"); err != nil || got != body {
		t.Fatalf("after unlocking: %q, %v", got, err)
	}
}

// TestSavingALockedNoteStaysLocked is the one that would bite in daily use:
// autosave must not quietly write the note back in the clear.
func TestSavingALockedNoteStaysLocked(t *testing.T) {
	s := lockedStore(t)
	if err := s.WriteNote("Private", "# Private\n\nfirst\n"); err != nil {
		t.Fatal(err)
	}
	if err := s.LockNote("Private"); err != nil {
		t.Fatal(err)
	}
	const secret = "second and more secret"
	if err := s.WriteNote("Private", "# Private\n\n"+secret+"\n"); err != nil {
		t.Fatalf("WriteNote on a locked note: %v", err)
	}
	if at := grep(t, s.VaultPath, secret); at != "" {
		t.Errorf("saving a locked note wrote it in the clear at %s", at)
	}
	if !s.IsNoteLocked("Private") {
		t.Error("saving a locked note unlocked it")
	}
	got, err := s.ReadNote("Private")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, secret) {
		t.Fatalf("the new content was not saved: %q", got)
	}
}

// TestWritingToALockedVaultFails: with the vault locked there is no key, so a
// save of a locked note has to fail rather than fall back to plaintext.
func TestWritingToALockedVaultFails(t *testing.T) {
	s := lockedStore(t)
	if err := s.WriteNote("Private", "# Private\n\nfirst\n"); err != nil {
		t.Fatal(err)
	}
	if err := s.LockNote("Private"); err != nil {
		t.Fatal(err)
	}
	s.Lock()
	const secret = "written while locked"
	if err := s.WriteNote("Private", secret); err != ErrLocked {
		t.Fatalf("WriteNote on a locked vault = %v, want ErrLocked", err)
	}
	if at := grep(t, s.VaultPath, secret); at != "" {
		t.Errorf("a refused write still put the content on disk at %s", at)
	}
}

// TestNoteInLockedFolderIsBornLocked: a new note in a locked folder must never
// exist in the clear, not even for the moment between writing and encrypting.
func TestNoteInLockedFolderIsBornLocked(t *testing.T) {
	s := lockedStore(t)
	if err := s.CreateFolder("Secret"); err != nil {
		t.Fatal(err)
	}
	if err := s.LockFolder("Secret"); err != nil {
		t.Fatalf("LockFolder: %v", err)
	}
	if !s.IsFolderLocked("Secret") {
		t.Fatal("the folder does not report itself as locked")
	}
	const secret = "born locked, never written in the clear"
	if err := s.WriteNote("Secret/New", "# New\n\n"+secret+"\n"); err != nil {
		t.Fatal(err)
	}
	if at := grep(t, s.VaultPath, secret); at != "" {
		t.Errorf("a note in a locked folder was written in the clear at %s", at)
	}
	if !s.IsNoteLocked("Secret/New") {
		t.Error("a note created in a locked folder is not locked")
	}
	if _, err := os.Stat(filepath.Join(s.VaultPath, "Secret", "New"+noteExt)); !os.IsNotExist(err) {
		t.Error("an unencrypted file exists for a note in a locked folder")
	}
}

func TestLockAndUnlockFolder(t *testing.T) {
	s := lockedStore(t)
	if err := s.CreateFolder("Secret"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Secret/One", "Secret/Two"} {
		if err := s.WriteNote(name, "# "+name+"\n\nbody of "+name+"\n"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.LockFolder("Secret"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Secret/One", "Secret/Two"} {
		if !s.IsNoteLocked(name) {
			t.Errorf("%s was not locked with its folder", name)
		}
	}
	if at := grep(t, s.VaultPath, "body of Secret/One"); at != "" {
		t.Errorf("a note in the locked folder is readable at %s", at)
	}

	if err := s.UnlockFolder("Secret"); err != nil {
		t.Fatalf("UnlockFolder: %v", err)
	}
	for _, name := range []string{"Secret/One", "Secret/Two"} {
		if s.IsNoteLocked(name) {
			t.Errorf("%s is still locked after unlocking the folder", name)
		}
		if got, err := s.ReadNote(name); err != nil || !strings.Contains(got, "body of "+name) {
			t.Errorf("%s did not survive the round trip: %q, %v", name, got, err)
		}
	}
	if s.IsFolderLocked("Secret") {
		t.Error("the folder marker survived unlocking")
	}
}

// TestDeleteAndRenameALockedNote: both act on the file, and a locked note's
// file is under the other extension.
func TestDeleteAndRenameALockedNote(t *testing.T) {
	s := lockedStore(t)
	if err := s.WriteNote("Private", "# Private\n\nsecret body\n"); err != nil {
		t.Fatal(err)
	}
	if err := s.LockNote("Private"); err != nil {
		t.Fatal(err)
	}
	if err := s.RenameNote("Private", "Renamed"); err != nil {
		t.Fatalf("RenameNote: %v", err)
	}
	if !s.IsNoteLocked("Renamed") {
		t.Error("the renamed note lost its lock")
	}
	if got, err := s.ReadNote("Renamed"); err != nil || !strings.Contains(got, "secret body") {
		t.Fatalf("the renamed note did not survive: %q, %v", got, err)
	}
	if err := s.DeleteNote("Renamed"); err != nil {
		t.Fatalf("DeleteNote: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.VaultPath, "Renamed"+lockedExt)); !os.IsNotExist(err) {
		t.Error("deleting a locked note left its encrypted file behind")
	}
	if at := grep(t, s.VaultPath, "secret body"); at != "" {
		t.Errorf("the deleted note's content is still on disk at %s", at)
	}
}

// TestReindexFindsLockedNotes: the index is a cache rebuilt from the vault, so
// a rebuild has to recover which notes are locked.
func TestReindexFindsLockedNotes(t *testing.T) {
	s := lockedStore(t)
	if err := s.WriteNote("Open", "# Open\n\nplain\n"); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteNote("Private", "# Private\n\nsecret\n"); err != nil {
		t.Fatal(err)
	}
	if err := s.LockNote("Private"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DELETE FROM notes`); err != nil {
		t.Fatal(err)
	}
	if err := s.Reindex(); err != nil {
		t.Fatalf("Reindex: %v", err)
	}
	notes, err := s.ListNotes()
	if err != nil {
		t.Fatal(err)
	}
	state := map[string]bool{}
	for _, n := range notes {
		state[n.Path] = n.Locked
	}
	if locked, ok := state["Private"]; !ok || !locked {
		t.Errorf("Private: indexed=%v locked=%v, want indexed and locked", ok, locked)
	}
	if locked, ok := state["Open"]; !ok || locked {
		t.Errorf("Open: indexed=%v locked=%v, want indexed and unlocked", ok, locked)
	}
	list, err := s.LockedNotes()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0] != "Private" {
		t.Errorf("LockedNotes = %v, want [Private]", list)
	}
}

func TestChangePassword(t *testing.T) {
	s := lockedStore(t)
	const body = "# Private\n\nsecret body\n"
	if err := s.WriteNote("Private", body); err != nil {
		t.Fatal(err)
	}
	if err := s.LockNote("Private"); err != nil {
		t.Fatal(err)
	}
	if err := s.ChangePassword("wrong", "new password"); err == nil {
		t.Fatal("the password was changed with the wrong current password")
	}
	if err := s.ChangePassword(testPassword, "new password"); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	s.Lock()
	if err := s.Unlock(testPassword); err == nil {
		t.Fatal("the old password still opens the vault")
	}
	if err := s.Unlock("new password"); err != nil {
		t.Fatalf("the new password does not open the vault: %v", err)
	}
	if got, err := s.ReadNote("Private"); err != nil || got != body {
		t.Fatalf("the note did not survive the re-seal: %q, %v", got, err)
	}
}

// TestLockWithoutPasswordRefused: locking needs a key, and there is none until
// a password is set.
func TestLockWithoutPasswordRefused(t *testing.T) {
	s := testStore(t)
	if err := s.WriteNote("Note", "# Note\n\nbody\n"); err != nil {
		t.Fatal(err)
	}
	if s.HasPassword() {
		t.Fatal("a fresh vault reports a password")
	}
	if err := s.LockNote("Note"); err != ErrNoPassword {
		t.Errorf("LockNote = %v, want ErrNoPassword", err)
	}
	if err := s.Unlock("anything"); err != ErrNoPassword {
		t.Errorf("Unlock = %v, want ErrNoPassword", err)
	}
	if err := s.SetPassword(""); err == nil {
		t.Error("an empty password was accepted")
	}
}

// TestPasswordSurvivesReopening: the salt and verifier live in the vault, so a
// vault copied elsewhere still opens with its own password.
func TestPasswordSurvivesReopening(t *testing.T) {
	dir := t.TempDir()
	vault := filepath.Join(dir, "vault")

	first, err := Open(vault, filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := first.SetPassword(testPassword); err != nil {
		t.Fatal(err)
	}
	if err := first.WriteNote("Private", "# Private\n\nsecret body\n"); err != nil {
		t.Fatal(err)
	}
	if err := first.LockNote("Private"); err != nil {
		t.Fatal(err)
	}
	first.Close()

	second, err := Open(vault, filepath.Join(dir, "index2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if !second.HasPassword() {
		t.Fatal("the reopened vault has no password")
	}
	if second.IsUnlocked() {
		t.Fatal("the reopened vault started unlocked")
	}
	if _, err := second.ReadNote("Private"); err != ErrLocked {
		t.Fatalf("ReadNote = %v, want ErrLocked", err)
	}
	if err := second.Unlock(testPassword); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	if got, err := second.ReadNote("Private"); err != nil || !strings.Contains(got, "secret body") {
		t.Fatalf("after reopening: %q, %v", got, err)
	}
}

// TestLockedNoteSurvivesADamagedIndex: the index is a cache. Losing it must not
// lose the notes, locked ones included.
func TestLockedNoteSurvivesADamagedIndex(t *testing.T) {
	s := lockedStore(t)
	if err := s.WriteNote("Private", "# Private\n\nsecret body\n"); err != nil {
		t.Fatal(err)
	}
	if err := s.LockNote("Private"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DELETE FROM notes`); err != nil {
		t.Fatal(err)
	}
	if got, err := s.ReadNote("Private"); err != nil || !strings.Contains(got, "secret body") {
		t.Fatalf("reading a locked note with no index row: %q, %v", got, err)
	}
}
