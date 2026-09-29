package storage

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"atlas-notes/internal/vaultlock"
)

// historyStore is a store that keeps history, in a directory of its own outside
// the vault, as the app arranges it.
func historyStore(t *testing.T) *Store {
	t.Helper()
	s := testStore(t)
	s.HistoryDir = filepath.Join(t.TempDir(), "history")
	return s
}

// lockedHistoryStore is lockedStore that keeps history.
func lockedHistoryStore(t *testing.T) *Store {
	t.Helper()
	s := lockedStore(t)
	s.HistoryDir = filepath.Join(t.TempDir(), "history")
	return s
}

// moveClock makes history think it is ahead of the real time by d, for the rest
// of the test. Nothing else reads the clock, so file times stay real.
func moveClock(t *testing.T, d time.Duration) {
	t.Helper()
	historyNow = func() time.Time { return time.Now().Add(d) }
	t.Cleanup(func() { historyNow = time.Now })
}

func mustWrite(t *testing.T, s *Store, rel, text string) {
	t.Helper()
	if err := s.WriteNote(rel, text); err != nil {
		t.Fatalf("WriteNote(%q): %v", rel, err)
	}
}

func mustHistory(t *testing.T, s *Store, rel string) []Version {
	t.Helper()
	vs, err := s.History(rel)
	if err != nil {
		t.Fatalf("History(%q): %v", rel, err)
	}
	return vs
}

func mustReadVersion(t *testing.T, s *Store, rel, id string) string {
	t.Helper()
	text, err := s.ReadVersion(rel, id)
	if err != nil {
		t.Fatalf("ReadVersion(%q, %q): %v", rel, id, err)
	}
	return text
}

// putVersion writes a plain version of a note into its history directly, dated
// at, so that a test can have any past it likes without waiting for one.
func putVersion(t *testing.T, s *Store, rel string, at time.Time, text string) string {
	t.Helper()
	dir := s.historyDirFor(rel)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	id := strconv.FormatInt(at.UnixNano(), 10) + ".md"
	if err := os.WriteFile(filepath.Join(dir, id), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return id
}

func versionIDs(vs []Version) []string {
	var ids []string
	for _, v := range vs {
		ids = append(ids, v.ID)
	}
	return ids
}

func hasVersion(vs []Version, id string) bool {
	for _, v := range vs {
		if v.ID == id {
			return true
		}
	}
	return false
}

func TestFirstSaveKeepsNoVersion(t *testing.T) {
	s := historyStore(t)
	mustWrite(t, s, "Work/Todo", "first")
	if vs := mustHistory(t, s, "Work/Todo"); len(vs) != 0 {
		t.Errorf("a new note has %d versions, want none", len(vs))
	}
	if exists(s.HistoryDir) {
		t.Error("saving a new note created the history directory")
	}
}

func TestVersionsAreSpacedByTenMinutes(t *testing.T) {
	s := historyStore(t)
	mustWrite(t, s, "n", "one")
	// The note has a file now, and no version: the second save keeps the first.
	mustWrite(t, s, "n", "two")
	if got := len(mustHistory(t, s, "n")); got != 1 {
		t.Fatalf("after the second save: %d versions, want 1", got)
	}
	// Within ten minutes of that version, saving keeps nothing more.
	mustWrite(t, s, "n", "three")
	mustWrite(t, s, "n", "four")
	if got := len(mustHistory(t, s, "n")); got != 1 {
		t.Fatalf("saves within ten minutes made versions: %d, want 1", got)
	}
	moveClock(t, 11*time.Minute)
	mustWrite(t, s, "n", "five")
	vs := mustHistory(t, s, "n")
	if len(vs) != 2 {
		t.Fatalf("after eleven minutes: %d versions, want 2", len(vs))
	}
	if got := mustReadVersion(t, s, "n", vs[0].ID); got != "four" {
		t.Errorf("newest version = %q, want the text the save replaced, %q", got, "four")
	}
	if got := mustReadVersion(t, s, "n", vs[1].ID); got != "one" {
		t.Errorf("oldest version = %q, want %q", got, "one")
	}
}

func TestVersionsKeepTheNotesFormat(t *testing.T) {
	for _, c := range Compressions() {
		t.Run(string(c), func(t *testing.T) {
			s := historyStore(t)
			if err := s.SetCompression(c); err != nil {
				t.Fatal(err)
			}
			mustWrite(t, s, "n", "old text")
			mustWrite(t, s, "n", "new text")
			vs := mustHistory(t, s, "n")
			if len(vs) != 1 {
				t.Fatalf("%d versions, want 1", len(vs))
			}
			if !strings.HasSuffix(vs[0].ID, c.ext()) {
				t.Errorf("version %q does not end in %q", vs[0].ID, c.ext())
			}
			if vs[0].Locked || vs[0].Size == 0 {
				t.Errorf("version = %+v", vs[0])
			}
			if got := mustReadVersion(t, s, "n", vs[0].ID); got != "old text" {
				t.Errorf("version reads %q", got)
			}
		})
	}
}

// A note left in an older format is still kept as it is, and read back.
func TestVersionOfANoteInAnotherFormat(t *testing.T) {
	s := historyStore(t)
	if err := s.SetCompression(CompressionGzip); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, s, "n", "in gzip")
	if err := s.SetCompression(CompressionNone); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, s, "n", "in the clear")
	vs := mustHistory(t, s, "n")
	if len(vs) != 1 || !strings.HasSuffix(vs[0].ID, ".md.gz") {
		t.Fatalf("versions = %v, want one .md.gz", versionIDs(vs))
	}
	if got := mustReadVersion(t, s, "n", vs[0].ID); got != "in gzip" {
		t.Errorf("version reads %q", got)
	}
}

func TestVersionsListNewestFirst(t *testing.T) {
	s := historyStore(t)
	now := time.Now()
	old := putVersion(t, s, "n", now.Add(-3*time.Hour), "old")
	mid := putVersion(t, s, "n", now.Add(-2*time.Hour), "mid")
	recent := putVersion(t, s, "n", now.Add(-time.Hour), "recent")
	// Things in the directory that are not versions are not listed.
	dir := s.historyDirFor("n")
	os.WriteFile(filepath.Join(dir, historyNoteFile), []byte("n"), 0o600)
	os.WriteFile(filepath.Join(dir, ".atlas-123.tmp"), []byte("x"), 0o600)
	os.WriteFile(filepath.Join(dir, "notes.md"), []byte("x"), 0o600)

	got := versionIDs(mustHistory(t, s, "n"))
	want := []string{recent, mid, old}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("History = %v, want %v", got, want)
	}
}

func TestRetentionDropsVersionsOlderThanNinetyDays(t *testing.T) {
	s := historyStore(t)
	now := time.Now()
	mustWrite(t, s, "n", "current")
	gone1 := putVersion(t, s, "n", now.Add(-100*24*time.Hour), "very old")
	gone2 := putVersion(t, s, "n", now.Add(-91*24*time.Hour), "old")
	kept := putVersion(t, s, "n", now.Add(-89*24*time.Hour), "recent enough")

	mustWrite(t, s, "n", "next")
	vs := mustHistory(t, s, "n")
	if hasVersion(vs, gone1) || hasVersion(vs, gone2) {
		t.Errorf("versions past ninety days are still there: %v", versionIDs(vs))
	}
	if !hasVersion(vs, kept) {
		t.Errorf("a version inside ninety days was removed: %v", versionIDs(vs))
	}
	if len(vs) != 2 {
		t.Errorf("%d versions, want the recent one and the new one", len(vs))
	}
}

func TestRetentionKeepsAtMostFiftyVersions(t *testing.T) {
	s := historyStore(t)
	now := time.Now()
	mustWrite(t, s, "n", "current")
	var ids []string // newest first
	for i := 0; i < 60; i++ {
		ids = append(ids, putVersion(t, s, "n", now.Add(-time.Hour-time.Duration(i)*time.Minute), "v"))
	}

	mustWrite(t, s, "n", "next")
	vs := mustHistory(t, s, "n")
	if len(vs) != 50 {
		t.Fatalf("%d versions, want 50", len(vs))
	}
	// The one just made, and the newest of the rest.
	for _, id := range ids[:49] {
		if !hasVersion(vs, id) {
			t.Fatalf("newest version %s was pruned", id)
		}
	}
	for _, id := range ids[49:] {
		if hasVersion(vs, id) {
			t.Errorf("old version %s survived", id)
		}
	}
	if got := mustReadVersion(t, s, "n", vs[0].ID); got != "current" {
		t.Errorf("newest version = %q, want the text just replaced", got)
	}
}

// A note not touched for months has its old text kept when it is finally
// edited, even though that text is older than the limit.
func TestTheVersionJustMadeIsNeverPruned(t *testing.T) {
	s := historyStore(t)
	mustWrite(t, s, "n", "from long ago")
	old := time.Now().Add(-200 * 24 * time.Hour)
	if err := os.Chtimes(s.notePath("n"), old, old); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, s, "n", "today")
	vs := mustHistory(t, s, "n")
	if len(vs) != 1 {
		t.Fatalf("%d versions, want 1", len(vs))
	}
	if got := mustReadVersion(t, s, "n", vs[0].ID); got != "from long ago" {
		t.Errorf("version reads %q", got)
	}
	if d := vs[0].Time.Sub(old); d < -time.Second || d > time.Second {
		t.Errorf("version is dated %v, want the file's own time %v", vs[0].Time, old)
	}
}

func TestRestoreBringsBackOldTextAndKeepsTheCurrent(t *testing.T) {
	s := historyStore(t)
	mustWrite(t, s, "n", "one")
	mustWrite(t, s, "n", "two")
	vs := mustHistory(t, s, "n")
	if len(vs) != 1 {
		t.Fatalf("%d versions, want 1", len(vs))
	}

	// The last version is a moment old, so an ordinary save would keep nothing:
	// a restore keeps the current text regardless.
	if err := s.RestoreVersion("n", vs[0].ID); err != nil {
		t.Fatalf("RestoreVersion: %v", err)
	}
	if got, _ := s.ReadNote("n"); got != "one" {
		t.Errorf("note reads %q after restoring, want %q", got, "one")
	}
	after := mustHistory(t, s, "n")
	if len(after) != 2 {
		t.Fatalf("%d versions after restoring, want 2", len(after))
	}
	if got := mustReadVersion(t, s, "n", after[0].ID); got != "two" {
		t.Errorf("newest version = %q, want the text the restore replaced, %q", got, "two")
	}

	// So the restore can be undone.
	if err := s.RestoreVersion("n", after[0].ID); err != nil {
		t.Fatalf("undoing the restore: %v", err)
	}
	if got, _ := s.ReadNote("n"); got != "two" {
		t.Errorf("note reads %q after undoing, want %q", got, "two")
	}
	if got := len(mustHistory(t, s, "n")); got != 3 {
		t.Errorf("%d versions after undoing, want 3", got)
	}
}

func TestRestoreRefusesABadVersion(t *testing.T) {
	s := historyStore(t)
	mustWrite(t, s, "n", "one")
	mustWrite(t, s, "n", "two")
	if err := s.RestoreVersion("n", "123.md"); err == nil {
		t.Error("restored a version that is not there")
	}
	if got, _ := s.ReadNote("n"); got != "two" {
		t.Errorf("a failed restore changed the note to %q", got)
	}
}

func TestRenameMovesHistory(t *testing.T) {
	s := historyStore(t)
	mustWrite(t, s, "A/old", "one")
	mustWrite(t, s, "A/old", "two")
	before := mustHistory(t, s, "A/old")
	if len(before) != 1 {
		t.Fatalf("%d versions, want 1", len(before))
	}

	if err := s.RenameNote("A/old", "B/new"); err != nil {
		t.Fatal(err)
	}
	if got := mustHistory(t, s, "A/old"); len(got) != 0 {
		t.Errorf("the old name still has %d versions", len(got))
	}
	after := mustHistory(t, s, "B/new")
	if len(after) != 1 || after[0].ID != before[0].ID {
		t.Fatalf("history after the rename = %v, want %v", versionIDs(after), versionIDs(before))
	}
	if got := mustReadVersion(t, s, "B/new", after[0].ID); got != "one" {
		t.Errorf("version reads %q", got)
	}
	if exists(s.historyDirFor("A/old")) {
		t.Error("the old history directory is still there")
	}
	name, err := os.ReadFile(filepath.Join(s.historyDirFor("B/new"), historyNoteFile))
	if err != nil || string(name) != "B/new" {
		t.Errorf("note.txt = %q, %v; want the new name", name, err)
	}
}

// Renaming a note onto a name that already has history, as when that note went
// to the Trash, joins the two rather than losing either.
func TestRenameJoinsExistingHistory(t *testing.T) {
	s := historyStore(t)
	tr := newFakeTrash(t)
	s.Trash = tr.trash
	mustWrite(t, s, "b", "b one")
	mustWrite(t, s, "b", "b two")
	if err := s.DeleteNote("b"); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, s, "a", "a one")
	mustWrite(t, s, "a", "a two")

	if err := s.RenameNote("a", "b"); err != nil {
		t.Fatal(err)
	}
	vs := mustHistory(t, s, "b")
	if len(vs) != 2 {
		t.Fatalf("%d versions, want 2", len(vs))
	}
	seen := map[string]bool{}
	for _, v := range vs {
		seen[mustReadVersion(t, s, "b", v.ID)] = true
	}
	if !seen["a one"] || !seen["b one"] {
		t.Errorf("versions = %v, want both notes' history", seen)
	}
	if exists(s.historyDirFor("a")) {
		t.Error("the old history directory is still there")
	}
}

func TestPermanentDeleteRemovesHistory(t *testing.T) {
	s := historyStore(t)
	mustWrite(t, s, "n", "one")
	mustWrite(t, s, "n", "two")
	if len(mustHistory(t, s, "n")) != 1 {
		t.Fatal("setup: no version")
	}
	if err := s.DeleteNotePermanently("n"); err != nil {
		t.Fatal(err)
	}
	if got := mustHistory(t, s, "n"); len(got) != 0 {
		t.Errorf("%d versions survived a permanent delete", len(got))
	}
	if exists(s.historyDirFor("n")) {
		t.Error("the history directory survived a permanent delete")
	}
}

func TestTrashedNoteKeepsItsHistory(t *testing.T) {
	s := historyStore(t)
	tr := newFakeTrash(t)
	s.Trash = tr.trash
	mustWrite(t, s, "n", "one")
	mustWrite(t, s, "n", "two")
	if err := s.DeleteNote("n"); err != nil {
		t.Fatal(err)
	}
	vs := mustHistory(t, s, "n")
	if len(vs) != 1 {
		t.Fatalf("%d versions after a Trash delete, want 1", len(vs))
	}
	// Restored from the Trash, the note has its versions again.
	if err := os.Rename(filepath.Join(tr.dir, "n.md"), s.notePath("n")); err != nil {
		t.Fatal(err)
	}
	if got := mustReadVersion(t, s, "n", vs[0].ID); got != "one" {
		t.Errorf("version reads %q", got)
	}
}

// With no Trash a delete is permanent, and so is the history's.
func TestDeleteWithoutATrashRemovesHistory(t *testing.T) {
	s := historyStore(t)
	mustWrite(t, s, "n", "one")
	mustWrite(t, s, "n", "two")
	if err := s.DeleteNote("n"); err != nil {
		t.Fatal(err)
	}
	if exists(s.historyDirFor("n")) {
		t.Error("the history of a note deleted for good survived")
	}
}

func TestBadVersionIDsAreRefused(t *testing.T) {
	s := historyStore(t)
	mustWrite(t, s, "n", "one")
	mustWrite(t, s, "n", "two")
	// Something an id could reach if it were not checked.
	secret := filepath.Join(s.HistoryDir, "secret.md")
	if err := os.WriteFile(secret, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	real := mustHistory(t, s, "n")[0].ID

	for _, id := range []string{
		"", ".", "..", "../x", "a/b", `a\b`, "../secret.md", "../123.md",
		"../" + filepath.Base(s.historyDirFor("n")) + "/" + real,
		filepath.Join(s.historyDirFor("n"), real), // absolute
		historyNoteFile, "123", "abc.md", "-5.md", "+5.md", ".md", "0.md",
		real + "/", "./" + real, real + ".txt",
	} {
		if _, err := s.ReadVersion("n", id); !errors.Is(err, errBadVersion) {
			t.Errorf("ReadVersion(%q) = %v, want errBadVersion", id, err)
		}
		if err := s.RestoreVersion("n", id); !errors.Is(err, errBadVersion) {
			t.Errorf("RestoreVersion(%q) = %v, want errBadVersion", id, err)
		}
	}
	// A good id is not one that belongs to another note.
	mustWrite(t, s, "other", "x")
	if _, err := s.ReadVersion("other", real); !os.IsNotExist(err) {
		t.Errorf("another note's version read as %v, want not found", err)
	}
}

func TestLockedNoteVersionsNeedTheKey(t *testing.T) {
	s := lockedHistoryStore(t)
	mustWrite(t, s, "n", "alpha")
	if err := s.LockNote("n"); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, s, "n", "beta") // stays locked, and keeps the locked text

	vs := mustHistory(t, s, "n")
	if len(vs) != 1 || !vs[0].Locked || !strings.HasSuffix(vs[0].ID, ".md.enc") {
		t.Fatalf("versions = %+v, want one locked version", vs)
	}
	if !s.IsNoteLocked("n") {
		t.Error("saving a locked note unlocked it")
	}
	if p := grep(t, s.HistoryDir, "alpha"); p != "" {
		t.Errorf("the text of a locked note is in history in the clear: %s", p)
	}

	s.Lock()
	if _, err := s.ReadVersion("n", vs[0].ID); !errors.Is(err, ErrLocked) {
		t.Errorf("ReadVersion while locked = %v, want ErrLocked", err)
	}
	// Listing needs nothing: it says only that versions exist.
	if got := len(mustHistory(t, s, "n")); got != 1 {
		t.Errorf("History while locked has %d versions, want 1", got)
	}
	if err := s.Unlock(testPassword); err != nil {
		t.Fatal(err)
	}
	if got := mustReadVersion(t, s, "n", vs[0].ID); got != "alpha" {
		t.Errorf("version reads %q, want %q", got, "alpha")
	}

	// A restore is a save: the note stays locked.
	if err := s.RestoreVersion("n", vs[0].ID); err != nil {
		t.Fatal(err)
	}
	if !s.IsNoteLocked("n") {
		t.Error("restoring a version unlocked the note")
	}
	if got, _ := s.ReadNote("n"); got != "alpha" {
		t.Errorf("note reads %q after restoring", got)
	}
}

// lockFolderMarker marks a folder locked without touching the notes in it, so a
// note in the clear is left there for the next save to find.
func lockFolderMarker(t *testing.T, s *Store, folder string) {
	t.Helper()
	marker, err := s.folderMarkerPath(folder)
	if err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(marker, nil); err != nil {
		t.Fatal(err)
	}
}

// A note in the clear that lands in a locked folder is written locked, and
// neither the text it had nor the versions it already had may be left in
// history in the clear.
func TestSavingIntoALockedFolderLeavesNoPlainVersion(t *testing.T) {
	for _, gap := range []time.Duration{0, 11 * time.Minute} {
		t.Run(gap.String(), func(t *testing.T) {
			s := lockedHistoryStore(t)
			mustWrite(t, s, "Work/n", "first draft words")
			mustWrite(t, s, "Work/n", "second draft words")
			if vs := mustHistory(t, s, "Work/n"); len(vs) != 1 || vs[0].Locked {
				t.Fatalf("setup: versions = %+v, want one in the clear", vs)
			}
			lockFolderMarker(t, s, "Work")
			if gap > 0 {
				moveClock(t, gap)
			}
			mustWrite(t, s, "Work/n", "third draft words")

			if !s.IsNoteLocked("Work/n") {
				t.Fatal("the note was not written locked")
			}
			if p := grep(t, s.HistoryDir, "draft words"); p != "" {
				t.Errorf("plaintext left in history: %s", p)
			}
			vs := mustHistory(t, s, "Work/n")
			want := map[string]bool{"first draft words": true}
			if gap > 0 {
				// The text the locked write replaced is kept, and sealed.
				want["second draft words"] = true
			}
			if len(vs) != len(want) {
				t.Fatalf("%d versions, want %d", len(vs), len(want))
			}
			for _, v := range vs {
				if !v.Locked {
					t.Errorf("version %s is in the clear", v.ID)
				}
				got := mustReadVersion(t, s, "Work/n", v.ID)
				if !want[got] {
					t.Errorf("version reads %q", got)
				}
				delete(want, got)
			}
		})
	}
}

// Without the key nothing can be sealed and the save cannot go ahead, so the
// note and its history are left exactly as they were.
func TestSavingIntoALockedFolderWithoutTheKeyChangesNothing(t *testing.T) {
	s := lockedHistoryStore(t)
	mustWrite(t, s, "Work/n", "first draft words")
	mustWrite(t, s, "Work/n", "second draft words")
	lockFolderMarker(t, s, "Work")
	s.Lock()
	moveClock(t, 11*time.Minute)

	if err := s.WriteNote("Work/n", "third draft words"); !errors.Is(err, ErrLocked) {
		t.Fatalf("WriteNote = %v, want ErrLocked", err)
	}
	vs := mustHistory(t, s, "Work/n")
	if len(vs) != 1 || vs[0].Locked {
		t.Errorf("versions = %+v, want the one that was there, unchanged", vs)
	}
	if got, _ := s.ReadNote("Work/n"); got != "second draft words" {
		t.Errorf("note reads %q, want it unchanged", got)
	}
}

func TestSealHistoryLeavesNoPlaintext(t *testing.T) {
	s := lockedHistoryStore(t)
	mustWrite(t, s, "n", "alpha secret")
	mustWrite(t, s, "n", "beta secret")
	mustWrite(t, s, "n", "gamma secret")
	moveClock(t, 11*time.Minute)
	mustWrite(t, s, "n", "delta secret")

	before := mustHistory(t, s, "n")
	if len(before) != 2 {
		t.Fatalf("%d versions, want 2", len(before))
	}
	if grep(t, s.HistoryDir, "secret") == "" {
		t.Fatal("setup: nothing readable in history to seal")
	}

	key, err := s.key()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.sealHistory("n", key); err != nil {
		t.Fatalf("sealHistory: %v", err)
	}

	if p := grep(t, s.HistoryDir, "secret"); p != "" {
		t.Errorf("plaintext left in history: %s", p)
	}
	after := mustHistory(t, s, "n")
	if len(after) != len(before) {
		t.Fatalf("%d versions after sealing, want %d", len(after), len(before))
	}
	want := map[string]string{"alpha secret": "", "gamma secret": ""}
	for i, v := range after {
		if !v.Locked || !strings.HasSuffix(v.ID, ".md.enc") {
			t.Errorf("version %s is not sealed", v.ID)
		}
		if !v.Time.Equal(before[i].Time) {
			t.Errorf("version moved from %v to %v", before[i].Time, v.Time)
		}
		text := mustReadVersion(t, s, "n", v.ID)
		if _, ok := want[text]; !ok {
			t.Errorf("sealed version reads %q", text)
		}
		delete(want, text)
	}
	if len(want) != 0 {
		t.Errorf("texts lost in sealing: %v", want)
	}

	s.Lock()
	if _, err := s.ReadVersion("n", after[0].ID); !errors.Is(err, ErrLocked) {
		t.Errorf("sealed version while locked = %v, want ErrLocked", err)
	}
	// Sealing again finds nothing to do.
	if err := s.sealHistory("n", key); err != nil {
		t.Errorf("sealing twice: %v", err)
	}
}

// A version that cannot be decoded is still not left in the clear.
func TestSealHistorySealsWhatItCannotDecode(t *testing.T) {
	s := lockedHistoryStore(t)
	dir := s.historyDirFor("n")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	id := strconv.FormatInt(time.Now().UnixNano(), 10) + ".md.gz"
	if err := os.WriteFile(filepath.Join(dir, id), []byte("not gzip at all: hidden words"), 0o600); err != nil {
		t.Fatal(err)
	}
	key, _ := s.key()
	if err := s.sealHistory("n", key); err != nil {
		t.Fatal(err)
	}
	if p := grep(t, s.HistoryDir, "hidden words"); p != "" {
		t.Errorf("plaintext left in history: %s", p)
	}
	if vs := mustHistory(t, s, "n"); len(vs) != 1 || !vs[0].Locked {
		t.Errorf("versions = %+v, want one sealed", vs)
	}
}

// With no history directory, history does nothing at all: not even a relative
// path is written.
func TestNoHistoryDirWritesNothing(t *testing.T) {
	s := testStore(t)
	if s.HistoryDir != "" {
		t.Fatal("a store keeps history by default")
	}
	cwd := t.TempDir()
	t.Chdir(cwd)

	mustWrite(t, s, "a", "one")
	mustWrite(t, s, "a", "two")
	moveClock(t, 11*time.Minute)
	mustWrite(t, s, "a", "three")
	if err := s.RenameNote("a", "b"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteNotePermanently("b"); err != nil {
		t.Fatal(err)
	}

	if entries, _ := os.ReadDir(cwd); len(entries) != 0 {
		t.Errorf("wrote %d entries into the working directory", len(entries))
	}
	if vs, err := s.History("a"); err != nil || len(vs) != 0 {
		t.Errorf("History = %v, %v; want empty", vs, err)
	}
	if _, err := s.ReadVersion("a", "1.md"); err == nil {
		t.Error("ReadVersion worked with no history")
	}
	// The vault holds the note's own file and the store's settings, and no
	// history.
	err := filepath.Walk(s.VaultPath, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.Contains(p, "history") {
			t.Errorf("history file in the vault: %s", p)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// History lives outside the vault, so a vault copied or synced carries none.
func TestHistoryStaysOutOfTheVault(t *testing.T) {
	s := historyStore(t)
	mustWrite(t, s, "n", "one")
	mustWrite(t, s, "n", "two")
	if len(mustHistory(t, s, "n")) != 1 {
		t.Fatal("setup: no version")
	}
	if p := grep(t, s.VaultPath, "one"); p != "" && !strings.HasSuffix(p, "n.md") {
		t.Errorf("old text found in the vault outside the note: %s", p)
	}
	if entries, _ := os.ReadDir(s.VaultPath); len(entries) != 1 {
		t.Errorf("vault has %d entries, want just the note", len(entries))
	}
}

func TestHistoryOfANoteThatIsNotThere(t *testing.T) {
	s := historyStore(t)
	vs, err := s.History("missing")
	if err != nil || len(vs) != 0 {
		t.Errorf("History = %v, %v; want an empty list and no error", vs, err)
	}
}

// File times are coarse, so snapshots of files that share a modification time
// are common, and none may be written over another.
func TestVersionsWithTheSameTimeDoNotOverwrite(t *testing.T) {
	s := historyStore(t)
	at := time.Now().Add(-time.Hour)
	mustWrite(t, s, "n", "start")
	src := putVersion(t, s, "n", at.Add(-time.Hour), "restorable")
	for i := 0; i < 3; i++ {
		if err := os.Chtimes(s.notePath("n"), at, at); err != nil {
			t.Fatal(err)
		}
		if err := s.RestoreVersion("n", src); err != nil {
			t.Fatal(err)
		}
	}
	vs := mustHistory(t, s, "n")
	if len(vs) != 4 {
		t.Fatalf("%d versions, want the one put there and three snapshots", len(vs))
	}
	texts := map[string]int{}
	for _, v := range vs {
		texts[mustReadVersion(t, s, "n", v.ID)]++
	}
	if texts["start"] != 1 || texts["restorable"] != 3 {
		t.Errorf("version texts = %v", texts)
	}
}

// The newest version stays whatever its age, and so does the one just made.
func TestPruningSparesTheNewestVersion(t *testing.T) {
	s := historyStore(t)
	mustWrite(t, s, "n", "from long ago")
	now := time.Now()
	old := now.Add(-200 * 24 * time.Hour)
	if err := os.Chtimes(s.notePath("n"), old, old); err != nil {
		t.Fatal(err)
	}
	newest := putVersion(t, s, "n", now.Add(-95*24*time.Hour), "newest of the old")
	older := putVersion(t, s, "n", now.Add(-100*24*time.Hour), "older")

	mustWrite(t, s, "n", "today")
	vs := mustHistory(t, s, "n")
	if !hasVersion(vs, newest) {
		t.Error("the newest version was pruned for its age")
	}
	if hasVersion(vs, older) {
		t.Error("an older version past ninety days survived")
	}
	if len(vs) != 2 { // the newest, and the one the save just made
		t.Errorf("%d versions, want 2", len(vs))
	}
}

func TestResealHistoryFollowsAPasswordChange(t *testing.T) {
	s := lockedHistoryStore(t)
	for _, rel := range []string{"a", "Work/b"} {
		mustWrite(t, s, rel, "first "+rel)
		if err := s.LockNote(rel); err != nil {
			t.Fatal(err)
		}
		mustWrite(t, s, rel, "second "+rel) // keeps the locked "first"
	}
	mustWrite(t, s, "plain", "first plain")
	mustWrite(t, s, "plain", "second plain") // keeps "first plain", in the clear

	oldKey, err := s.key()
	if err != nil {
		t.Fatal(err)
	}
	_, newKey, err := vaultlock.New("another password entirely")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.resealHistory(oldKey, newKey); err != nil {
		t.Fatalf("resealHistory: %v", err)
	}

	// The vault now holds the new key, as it does once ChangePassword is done.
	s.lockMu.Lock()
	s.lockKey = newKey
	s.lockMu.Unlock()
	for _, rel := range []string{"a", "Work/b"} {
		vs := mustHistory(t, s, rel)
		if len(vs) != 1 || !vs[0].Locked {
			t.Fatalf("%s: versions = %+v", rel, vs)
		}
		if got := mustReadVersion(t, s, rel, vs[0].ID); got != "first "+rel {
			t.Errorf("%s: version reads %q under the new key", rel, got)
		}
	}
	vs := mustHistory(t, s, "plain")
	if len(vs) != 1 || vs[0].Locked {
		t.Fatalf("plain: versions = %+v, want the one in the clear, untouched", vs)
	}
	if got := mustReadVersion(t, s, "plain", vs[0].ID); got != "first plain" {
		t.Errorf("plain version reads %q", got)
	}

	// Under the old key they no longer open.
	s.lockMu.Lock()
	s.lockKey = oldKey
	s.lockMu.Unlock()
	a := mustHistory(t, s, "a")
	if _, err := s.ReadVersion("a", a[0].ID); err == nil {
		t.Error("a version still opens under the old key")
	}
}

// A version that will not open is reported, and does not stop the others.
func TestResealHistoryCarriesOnPastABadVersion(t *testing.T) {
	s := lockedHistoryStore(t)
	mustWrite(t, s, "a", "first a")
	if err := s.LockNote("a"); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, s, "a", "second a")
	mustWrite(t, s, "b", "first b")
	if err := s.LockNote("b"); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, s, "b", "second b")

	// One sealed under some other key altogether, in a's directory.
	_, strayKey, err := vaultlock.New("some other password")
	if err != nil {
		t.Fatal(err)
	}
	stray, err := vaultlock.Seal(strayKey, []byte("stray"))
	if err != nil {
		t.Fatal(err)
	}
	strayID := strconv.FormatInt(time.Now().Add(-time.Hour).UnixNano(), 10) + ".md.enc"
	if err := os.WriteFile(filepath.Join(s.historyDirFor("a"), strayID), stray, 0o600); err != nil {
		t.Fatal(err)
	}

	oldKey, _ := s.key()
	_, newKey, err := vaultlock.New("another password entirely")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.resealHistory(oldKey, newKey); err == nil {
		t.Error("a version that would not open was not reported")
	}
	s.lockMu.Lock()
	s.lockKey = newKey
	s.lockMu.Unlock()
	for _, rel := range []string{"a", "b"} {
		for _, v := range mustHistory(t, s, rel) {
			if v.ID == strayID {
				continue
			}
			if got := mustReadVersion(t, s, rel, v.ID); got != "first "+rel {
				t.Errorf("%s: version reads %q", rel, got)
			}
		}
	}
}

func TestResealHistoryWithNoHistory(t *testing.T) {
	s := lockedStore(t) // no HistoryDir
	key, _ := s.key()
	if err := s.resealHistory(key, key); err != nil {
		t.Errorf("with history off: %v", err)
	}
	s.HistoryDir = filepath.Join(t.TempDir(), "never made")
	if err := s.resealHistory(key, key); err != nil {
		t.Errorf("with no history yet: %v", err)
	}
}
