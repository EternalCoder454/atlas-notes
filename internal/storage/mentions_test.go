package storage

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// unlinked asks the way the app does: with the backlinks already in hand.
func unlinked(s *Store, rel string, limit int) ([]string, error) {
	linked, err := s.Backlinks(rel)
	if err != nil {
		return nil, err
	}
	return s.UnlinkedMentions(context.Background(), rel, limit, linked)
}

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
		{"Plan", "---\ntitle: the plan\n---\nbody", ""},
		{"Plan", "---\nnot front matter, the plan", "plan"},
		{"Plan", "    the plan indented\n\tthe plan tabbed", ""},
		{"Plan", "<!-- the plan -->\n<!--\nthe plan\n-->\nthe Plan", "Plan"},
		{"Plan", "[plan]: https://example.com/x\n", ""},
		{"Plan", "a [Plan] and [Plan][1]", ""},
		{"Plan", "foo/Plan and Plan.md and foo.Plan", ""},
		{"Plan", "Finished the Plan. Next", "Plan"},
		{"Cafe", "Cafe\u0301 opens", ""},
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

	got, err := unlinked(s, "Work/Roadmap", 10)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"Another/Deep", "Plain"}; !slices.Equal(got, want) {
		t.Errorf("UnlinkedMentions = %v, want %v", got, want)
	}
	if got, _ := unlinked(s, "Work/Roadmap", 1); len(got) != 1 {
		t.Errorf("limit 1 gave %v", got)
	}
	// Too short a name to look for.
	saveNote(t, s, "Go", "Go is a language\n")
	if got, _ := unlinked(s, "Go", 10); len(got) != 0 {
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
	if got, _ := unlinked(s, "Work/Roadmap", 10); !slices.Equal(got, []string{"Shouty"}) {
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

func TestLinkMentionEdgeCases(t *testing.T) {
	s := testStore(t)
	s.HistoryDir = filepath.Join(t.TempDir(), "history")
	saveNote(t, s, "Roadmap", "# Roadmap\n")
	saveNote(t, s, "Table", "| a | b |\n| - | - |\n| the roadmap | x |\n")
	saveNote(t, s, "Both", "The [[Roadmap]] and, later, the roadmap.\n")
	saveNote(t, s, "Solo", "the roadmap here\n")

	// A pipe inside a table row must not split the cell.
	if ok, err := s.LinkMention("Roadmap", "Table"); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if got := readBack(t, s, "Table"); !strings.Contains(got, `[[Roadmap\|roadmap]]`) {
		t.Errorf("Table = %q", got)
	}

	// A note that already links is left alone.
	if ok, err := s.LinkMention("Roadmap", "Both"); ok || err != nil {
		t.Errorf("already linked: %v, %v", ok, err)
	}
	if got := readBack(t, s, "Both"); !strings.HasSuffix(got, "the roadmap.\n") {
		t.Errorf("Both changed: %q", got)
	}

	// The text before the change is kept as a version.
	if ok, err := s.LinkMention("Roadmap", "Solo"); !ok || err != nil {
		t.Fatal(ok, err)
	}
	found := false
	vs, _ := s.History("Solo")
	for _, v := range vs {
		if text, err := s.ReadVersion("Solo", v.ID); err == nil && text == "the roadmap here\n" {
			found = true
		}
	}
	if !found {
		t.Error("the pre-link text was not kept in history")
	}

	// A note that is gone is not brought back.
	if err := s.DeleteNote("Solo"); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.LinkMention("Roadmap", "Solo"); ok || err != nil {
		t.Errorf("deleted note: %v, %v", ok, err)
	}
	if s.NoteExists("Solo") {
		t.Error("LinkMention resurrected a deleted note")
	}
}

func TestUnlinkedMentionsStopsWhenCancelled(t *testing.T) {
	s := testStore(t)
	saveNote(t, s, "Roadmap", "# Roadmap\n")
	saveNote(t, s, "A", "the roadmap\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.UnlinkedMentions(ctx, "Roadmap", 10, nil); err == nil {
		t.Error("a cancelled search carried on")
	}
}
