package checklist

import (
	"strings"
	"testing"
)

// FuzzParseLine checks the scanner against arbitrary input. A note is just a
// text file, so any byte sequence can reach it — from a synced vault, a paste,
// or the assistant rewriting a note.
func FuzzParseLine(f *testing.F) {
	for _, seed := range []string{
		"- [ ] task", "- [x] done <!-- priority:high due:2026-01-01 order:2 -->",
		"-[ ]x", "- [", "- [ ", "- [ ]", "  \t- [X]\tx", "- [ ] \xff\xfe",
		"- [ ] a <!-- --> b <!--", "", "-", "--", "- [ ] " + strings.Repeat("x", 500),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, line string) {
		it, ok := ParseLine(line)
		if !ok {
			return
		}
		// The reported text offset must be a valid rune index into the line.
		off := TextOffset(line)
		if off < 0 || off > len([]rune(line)) {
			t.Fatalf("TextOffset(%q) = %d, out of range for %d runes", line, off, len([]rune(line)))
		}
		// Marshalling a parsed item must produce something that parses back to
		// the same item: the editor round-trips through this on every edit.
		again, ok2 := ParseLine(it.Marshal())
		if !ok2 {
			t.Fatalf("Marshal of %+v (from %q) did not parse back: %q", it, line, it.Marshal())
		}
		if again.Checked != it.Checked || again.Priority != it.Priority || again.DueDate != it.DueDate {
			t.Fatalf("round trip changed the item: %+v -> %+v (line %q)", it, again, line)
		}
	})
}

// FuzzTaskLine checks the line-level task test against arbitrary input, and
// that it agrees with the full parser about what is a task.
func FuzzTaskLine(f *testing.F) {
	f.Add("- [ ] a")
	f.Add("- [x] b")
	f.Add("text")
	f.Add("")
	f.Add(strings.Repeat("- [x] x", 50))
	f.Fuzz(func(t *testing.T, line string) {
		checked, ok := TaskLine(line)
		it, parsed := ParseLine(line)
		if ok != parsed {
			t.Fatalf("TaskLine(%q) says %v but ParseLine says %v", line, ok, parsed)
		}
		if ok && checked != it.Checked {
			t.Fatalf("TaskLine(%q) checked=%v but ParseLine says %v", line, checked, it.Checked)
		}
	})
}
