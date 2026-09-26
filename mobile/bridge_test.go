package bridge

import (
	"encoding/json"
	"strings"
	"testing"
)

// The Android app is only ever built on CI, and only ever run on a phone, so
// these tests are where the bridge's behaviour is actually pinned down. They
// exercise it the way Kotlin does: through the exported functions, against a
// real vault in a temporary directory.

// newVault opens a fresh vault for one test and closes it afterwards.
func newVault(t *testing.T) {
	t.Helper()
	// The package holds one open vault in a package-level variable, so tests
	// that share a process have to put it back.
	Close()
	if err := Open(t.TempDir()); err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(Close)
}

func decodeNotes(t *testing.T, raw string) []noteRow {
	t.Helper()
	var notes []noteRow
	if err := json.Unmarshal([]byte(raw), &notes); err != nil {
		t.Fatalf("ListNotes returned invalid JSON: %v", err)
	}
	return notes
}

func TestOpenIsIdempotent(t *testing.T) {
	newVault(t)
	// Android may call this again after the activity is recreated; a second
	// call has to be harmless rather than opening a second vault.
	if err := Open(t.TempDir()); err != nil {
		t.Fatalf("second Open: %v", err)
	}
}

func TestWriteReadRoundTrip(t *testing.T) {
	newVault(t)

	rel, err := NewNote("Shopping")
	if err != nil {
		t.Fatalf("NewNote: %v", err)
	}
	const body = "# Shopping\n\n- [ ] Bread\n- [x] Milk\n"
	if err := WriteNote(rel, body); err != nil {
		t.Fatalf("WriteNote: %v", err)
	}
	got, err := ReadNote(rel)
	if err != nil {
		t.Fatalf("ReadNote: %v", err)
	}
	if got != body {
		t.Errorf("round trip changed the note:\n got %q\nwant %q", got, body)
	}
}

func TestListNotesIsValidJSONWithTheRightShape(t *testing.T) {
	newVault(t)

	rel, err := NewNote("Work")
	if err != nil {
		t.Fatalf("NewNote: %v", err)
	}
	if err := WriteNote(rel, "- [ ] one\n"); err != nil {
		t.Fatalf("WriteNote: %v", err)
	}

	raw, err := ListNotes()
	if err != nil {
		t.Fatalf("ListNotes: %v", err)
	}
	notes := decodeNotes(t, raw)
	var found *noteRow
	for i := range notes {
		if notes[i].Path == rel {
			found = &notes[i]
		}
	}
	if found == nil {
		t.Fatalf("the new note is not in the list: %+v", notes)
	}
	// Name is what the interface puts in the list, so an empty one would show
	// a row with no title.
	if found.Name == "" {
		t.Error("Name is empty")
	}
	if found.Locked {
		t.Error("a note nobody locked came back locked")
	}
}

func TestTasksAndToggle(t *testing.T) {
	newVault(t)
	const body = "notes\n- [ ] one\nmiddle\n- [x] two\n"

	raw, err := Tasks(body)
	if err != nil {
		t.Fatalf("Tasks: %v", err)
	}
	var tasks []taskRow
	if err := json.Unmarshal([]byte(raw), &tasks); err != nil {
		t.Fatalf("Tasks returned invalid JSON: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("got %d tasks, want 2: %+v", len(tasks), tasks)
	}
	// The line numbers are what a tap sends back, so they have to index the
	// note's real lines and not the list of tasks.
	if tasks[0].Line != 1 || tasks[1].Line != 3 {
		t.Errorf("wrong line numbers: %+v", tasks)
	}
	if tasks[0].Checked || !tasks[1].Checked {
		t.Errorf("wrong checked state: %+v", tasks)
	}

	toggled, err := ToggleTask(body, 1)
	if err != nil {
		t.Fatalf("ToggleTask: %v", err)
	}
	if !strings.Contains(toggled, "- [x] one") {
		t.Errorf("ticking line 1 did not tick it:\n%s", toggled)
	}
	// Everything else has to survive untouched: this text is the note.
	if !strings.Contains(toggled, "middle") || !strings.Contains(toggled, "- [x] two") {
		t.Errorf("ticking one item disturbed the rest:\n%s", toggled)
	}
	if lines := strings.Count(toggled, "\n"); lines != strings.Count(body, "\n") {
		t.Errorf("line count changed: got %d, want %d", lines, strings.Count(body, "\n"))
	}
}

func TestToggleTaskRejectsLinesThatAreNotTasks(t *testing.T) {
	newVault(t)
	const body = "just a paragraph\n"
	if _, err := ToggleTask(body, 0); err == nil {
		t.Error("ticking a paragraph was allowed")
	}
	if _, err := ToggleTask(body, 99); err == nil {
		t.Error("ticking a line past the end was allowed")
	}
}

func TestLockedNoteReportsItselfAsLocked(t *testing.T) {
	newVault(t)

	rel, err := NewNote("Private")
	if err != nil {
		t.Fatalf("NewNote: %v", err)
	}
	const secret = "# Private\n\nsomething worth locking\n"
	if err := WriteNote(rel, secret); err != nil {
		t.Fatalf("WriteNote: %v", err)
	}
	if err := SetPassword("correct horse battery staple"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	if err := LockNote(rel); err != nil {
		t.Fatalf("LockNote: %v", err)
	}

	// Still unlocked in this session, so it reads normally.
	if got, err := ReadNote(rel); err != nil || got != secret {
		t.Fatalf("reading a locked note while unlocked: %q, %v", got, err)
	}

	// Forgetting the password is what closing the app does.
	Lock()
	_, err = ReadNote(rel)
	if err == nil {
		t.Fatal("a locked note was readable with no password")
	}
	// Kotlin matches on this exact string to know it should ask for the
	// password rather than report a failure, so it is part of the interface.
	if err.Error() != ErrLockedMessage {
		t.Errorf("got error %q, want %q", err.Error(), ErrLockedMessage)
	}

	if err := Unlock("correct horse battery staple"); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	if got, _ := ReadNote(rel); got != secret {
		t.Errorf("after unlocking, got %q, want %q", got, secret)
	}
}

func TestWrongPasswordIsRefused(t *testing.T) {
	newVault(t)
	if err := SetPassword("the right one"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	Lock()
	if err := Unlock("not the right one"); err == nil {
		t.Error("the wrong password was accepted")
	}
}

func TestCallsBeforeOpenFailRatherThanPanic(t *testing.T) {
	// Android can call into this before Open finishes, or after the vault has
	// been closed. That has to be an error, not a crash in a Go goroutine that
	// takes the whole app down with it.
	Close()
	if _, err := ListNotes(); err == nil {
		t.Error("ListNotes worked with no vault open")
	}
	if err := WriteNote("x", "y"); err == nil {
		t.Error("WriteNote worked with no vault open")
	}
	if HasPassword() {
		t.Error("HasPassword was true with no vault open")
	}
	if IsUnlocked() {
		t.Error("IsUnlocked was true with no vault open")
	}
}
