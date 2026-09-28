package storage

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// fakeTrash moves what it is given into a directory of its own and records
// it, standing in for the system Trash. Tests never touch the real one.
type fakeTrash struct {
	dir   string
	calls []string
	fail  error
}

func newFakeTrash(t *testing.T) *fakeTrash { return &fakeTrash{dir: t.TempDir()} }

func (f *fakeTrash) trash(path string) error {
	f.calls = append(f.calls, path)
	if f.fail != nil {
		return f.fail
	}
	return os.Rename(path, filepath.Join(f.dir, filepath.Base(path)))
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func TestDeletedNoteGoesToTheTrash(t *testing.T) {
	s := testStore(t)
	tr := newFakeTrash(t)
	s.Trash = tr.trash
	s.WriteNote("Keep me", "# Keep me\n\nrecoverable\n")
	abs := filepath.Join(s.VaultPath, "Keep me"+noteExt)

	if err := s.DeleteNote("Keep me"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(tr.calls, []string{abs}) {
		t.Fatalf("trash was given %v, want the note", tr.calls)
	}
	if !exists(filepath.Join(tr.dir, "Keep me"+noteExt)) {
		t.Error("the note is not in the Trash")
	}
	if notes, _ := s.ListNotes(); len(notes) != 0 {
		t.Error("a note in the Trash is still listed")
	}
	if indexHas(t, s, "recoverable") {
		t.Error("a note in the Trash is still searchable")
	}
}

func TestDeletedFolderGoesToTheTrashWhole(t *testing.T) {
	s := testStore(t)
	tr := newFakeTrash(t)
	s.Trash = tr.trash
	s.CreateFolder("Projects")
	s.WriteNote("Projects/One", "# One\n")
	s.WriteNote("Projects/Two", "# Two\n")

	if err := s.DeleteFolder("Projects"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(tr.calls, []string{filepath.Join(s.VaultPath, "Projects")}) {
		t.Fatalf("trash was given %v, want the folder once", tr.calls)
	}
	if !exists(filepath.Join(tr.dir, "Projects", "Two"+noteExt)) {
		t.Error("the folder's notes did not go to the Trash with it")
	}
}

// When the Trash cannot take something, nothing is deleted. Deleting it for
// good instead is a separate decision, made by whoever asked.
func TestAFailedTrashDeletesNothing(t *testing.T) {
	s := testStore(t)
	tr := newFakeTrash(t)
	tr.fail = errors.New("no Trash on this drive")
	s.Trash = tr.trash
	s.WriteNote("Stays", "# Stays\n")
	abs := filepath.Join(s.VaultPath, "Stays"+noteExt)

	err := s.DeleteNote("Stays")
	if !errors.Is(err, ErrTrashFailed) {
		t.Fatalf("got %v, want ErrTrashFailed", err)
	}
	if !exists(abs) {
		t.Fatal("a failed trash deleted the note anyway")
	}
	if notes, _ := s.ListNotes(); len(notes) != 1 {
		t.Fatal("a failed trash dropped the note from the index")
	}

	if err := s.DeleteNotePermanently("Stays"); err != nil {
		t.Fatal(err)
	}
	if exists(abs) {
		t.Error("deleting permanently left the note")
	}
	if len(tr.calls) != 1 {
		t.Errorf("deleting permanently went through the Trash: %v", tr.calls)
	}
}

// The trap this guards: locking a note deletes its unencrypted copy, and that
// copy has to be gone. If it went to the Trash, locking would leave the
// note's text sitting in plain view in ~/.local/share/Trash.
func TestLockingNeverUsesTheTrash(t *testing.T) {
	s := lockedStore(t)
	tr := newFakeTrash(t)
	s.Trash = tr.trash

	s.WriteNote("Secret", "# Secret\n\nplaintext that must not survive\n")
	if err := s.LockNote("Secret"); err != nil {
		t.Fatal(err)
	}
	s.CreateFolder("Vault")
	s.WriteNote("Vault/Inside", "# Inside\n")
	if err := s.LockFolder("Vault"); err != nil {
		t.Fatal(err)
	}
	// Saving into a locked folder also deletes an unencrypted copy.
	s.WriteNote("Vault/Inside", "# Inside\n\nedited\n")
	if err := s.UnlockNote("Secret"); err != nil {
		t.Fatal(err)
	}

	if len(tr.calls) != 0 {
		t.Fatalf("locking handed files to the Trash: %v", tr.calls)
	}
	if entries, _ := os.ReadDir(tr.dir); len(entries) != 0 {
		t.Fatal("something ended up in the Trash")
	}
}

// Without a Trash, as on Android, deleting is permanent, as it always was.
func TestWithoutATrashDeletingIsPermanent(t *testing.T) {
	s := testStore(t)
	s.WriteNote("Gone", "# Gone\n")
	abs := filepath.Join(s.VaultPath, "Gone"+noteExt)
	if err := s.DeleteNote("Gone"); err != nil {
		t.Fatal(err)
	}
	if exists(abs) {
		t.Error("the note is still on disk")
	}
}
