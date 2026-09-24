package update

import (
	"strings"
	"testing"
)

// FuzzParseNotes feeds the parser arbitrary input, because what it parses comes
// off the network. It must never panic, and whatever it does return has to be
// safe to put straight into a dialog: a version that parses as a version, and
// no more lines than the dialog is willing to show.
func FuzzParseNotes(f *testing.F) {
	f.Add("## 0.5.5\n- one\n- two\n")
	f.Add("# What's new\n\n## 1.0\n* a bullet\n\n## 0.9\n- older\n")
	f.Add("## v2.0.0-beta\n-\n")
	f.Add("")
	f.Add("## \n- orphan bullet\n")
	f.Add(strings.Repeat("- bullet\n", 200))

	f.Fuzz(func(t *testing.T, s string) {
		rel, err := ParseNotes(strings.NewReader(s))
		if err != nil {
			if rel != nil {
				t.Fatalf("returned a release alongside an error: %+v", rel)
			}
			return
		}
		if rel == nil {
			t.Fatal("no release and no error")
		}
		if rel.Version == "" {
			t.Fatal("a release with no version would render as 'Update Found — v'")
		}
		// The version is put straight into a window heading, so whatever the
		// server sent, what comes out is digits and dots and nothing else.
		for _, r := range rel.Version {
			if (r < '0' || r > '9') && r != '.' {
				t.Fatalf("version %q would reach the dialog carrying %q", rel.Version, r)
			}
		}
		if len(rel.Notes) > maxNoteLines {
			t.Fatalf("%d notes, more than the %d the dialog shows", len(rel.Notes), maxNoteLines)
		}
		// Whatever was parsed has to be comparable, or the check can neither
		// offer an update nor stay quiet on purpose.
		Newer("0.0.1", rel.Version)
		Newer(rel.Version, "99.0.0")
	})
}

// FuzzNewer checks the comparison never panics and never contradicts itself:
// two versions cannot both be newer than each other.
func FuzzNewer(f *testing.F) {
	f.Add("0.5.4", "0.5.5")
	f.Add("v1", "1.0.0.0.1")
	f.Add("0.5.4-beta+meta", "0.5.4")
	f.Add("", "")
	f.Add("9999999999999999999999", "1")

	f.Fuzz(func(t *testing.T, a, b string) {
		if Newer(a, b) && Newer(b, a) {
			t.Fatalf("%q and %q are each newer than the other", a, b)
		}
		if Newer(a, a) {
			t.Fatalf("%q is newer than itself", a)
		}
	})
}
