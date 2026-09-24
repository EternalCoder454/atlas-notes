package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
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

// TestUpgradePrompts upgrades unmodified old default prompts to the current
// defaults and leaves user-customized prompts untouched.
func TestUpgradePrompts(t *testing.T) {
	cfg := Config{
		SystemPrompt: oldSystemPrompt,
		Actions: []AIAction{
			{Name: "Summarize Note", Mode: ActionModeShow, Prompt: oldSummarizePrompt},
			{Name: "Clean & Format", Mode: ActionModeReplace, Prompt: cleanPromptV031},
			{Name: "Custom", Mode: ActionModeShow, Prompt: "do my own thing {content}"},
			{Name: "Sort Priorities", Mode: ActionModeSort, Prompt: oldSortPrompt},
		},
	}
	upgradePrompts(&cfg)
	if cfg.SystemPrompt != DefaultSystemPrompt {
		t.Error("system prompt not upgraded")
	}
	if cfg.Actions[0].Prompt != defaultSummarizePrompt {
		t.Error("summarize prompt not upgraded")
	}
	if cfg.Actions[1].Prompt != defaultCleanPrompt {
		t.Error("clean prompt not upgraded (chained from v0.3.1)")
	}
	if cfg.Actions[2].Prompt != "do my own thing {content}" {
		t.Error("custom action prompt should be left untouched")
	}
	if cfg.Actions[3].Prompt != DefaultSortPrompt {
		t.Error("sort prompt not upgraded")
	}
}

// TestConcurrentWrites exercises the store's write mutex: many goroutines write
// and read distinct notes at once (meaningful under `go test -race`). Each note's
// final content is its last revision, so a torn or lost write is caught.
func TestConcurrentWrites(t *testing.T) {
	s := testStore(t)
	const notes, revs = 16, 6
	var wg sync.WaitGroup
	for i := 0; i < notes; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rel := fmt.Sprintf("Concurrent/note-%d", i)
			for j := 0; j < revs; j++ {
				if err := s.WriteNote(rel, fmt.Sprintf("# note %d\n\nrevision %d\n", i, j)); err != nil {
					t.Errorf("WriteNote %s: %v", rel, err)
					return
				}
				if _, err := s.ReadNote(rel); err != nil {
					t.Errorf("ReadNote %s: %v", rel, err)
					return
				}
			}
		}(i)
	}
	wg.Wait()

	all, err := s.ListNotes()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != notes {
		t.Fatalf("ListNotes = %d want %d", len(all), notes)
	}
	for i := 0; i < notes; i++ {
		rel := fmt.Sprintf("Concurrent/note-%d", i)
		got, err := s.ReadNote(rel)
		if err != nil {
			t.Fatalf("ReadNote %s: %v", rel, err)
		}
		if want := fmt.Sprintf("# note %d\n\nrevision %d\n", i, revs-1); got != want {
			t.Errorf("note %d = %q want %q", i, got, want)
		}
	}
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

func TestIndexMetadata(t *testing.T) {
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
	if n.Path != "Work/Plan" || n.Folder != "Work" {
		t.Errorf("meta = %+v", n)
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
	if len(notes) != 1 || notes[0].Path != "Getting Started" {
		t.Fatalf("guide note not created: %+v", notes)
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
	if len(n) != 1 || n[0].Path != "Getting Started" {
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

func TestUniqueName(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "vault"), filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if got := s.UniqueName("", "Untitled note"); got != "Untitled note" {
		t.Errorf("first name = %q", got)
	}
	if err := s.WriteNote("Untitled note", "x"); err != nil {
		t.Fatal(err)
	}
	if got := s.UniqueName("", "Untitled note"); got != "Untitled note 2" {
		t.Errorf("second name = %q want %q", got, "Untitled note 2")
	}
	if err := s.WriteNote("Work/Untitled note", "x"); err != nil {
		t.Fatal(err)
	}
	if got := s.UniqueName("Work", "Untitled note"); got != "Work/Untitled note 2" {
		t.Errorf("name in folder = %q", got)
	}
}

// TestReindexIncremental covers the paths Reindex takes now that it skips
// unchanged files: a new note is picked up, a deleted one is pruned, and a
// second run over an untouched vault is a no-op.
func TestReindexIncremental(t *testing.T) {
	dir := t.TempDir()
	vault := filepath.Join(dir, "vault")
	s, err := Open(vault, filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	for _, name := range []string{"One", "Two", "Folder/Three"} {
		if err := s.WriteNote(name, "# "+name+"\n"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Reindex(); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountNotes(); n != 3 {
		t.Fatalf("after reindex: %d notes, want 3", n)
	}

	// A file that appears without going through the store is picked up.
	if err := s.WriteNote("Four", "# Four\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DELETE FROM notes WHERE path = 'Four'`); err != nil {
		t.Fatal(err)
	}
	if err := s.Reindex(); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountNotes(); n != 4 {
		t.Fatalf("new file not indexed: %d notes, want 4", n)
	}

	// A file that disappears behind the store's back is pruned.
	if err := os.Remove(s.notePath("Two")); err != nil {
		t.Fatal(err)
	}
	if err := s.Reindex(); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountNotes(); n != 3 {
		t.Fatalf("stale row not pruned: %d notes, want 3", n)
	}
	notes, _ := s.ListNotes()
	for _, n := range notes {
		if n.Path == "Two" {
			t.Error("deleted note still indexed")
		}
	}

	// Running again over an unchanged vault must be stable.
	if err := s.Reindex(); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountNotes(); n != 3 {
		t.Fatalf("second reindex changed the index: %d notes", n)
	}
}

// TestCheckUpdatesSetting pins the two things the launch-time update check
// depends on: a config written before the setting existed keeps it on, and a
// user who turns it off stays off across a restart. The second is the one that
// could quietly break — false is also a bool's zero value, so any "backfill the
// empty fields" logic added to LoadConfig would silently re-enable it.
func TestCheckUpdatesSetting(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	path := ConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	// A config from before the setting existed.
	if err := os.WriteFile(path, []byte(`{"model":"qwen3.5:9b"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !cfg.CheckUpdates {
		t.Error("an older config should keep the update check on by default")
	}

	// Turning it off has to survive a save and reload.
	cfg.CheckUpdates = false
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	reloaded, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if reloaded.CheckUpdates {
		t.Error("the update check switched itself back on")
	}
}

// TestHasTasksIsIndexed pins how a checklist is told apart from a note. The
// vault panel draws them differently, and it reads the answer from the index
// rather than opening every file, so the index has to be right in both the
// paths that fill it: writing through the app, and scanning a vault the app
// did not write.
func TestHasTasksIsIndexed(t *testing.T) {
	s := testStore(t)
	if err := s.WriteNote("Plain", "# Plain\n\nJust prose, no tasks.\n"); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteNote("Listy", "# Listy\n\n- [ ] something to do\n"); err != nil {
		t.Fatal(err)
	}
	check := func(when string) {
		t.Helper()
		notes, err := s.ListNotes()
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]bool{}
		for _, n := range notes {
			got[n.Path] = n.HasTasks
		}
		if got["Plain"] {
			t.Errorf("%s: a note with no tasks is flagged as a checklist", when)
		}
		if !got["Listy"] {
			t.Errorf("%s: a note with tasks is not flagged as a checklist", when)
		}
	}
	check("after writing")

	// The same answer has to come back from a rebuild, which reads the files.
	if _, err := s.db.Exec(`DELETE FROM notes`); err != nil {
		t.Fatal(err)
	}
	if err := s.Reindex(); err != nil {
		t.Fatal(err)
	}
	check("after reindexing")

	// Editing the tasks out has to clear it again.
	if err := s.WriteNote("Listy", "# Listy\n\nthe task is gone\n"); err != nil {
		t.Fatal(err)
	}
	notes, _ := s.ListNotes()
	for _, n := range notes {
		if n.Path == "Listy" && n.HasTasks {
			t.Error("removing the tasks left the note flagged as a checklist")
		}
	}
}

// TestHasTasksSurvivesLocking: the vault scan cannot read a locked note, and
// must not conclude from that that it has no tasks.
func TestHasTasksSurvivesLocking(t *testing.T) {
	s := lockedStore(t)
	if err := s.WriteNote("Secret", "# Secret\n\n- [ ] a private task\n"); err != nil {
		t.Fatal(err)
	}
	if err := s.LockNote("Secret"); err != nil {
		t.Fatal(err)
	}
	if err := s.Reindex(); err != nil {
		t.Fatal(err)
	}
	notes, err := s.ListNotes()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range notes {
		if n.Path != "Secret" {
			continue
		}
		if !n.Locked {
			t.Error("the note lost its locked flag")
		}
		if !n.HasTasks {
			t.Error("a scan that could not read the note forgot it was a checklist")
		}
		return
	}
	t.Fatal("the note is not in the index")
}

// TestHasTasksBackfillsOnUpgrade: an index written before the column existed
// has to gain it. The scan skips files whose modification time has not
// changed, so without a nudge every existing note would stay flagged as having
// no tasks until it was next edited.
func TestHasTasksBackfillsOnUpgrade(t *testing.T) {
	dir := t.TempDir()
	vault, db := filepath.Join(dir, "vault"), filepath.Join(dir, "index.db")

	s, err := Open(vault, db)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteNote("Listy", "# Listy\n\n- [ ] a task\n"); err != nil {
		t.Fatal(err)
	}
	// Put the index back the way an older version left it.
	if _, err := s.db.Exec(`ALTER TABLE notes DROP COLUMN has_tasks`); err != nil {
		t.Fatalf("simulating the older schema: %v", err)
	}
	s.Close()

	reopened, err := Open(vault, db)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.Reindex(); err != nil {
		t.Fatal(err)
	}
	notes, err := reopened.ListNotes()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range notes {
		if n.Path == "Listy" {
			if !n.HasTasks {
				t.Error("upgrading an existing index left the checklist unflagged")
			}
			return
		}
	}
	t.Fatal("the note vanished from the index")
}

// TestLockedNoteIsNotGuessedToBeAChecklist: on a fresh index the scan has no
// previous answer to keep for a note it cannot read, so the "unknown" value is
// what gets stored. It must not come back out as "yes".
func TestLockedNoteIsNotGuessedToBeAChecklist(t *testing.T) {
	s := lockedStore(t)
	if err := s.WriteNote("Secret", "# Secret\n\nprose only, no tasks\n"); err != nil {
		t.Fatal(err)
	}
	if err := s.LockNote("Secret"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DELETE FROM notes`); err != nil { // a fresh index
		t.Fatal(err)
	}
	if err := s.Reindex(); err != nil {
		t.Fatal(err)
	}
	notes, err := s.ListNotes()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range notes {
		if n.Path == "Secret" && n.HasTasks {
			t.Error("a locked note the scan could not read was drawn as a checklist")
		}
	}
}
