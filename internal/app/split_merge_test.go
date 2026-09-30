package app

import "testing"

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
