package editor

import (
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"atlas-notes/internal/markup"
)

// hidden is the tag markers get when they are not shown, which depends on
// ATLAS_NO_HIDE.
func hidden() string {
	if hideMarkers {
		return "invisible"
	}
	return "marker"
}

// covered describes spans as a tag and the text they cover, reading the text
// out by character offset the way the buffer would. A span that is out of
// bounds panics, which is the failure a wrong offset conversion causes.
func covered(line string, spans []span) [][2]string {
	r := []rune(line)
	var out [][2]string
	for _, sp := range spans {
		out = append(out, [2]string{sp.tag, string(r[sp.start:sp.end])})
	}
	return out
}

func TestRankNotes(t *testing.T) {
	notes := []string{
		"Work/Todo", "Todo", "Todo list", "Projects/Atlas todo",
		"Archive/Old/Todos", "Meeting notes", "Notes/Todo/Ideas", "todo",
	}
	orig := append([]string(nil), notes...)

	t.Run("buckets then length then alphabet", func(t *testing.T) {
		got := rankNotes("todo", notes, 20)
		want := []string{
			// The name starts with the query: shorter first, then alphabetical
			// without regard to case, then the text as written.
			"Todo", "todo", "Todo list", "Work/Todo", "Archive/Old/Todos",
			// The name contains it.
			"Projects/Atlas todo",
			// Only the folder does.
			"Notes/Todo/Ideas",
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("rankNotes(todo) = %q, want %q", got, want)
		}
	})
	t.Run("limit", func(t *testing.T) {
		got := rankNotes("todo", notes, 3)
		want := []string{"Todo", "todo", "Todo list"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("limit 3 = %q, want %q", got, want)
		}
		if got := rankNotes("todo", notes, 0); got != nil {
			t.Errorf("limit 0 = %q, want nothing", got)
		}
		if got := rankNotes("todo", notes, -1); got != nil {
			t.Errorf("negative limit = %q, want nothing", got)
		}
	})
	t.Run("case is ignored on both sides", func(t *testing.T) {
		// Both names start with the query; the shorter one comes first.
		got := rankNotes("TODO", []string{"TODO LIST", "a/todo"}, 5)
		want := []string{"a/todo", "TODO LIST"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("rankNotes(TODO) = %q, want %q", got, want)
		}
	})
	t.Run("empty query offers everything, shortest first", func(t *testing.T) {
		got := rankNotes("", []string{"Longer name", "B", "A", "Dir/C"}, 3)
		want := []string{"A", "B", "Dir/C"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("rankNotes(empty) = %q, want %q", got, want)
		}
	})
	t.Run("no match", func(t *testing.T) {
		if got := rankNotes("zzz", notes, 8); got != nil {
			t.Errorf("rankNotes(zzz) = %q, want nothing", got)
		}
		if got := rankNotes("a", nil, 8); got != nil {
			t.Errorf("rankNotes on no notes = %q, want nothing", got)
		}
	})
	t.Run("accents", func(t *testing.T) {
		got := rankNotes("ét", []string{"Zeta", "Été", "Dir/Étude"}, 8)
		want := []string{"Été", "Dir/Étude"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("rankNotes(ét) = %q, want %q", got, want)
		}
	})
	t.Run("order does not depend on the input order", func(t *testing.T) {
		rev := append([]string(nil), notes...)
		for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
			rev[i], rev[j] = rev[j], rev[i]
		}
		if a, b := rankNotes("to", notes, 8), rankNotes("to", rev, 8); !reflect.DeepEqual(a, b) {
			t.Errorf("order changed with the input: %q vs %q", a, b)
		}
	})
	if !reflect.DeepEqual(notes, orig) {
		t.Errorf("rankNotes changed its input: %q", notes)
	}
}

func TestRankTags(t *testing.T) {
	tags := []string{"project/atlas", "project", "work", "artwork", "atlas"}
	cases := []struct {
		query string
		limit int
		want  []string
	}{
		{"work", 8, []string{"work", "artwork"}},
		{"at", 8, []string{"atlas", "project/atlas"}},
		{"pro", 8, []string{"project", "project/atlas"}},
		{"pro", 1, []string{"project"}},
		{"WORK", 8, []string{"work", "artwork"}},
		{"zzz", 8, nil},
	}
	for _, c := range cases {
		if got := rankTags(c.query, tags, c.limit); !reflect.DeepEqual(got, c.want) {
			t.Errorf("rankTags(%q, %d) = %q, want %q", c.query, c.limit, got, c.want)
		}
	}
}

func TestNoteInsert(t *testing.T) {
	notes := []string{"Todo", "Work/Todo", "Work/Plan", "Ideas/Plans", "Inbox", "inbox/Later"}
	cases := []struct {
		choice string
		want   string
	}{
		{"Work/Plan", "Plan"},      // no other note has this name: the name will do
		{"Ideas/Plans", "Plans"},   // close to another name, but not the same
		{"Work/Todo", "Work/Todo"}, // "Todo" is also a note in the root: the path
		{"Todo", "Todo"},           // and the root one is its own path
		{"Inbox", "Inbox"},         // a folder called "inbox" is not a note called that
		{"inbox/Later", "Later"},
	}
	for _, c := range cases {
		if got := noteInsert(c.choice, notes); got != c.want {
			t.Errorf("noteInsert(%q) = %q, want %q", c.choice, got, c.want)
		}
	}

	t.Run("the same name in another case is the same name", func(t *testing.T) {
		both := []string{"a/Notes", "b/notes"}
		if got := noteInsert("a/Notes", both); got != "a/Notes" {
			t.Errorf("got %q, want the path", got)
		}
	})
	t.Run("a note listed twice is not ambiguous with itself", func(t *testing.T) {
		if got := noteInsert("Work/Todo", []string{"Work/Todo", "Work/Todo"}); got != "Todo" {
			t.Errorf("got %q, want the name", got)
		}
	})
	t.Run("no notes at all", func(t *testing.T) {
		if got := noteInsert("Work/Todo", nil); got != "Todo" {
			t.Errorf("got %q, want the name", got)
		}
	})
}

func TestSplitNote(t *testing.T) {
	cases := []struct{ in, name, dir string }{
		{"Todo", "Todo", ""},
		{"Work/Todo", "Todo", "Work"},
		{"A/B/Todo", "Todo", "A/B"},
	}
	for _, c := range cases {
		if name, dir := splitNote(c.in); name != c.name || dir != c.dir {
			t.Errorf("splitNote(%q) = %q, %q; want %q, %q", c.in, name, dir, c.name, c.dir)
		}
	}
}

func TestWikiQuery(t *testing.T) {
	cases := []struct {
		before string
		want   string
		ok     bool
	}{
		{"[[", "", true},
		{"see [[Wo", "Wo", true},
		{"see [[Work/To", "Work/To", true},
		{"[[Ünï", "Ünï", true},
		{"a [[b]] and [[c", "c", true}, // the last unfinished link
		{"see [Wo", "", false},         // one bracket is not a link
		{"[[a]]", "", false},           // closed
		{"[[a]] x", "", false},
		{"[[Note#Head", "", false}, // past the name
		{"[[Note|Ali", "", false},
		{"plain text", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := wikiQuery(c.before)
		if got != c.want || ok != c.ok {
			t.Errorf("wikiQuery(%q) = %q, %v; want %q, %v", c.before, got, ok, c.want, c.ok)
		}
	}
}

func TestTagQuery(t *testing.T) {
	cases := []struct {
		before string
		want   string
		ok     bool
	}{
		{"#w", "w", true},
		{"a #work", "work", true},
		{"a #project/at", "project/at", true},
		{"(#tag", "tag", true},
		{"a #é", "é", true},
		{"#", "", false},    // nothing typed after it yet
		{"a #", "", false},  //
		{"a # ", "", false}, // a heading marker, not a tag
		{"# Title", "", false},
		{"C#", "", false}, // not the start of a word
		{"page#sec", "", false},
		{"a ##x", "", false},
		{"http://x.org/#anch", "", false},
		{"word", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := tagQuery(c.before)
		if got != c.want || ok != c.ok {
			t.Errorf("tagQuery(%q) = %q, %v; want %q, %v", c.before, got, ok, c.want, c.ok)
		}
	}
}

func TestSuggestContext(t *testing.T) {
	cases := []struct {
		before string
		kind   suggestKind
		query  string
	}{
		{"see [[Wo", suggestNotes, "Wo"},
		{"see #wo", suggestTags, "wo"},
		{"see `[[wo", suggestNone, ""}, // inside inline code
		{"see `x` [[wo", suggestNotes, "wo"},
		{"[[Note#he", suggestNone, ""}, // a heading, not a tag
		{"[[#he", suggestNone, ""},
		{"[[a]] #t", suggestTags, "t"},
		{"# Heading", suggestNone, ""},
		{"plain", suggestNone, ""},
	}
	for _, c := range cases {
		kind, q := suggestContext(c.before)
		if kind != c.kind || q != c.query {
			t.Errorf("suggestContext(%q) = %v, %q; want %v, %q", c.before, kind, q, c.kind, c.query)
		}
	}
}

// applyEdit plays a completion on a line the way the buffer would, so the
// character counts in it are checked end to end. It returns the new line and
// the caret's character offset.
func applyEdit(line string, caret int, c completion) (string, int) {
	r := []rune(line)
	out := string(r[:caret-c.before]) + c.text + string(r[caret+c.after:])
	return out, caret - c.before + utf8.RuneCountInString(c.text) + c.skip
}

func TestCompleteNote(t *testing.T) {
	notes := []string{"Todo", "Work/Todo", "Work/Plan", "Ünï/Café"}
	cases := []struct {
		name          string
		line          string // the caret is at the end of the text before "|"
		choice        string
		want          string
		wantCaret     int
		wantCompleted bool
	}{
		{"closes the link", "see [[Pl|", "Work/Plan", "see [[Plan]]", 12, true},
		{"empty query", "[[|", "Work/Plan", "[[Plan]]", 8, true},
		{"already closed", "see [[Pl|]] and more", "Work/Plan", "see [[Plan]] and more", 12, true},
		{"ambiguous names get the path", "[[To|", "Work/Todo", "[[Work/Todo]]", 13, true},
		{"non-ASCII text around", "é 😀 [[Ca|", "Ünï/Café", "é 😀 [[Café]]", 12, true},
		{"not in a link", "see Pl|", "Work/Plan", "", 0, false},

		// The caret inside a link that is already written.
		{"replaces the rest of the name", "see [[Wo|rk plan]] end", "Work/Plan", "see [[Plan]] end", 12, true},
		{"rest of the name, closing brackets stay", "[[Pl|an]]", "Work/Plan", "[[Plan]]", 8, true},
		{"stops at an alias", "[[Pl|an|shown]]", "Work/Plan", "[[Plan|shown]]", 6, true},
		{"stops at a heading", "[[Pl|an#Intro]]", "Work/Plan", "[[Plan#Intro]]", 6, true},
		{"alias right after the caret", "[[Pl||shown]]", "Work/Plan", "[[Plan|shown]]", 6, true},
		{"heading right after the caret", "[[Pl|#Intro]]", "Work/Plan", "[[Plan#Intro]]", 6, true},
		{"non-ASCII rest of the name", "[[Ca|fé x]] y", "Ünï/Café", "[[Café]] y", 8, true},
		{"a link that follows is left alone", "[[Pl| and [[Other]]", "Work/Plan", "[[Plan]] and [[Other]]", 8, true},
		{"open link, text after is kept", "see [[Pl| for details", "Work/Plan", "see [[Plan]] for details", 12, true},
		{"open link at the end", "[[Pl|an", "Work/Plan", "[[Plan]]an", 8, true},
	}
	for _, c := range cases {
		before, after, _ := strings.Cut(c.line, "|")
		comp, ok := completeNote(before, after, c.choice, notes)
		if ok != c.wantCompleted {
			t.Errorf("%s: ok = %v", c.name, ok)
			continue
		}
		if !ok {
			continue
		}
		got, caret := applyEdit(before+after, utf8.RuneCountInString(before), comp)
		if got != c.want || caret != c.wantCaret {
			t.Errorf("%s: got %q caret %d, want %q caret %d", c.name, got, caret, c.want, c.wantCaret)
		}
	}
}

func TestCompleteTag(t *testing.T) {
	cases := []struct {
		name      string
		line      string
		choice    string
		want      string
		wantCaret int
		ok        bool
	}{
		{"adds a space", "a #pro|", "project", "a #project ", 11, true},
		{"finishes the word", "a #pro|ject", "project", "a #project ", 11, true},
		{"finishes the word and keeps what follows", "a #pro|ject rest", "project", "a #project rest", 11, true},
		{"a space is already there", "a #pro| rest", "project", "a #project rest", 11, true},
		{"nested tag", "#pro|", "project/atlas", "#project/atlas ", 15, true},
		{"non-ASCII", "é #ét|", "étude", "é #étude ", 9, true},
		{"not a tag", "C#pro|", "project", "", 0, false},
	}
	for _, c := range cases {
		before, after, _ := strings.Cut(c.line, "|")
		comp, ok := completeTag(before, after, c.choice)
		if ok != c.ok {
			t.Errorf("%s: ok = %v", c.name, ok)
			continue
		}
		if !ok {
			continue
		}
		got, caret := applyEdit(before+after, utf8.RuneCountInString(before), comp)
		if got != c.want || caret != c.wantCaret {
			t.Errorf("%s: got %q caret %d, want %q caret %d", c.name, got, caret, c.want, c.wantCaret)
		}
	}
}

func TestByteOffset(t *testing.T) {
	line := "aé😀b" // 1 + 2 + 4 + 1 bytes, 4 characters
	cases := []struct {
		chars int
		want  int
		ok    bool
	}{
		{0, 0, true}, {1, 1, true}, {2, 3, true}, {3, 7, true},
		{4, 8, true}, // the end of the line
		{5, 0, false}, {-1, 0, false},
	}
	for _, c := range cases {
		got, ok := byteOffset(line, c.chars)
		if got != c.want || ok != c.ok {
			t.Errorf("byteOffset(%q, %d) = %d, %v; want %d, %v", line, c.chars, got, ok, c.want, c.ok)
		}
	}
	if b, ok := byteOffset("", 0); b != 0 || !ok {
		t.Errorf("byteOffset of an empty line at 0 = %d, %v", b, ok)
	}
}

func TestLinkSpans(t *testing.T) {
	h := hidden()
	cases := []struct {
		name   string
		line   string
		reveal bool
		want   [][2]string
	}{
		{"wiki link and tag after accents and emoji", "é [[Note]] 😀 #tag", false, [][2]string{
			{"wikilink", "[[Note]]"}, {h, "[["}, {h, "]]"}, {"hashtag", "#tag"},
		}},
		{"revealed keeps the brackets", "é [[Note]] 😀 #tag", true, [][2]string{
			{"wikilink", "[[Note]]"}, {"hashtag", "#tag"},
		}},
		{"alias hides the target and heading", "[[Work/Todo#Top|the list]] é", false, [][2]string{
			{"wikilink", "[[Work/Todo#Top|the list]]"}, {h, "[["}, {h, "]]"}, {h, "Work/Todo#Top|"},
		}},
		{"no alias shows the heading", "[[Todo#Top]]", false, [][2]string{
			{"wikilink", "[[Todo#Top]]"}, {h, "[["}, {h, "]]"},
		}},
		{"an empty alias does not hide the target", "[[Todo|]]", false, [][2]string{
			{"wikilink", "[[Todo|]]"}, {h, "[["}, {h, "]]"},
		}},
		{"markdown link shows its text", "é [text](https://x.org/é) 😀", false, [][2]string{
			{"url", "text"}, {h, "["}, {h, "](https://x.org/é)"},
		}},
		{"markdown link, revealed", "é [text](https://x.org/é) 😀", true, [][2]string{
			{"url", "text"},
		}},
		{"a link with no text is left as written", "[](https://x.org)", false, [][2]string{
			{"url", "[](https://x.org)"},
		}},
		{"bare URL keeps its sentence out", "é see https://example.com/a?b=1. 😀", false, [][2]string{
			{"url", "https://example.com/a?b=1"},
		}},
		{"embedded note leaves the bang", "![[Note]] x", false, [][2]string{
			{"wikilink", "[[Note]]"}, {h, "[["}, {h, "]]"},
		}},
		{"images stay plain", "![[pic.png]] ![alt](pic.png) é", false, nil},
		{"code stays plain", "`[[x]]` and `#y` then #z", false, [][2]string{{"hashtag", "#z"}}},
		{"headings are not tags", "## Heading", false, nil},
		{"no links", "plain text é 😀", false, nil},
		{"empty", "", false, nil},

		// Lines that start with the character standing in for a checkbox.
		{"anchor then tag", anchorChar + "#work today", false, [][2]string{{"hashtag", "#work"}}},
		{"anchor then wiki link", anchorChar + "call [[Ünï]] é", false, [][2]string{
			{"wikilink", "[[Ünï]]"}, {h, "[["}, {h, "]]"},
		}},
		{"anchor, accents, markdown link", anchorChar + "café [text](https://x.org/é) 😀", false, [][2]string{
			{"url", "text"}, {h, "["}, {h, "](https://x.org/é)"},
		}},
		{"task metadata is not scanned", anchorChar + "task #a <!-- p:high #b [[c]] -->", false, [][2]string{
			{"hashtag", "#a"},
		}},
	}
	for _, c := range cases {
		spans := linkSpans(c.line, c.reveal)
		if got := covered(c.line, spans); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: linkSpans(%q, %v) covers %q, want %q", c.name, c.line, c.reveal, got, c.want)
		}
	}

	t.Run("offsets are characters, not bytes", func(t *testing.T) {
		// "é" is two bytes and one character; a byte offset would be 1 too far.
		if got := linkSpans("é[[a]]", true); !reflect.DeepEqual(got, []span{{"wikilink", 1, 6}}) {
			t.Errorf("é[[a]] = %v", got)
		}
		// The anchor is three bytes and one character, and it counts in the
		// buffer as one.
		if got := linkSpans(anchorChar+"é #t", true); !reflect.DeepEqual(got, []span{{"hashtag", 3, 5}}) {
			t.Errorf("anchor line = %v", got)
		}
		if got := linkSpans("😀 #t", true); !reflect.DeepEqual(got, []span{{"hashtag", 2, 4}}) {
			t.Errorf("emoji line = %v", got)
		}
	})
}

func TestLinkSpansAllocateNothingWithoutLinks(t *testing.T) {
	lines := []string{
		"just some plain prose, nothing to see here",
		"café au lait, 😀 and more",
		anchorChar + "buy milk",
		"",
	}
	for _, line := range lines {
		if n := testing.AllocsPerRun(50, func() { _ = linkSpans(line, false) }); n != 0 {
			t.Errorf("linkSpans(%q) allocates %v times", line, n)
		}
	}
}

func TestSpanAt(t *testing.T) {
	// Every offset here counts characters, and the line has accents and an emoji
	// ahead of the links, so a byte offset would land in the wrong place.
	line := "é😀 [[Work/Todo#Top|list]] #tag, https://x.org/é [text](https://y.org) end"
	pos := func(s string) int { return utf8.RuneCountInString(line[:strings.Index(line, s)]) }
	size := func(s string) int { return utf8.RuneCountInString(s) }

	type want struct {
		kind    markup.Kind
		target  string
		heading string
	}
	cases := []struct {
		at   string // the piece the offset is measured from
		size int
		want want
	}{
		{"[[Work/Todo#Top|list]]", size("[[Work/Todo#Top|list]]"), want{markup.KindWikiLink, "Work/Todo", "Top"}},
		{"#tag", size("#tag"), want{markup.KindTag, "tag", ""}},
		{"https://x.org/é", size("https://x.org/é"), want{markup.KindURL, "https://x.org/é", ""}},
		{"[text](https://y.org)", size("[text](https://y.org)"), want{markup.KindURL, "https://y.org", ""}},
	}
	for _, c := range cases {
		start := pos(c.at)
		for _, off := range []int{start, start + 1, start + c.size/2, start + c.size - 1} {
			sp, ok := spanAt(line, off)
			if !ok || sp.Kind != c.want.kind || sp.Target != c.want.target || sp.Heading != c.want.heading {
				t.Errorf("offset %d inside %q: got %+v, %v", off, c.at, sp, ok)
			}
		}
		// The character before the first and the one after the last belong to
		// something else.
		if sp, ok := spanAt(line, start-1); ok && sp.Target == c.want.target {
			t.Errorf("offset %d, before %q, still hits it", start-1, c.at)
		}
		if sp, ok := spanAt(line, start+c.size); ok && sp.Target == c.want.target {
			t.Errorf("offset %d, after %q, still hits it", start+c.size, c.at)
		}
	}

	t.Run("plain text hits nothing", func(t *testing.T) {
		for _, at := range []string{"é😀", " end", "end"} {
			if sp, ok := spanAt(line, pos(at)); ok {
				t.Errorf("offset in %q hit %+v", at, sp)
			}
		}
	})
	t.Run("out of range", func(t *testing.T) {
		n := utf8.RuneCountInString(line)
		for _, off := range []int{-1, n, n + 1, 1000} {
			if sp, ok := spanAt(line, off); ok {
				t.Errorf("offset %d hit %+v", off, sp)
			}
		}
		if _, ok := spanAt("", 0); ok {
			t.Error("an empty line hit something")
		}
	})
	t.Run("a link ending the line owns its last character only", func(t *testing.T) {
		l := "é [[A]]"
		if _, ok := spanAt(l, 6); !ok {
			t.Error("the last character of the link is not a hit")
		}
		if _, ok := spanAt(l, 7); ok {
			t.Error("the end of the line is a hit")
		}
	})
	t.Run("neighbours split at the boundary", func(t *testing.T) {
		l := "[[A]][[B]]"
		for off, want := range map[int]string{0: "A", 4: "A", 5: "B", 9: "B"} {
			if sp, ok := spanAt(l, off); !ok || sp.Target != want {
				t.Errorf("offset %d = %+v, %v; want %s", off, sp, ok, want)
			}
		}
	})
	t.Run("images are not links", func(t *testing.T) {
		l := "![alt](p.png) ![[pic.png]] x"
		for off := 0; off < utf8.RuneCountInString(l); off++ {
			if sp, ok := spanAt(l, off); ok {
				t.Errorf("offset %d in an image hit %+v", off, sp)
			}
		}
	})
	t.Run("an embedded note is a link, without its bang", func(t *testing.T) {
		l := "![[Note]] x"
		if _, ok := spanAt(l, 0); ok {
			t.Error("the bang is a hit")
		}
		if sp, ok := spanAt(l, 1); !ok || sp.Target != "Note" {
			t.Errorf("offset 1 = %+v, %v", sp, ok)
		}
	})
	t.Run("the anchor at the start of a task line", func(t *testing.T) {
		l := anchorChar + "#work é [[Ünï]]"
		if _, ok := spanAt(l, 0); ok {
			t.Error("the checkbox is a hit")
		}
		if sp, ok := spanAt(l, 1); !ok || sp.Target != "work" {
			t.Errorf("offset 1 = %+v, %v", sp, ok)
		}
		if sp, ok := spanAt(l, 9); !ok || sp.Target != "Ünï" {
			t.Errorf("offset 9 = %+v, %v", sp, ok)
		}
	})
}

// TestLinkSpansStayInBounds throws lines made of the characters that matter to
// the parser at both functions. Spans must sit inside the line, in characters,
// and hitting or converting anything must not panic: an offset past the end of
// a line is what makes GTK abort.
func TestLinkSpansStayInBounds(t *testing.T) {
	pieces := []string{
		"[", "]", "[[", "]]", "(", ")", "!", "#", "|", "`", "http://", "https://x.org",
		" ", "a", "Z", "1", "é", "😀", anchorChar, "<!--", "-->", "/", ".", ",", "\t",
	}
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 3000; i++ {
		var b strings.Builder
		for n := rng.Intn(14); n >= 0; n-- {
			b.WriteString(pieces[rng.Intn(len(pieces))])
		}
		line := b.String()
		chars := utf8.RuneCountInString(line)
		for _, reveal := range []bool{false, true} {
			for _, sp := range linkSpans(line, reveal) {
				if sp.start < 0 || sp.start > sp.end || sp.end > chars {
					t.Fatalf("linkSpans(%q, %v): %+v is outside 0..%d", line, reveal, sp, chars)
				}
				if reveal && (sp.tag == "invisible" || sp.tag == "marker") {
					t.Fatalf("linkSpans(%q, true) hides %+v", line, sp)
				}
			}
		}
		for off := -1; off <= chars+1; off++ {
			spanAt(line, off)
		}
	}
}
