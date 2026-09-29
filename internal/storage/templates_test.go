package storage

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

var tuesday = time.Date(2026, 9, 29, 14, 5, 0, 0, time.Local)

func TestExpandTemplate(t *testing.T) {
	in := "# {{title}}\n{{ Date }} {{time}} {{datetime}} {{weekday}} {{longdate}} {{unknown}} {{ unclosed"
	want := "# Plan\n2026-09-29 14:05 2026-09-29 14:05 Tuesday Tuesday 29 September 2026 {{unknown}} {{ unclosed"
	if got := ExpandTemplate(in, "Plan", tuesday); got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestDailyNoteIsMadeOnceAndReused(t *testing.T) {
	s := testStore(t)
	rel, created, err := s.DailyNote(tuesday)
	if err != nil || !created || rel != "Daily/2026-09-29" {
		t.Fatalf("first call: %q %v %v", rel, created, err)
	}
	body, _ := s.ReadNote(rel)
	if body != "# Tuesday 29 September 2026\n\n" {
		t.Fatalf("default body %q", body)
	}
	s.WriteNote(rel, "# Today\n\nwritten in\n")
	again, created, err := s.DailyNote(tuesday)
	if err != nil || created || again != rel {
		t.Fatalf("second call: %q %v %v", again, created, err)
	}
	if body, _ := s.ReadNote(rel); !strings.Contains(body, "written in") {
		t.Fatal("asking for the day's note again replaced what was written in it")
	}
}

func TestDailyNoteUsesTheDailyTemplate(t *testing.T) {
	s := testStore(t)
	s.WriteNote("Templates/Daily", "# {{longdate}}\n\n## Tasks\n- [ ] \n")
	rel, _, err := s.DailyNote(tuesday)
	if err != nil {
		t.Fatal(err)
	}
	if body, _ := s.ReadNote(rel); body != "# Tuesday 29 September 2026\n\n## Tasks\n- [ ] \n" {
		t.Fatalf("got %q", body)
	}
}

func TestALockedDailyTemplateGivesWayToTheDefault(t *testing.T) {
	s := lockedStore(t)
	s.WriteNote("Templates/Daily", "# secret template {{date}}\n")
	if err := s.LockNote("Templates/Daily"); err != nil {
		t.Fatal(err)
	}
	s.Lock()
	rel, created, err := s.DailyNote(tuesday)
	if err != nil || !created {
		t.Fatalf("%q %v %v", rel, created, err)
	}
	if body, _ := s.ReadNote(rel); strings.Contains(body, "secret") {
		t.Fatalf("used a template it could not have read: %q", body)
	}
}

func TestDailyNoteInALockedFolderNeedsThePassword(t *testing.T) {
	s := lockedStore(t)
	s.CreateFolder(DailyFolder)
	if err := s.LockFolder(DailyFolder); err != nil {
		t.Fatal(err)
	}
	s.Lock()
	if _, _, err := s.DailyNote(tuesday); !errors.Is(err, ErrLocked) {
		t.Fatalf("got %v, want ErrLocked", err)
	}
}

func TestTemplatesAndNewFromTemplate(t *testing.T) {
	s := testStore(t)
	s.WriteNote("Templates/Meeting", "# {{title}}\n\nDate: {{date}}\n")
	s.WriteNote("Templates/Work/Standup", "standup\n")
	s.WriteNote("Templates_old/Nope", "not a template\n") // LIKE's "_" must not match "s"
	s.WriteNote("Elsewhere", "no\n")
	got, err := s.Templates()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"Templates/Meeting", "Templates/Work/Standup"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Templates() = %v, want %v", got, want)
	}

	rel, err := s.NewFromTemplate("Templates/Meeting", "Work", "Kickoff", tuesday)
	if err != nil || rel != "Work/Kickoff" {
		t.Fatalf("%q %v", rel, err)
	}
	if body, _ := s.ReadNote(rel); body != "# Kickoff\n\nDate: 2026-09-29\n" {
		t.Fatalf("got %q", body)
	}
	// The same name again gets a new note, not the old one written over.
	rel2, err := s.NewFromTemplate("Templates/Meeting", "Work", "Kickoff", tuesday)
	if err != nil || rel2 == rel {
		t.Fatalf("second note %q %v", rel2, err)
	}
	if body, _ := s.ReadNote(rel2); body != "# "+rel2[len("Work/"):]+"\n\nDate: 2026-09-29\n" {
		t.Fatalf("second note's title placeholder: %q", body)
	}
}
