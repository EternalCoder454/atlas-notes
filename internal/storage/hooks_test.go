package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These cover the places where two features meet: history and locking,
// history and folders, links and renaming. Each feature's own tests cover it
// alone; these check that the join between them holds.

func withHistory(t *testing.T, s *Store) {
	t.Helper()
	s.HistoryDir = filepath.Join(t.TempDir(), "history")
	now := time.Now()
	historyNow = func() time.Time { return now }
	t.Cleanup(func() { historyNow = time.Now })
	// Every save in these tests is far enough apart to be kept.
	advanceHistory = func() { now = now.Add(11 * time.Minute) }
	t.Cleanup(func() { advanceHistory = nil })
}

var advanceHistory func()

func saveVersioned(t *testing.T, s *Store, rel, text string) {
	t.Helper()
	if err := s.WriteNote(rel, text); err != nil {
		t.Fatal(err)
	}
	advanceHistory()
}

// plainHistoryFiles lists every version in HistoryDir stored in the clear.
func plainHistoryFiles(t *testing.T, s *Store) []string {
	t.Helper()
	var out []string
	filepath.WalkDir(s.HistoryDir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() != "note.txt" && !strings.HasSuffix(p, lockedExt) {
			out = append(out, p)
		}
		return nil
	})
	return out
}

func TestLockingANoteSealsItsHistory(t *testing.T) {
	s := lockedStore(t)
	withHistory(t, s)
	saveVersioned(t, s, "Diary", "# Diary\n\nfirst secret\n")
	saveVersioned(t, s, "Diary", "# Diary\n\nsecond secret\n")
	saveVersioned(t, s, "Diary", "# Diary\n\nthird\n")
	if len(plainHistoryFiles(t, s)) == 0 {
		t.Fatal("setup: expected plain versions before locking")
	}
	if err := s.LockNote("Diary"); err != nil {
		t.Fatal(err)
	}
	if left := plainHistoryFiles(t, s); len(left) != 0 {
		t.Fatalf("locking left plaintext versions: %v", left)
	}
	versions, err := s.History("Diary")
	if err != nil || len(versions) == 0 {
		t.Fatalf("history after locking: %v %v", versions, err)
	}
	text, err := s.ReadVersion("Diary", versions[len(versions)-1].ID)
	if err != nil || !strings.Contains(text, "first secret") {
		t.Fatalf("oldest version after locking: %q %v", text, err)
	}
}

func TestChangingThePasswordKeepsSealedHistoryReadable(t *testing.T) {
	s := lockedStore(t)
	withHistory(t, s)
	saveVersioned(t, s, "Diary", "# Diary\n\nold words\n")
	saveVersioned(t, s, "Diary", "# Diary\n\nnewer\n")
	if err := s.LockNote("Diary"); err != nil {
		t.Fatal(err)
	}
	if err := s.ChangePassword(testPassword, "a new password entirely"); err != nil {
		t.Fatal(err)
	}
	versions, _ := s.History("Diary")
	if len(versions) == 0 {
		t.Fatal("no history")
	}
	for _, v := range versions {
		if _, err := s.ReadVersion("Diary", v.ID); err != nil {
			t.Fatalf("version %s unreadable after the password changed: %v", v.ID, err)
		}
	}
}

func TestFolderRenameMovesHistoryAndPermanentDeleteRemovesIt(t *testing.T) {
	s := testStore(t)
	withHistory(t, s)
	s.CreateFolder("Work")
	saveVersioned(t, s, "Work/Plan", "one\n")
	saveVersioned(t, s, "Work/Plan", "two\n")
	if v, _ := s.History("Work/Plan"); len(v) == 0 {
		t.Fatal("setup: no history")
	}
	if err := s.RenameFolder("Work", "Projects"); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.History("Projects/Plan"); len(v) == 0 {
		t.Fatal("history did not follow the folder rename")
	}
	if v, _ := s.History("Work/Plan"); len(v) != 0 {
		t.Fatal("history stayed under the old name")
	}
	if err := s.DeleteFolderPermanently("Projects"); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.History("Projects/Plan"); len(v) != 0 {
		t.Fatal("a folder deleted for good kept its notes' history")
	}
}

func TestFolderNamesAreNotLikePatterns(t *testing.T) {
	s := testStore(t)
	s.CreateFolder("a_b")
	s.CreateFolder("axb")
	s.WriteNote("a_b/One", "x\n")
	s.WriteNote("axb/Two", "y\n")
	if err := s.DeleteFolderPermanently("a_b"); err != nil {
		t.Fatal(err)
	}
	notes, _ := s.ListNotes()
	if len(notes) != 1 || notes[0].Path != "axb/Two" {
		t.Fatalf("deleting a_b touched axb in the index: %+v", notes)
	}
	s.CreateFolder("c%d")
	s.WriteNote("c%d/Three", "z\n")
	if err := s.RenameFolder("axb", "moved"); err != nil {
		t.Fatal(err)
	}
	notes, _ = s.ListNotes()
	paths := map[string]bool{}
	for _, n := range notes {
		paths[n.Path] = true
	}
	if !paths["moved/Two"] || !paths["c%d/Three"] {
		t.Fatalf("after rename: %v", paths)
	}
}

func TestRenameNoteAndLinks(t *testing.T) {
	s := testStore(t)
	s.WriteNote("Plan", "# Plan\n")
	s.WriteNote("Index", "see [[Plan]] and [[Plan|the plan]]\n")
	n, err := s.RenameNoteAndLinks("Plan", "Roadmap")
	if err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	if text, _ := s.ReadNote("Index"); text != "see [[Roadmap]] and [[Roadmap|the plan]]\n" {
		t.Fatalf("got %q", text)
	}
	s.CreateFolder("Old")
	s.WriteNote("Old/Deep", "x\n")
	s.WriteNote("Index", "[[Old/Deep]]\n")
	if _, err := s.RenameFolderAndLinks("Old", "New"); err != nil {
		t.Fatal(err)
	}
	if text, _ := s.ReadNote("Index"); text != "[[New/Deep]]\n" {
		t.Fatalf("folder rename: %q", text)
	}
}
