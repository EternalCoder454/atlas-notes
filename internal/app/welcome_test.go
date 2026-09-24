package app

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestSummarizeOpeningHidesMetadata: a task line carries its priority and due
// date in an HTML comment. The home screen shows the opening of a note, and
// showing that comment means showing markup the reader never typed.
func TestSummarizeOpeningHidesMetadata(t *testing.T) {
	note := "# Trip\n\nFlights are booked.\n\n" +
		"- [ ] Renew the travel insurance <!-- priority:high due:2026-10-05 -->\n" +
		"- [x] Sort out the dog <!-- priority:high -->\n"
	got := summarizeOpening(note)
	for _, bad := range []string{"<!--", "-->", "priority:", "due:", "2026-10-05"} {
		if strings.Contains(got, bad) {
			t.Errorf("preview leaks %q: %q", bad, got)
		}
	}
	if !strings.Contains(got, "Renew the travel insurance") {
		t.Errorf("preview lost the text around the metadata: %q", got)
	}
	// An unterminated comment must not leave its contents visible either.
	if open := summarizeOpening("# T\n\nvisible <!-- hidden and unclosed\n"); strings.Contains(open, "hidden") {
		t.Errorf("an unclosed comment leaked: %q", open)
	}
}

// TestSummarizeOpeningIsValidUTF8: the preview is cut to a fixed length, and
// cutting mid-character renders as a replacement glyph. The separator between
// lines is itself multi-byte, so this is easy to hit.
func TestSummarizeOpeningIsValidUTF8(t *testing.T) {
	for _, note := range []string{
		"# N\n\n" + strings.Repeat("alpha beta gamma\n\n", 40),
		"# N\n\n" + strings.Repeat("- one\n- two\n- three\n", 40),
		"# N\n\n" + strings.Repeat("héllo wörld ünïcode\n\n", 40),
		"# N\n\n" + strings.Repeat("🔐 emoji line here\n\n", 40),
	} {
		got := summarizeOpening(note)
		if !utf8.ValidString(got) {
			t.Errorf("preview is not valid UTF-8: %q", got)
		}
		if strings.ContainsRune(got, '�') {
			t.Errorf("preview contains a replacement character: %q", got)
		}
	}
}

func TestSummarizeOpeningSkipsHeadingsAndRules(t *testing.T) {
	got := summarizeOpening("# Title\n\n## Section\n\n---\n\nThe actual prose.\n")
	if strings.Contains(got, "Title") || strings.Contains(got, "Section") {
		t.Errorf("preview repeats the headings: %q", got)
	}
	if strings.Contains(got, "---") {
		t.Errorf("preview shows a horizontal rule: %q", got)
	}
	if got != "The actual prose." {
		t.Errorf("preview = %q, want just the prose", got)
	}
}

func TestSummarizeOpeningEmpty(t *testing.T) {
	for _, note := range []string{"", "# Only a title\n", "---\n\n---\n", "\n\n\n"} {
		if got := summarizeOpening(note); got != "" {
			t.Errorf("summarizeOpening(%q) = %q, want empty", note, got)
		}
	}
}
