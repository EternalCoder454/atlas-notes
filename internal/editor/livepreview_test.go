package editor

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"atlas-notes/internal/markup"
)

// isHide says whether a span is one that hides (or dims, under ATLAS_NO_HIDE) a
// marker, as opposed to styling text.
func isHide(sp span) bool { return sp.tag == "invisible" || sp.tag == "marker" }

// hiddenText lists the text of the spans that hide markers, in order, reading it
// out by character offset the way the buffer would.
func hiddenText(line string, spans []span) []string {
	r := []rune(line)
	var out []string
	for _, sp := range spans {
		if sp.tag == hidden() {
			out = append(out, string(r[sp.start:sp.end]))
		}
	}
	return out
}

// onlyUnhides reports whether got is base with some of its hiding spans taken
// away: a caret shows markers and does nothing else, so it never adds, moves or
// restyles a span.
func onlyUnhides(base, got []span) bool {
	next := 0
	for _, b := range base {
		if next < len(got) && got[next] == b {
			next++
		} else if !isHide(b) {
			return false
		}
	}
	return next == len(got)
}

func TestInlineMarkersFollowTheCaret(t *testing.T) {
	// The line is "**bold** and `code`": the bold construct covers characters 0
	// to 8, the code construct 13 to 19, and the stretch between them is prose.
	const line = "**bold** and `code`"
	n := utf8.RuneCountInString(line)
	both := []string{"**", "**", "`", "`"}
	cases := []struct {
		caret int
		want  []string // the markers left hidden
	}{
		{-1, both},
		{0, []string{"`", "`"}},
		{3, []string{"`", "`"}},
		{8, []string{"`", "`"}}, // right after the closing stars is still the bold
		{9, both},
		{12, both}, // right before the backtick is not yet in the code
		{13, []string{"**", "**"}},
		{16, []string{"**", "**"}},
		{18, []string{"**", "**"}},
		{n, []string{"**", "**"}}, // the end of the line is right after the code
	}
	for _, c := range cases {
		got := hiddenText(line, parseLineSpans(line, c.caret))
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("caret %d hides %q, want %q", c.caret, got, c.want)
		}
	}
}

func TestEveryInlineConstruct(t *testing.T) {
	// Each construct shows its own markers, and only those, with the caret inside.
	cases := []struct {
		line  string
		caret int
		want  []string
	}{
		{"a *it* b", 3, nil},
		{"a *it* b", 0, []string{"*", "*"}},
		{"a ~~gone~~ b", 6, nil},
		{"a ~~gone~~ b", 12, []string{"~~", "~~"}},
		{"`a` and `b`", 1, []string{"`", "`"}},
		{"`a` and `b`", 10, []string{"`", "`"}},
		{"`a` and *b*", 10, []string{"`", "`"}},
		// Non-ASCII text before the construct: the caret counts characters.
		{"é **b** ü", 2, nil},
		{"é **b** ü", 1, []string{"**", "**"}},
		{"é **b** ü", 7, nil},
		{"é **b** ü", 8, []string{"**", "**"}},
		{"😀 `c`", 2, nil},
		{"😀 `c`", 1, []string{"`", "`"}},
	}
	for _, c := range cases {
		got := hiddenText(c.line, parseLineSpans(c.line, c.caret))
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("parseLineSpans(%q, %d) hides %q, want %q", c.line, c.caret, got, c.want)
		}
	}
}

func TestBlockPrefixesFollowTheCaret(t *testing.T) {
	cases := []struct {
		line  string
		caret int
		want  []string
	}{
		{"# Title", -1, []string{"# "}},
		{"# Title", 0, nil},
		{"# Title", 2, nil}, // right after the prefix
		{"# Title", 3, []string{"# "}},
		{"# Title", 5, []string{"# "}},
		{"## Title", 3, nil},
		{"## Title", 4, []string{"## "}},
		{"#### Deep", 5, nil},
		{"#### Deep", 6, []string{"#### "}},
		// A quote's ">" is never hidden: it is the quote's bar, dimmed away
		// from the caret (checked below).
		{"> Quote", -1, nil},
		{"> Quote", 1, nil},
		{"> Quote", 4, nil},
		// A heading's inline markers are the caret's business, not the prefix's.
		{"# A **b** c", 6, []string{"# "}},
		{"# A **b** c", 2, []string{"**", "**"}},
	}
	for _, c := range cases {
		got := hiddenText(c.line, parseLineSpans(c.line, c.caret))
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("parseLineSpans(%q, %d) hides %q, want %q", c.line, c.caret, got, c.want)
		}
	}
	// The quote bar is dimmed away from the caret, and plain beside it.
	dimmed := func(caret int) bool {
		for _, sp := range parseLineSpans("> Quote", caret) {
			if sp.tag == "marker" && sp.start == 0 && sp.end == 1 {
				return true
			}
		}
		return false
	}
	if !dimmed(-1) || !dimmed(4) || dimmed(1) {
		t.Errorf("quote bar: dimmed at -1 %v, at 4 %v, at 1 %v; want true, true, false", dimmed(-1), dimmed(4), dimmed(1))
	}
	// List bullets stay visible wherever the caret is: dimmed, never hidden.
	for _, caret := range []int{-1, 0, 2, 5} {
		for _, sp := range parseLineSpans("- item", caret) {
			if sp.tag == "invisible" {
				t.Errorf("caret %d hides a list bullet: %v", caret, sp)
			}
		}
	}
}

func TestTaskMetadataStaysHidden(t *testing.T) {
	// A task line as the buffer holds it: the checkbox's anchor, the text, and
	// the metadata comment.
	line := anchorChar + "buy **milk** <!-- p:high due:2026-01-02 -->"
	n := utf8.RuneCountInString(line)
	for caret := -1; caret <= n; caret++ {
		hiddenNow := hiddenText(line, parseLineSpans(line, caret))
		found := false
		for _, h := range hiddenNow {
			if h == "<!-- p:high due:2026-01-02 -->" {
				found = true
			}
		}
		if !found {
			t.Errorf("caret %d shows the metadata: hidden %q", caret, hiddenNow)
		}
	}
	// Nothing inside the comment is read as markup, either.
	spans := parseLineSpans("x <!-- **a** -->", -1)
	if got := hiddenText("x <!-- **a** -->", spans); !reflect.DeepEqual(got, []string{"<!-- **a** -->"}) {
		t.Errorf("hid %q", got)
	}
}

func TestSnapCaret(t *testing.T) {
	const task = "buy milk <!-- p:high -->" // "buy milk" is 8 characters
	cases := []struct {
		name  string
		line  string
		caret int
		want  int
	}{
		{"in the text", task, 3, 3},
		{"at the end of the text", task, 8, 8},
		{"after the space that leads to the comment", task, 9, 8},
		{"inside the comment", task, 14, 8},
		{"at the end of the line", task, 24, 8},
		{"several spaces before it", "buy   <!-- x -->", 8, 3},
		{"between those spaces", "buy   <!-- x -->", 4, 3},
		{"no space before it", "buy<!-- x -->", 13, 3},
		{"spaces after it", "buy <!-- x -->  ", 16, 3},
		{"only a comment", "<!-- x -->", 5, 0},
		{"characters, not bytes", "é ü <!-- x -->", 14, 3},
		{"the checkbox's anchor counts as one", anchorChar + "é <!-- x -->", 12, 2},
		{"no comment", "plain text", 5, 5},
		{"an unclosed comment is text", "a <!-- b", 8, 8},
		{"text after the comment is reachable", "a <!-- b --> c", 14, 14},
		{"an empty line", "", 0, 0},
	}
	for _, c := range cases {
		if got := snapCaret(c.line, c.caret); got != c.want {
			t.Errorf("%s: snapCaret(%q, %d) = %d, want %d", c.name, c.line, c.caret, got, c.want)
		}
	}
}

func TestNumberPrefix(t *testing.T) {
	cases := []struct {
		line string
		want int // bytes up to and including the space, 0 when not a list
	}{
		{"1. item", 3},
		{"12) item", 4},
		{" 3. indented", 4},
		{"\t4. tabbed", 4},
		{"1. ", 3},
		{"123456789. nine digits", 11},
		{"1.5 is a number", 0},
		{"1.", 0},
		{"1.item", 0},
		{"1) ", 3},
		{"a. letter", 0},
		{". dot", 0},
		{"1 . spaced", 0},
		{"1234567890. ten digits", 0},
		{"", 0},
		{"text 1. later", 0},
		{"- 1. bullet first", 0},
	}
	for _, c := range cases {
		if got := numberPrefix(c.line); got != c.want {
			t.Errorf("numberPrefix(%q) = %d, want %d", c.line, got, c.want)
		}
	}
}

func TestNumberedItemsAreListItems(t *testing.T) {
	for _, line := range []string{"1. one", "12) twelve", " 3. three"} {
		spans := parseLineSpans(line, -1)
		if len(spans) != 1 || spans[0] != (span{"listitem", 0, utf8.RuneCountInString(line)}) {
			t.Errorf("parseLineSpans(%q) = %v, want one listitem span", line, spans)
		}
	}
	if spans := parseLineSpans("1.5 is a number", -1); len(spans) != 0 {
		t.Errorf("1.5 is a number = %v, want none", spans)
	}
	// The text after the number is still read for emphasis.
	got := parseLineSpans("2. a **b**", -1)
	if len(got) < 2 || got[0].tag != "listitem" || got[1].tag != "bold" {
		t.Errorf("2. a **b** = %v", got)
	}
}

func TestFenceRoles(t *testing.T) {
	doc := []string{
		"# Title",                 // 0 ordinary
		"```go",                   // 1 opens
		"# not a heading",         // 2 body
		"**not bold** [[x]] #tag", // 3 body
		"",                        // 4 body, empty
		"~~~",                     // 5 body: the other kind of fence is only text here
		"```",                     // 6 closes
		"after **bold**",          // 7 ordinary
		"~~~",                     // 8 opens, never closed
		"text",                    // 9 body
	}
	fence := markup.InCodeFence(strings.Join(doc, "\n"))
	want := []fenceRole{
		fenceNone, fenceEdge, fenceBody, fenceBody, fenceBody, fenceBody, fenceEdge,
		fenceNone, fenceEdge, fenceBody,
	}
	for i, line := range doc {
		if got := roleOf(fence, i, line); got != want[i] {
			t.Errorf("line %d %q: role %d, want %d", i, line, got, want[i])
		}
	}

	// What each role is tagged with.
	if got := fenceSpans("```go", fenceEdge); !reflect.DeepEqual(got, []span{{"marker", 0, 5}}) {
		t.Errorf("a fence line = %v", got)
	}
	if got := fenceSpans("é **x**", fenceBody); !reflect.DeepEqual(got, []span{{"codeblock", 0, 7}}) {
		t.Errorf("a body line = %v", got)
	}
	if got := fenceSpans("", fenceBody); !reflect.DeepEqual(got, []span{{"codeblock", 0, 0}}) {
		t.Errorf("an empty body line = %v", got)
	}

	// A fence closed by tildes, and a note whose last line closes the block.
	tilde := []string{"~~~", "```", "~~~"}
	tf := markup.InCodeFence(strings.Join(tilde, "\n"))
	for i, wantRole := range []fenceRole{fenceEdge, fenceBody, fenceEdge} {
		if got := roleOf(tf, i, tilde[i]); got != wantRole {
			t.Errorf("tilde block, line %d: role %d, want %d", i, got, wantRole)
		}
	}
	// No fence at all: the state slice is nil and every line is ordinary.
	if got := roleOf(nil, 3, "```"); got != fenceNone {
		t.Errorf("role with no fences = %d", got)
	}
}

func TestFenceChanged(t *testing.T) {
	doc := func(s string) []bool { return markup.InCodeFence(s) }
	cases := []struct {
		name     string
		old, cur string
		to       int // the last line the pass re-tags anyway
		want     bool
	}{
		{"no fences before or after", "a\nb\nc", "a\nb\nc", 0, false},
		{"typing inside a block", "```\nx\n```\nz", "```\nxy\n```\nz", 1, false},
		{"a line added inside a block", "```\nx\n```\nz", "```\nx\nx\n```\nz", 2, false},
		{"a line added before any fence", "a\n```\nx\n```", "a\nb\n```\nx\n```", 1, false},
		{"a fence typed", "a\nb\nc\nd", "```\nb\nc\nd", 0, true},
		{"the closing fence removed", "```\nx\n```\nz", "```\nx\n\nz", 2, true},
		{"the opening fence removed", "```\nx\n```\nz", "\nx\n```\nz", 0, true},
		{"the last fence removed", "```\nx\n```", "\nx\n", 2, false}, // nothing lies after the pass
		{"the last fence removed, one line early", "```\nx\n```", "\nx\n", 0, true},
		{"every fence removed", "a\n```\nx\n```\ny", "a\n\nx\n\ny", 1, true},
		{"a fence pasted and never closed", "a\nb", "a\n```\nb\nc", 2, true},
		{"a whole fenced block pasted", "a\nb", "a\n```\nb\n```\nc", 3, false},
		{"a whole fenced line deleted", "```\nx\n```\nz\nw", "x\n```\nz\nw", 0, true},
	}
	for _, c := range cases {
		old, cur := doc(c.old), doc(c.cur)
		// The slices are nil for a note with no fence, as refreshFence keeps them.
		if !strings.Contains(c.old, "```") {
			old = nil
		}
		if !strings.Contains(c.cur, "```") {
			cur = nil
		}
		got := fenceChanged(old, cur, strings.Count(c.old, "\n")+1, strings.Count(c.cur, "\n")+1, c.to)
		if got != c.want {
			t.Errorf("%s: fenceChanged = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestLinkSpansFollowTheCaret(t *testing.T) {
	h := hidden()
	const line = "see [[Alpha]] and [text](https://x.org) then [[Beta|b]]"
	cases := []struct {
		name  string
		caret int
		want  [][2]string
	}{
		{"away", -1, [][2]string{
			{"wikilink", "[[Alpha]]"}, {h, "[["}, {h, "]]"},
			{"url", "text"}, {h, "["}, {h, "](https://x.org)"},
			{"wikilink", "[[Beta|b]]"}, {h, "[["}, {h, "]]"}, {h, "Beta|"},
		}},
		{"in the first link", 7, [][2]string{
			{"wikilink", "[[Alpha]]"},
			{"url", "text"}, {h, "["}, {h, "](https://x.org)"},
			{"wikilink", "[[Beta|b]]"}, {h, "[["}, {h, "]]"}, {h, "Beta|"},
		}},
		{"just before the first link", 4, [][2]string{
			{"wikilink", "[[Alpha]]"},
			{"url", "text"}, {h, "["}, {h, "](https://x.org)"},
			{"wikilink", "[[Beta|b]]"}, {h, "[["}, {h, "]]"}, {h, "Beta|"},
		}},
		{"just after the first link", 13, [][2]string{
			{"wikilink", "[[Alpha]]"},
			{"url", "text"}, {h, "["}, {h, "](https://x.org)"},
			{"wikilink", "[[Beta|b]]"}, {h, "[["}, {h, "]]"}, {h, "Beta|"},
		}},
		{"between the links", 15, [][2]string{
			{"wikilink", "[[Alpha]]"}, {h, "[["}, {h, "]]"},
			{"url", "text"}, {h, "["}, {h, "](https://x.org)"},
			{"wikilink", "[[Beta|b]]"}, {h, "[["}, {h, "]]"}, {h, "Beta|"},
		}},
		{"in the markdown link", 25, [][2]string{
			{"wikilink", "[[Alpha]]"}, {h, "[["}, {h, "]]"},
			{"url", "text"},
			{"wikilink", "[[Beta|b]]"}, {h, "[["}, {h, "]]"}, {h, "Beta|"},
		}},
		{"in the aliased link", 50, [][2]string{
			{"wikilink", "[[Alpha]]"}, {h, "[["}, {h, "]]"},
			{"url", "text"}, {h, "["}, {h, "](https://x.org)"},
			{"wikilink", "[[Beta|b]]"},
		}},
		{"at the end of the line", 55, [][2]string{
			{"wikilink", "[[Alpha]]"}, {h, "[["}, {h, "]]"},
			{"url", "text"}, {h, "["}, {h, "](https://x.org)"},
			{"wikilink", "[[Beta|b]]"},
		}},
	}
	for _, c := range cases {
		got := covered(line, linkSpans(line, c.caret))
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: linkSpans(%q, %d) covers %q, want %q", c.name, line, c.caret, got, c.want)
		}
	}

	// An embedded note's "!" is not part of the link: a caret before it is outside.
	if got := covered("![[Note]]", linkSpans("![[Note]]", 0)); !reflect.DeepEqual(got, [][2]string{
		{"wikilink", "[[Note]]"}, {h, "[["}, {h, "]]"},
	}) {
		t.Errorf("caret before the bang = %q", got)
	}
	if got := covered("![[Note]]", linkSpans("![[Note]]", 1)); !reflect.DeepEqual(got, [][2]string{
		{"wikilink", "[[Note]]"},
	}) {
		t.Errorf("caret at the brackets = %q", got)
	}
}

func TestCaretKey(t *testing.T) {
	key := func(line string, caret int) caretKey {
		k := caretKey{line: 4}
		parseLine(line, caret, &k)
		linkSpansKey(line, caret, &k)
		return k
	}
	const line = "## Head **b** and [[L]] `c`"

	// Moving inside one construct leaves the key as it was; that is what lets the
	// render pass be skipped.
	if key(line, 10) != key(line, 12) {
		t.Errorf("two carets in the bold differ: %+v and %+v", key(line, 10), key(line, 12))
	}
	if key(line, 4) != key(line, 5) {
		t.Errorf("two carets in the prose after the prefix differ")
	}
	// Entering or leaving a construct, or the prefix, changes it.
	steps := []int{-1, 0, 2, 3, 8, 10, 13, 14, 20, 24, 25, 28}
	distinct := map[caretKey]bool{}
	for _, c := range steps {
		distinct[key(line, c)] = true
	}
	// none (off the line, or in prose), the prefix, the bold, the link, the code.
	if len(distinct) != 5 {
		t.Errorf("%d distinct keys over %v, want 5", len(distinct), steps)
	}
	if k := key(line, 1); !k.prefix || k.inline != ([2]int{}) {
		t.Errorf("caret in the prefix = %+v", k)
	}
	if k := key(line, 20); k.link == ([2]int{}) {
		t.Errorf("caret in the link has no link in its key: %+v", k)
	}
	// The key is the same whichever function is asked, with or without one.
	if !reflect.DeepEqual(parseLineSpans(line, 10), parseLine(line, 10, &caretKey{})) {
		t.Errorf("parseLine with a key differs from parseLineSpans")
	}
}

func TestPlainLinesAllocateNothing(t *testing.T) {
	lines := []string{
		"just some plain prose, nothing to see here",
		"café au lait, 😀 and more",
		"",
		"a line with a # in it and 2 * 3 = 6",
	}
	for _, line := range lines {
		for _, caret := range []int{-1, 0, 4} {
			if n := testing.AllocsPerRun(50, func() { _ = parseLineSpans(line, caret) }); n != 0 {
				t.Errorf("parseLineSpans(%q, %d) allocates %v times", line, caret, n)
			}
		}
	}
}

// The Obsidian emoji form of a task's metadata is hidden on a rendered task
// line, and shown on a raw one being edited.
func TestEmojiMetadataHiddenOnRenderedTask(t *testing.T) {
	line := anchorChar + "buy milk ⏫ 📅 2026-01-02"
	var hidden []span
	for _, sp := range parseLineSpans(line, -1) {
		if sp.tag == "invisible" {
			hidden = append(hidden, sp)
		}
	}
	// Character offsets: the anchor and "buy milk" are 9, then " ⏫" and
	// " 📅 2026-01-02" follow.
	want := []span{{"invisible", 9, 11}, {"invisible", 11, 24}}
	if len(hidden) != 2 || hidden[0] != want[0] || hidden[1] != want[1] {
		t.Errorf("hidden = %+v, want %+v", hidden, want)
	}
	for _, sp := range parseLineSpans("- [ ] buy milk ⏫", -1) {
		if sp.tag == "invisible" {
			t.Errorf("a raw task line hid %+v", sp)
		}
	}
}
