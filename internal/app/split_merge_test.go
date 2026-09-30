package app

import (
	"strings"
	"testing"
)

func TestNoteNameFromText(t *testing.T) {
	for in, want := range map[string]string{
		"# A heading\nbody":       "A heading",
		"\n\n- [ ] Buy **milk**":  "Buy milk",
		"see [[Other note]] here": "see Other note here",
		"a/b\\c":                  "a-b-c",
		"   \n\n":                 "",
		"> quoted words":          "quoted words",
		"...hidden":               "hidden",
	} {
		if got := noteNameFromText(in); got != want {
			t.Errorf("noteNameFromText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCleanNoteNameLimitsLength(t *testing.T) {
	long := ""
	for i := 0; i < 100; i++ {
		long += "x"
	}
	if got := cleanNoteName(long); len([]rune(got)) != noteNameLimit {
		t.Errorf("length %d", len([]rune(got)))
	}
}

func TestSplitReplacementKeepsTheNewlinesOfALineWiseSelection(t *testing.T) {
	for text, want := range map[string]string{
		"one\ntwo\n": "[[N]]\n",
		"\nword":     "\n[[N]]",
		"\n\nx\n\n":  "\n\n[[N]]\n\n",
		"inline":     "[[N]]",
	} {
		if got := splitReplacement(text, "[[N]]"); got != want {
			t.Errorf("splitReplacement(%q) = %q, want %q", text, got, want)
		}
	}
}

func TestCleanNoteNameDropsWhatWouldBreakALink(t *testing.T) {
	got := cleanNoteName("a#b^c[d]e|f")
	if strings.ContainsAny(got, "#^[]|") || got != "abcde-f" {
		t.Errorf("got %q", got)
	}
}
