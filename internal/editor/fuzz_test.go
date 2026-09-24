package editor

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzParseLineSpans checks the markdown span scanner. Its output drives
// TextIter offsets in the editor, so a span that runs past the end of the line,
// or starts after it ends, would apply a tag to the wrong text — or none.
func FuzzParseLineSpans(f *testing.F) {
	for _, seed := range []string{
		"# Heading", "## Sub", "#### Four", "plain text",
		"**bold** and *italic* and `code` and ~~strike~~",
		"- bullet", "> quote", "---", "***", "___",
		"a <!-- priority:high --> b", "<!-->", "<!--", "-->",
		"`unclosed", "**unclosed", "*", "**", "~~", "~",
		"￼task text", "émoji ✅ ünïcode", "",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, line string) {
		if !utf8.ValidString(line) {
			return // the buffer only ever holds valid UTF-8
		}
		n := utf8.RuneCountInString(line)
		for _, reveal := range []bool{false, true} {
			for _, sp := range parseLineSpans(line, reveal) {
				if sp.start < 0 || sp.end < 0 {
					t.Fatalf("negative span %+v for %q", sp, line)
				}
				if sp.start > n || sp.end > n {
					t.Fatalf("span %+v past end (%d runes) for %q", sp, n, line)
				}
				if sp.tag == "" {
					t.Fatalf("empty tag in span %+v for %q", sp, line)
				}
			}
		}
	})
}

// FuzzBulletPrefix pins the list-marker scanner: the offset it returns is used
// to slice the line.
func FuzzBulletPrefix(f *testing.F) {
	f.Add("- item")
	f.Add("  * item")
	f.Add("- [ ] task")
	f.Add("-")
	f.Add(strings.Repeat(" ", 40) + "+ x")
	f.Fuzz(func(t *testing.T, line string) {
		if !utf8.ValidString(line) {
			return
		}
		off := bulletPrefix(line)
		if off < 0 || off > len(line) {
			t.Fatalf("bulletPrefix(%q) = %d, out of range for %d bytes", line, off, len(line))
		}
		if off > 0 && !utf8.ValidString(line[off:]) {
			t.Fatalf("bulletPrefix(%q) = %d split a character", line, off)
		}
	})
}
