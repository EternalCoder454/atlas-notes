package bridge

import (
	"encoding/json"
	"slices"
	"testing"
	"time"
)

// These go through the exported functions against a real vault, as Kotlin
// does, and pin down the JSON the interface decodes.

func TestDailyNoteIsTodaysAndMadeOnce(t *testing.T) {
	newVault(t)

	rel, err := DailyNote()
	if err != nil {
		t.Fatalf("DailyNote: %v", err)
	}
	if want := "Daily/" + time.Now().Format("2006-01-02"); rel != want {
		t.Errorf("DailyNote = %q, want %q", rel, want)
	}
	if !slices.Contains(listed(t), rel) {
		t.Errorf("the day's note was not made: %v", listed(t))
	}

	// Opening it a second time from the shortcut must find the note again,
	// with what was written in it, and not start it over.
	const body = "# Today\n\nwritten in\n"
	if err := WriteNote(rel, body); err != nil {
		t.Fatalf("WriteNote: %v", err)
	}
	again, err := DailyNote()
	if err != nil || again != rel {
		t.Fatalf("second DailyNote = %q, %v; want %q", again, err, rel)
	}
	if got, _ := ReadNote(rel); got != body {
		t.Errorf("asking again replaced the note: %q", got)
	}
}

func TestDailyNoteInALockedFolderAsksForThePassword(t *testing.T) {
	newVault(t)
	if err := SetPassword("correct horse battery staple"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	s, err := vault()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateFolder("Daily"); err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	if err := s.LockFolder("Daily"); err != nil {
		t.Fatalf("LockFolder: %v", err)
	}
	Lock()

	// Kotlin matches on this string, as it does for a locked note.
	if _, err := DailyNote(); err == nil || err.Error() != ErrLockedMessage {
		t.Fatalf("DailyNote = %v, want %q", err, ErrLockedMessage)
	}
	if err := Unlock("correct horse battery staple"); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	if _, err := DailyNote(); err != nil {
		t.Errorf("DailyNote after unlocking: %v", err)
	}
}

func TestDueTasksIsValidJSONWithTheRightShape(t *testing.T) {
	newVault(t)
	if err := WriteNote("Work", "# Work\n"+
		"- [ ] later <!-- due:2026-10-05 -->\n"+
		"- [ ] pay rent <!-- priority:high due:2026-09-29 -->\n"+
		"- [ ] overdue <!-- due:2026-09-01 -->\n"+
		"- [x] done <!-- due:2026-09-01 -->\n"+
		"- [ ] no date\n"); err != nil {
		t.Fatalf("WriteNote: %v", err)
	}

	raw, err := DueTasks("2026-09-29")
	if err != nil {
		t.Fatalf("DueTasks: %v", err)
	}
	var tasks []dueRow
	if err := json.Unmarshal([]byte(raw), &tasks); err != nil {
		t.Fatalf("DueTasks returned invalid JSON: %v", err)
	}
	// Overdue is included, so is the day itself, and neither a finished task,
	// an undated one nor one due later is.
	want := []dueRow{
		{Path: "Work", Line: 3, Text: "overdue", Due: "2026-09-01"},
		{Path: "Work", Line: 2, Text: "pay rent", Due: "2026-09-29", Priority: "high"},
	}
	if !slices.Equal(tasks, want) {
		t.Errorf("DueTasks(2026-09-29) = %+v\nwant %+v", tasks, want)
	}
}

func TestDueTasksWithNothingDueIsAnEmptyArray(t *testing.T) {
	newVault(t)
	raw, err := DueTasks("2026-09-29")
	if err != nil {
		t.Fatalf("DueTasks: %v", err)
	}
	// Kotlin reads this as an array; null would not parse as one.
	if raw != "[]" {
		t.Errorf("DueTasks with nothing due = %s, want []", raw)
	}
}

func TestDueTasksRefusesAnythingButADate(t *testing.T) {
	newVault(t)
	for _, bad := range []string{"", "today", "29/09/2026", "2026-9-29"} {
		if _, err := DueTasks(bad); err == nil {
			t.Errorf("DueTasks(%q) was accepted", bad)
		}
	}
}

func TestTagsAndNotesWithTag(t *testing.T) {
	newVault(t)
	for rel, text := range map[string]string{
		"A": "# A\n\n#project/atlas and #Ideas\n",
		"B": "# B\n\n#ideas\n",
		"C": "# C\n\nno tags here\n",
	} {
		if err := WriteNote(rel, text); err != nil {
			t.Fatalf("WriteNote(%s): %v", rel, err)
		}
	}

	raw, err := Tags()
	if err != nil {
		t.Fatalf("Tags: %v", err)
	}
	var tags []tagRow
	if err := json.Unmarshal([]byte(raw), &tags); err != nil {
		t.Fatalf("Tags returned invalid JSON: %v", err)
	}
	// Sorted, lowercased and without the "#", with the count of notes.
	want := []tagRow{{Tag: "ideas", Notes: 2}, {Tag: "project/atlas", Notes: 1}}
	if !slices.Equal(tags, want) {
		t.Errorf("Tags = %+v, want %+v", tags, want)
	}

	paths := func(tag string) []string {
		t.Helper()
		raw, err := NotesWithTag(tag)
		if err != nil {
			t.Fatalf("NotesWithTag(%q): %v", tag, err)
		}
		var out []string
		if err := json.Unmarshal([]byte(raw), &out); err != nil || out == nil {
			t.Fatalf("NotesWithTag(%q) returned %s, which is not an array of paths (%v)", tag, raw, err)
		}
		slices.Sort(out)
		return out
	}
	// The search box passes what was typed, so "#" and case must not matter,
	// and a parent tag finds the notes under it.
	for _, q := range []string{"ideas", "#ideas", "#IDEAS"} {
		if got := paths(q); !slices.Equal(got, []string{"A", "B"}) {
			t.Errorf("NotesWithTag(%q) = %v, want [A B]", q, got)
		}
	}
	if got := paths("#project"); !slices.Equal(got, []string{"A"}) {
		t.Errorf("NotesWithTag(#project) = %v, want [A]", got)
	}
	if got := paths("#nothing"); len(got) != 0 {
		t.Errorf("NotesWithTag(#nothing) = %v, want none", got)
	}
}

func TestFeaturesBeforeOpenFailRatherThanPanic(t *testing.T) {
	Close()
	if _, err := DailyNote(); err == nil {
		t.Error("DailyNote worked with no vault open")
	}
	if _, err := DueTasks("2026-09-29"); err == nil {
		t.Error("DueTasks worked with no vault open")
	}
	if _, err := Tags(); err == nil {
		t.Error("Tags worked with no vault open")
	}
	if _, err := NotesWithTag("x"); err == nil {
		t.Error("NotesWithTag worked with no vault open")
	}
}
