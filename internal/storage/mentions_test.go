package storage

import (
	"slices"
	"strings"
	"testing"
)

func TestFindMention(t *testing.T) {
	cases := []struct {
		name, text string
		want       string // the text found, "" for none
	}{
		{"Plan", "We agreed the plan today.", "plan"},
		{"Plan", "Planning is not the plan", "plan"}, // a longer word is skipped
		{"Plan", "Planning and planned", ""},
		{"Plan", "Already [[Plan]] and [[Other|the plan]].", ""},
		{"Plan", "See [the plan](https://example.com) now", ""},
		{"Plan", "In `code plan` only", ""},
		{"Plan", "```\nplan\n```\nnothing else", ""},
		{"Plan", "#plan is a tag", ""},
		{"Plan", "https://example.com/plan", ""},
		{"Plan", "![plan](pic.png)", ""},
		{"Plan", "In `code` then the Plan", "Plan"},
		{"Road Map", "The road map is late", "road map"},
		{"C++", "I like C++ a lot", "C++"},
	}
	for _, c := range cases {
		a, b, ok := FindMention(c.text, c.name)
		got := ""
		if ok {
			got = c.text[a:b]
		}
		if got != c.want {
			t.Errorf("FindMention(%q, %q) = %q, want %q", c.text, c.name, got, c.want)
		}
	}
	// Offsets count from the start of the text, not of the line.
	text := "first line\nsecond has Plan here"
	a, b, _ := FindMention(text, "Plan")
	if text[a:b] != "Plan" {
		t.Errorf("range %d:%d is %q", a, b, text[a:b])
	}
}

func TestUnlinkedMentions(t *testing.T) {
	s := testStore(t)
	saveNote(t, s, "Work/Roadmap", "# Roadmap\n\nThe roadmap is this note; Roadmap alone is fine.\n")
	saveNote(t, s, "Plain", "We should read the roadmap before Friday.\n")
	saveNote(t, s, "Linked", "The [[Roadmap]] and also the roadmap.\n")
	saveNote(t, s, "Code", "```\nroadmap\n```\nand `roadmap`\n")
	saveNote(t, s, "Longer", "roadmaps and roadmapping\n")
	saveNote(t, s, "Another/Deep", "A ROADMAP, shouted.\n")
	saveNote(t, s, "Secret", "the roadmap, locked away\n")
	if err := s.SetPassword("pw"); err != nil {
		t.Fatal(err)
	}
	if err := s.LockNote("Secret"); err != nil {
		t.Fatal(err)
	}

	got, err := s.UnlinkedMentions("Work/Roadmap", 10)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"Another/Deep", "Plain"}; !slices.Equal(got, want) {
		t.Errorf("UnlinkedMentions = %v, want %v", got, want)
	}
	if got, _ := s.UnlinkedMentions("Work/Roadmap", 1); len(got) != 1 {
		t.Errorf("limit 1 gave %v", got)
	}
	// Too short a name to look for.
	saveNote(t, s, "Go", "Go is a language\n")
	if got, _ := s.UnlinkedMentions("Go", 10); len(got) != 0 {
		t.Errorf("a two-letter name has mentions: %v", got)
	}
}

func TestLinkMention(t *testing.T) {
	s := testStore(t)
	saveNote(t, s, "Work/Roadmap", "# Roadmap\n")
	saveNote(t, s, "Plain", "Skip `roadmap` and [[Elsewhere|the roadmap]]; read the roadmap, then the roadmap again.\n")
	saveNote(t, s, "Shouty", "A ROADMAP.\n")

	ok, err := s.LinkMention("Work/Roadmap", "Plain")
	if err != nil || !ok {
		t.Fatalf("LinkMention = %v, %v", ok, err)
	}
	want := "Skip `roadmap` and [[Elsewhere|the roadmap]]; read the [[Roadmap|roadmap]], then the roadmap again.\n"
	if got := readBack(t, s, "Plain"); got != want {
		t.Errorf("Plain = %q, want %q", got, want)
	}
	if got, _ := s.Backlinks("Work/Roadmap"); !slices.Equal(got, []string{"Plain"}) {
		t.Errorf("Backlinks after linking = %v", got)
	}
	// It is no longer an unlinked mention, and only the first was changed.
	if got, _ := s.UnlinkedMentions("Work/Roadmap", 10); !slices.Equal(got, []string{"Shouty"}) {
		t.Errorf("UnlinkedMentions after linking = %v", got)
	}

	// Nothing to link: no write, and it says so.
	saveNote(t, s, "None", "Nothing here `roadmap`\n")
	if ok, err := s.LinkMention("Work/Roadmap", "None"); ok || err != nil {
		t.Errorf("LinkMention with no mention = %v, %v", ok, err)
	}
	if got := readBack(t, s, "None"); !strings.Contains(got, "`roadmap`") {
		t.Errorf("None changed: %q", got)
	}

	// A name shared by two notes is written as a path when the bare name
	// would mean the other one.
	saveNote(t, s, "Roadmap", "the root one\n")
	saveNote(t, s, "Third", "the roadmap\n")
	if ok, err := s.LinkMention("Work/Roadmap", "Third"); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if got := readBack(t, s, "Third"); got != "the [[Work/Roadmap|roadmap]]\n" {
		t.Errorf("Third = %q", got)
	}
}
