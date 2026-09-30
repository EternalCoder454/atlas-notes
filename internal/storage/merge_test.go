package storage

import (
	"errors"
	"strings"
	"testing"
)

func TestMergedTextAddsTheNoteUnderItsName(t *testing.T) {
	got := MergedText("# Target\n\nbody\n", "Other", "# Other\n\nits text\n")
	want := "# Target\n\nbody\n\n## Other\n\nits text\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestMergedTextKeepsAFirstLineThatIsNotTheTitle(t *testing.T) {
	got := MergedText("body", "Other", "# Something else\ntext")
	if !strings.Contains(got, "# Something else") {
		t.Errorf("a different heading was dropped: %q", got)
	}
}

func TestMergedTextOfAnEmptyNoteIsJustTheHeading(t *testing.T) {
	if got := MergedText("body\n", "Empty", ""); got != "body\n\n## Empty\n" {
		t.Errorf("got %q", got)
	}
	if got := MergedText("", "First", "text"); got != "## First\n\ntext\n" {
		t.Errorf("into an empty note: %q", got)
	}
}

func TestMergeNoteAppendsRewritesLinksAndTrashes(t *testing.T) {
	s := testStore(t)
	tr := newFakeTrash(t)
	s.Trash = tr.trash
	s.WriteNote("Target", "# Target\n\nkeep\n")
	s.WriteNote("Source", "# Source\n\nmoved text\n")
	s.WriteNote("Third", "See [[Source]] and [[Source#Part|the part]] and [[Target]].\n")

	n, err := s.MergeNote("Source", "Target")
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("%d notes had links rewritten, want 1", n)
	}
	got, _ := s.ReadNote("Target")
	if want := "# Target\n\nkeep\n\n## Source\n\nmoved text\n"; got != want {
		t.Errorf("target is %q, want %q", got, want)
	}
	third, _ := s.ReadNote("Third")
	if want := "See [[Target]] and [[Target#Part|the part]] and [[Target]].\n"; third != want {
		t.Errorf("links are %q, want %q", third, want)
	}
	if s.NoteExists("Source") {
		t.Error("the merged note is still in the vault")
	}
	if len(tr.calls) != 1 {
		t.Errorf("the Trash was given %v", tr.calls)
	}
}

func TestMergeNoteWithoutATrashChangesNothing(t *testing.T) {
	s := testStore(t)
	s.WriteNote("A", "a")
	s.WriteNote("B", "b")
	if _, err := s.MergeNote("A", "B"); !errors.Is(err, ErrNoTrash) {
		t.Fatalf("err = %v, want ErrNoTrash", err)
	}
	if got, _ := s.ReadNote("B"); got != "b" {
		t.Errorf("B was changed to %q", got)
	}
	if !s.NoteExists("A") {
		t.Error("A was removed")
	}
}

func TestMergeNoteRefusesItself(t *testing.T) {
	s := testStore(t)
	s.Trash = newFakeTrash(t).trash
	s.WriteNote("A", "a")
	if _, err := s.MergeNote("A", "a"); err == nil {
		t.Error("a note was merged into itself")
	}
}

func TestMergeNoteKeepsTheTextWhenTheTrashFails(t *testing.T) {
	s := testStore(t)
	tr := newFakeTrash(t)
	tr.fail = errors.New("no trash here")
	s.Trash = tr.trash
	s.WriteNote("A", "# A\n\nprecious\n")
	s.WriteNote("B", "b\n")
	s.WriteNote("C", "[[A]]\n")
	if _, err := s.MergeNote("A", "B"); !errors.Is(err, ErrTrashFailed) {
		t.Fatalf("err = %v, want ErrTrashFailed", err)
	}
	if !s.NoteExists("A") {
		t.Error("the note is gone though it could not be trashed")
	}
	if got, _ := s.ReadNote("C"); got != "[[A]]\n" {
		t.Errorf("links were rewritten to a merge that did not finish: %q", got)
	}
}

func TestExportedCreateNoteNeverWritesOverANote(t *testing.T) {
	s := testStore(t)
	if made, err := s.CreateNote("N", "one"); err != nil || !made {
		t.Fatalf("made=%v err=%v", made, err)
	}
	if made, _ := s.CreateNote("N", "two"); made {
		t.Error("an existing note was written over")
	}
	if got, _ := s.ReadNote("N"); got != "one" {
		t.Errorf("N is %q", got)
	}
}
