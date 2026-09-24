package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		current, remote string
		want            bool
	}{
		{"0.5.4", "0.5.5", true},
		{"0.5.4", "0.6.0", true},
		{"0.5.9", "0.5.10", true}, // numbers, not text
		{"0.5.4", "1.0.0", true},
		{"0.5.4", "0.5.4", false},
		{"0.5.5", "0.5.4", false}, // never offer a downgrade
		{"1.0.0", "0.9.9", false},
		{"0.5.4", "0.5.4-beta", false},
		{"v0.5.4", "v0.5.5", true},
		{"0.5", "0.5.1", true},
		{"0.5.1", "0.5", false},
		{"", "0.5.5", false}, // unknown local version: stay quiet
		{"0.5.4", "", false}, // unreadable remote: stay quiet
		{"0.5.4", "not-a-version", false},
		{"0.5.4", "0.5.-1", false},
	}
	for _, c := range cases {
		if got := Newer(c.current, c.remote); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.current, c.remote, got, c.want)
		}
	}
}

func TestParseNotes(t *testing.T) {
	notes := `# What's new in Atlas Notes

Some words for anyone reading this file directly.

## 0.5.5
- Checkboxes line up with their text
- The app starts faster on big vaults

## 0.5.4
- An older change nobody should see here
`
	rel, err := ParseNotes(strings.NewReader(notes))
	if err != nil {
		t.Fatal(err)
	}
	if rel.Version != "0.5.5" {
		t.Errorf("version = %q, want 0.5.5", rel.Version)
	}
	if len(rel.Notes) != 2 || rel.Notes[0] != "Checkboxes line up with their text" {
		t.Errorf("notes = %q", rel.Notes)
	}
}

func TestParseNotesAwkwardInput(t *testing.T) {
	for name, in := range map[string]string{
		"empty":        "",
		"no version":   "# Title\n\n- a bullet with no version above it\n",
		"heading only": "## \n- orphan\n",
		"not markdown": "\x00\x01\x02 binary rubbish",
	} {
		if _, err := ParseNotes(strings.NewReader(in)); err == nil {
			t.Errorf("ParseNotes(%s) accepted input it should have refused", name)
		}
	}

	// A release with no bullets is still a release.
	rel, err := ParseNotes(strings.NewReader("## 1.2.3\n"))
	if err != nil || rel.Version != "1.2.3" || len(rel.Notes) != 0 {
		t.Errorf("bare version: %+v %v", rel, err)
	}

	// The bullet list is capped so the dialog stays readable.
	var b strings.Builder
	b.WriteString("## 2.0.0\n")
	for i := 0; i < 50; i++ {
		b.WriteString("- change\n")
	}
	rel, err = ParseNotes(strings.NewReader(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	if len(rel.Notes) != maxNoteLines {
		t.Errorf("notes = %d, want capped at %d", len(rel.Notes), maxNoteLines)
	}
}

func TestCheck(t *testing.T) {
	var served string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(served))
	}))
	defer srv.Close()

	checker := &Checker{NotesURL: srv.URL + "/%s/WHATSNEW.md", Client: srv.Client()}

	served = "## 0.9.0\n- Something new\n"
	rel, err := checker.Check(context.Background(), "main", "0.5.4")
	if err != nil {
		t.Fatal(err)
	}
	if rel == nil || rel.Version != "0.9.0" {
		t.Fatalf("expected an update, got %+v", rel)
	}

	// Already current: no update, no error, nothing to show.
	rel, err = checker.Check(context.Background(), "main", "0.9.0")
	if err != nil || rel != nil {
		t.Fatalf("expected no update, got %+v (%v)", rel, err)
	}
}

func TestCheckServerErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()
	checker := &Checker{NotesURL: srv.URL + "/%s/WHATSNEW.md", Client: srv.Client()}
	if _, err := checker.Check(context.Background(), "main", "0.5.4"); err == nil {
		t.Error("a 404 should be reported as an error, not treated as no update")
	}
}

// TestShippedNotes checks the file this whole feature reads in production. It
// has to parse, it has to describe the version that is actually being shipped,
// and its lines have to be the kind of thing a person who does not write
// software can read — the point of keeping it separate from CHANGELOG.md.
func TestShippedNotes(t *testing.T) {
	f, err := os.Open(filepath.Join("..", "..", "WHATSNEW.md"))
	if err != nil {
		t.Fatalf("WHATSNEW.md: %v", err)
	}
	defer f.Close()

	rel, err := ParseNotes(f)
	if err != nil {
		t.Fatalf("WHATSNEW.md does not parse: %v", err)
	}
	if len(rel.Notes) == 0 {
		t.Fatal("the newest entry has no lines; an update dialog would be empty")
	}
	if want := appVersion(t); rel.Version != want {
		t.Errorf("WHATSNEW.md leads with %q but the app is %q: a release would either\n"+
			"tell everyone about an update they already have, or tell no one at all",
			rel.Version, want)
	}
	if n := bulletsInNewestEntry(t); n > maxNoteLines {
		t.Errorf("the newest entry has %d lines but only %d are shown: the rest "+
			"would be dropped without a word", n, maxNoteLines)
	}
	for _, note := range rel.Notes {
		if strings.Contains(note, "`") {
			t.Errorf("release note reads like a changelog entry: %q", note)
		}
		if len(note) > 110 {
			t.Errorf("release note is too long to read in a dialog (%d chars): %q", len(note), note)
		}
	}
}

// appVersion reads the version the binary will report. It is parsed out of the
// source rather than imported because internal/app pulls in GTK.
func appVersion(t *testing.T) string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("..", "app", "version.go"))
	if err != nil {
		t.Fatalf("version.go: %v", err)
	}
	m := regexp.MustCompile(`(?m)^var version = "([^"]+)"`).FindSubmatch(src)
	if m == nil {
		t.Fatal("version.go no longer declares `var version = \"...\"`")
	}
	return string(m[1])
}

// bulletsInNewestEntry counts what the newest entry actually contains, which
// ParseNotes cannot report because it stops at the cap.
func bulletsInNewestEntry(t *testing.T) int {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("..", "..", "WHATSNEW.md"))
	if err != nil {
		t.Fatalf("WHATSNEW.md: %v", err)
	}
	_, rest, found := strings.Cut(string(src), "## ")
	if !found {
		t.Fatal("WHATSNEW.md has no version heading")
	}
	newest, _, _ := strings.Cut(rest, "\n## ")
	n := 0
	for _, line := range strings.Split(newest, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "- ") {
			n++
		}
	}
	return n
}
