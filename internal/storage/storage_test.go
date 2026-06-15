package storage

import (
	"os"
	"path/filepath"
	"testing"

	"atlas-notes/internal/checklist"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "vault"), filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestNoteRoundTrip(t *testing.T) {
	s := testStore(t)
	content := "# Hello\n\nThis is a note with some **bold** text.\n"
	if err := s.WriteNote("Hello", content); err != nil {
		t.Fatalf("WriteNote: %v", err)
	}
	got, err := s.ReadNote("Hello")
	if err != nil {
		t.Fatalf("ReadNote: %v", err)
	}
	if got != content {
		t.Errorf("ReadNote = %q want %q", got, content)
	}

	raw, err := os.ReadFile(filepath.Join(s.VaultPath, "Hello.md.zst"))
	if err != nil {
		t.Fatalf("read raw file: %v", err)
	}
	// zstd standard frame magic: 0x28 0xB5 0x2F 0xFD (little-endian).
	if len(raw) < 4 || raw[0] != 0x28 || raw[1] != 0xB5 || raw[2] != 0x2F || raw[3] != 0xFD {
		t.Errorf("file is not a zstd frame: % x", raw[:min(4, len(raw))])
	}
}

func TestIndexAndTitle(t *testing.T) {
	s := testStore(t)
	if err := s.WriteNote("Work/Plan", "# My Plan\n\n- [ ] step one\n"); err != nil {
		t.Fatal(err)
	}
	notes, err := s.ListNotes()
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 {
		t.Fatalf("ListNotes len = %d want 1", len(notes))
	}
	n := notes[0]
	if n.Path != "Work/Plan" || n.Folder != "Work" || n.Title != "Plan" || !n.IsChecklist {
		t.Errorf("meta = %+v", n)
	}
}

func TestChecklistItemsIndex(t *testing.T) {
	s := testStore(t)
	content := "- [ ] a <!-- priority:high order:1 -->\n- [x] b <!-- priority:low order:2 -->\n"
	if err := s.WriteNote("List", content); err != nil {
		t.Fatal(err)
	}
	items, err := s.ChecklistItems("List")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("items len = %d want 2", len(items))
	}
	if items[0].Priority != checklist.PriorityHigh {
		t.Errorf("item[0] priority = %q want high", items[0].Priority)
	}
	if !items[1].Checked {
		t.Errorf("item[1] should be checked: %+v", items[1])
	}
}

func TestFolders(t *testing.T) {
	s := testStore(t)
	if err := s.CreateFolder("Projects/Atlas"); err != nil {
		t.Fatal(err)
	}
	folders, err := s.ListFolders()
	if err != nil {
		t.Fatal(err)
	}
	if !contains(folders, "Projects") || !contains(folders, "Projects/Atlas") {
		t.Errorf("folders = %v", folders)
	}
}

func TestDeleteAndRename(t *testing.T) {
	s := testStore(t)
	if err := s.WriteNote("Temp", "x"); err != nil {
		t.Fatal(err)
	}
	if err := s.RenameNote("Temp", "Archive/Kept"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadNote("Temp"); err == nil {
		t.Error("old note still readable after rename")
	}
	if got, _ := s.ReadNote("Archive/Kept"); got != "x" {
		t.Errorf("renamed note content = %q want %q", got, "x")
	}
	if err := s.DeleteNote("Archive/Kept"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadNote("Archive/Kept"); err == nil {
		t.Error("note readable after delete")
	}
}

func TestReindexAndWelcome(t *testing.T) {
	dir := t.TempDir()
	vault := filepath.Join(dir, "vault")

	s, err := Open(vault, filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureWelcome(); err != nil {
		t.Fatal(err)
	}
	notes, _ := s.ListNotes()
	if len(notes) != 1 || notes[0].Path != "Welcome" {
		t.Fatalf("welcome not created: %+v", notes)
	}
	if err := s.EnsureWelcome(); err != nil { // idempotent
		t.Fatal(err)
	}
	if notes, _ := s.ListNotes(); len(notes) != 1 {
		t.Errorf("welcome duplicated: %d notes", len(notes))
	}
	s.Close()

	// A brand-new index over the same vault is empty until Reindex rebuilds it.
	s2, err := Open(vault, filepath.Join(dir, "index2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if n, _ := s2.ListNotes(); len(n) != 0 {
		t.Fatalf("fresh index should be empty, got %d", len(n))
	}
	if err := s2.Reindex(); err != nil {
		t.Fatal(err)
	}
	n, _ := s2.ListNotes()
	if len(n) != 1 || n[0].Title == "" {
		t.Errorf("after reindex: %+v", n)
	}
}

func TestRenameFolder(t *testing.T) {
	s := testStore(t)
	if err := s.WriteNote("Work/a", "# A"); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteNote("Work/sub/b", "# B"); err != nil {
		t.Fatal(err)
	}
	if err := s.RenameFolder("Work", "Office"); err != nil {
		t.Fatalf("RenameFolder: %v", err)
	}

	if _, err := s.ReadNote("Work/a"); err == nil {
		t.Error("old note still readable after folder rename")
	}
	if got, _ := s.ReadNote("Office/a"); got != "# A" {
		t.Errorf("Office/a = %q want %q", got, "# A")
	}
	if got, _ := s.ReadNote("Office/sub/b"); got != "# B" {
		t.Errorf("Office/sub/b = %q want %q", got, "# B")
	}

	folders := map[string]string{}
	notes, _ := s.ListNotes()
	for _, n := range notes {
		folders[n.Path] = n.Folder
	}
	if f, ok := folders["Office/a"]; !ok || f != "Office" {
		t.Errorf("index Office/a folder = %q (ok=%v); want Office", f, ok)
	}
	if f, ok := folders["Office/sub/b"]; !ok || f != "Office/sub" {
		t.Errorf("index Office/sub/b folder = %q (ok=%v); want Office/sub", f, ok)
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
