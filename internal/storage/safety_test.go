package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"atlas-notes/internal/vaultlock"
)

// resealHistory is planHistoryReseal followed by the writes, which is what a
// password change does with it. The history tests check the two halves together.
func (s *Store) resealHistory(oldKey, newKey vaultlock.Key) error {
	plan, first := s.planHistoryReseal(oldKey, newKey)
	for _, f := range plan {
		if err := atomicWrite(f.path, f.new); err != nil {
			return err
		}
	}
	return first
}

// conflictsOf lists the conflict copies kept of a note.
func conflictsOf(t *testing.T, s *Store, base string) []string {
	t.Helper()
	all, err := s.notePaths()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, p := range all {
		if strings.HasPrefix(p, base+" (conflict ") {
			out = append(out, p)
		}
	}
	return out
}

func readFileBytes(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// ---- ChangePassword is all or nothing

func TestChangePasswordWithACorruptImageChangesNothing(t *testing.T) {
	s := lockedHistoryStore(t)
	for _, rel := range []string{"A", "Work/B"} {
		mustWrite(t, s, rel, "text of "+rel)
		mustWrite(t, s, rel, "later text of "+rel)
		if err := s.LockNote(rel); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(s.attachmentsPath(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.attachmentsPath(), "broken.png.enc"), []byte("not a sealed file"), 0o644); err != nil {
		t.Fatal(err)
	}
	files := []string{vaultFile(s, "A.md.enc"), vaultFile(s, "Work/B.md.enc"), s.lockConfigPath()}
	var before [][]byte
	for _, f := range files {
		before = append(before, readFileBytes(t, f))
	}

	if err := s.ChangePassword(testPassword, "the new password"); err == nil {
		t.Fatal("the password changed with an image that cannot be opened")
	}
	for i, f := range files {
		if got := readFileBytes(t, f); string(got) != string(before[i]) {
			t.Errorf("%s was rewritten by a change that failed", filepath.Base(f))
		}
	}
	s.Lock()
	if err := s.Unlock("the new password"); err == nil {
		t.Fatal("the new password opens the vault after a failed change")
	}
	if err := s.Unlock(testPassword); err != nil {
		t.Fatalf("the old password stopped working: %v", err)
	}
	for _, rel := range []string{"A", "Work/B"} {
		if got, err := s.ReadNote(rel); err != nil || got != "later text of "+rel {
			t.Errorf("%s = %q, %v after a failed change", rel, got, err)
		}
	}
}

func TestChangePasswordPutsBackWhatItWroteWhenAWriteFails(t *testing.T) {
	s := lockedHistoryStore(t)
	rels := []string{"A", "B", "C"}
	for _, rel := range rels {
		mustWrite(t, s, rel, "text of "+rel)
		if err := s.LockNote(rel); err != nil {
			t.Fatal(err)
		}
	}
	files := []string{vaultFile(s, "A.md.enc"), vaultFile(s, "B.md.enc"), vaultFile(s, "C.md.enc"), s.lockConfigPath()}
	var before [][]byte
	for _, f := range files {
		before = append(before, readFileBytes(t, f))
	}

	calls := 0
	writeSealed = func(p string, data []byte) error {
		calls++
		if calls == 3 {
			return errors.New("the disk is full")
		}
		return atomicWrite(p, data)
	}
	t.Cleanup(func() { writeSealed = atomicWrite })

	if err := s.ChangePassword(testPassword, "the new password"); err == nil {
		t.Fatal("the password changed although a write failed")
	}
	writeSealed = atomicWrite
	for i, f := range files {
		if got := readFileBytes(t, f); string(got) != string(before[i]) {
			t.Errorf("%s was not put back", filepath.Base(f))
		}
	}
	s.Lock()
	if err := s.Unlock(testPassword); err != nil {
		t.Fatalf("the old password stopped working: %v", err)
	}
	for _, rel := range rels {
		if got, err := s.ReadNote(rel); err != nil || got != "text of "+rel {
			t.Errorf("%s = %q, %v", rel, got, err)
		}
	}
}

// ---- a differing copy of a note is never silently deleted

var conflictName = regexp.MustCompile(`^Plan \(conflict \d{4}-\d{2}-\d{2} \d{4}\)( \d+)?$`)

func TestSavingKeepsADifferingCopyAsAConflictNote(t *testing.T) {
	s := testStore(t)
	// Two forms after a sync. The newer is what the desktop was showing and
	// editing; the older is another device's, and says something else.
	now := time.Now()
	plant(t, s, CompressionZstd, "Plan", "from the phone\n", now.Add(-time.Hour))
	plant(t, s, CompressionNone, "Plan", "as the desktop had it\n", now)
	mustWrite(t, s, "Plan", "from the desktop\n")

	if exists(vaultFile(s, "Plan.md.zst")) {
		t.Error("the other form was left beside the saved note")
	}
	if got, _ := s.ReadNote("Plan"); got != "from the desktop\n" {
		t.Errorf("Plan = %q", got)
	}
	conflicts := conflictsOf(t, s, "Plan")
	if len(conflicts) != 1 || !conflictName.MatchString(conflicts[0]) {
		t.Fatalf("conflict copies = %q, want one named for the day and minute", conflicts)
	}
	if !exists(vaultFile(s, conflicts[0]+".md")) {
		t.Error("the conflict copy is not in the vault's format")
	}
	if got, _ := s.ReadNote(conflicts[0]); got != "from the phone\n" {
		t.Errorf("the conflict copy says %q", got)
	}

	// A second one, the same minute, is numbered and does not replace the first.
	plant(t, s, CompressionGzip, "Plan", "from the tablet\n", now.Add(-2*time.Hour))
	mustWrite(t, s, "Plan", "from the desktop again\n")
	conflicts = conflictsOf(t, s, "Plan")
	if len(conflicts) != 2 {
		t.Fatalf("conflict copies = %q, want two", conflicts)
	}
	var texts []string
	for _, c := range conflicts {
		got, _ := s.ReadNote(c)
		texts = append(texts, got)
	}
	slices.Sort(texts)
	if !slices.Equal(texts, []string{"from the phone\n", "from the tablet\n"}) {
		t.Errorf("the conflict copies say %q", texts)
	}
}

func TestSavingRemovesAnIdenticalCopyWithoutAConflict(t *testing.T) {
	s := testStore(t)
	now := time.Now()
	plant(t, s, CompressionZstd, "Plan", "same\n", now.Add(-time.Hour))
	plant(t, s, CompressionNone, "Plan", "what it was\n", now)
	mustWrite(t, s, "Plan", "same\n")
	if exists(vaultFile(s, "Plan.md.zst")) {
		t.Error("the identical copy was left")
	}
	if c := conflictsOf(t, s, "Plan"); len(c) != 0 {
		t.Errorf("an identical copy made conflict notes %q", c)
	}
}

func TestLockingKeepsADifferingCopySealed(t *testing.T) {
	s := lockedStore(t)
	now := time.Now()
	plant(t, s, CompressionNone, "Plan", "desktop text\n", now)
	plant(t, s, CompressionZstd, "Plan", "phone text\n", now.Add(-time.Hour))
	if err := s.LockNote("Plan"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.ReadNote("Plan"); err != nil || got != "desktop text\n" {
		t.Fatalf("Plan = %q, %v; the newest form is the note", got, err)
	}
	conflicts := conflictsOf(t, s, "Plan")
	if len(conflicts) != 1 {
		t.Fatalf("conflict copies = %q, want one", conflicts)
	}
	if !s.IsNoteLocked(conflicts[0]) {
		t.Error("the copy of a note being locked was kept in the clear")
	}
	if got, err := s.ReadNote(conflicts[0]); err != nil || got != "phone text\n" {
		t.Errorf("the conflict copy = %q, %v", got, err)
	}
	if exists(vaultFile(s, "Plan.md")) || exists(vaultFile(s, "Plan.md.zst")) {
		t.Error("a plain form is still there")
	}
}

func TestReadNoteReadsTheNewestForm(t *testing.T) {
	s := testStore(t) // plain: its own form is first in the order
	now := time.Now()
	plant(t, s, CompressionNone, "Plan", "older plain\n", now.Add(-2*time.Hour))
	plant(t, s, CompressionZstd, "Plan", "newer zstd\n", now.Add(-time.Hour))
	if got, _ := s.ReadNote("Plan"); got != "newer zstd\n" {
		t.Errorf("read %q, want the most recently modified form", got)
	}
	plant(t, s, CompressionNone, "Plan", "newest plain\n", now)
	if got, _ := s.ReadNote("Plan"); got != "newest plain\n" {
		t.Errorf("read %q, want the most recently modified form", got)
	}
}

func TestConvertNoteKeepsTheSourceWhenTheTargetDiffers(t *testing.T) {
	s := testStore(t)
	target := plant(t, s, CompressionNone, "Plan", "the target\n", time.Time{})
	plant(t, s, CompressionGzip, "Plan", "the source\n", time.Time{})
	n, err := s.ConvertVault(t.Context(), nil)
	if err != nil || n != 1 {
		t.Fatalf("ConvertVault = %d, %v", n, err)
	}
	if got := readFileBytes(t, target); string(got) != "the target\n" {
		t.Errorf("the target was overwritten: %q", got)
	}
	if exists(vaultFile(s, "Plan.md.gz")) {
		t.Error("the source was left beside the target")
	}
	conflicts := conflictsOf(t, s, "Plan")
	if len(conflicts) != 1 {
		t.Fatalf("conflict copies = %q", conflicts)
	}
	if got, _ := s.ReadNote(conflicts[0]); got != "the source\n" {
		t.Errorf("the conflict copy says %q", got)
	}
}

// ---- a format change from another device

func TestAFormatChangeFromAnotherDeviceIsPickedUp(t *testing.T) {
	s := testStore(t)
	mustWrite(t, s, "A", "one")
	if !exists(vaultFile(s, "A.md")) {
		t.Fatal("a plain vault did not write a plain note")
	}
	settings := vaultFile(s, vaultFileName)
	change := func(c Compression, at time.Time) {
		t.Helper()
		if err := os.WriteFile(settings, []byte(fmt.Sprintf(`{"compression":%q}`, c)), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(settings, at, at); err != nil {
			t.Fatal(err)
		}
	}
	change(CompressionZstd, time.Now().Add(time.Hour))
	mustWrite(t, s, "A", "two")
	if !exists(vaultFile(s, "A.md.zst")) || exists(vaultFile(s, "A.md")) {
		t.Error("the save did not follow the format another device chose")
	}
	if s.Compression() != CompressionZstd {
		t.Errorf("Compression() = %q", s.Compression())
	}
	change(CompressionGzip, time.Now().Add(2*time.Hour))
	mustWrite(t, s, "A", "three")
	if !exists(vaultFile(s, "A.md.gz")) || exists(vaultFile(s, "A.md.zst")) {
		t.Error("a second change was not picked up")
	}
}

// ---- history of a note locked on another device

// lockBehindItsBack locks a note the way another device would: the sealed file
// arrives, and this device's own history is not told.
func lockBehindItsBack(t *testing.T, s *Store, rel string) {
	t.Helper()
	key, err := s.key()
	if err != nil {
		t.Fatal(err)
	}
	text, err := s.readPlain(rel)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := vaultlock.Seal(key, s.enc.EncodeAll(text, nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(vaultFile(s, rel+lockedExt), sealed, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(vaultFile(s, rel+".md")); err != nil {
		t.Fatal(err)
	}
}

func plainVersions(t *testing.T, s *Store, rel string) int {
	t.Helper()
	vs, err := listVersions(s.historyDirFor(rel))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, v := range vs {
		if !v.Locked {
			n++
		}
	}
	return n
}

// twoSaves leaves a note with one plain version in its history.
func twoSaves(t *testing.T, s *Store, rel string) {
	t.Helper()
	mustWrite(t, s, rel, "first "+rel)
	mustWrite(t, s, rel, "second "+rel)
	if plainVersions(t, s, rel) != 1 {
		t.Fatal("the setup did not leave one plain version")
	}
}

func TestHistoryOfANoteLockedElsewhereIsSealedWithTheKey(t *testing.T) {
	s := lockedHistoryStore(t)
	twoSaves(t, s, "N")
	lockBehindItsBack(t, s, "N")

	vs, err := s.History("N")
	if err != nil || len(vs) != 1 || !vs[0].Locked {
		t.Fatalf("History = %+v, %v; want the one version, sealed", vs, err)
	}
	if plainVersions(t, s, "N") != 0 {
		t.Error("a plain version is still in the history of a locked note")
	}
	if got := mustReadVersion(t, s, "N", vs[0].ID); got != "first N" {
		t.Errorf("the version reads %q", got)
	}
}

func TestHistoryOfANoteLockedElsewhereHidesPlainVersionsWithoutTheKey(t *testing.T) {
	s := lockedHistoryStore(t)
	twoSaves(t, s, "M")
	before := mustHistory(t, s, "M")
	lockBehindItsBack(t, s, "M")
	s.Lock()

	vs, err := s.History("M")
	if err != nil || len(vs) != 0 {
		t.Errorf("History = %+v, %v; want no plain versions listed", vs, err)
	}
	if _, err := s.ReadVersion("M", before[0].ID); !errors.Is(err, ErrLocked) {
		t.Errorf("ReadVersion of a plain version of a locked note = %v, want ErrLocked", err)
	}
	// Nothing was sealed or removed without the key.
	if plainVersions(t, s, "M") != 1 {
		t.Error("the plain version was touched without the key")
	}
}

func TestSealLockedHistoryOnceUnlocked(t *testing.T) {
	s := lockedHistoryStore(t)
	twoSaves(t, s, "P")
	twoSaves(t, s, "Unlocked")
	lockBehindItsBack(t, s, "P")
	s.Lock()

	if err := s.SealLockedHistory(); !errors.Is(err, ErrLocked) {
		t.Fatalf("SealLockedHistory without the key = %v, want ErrLocked", err)
	}
	if err := s.Unlock(testPassword); err != nil {
		t.Fatal(err)
	}
	if err := s.SealLockedHistory(); err != nil {
		t.Fatalf("SealLockedHistory: %v", err)
	}
	if plainVersions(t, s, "P") != 0 {
		t.Error("a plain version of a locked note is left")
	}
	if plainVersions(t, s, "Unlocked") != 1 {
		t.Error("the history of a note that is not locked was sealed")
	}
}

func TestSavingALockedNoteFailsWhenItsHistoryCannotBeSealed(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("a directory cannot be made unwritable for root")
	}
	s := lockedHistoryStore(t)
	twoSaves(t, s, "Q")
	lockBehindItsBack(t, s, "Q")
	dir := s.historyDirFor("Q")
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })

	if err := s.WriteNote("Q", "third Q"); err == nil {
		t.Fatal("the save went ahead with the old text in the clear in history")
	}
	os.Chmod(dir, 0o700)
	if got, _ := s.ReadNote("Q"); got != "second Q" {
		t.Errorf("Q = %q; the failed save changed the note", got)
	}
}

// ---- readPlain and create-if-absent

func TestReadNoteFallsThroughAFormThatDoesNotDecode(t *testing.T) {
	s := testStore(t)
	now := time.Now()
	plant(t, s, CompressionNone, "Plan", "the good one\n", now.Add(-time.Hour))
	bad := vaultFile(s, "Plan.md.zst")
	if err := os.WriteFile(bad, []byte("this is not zstd"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(bad, now, now); err != nil {
		t.Fatal(err)
	}
	if got, err := s.ReadNote("Plan"); err != nil || got != "the good one\n" {
		t.Errorf("ReadNote = %q, %v; the readable form is the note", got, err)
	}

	if err := os.Remove(vaultFile(s, "Plan.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadNote("Plan"); err == nil || os.IsNotExist(err) {
		t.Errorf("ReadNote of a note that only has a broken form = %v, want a decode error", err)
	}
	if _, err := s.readPlain("Nothing"); !os.IsNotExist(err) {
		t.Errorf("readPlain of a missing note = %v, want not-exist", err)
	}
}

func TestCreateNoteNeverWritesOverANote(t *testing.T) {
	s := testStore(t)
	if made, err := s.createNote("N", "first"); err != nil || !made {
		t.Fatalf("createNote = %v, %v", made, err)
	}
	if made, err := s.createNote("N", "second"); err != nil || made {
		t.Fatalf("createNote of a taken name = %v, %v", made, err)
	}
	plant(t, s, CompressionGzip, "Other", "kept", time.Time{})
	if made, _ := s.createNote("Other", "second"); made {
		t.Error("a note in another format was not seen")
	}
	if got, _ := s.ReadNote("N"); got != "first" {
		t.Errorf("N = %q", got)
	}
}

func TestDailyNoteIsCreatedOnceWhateverTheRace(t *testing.T) {
	s := testStore(t)
	day := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	var wg sync.WaitGroup
	var mu sync.Mutex
	made := 0
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, created, err := s.DailyNote(day)
			if err != nil {
				t.Error(err)
				return
			}
			if created {
				mu.Lock()
				made++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if made != 1 {
		t.Errorf("%d goroutines created the day's note, want 1", made)
	}

	plant(t, s, CompressionZstd, "Daily/2026-09-30", "mine\n", time.Time{})
	if _, created, err := s.DailyNote(day.AddDate(0, 0, 1)); err != nil || created {
		t.Errorf("DailyNote over a note in another format = %v, %v", created, err)
	}
	if got, _ := s.ReadNote("Daily/2026-09-30"); got != "mine\n" {
		t.Errorf("the day's note = %q", got)
	}
}

func TestNewFromTemplateNeverOverwritesUnderARace(t *testing.T) {
	s := testStore(t)
	mustWrite(t, s, "Templates/T", "Hi {{title}}")
	var wg sync.WaitGroup
	var mu sync.Mutex
	var got []string
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rel, err := s.NewFromTemplate("Templates/T", "", "Same", time.Now())
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			got = append(got, rel)
			mu.Unlock()
		}()
	}
	wg.Wait()
	slices.Sort(got)
	if len(slices.Compact(slices.Clone(got))) != 8 {
		t.Errorf("names = %q, want 8 different ones", got)
	}
}

// ---- names ending in ".md"

func TestANoteNamedFooDotMd(t *testing.T) {
	s := testStore(t)
	mustWrite(t, s, "foo.md", "dotted")
	if !exists(vaultFile(s, "foo.md.md")) {
		t.Fatal("the note is not the file foo.md.md")
	}
	all, _ := s.notePaths()
	if !slices.Equal(all, []string{"foo.md"}) {
		t.Fatalf("notes = %q", all)
	}
	if got, err := s.ReadNote("foo.md"); err != nil || got != "dotted" {
		t.Errorf("ReadNote = %q, %v", got, err)
	}
	if !s.NoteExists("foo.md") || s.NoteExists("foo") {
		t.Error("NoteExists mixes foo.md and foo")
	}
	if err := s.RenameNote("foo.md", "bar.md"); err != nil {
		t.Fatal(err)
	}
	if !exists(vaultFile(s, "bar.md.md")) || exists(vaultFile(s, "foo.md.md")) {
		t.Error("the rename did not move foo.md.md to bar.md.md")
	}
	if all, _ = s.notePaths(); !slices.Equal(all, []string{"bar.md"}) {
		t.Errorf("notes after the rename = %q", all)
	}
	if err := s.DeleteNotePermanently("bar.md"); err != nil {
		t.Fatal(err)
	}
	if exists(vaultFile(s, "bar.md.md")) {
		t.Error("the note was not deleted")
	}
}

func TestReindexIndexesFileFooDotMdDotMdAsFooDotMd(t *testing.T) {
	s := testStore(t)
	if err := os.WriteFile(vaultFile(s, "foo.md.md"), []byte("synced"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Reindex(); err != nil {
		t.Fatal(err)
	}
	all, _ := s.notePaths()
	if !slices.Equal(all, []string{"foo.md"}) {
		t.Fatalf("notes = %q, want foo.md", all)
	}
	if got, err := s.ReadNote("foo.md"); err != nil || got != "synced" {
		t.Errorf("ReadNote = %q, %v", got, err)
	}
}

// ---- links and a stale index

func TestRenameRewritesLinksInANoteTheIndexHasNotRead(t *testing.T) {
	s := testStore(t)
	mustWrite(t, s, "Target", "the target")
	// Written behind the index's back: the file, then a scan, and nothing that
	// reads it, so it has no link rows.
	plant(t, s, CompressionNone, "Linker", "See [[Target]] for more.\n", time.Time{})
	if err := s.Reindex(); err != nil {
		t.Fatal(err)
	}
	n, err := s.RenameNoteAndLinks("Target", "Renamed")
	if err != nil || n != 1 {
		t.Fatalf("RenameNoteAndLinks = %d, %v", n, err)
	}
	got, _ := s.ReadNote("Linker")
	if strings.Contains(got, "[[Target]]") || !strings.Contains(got, "Renamed") {
		t.Errorf("Linker = %q, the link was not followed", got)
	}
}

// ---- RenameNote

func TestRenameNoteRefusesATakenName(t *testing.T) {
	s := testStore(t)
	mustWrite(t, s, "A", "a")
	plant(t, s, CompressionGzip, "B", "b", time.Time{}) // taken in another form
	if err := s.RenameNote("A", "B"); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("RenameNote onto a note = %v, want ErrNameTaken", err)
	}
	if got, _ := s.ReadNote("A"); got != "a" {
		t.Errorf("A = %q", got)
	}
	if got, _ := s.ReadNote("B"); got != "b" {
		t.Errorf("B = %q", got)
	}
}

func TestRenameNoteAllowsAChangeOfCase(t *testing.T) {
	s := testStore(t)
	mustWrite(t, s, "note", "text")
	if err := s.RenameNote("note", "Note"); err != nil {
		t.Fatalf("RenameNote: %v", err)
	}
	if got, err := s.ReadNote("Note"); err != nil || got != "text" {
		t.Errorf("Note = %q, %v", got, err)
	}
	entries, _ := os.ReadDir(s.VaultPath)
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") {
			names = append(names, e.Name())
		}
	}
	if !slices.Equal(names, []string{"Note.md"}) {
		t.Errorf("files = %q, want Note.md", names)
	}
}

func TestRenameNoteMovesTheLockedFormToo(t *testing.T) {
	s := lockedStore(t)
	mustWrite(t, s, "P", "secret")
	if err := s.LockNote("P"); err != nil {
		t.Fatal(err)
	}
	plant(t, s, CompressionNone, "P", "and a plain copy", time.Time{})
	if err := s.RenameNote("P", "Q"); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"Q.md", "Q.md.enc"} {
		if !exists(vaultFile(s, f)) {
			t.Errorf("%s did not arrive", f)
		}
	}
	for _, f := range []string{"P.md", "P.md.enc"} {
		if exists(vaultFile(s, f)) {
			t.Errorf("%s was left behind", f)
		}
	}
}

func TestRenameNoteUndoesTheRenamesAlreadyDone(t *testing.T) {
	s := testStore(t)
	plant(t, s, CompressionNone, "P", "plain", time.Time{})
	plant(t, s, CompressionZstd, "P", "zstd", time.Time{})
	calls := 0
	renameFile = func(from, to string) error {
		calls++
		if calls == 2 {
			return errors.New("the second rename failed")
		}
		return os.Rename(from, to)
	}
	t.Cleanup(func() { renameFile = os.Rename })

	if err := s.RenameNote("P", "Q"); err == nil {
		t.Fatal("RenameNote succeeded")
	}
	for _, f := range []string{"P.md", "P.md.zst"} {
		if !exists(vaultFile(s, f)) {
			t.Errorf("%s was not put back", f)
		}
	}
	for _, f := range []string{"Q.md", "Q.md.zst"} {
		if exists(vaultFile(s, f)) {
			t.Errorf("%s was left behind", f)
		}
	}
}

// ---- a folder with a non-ASCII name

func TestRenameFolderWithANonASCIIName(t *testing.T) {
	s := testStore(t)
	mustWrite(t, s, "Café/One", "1")
	mustWrite(t, s, "Café/Sub/Two", "2")
	if err := s.RenameFolder("Café", "Tea"); err != nil {
		t.Fatal(err)
	}
	all, _ := s.notePaths()
	if !slices.Equal(all, []string{"Tea/One", "Tea/Sub/Two"}) {
		t.Errorf("notes = %q", all)
	}
	notes, _ := s.ListNotes()
	for _, n := range notes {
		if n.Folder != path0(n.Path) {
			t.Errorf("%s has folder %q", n.Path, n.Folder)
		}
	}
}

func path0(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[:i]
	}
	return ""
}
