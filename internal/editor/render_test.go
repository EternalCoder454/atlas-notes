package editor

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"atlas-notes/internal/markup"
)

func TestQuoteMarkerIsHiddenAwayFromTheCaret(t *testing.T) {
	// The bug: the "> " of a quote stayed on screen, dimmed, in every quote.
	got := parseLineSpans("> A quote", -1)
	if h := hiddenText("> A quote", got); !reflect.DeepEqual(h, []string{"> "}) {
		t.Fatalf("hidden text %q, want the quote marker", h)
	}
	// With the caret in the prefix it shows, and a quote with no space after the
	// ">" hides just the ">".
	if h := hiddenText("> A quote", parseLineSpans("> A quote", 1)); len(h) != 0 {
		t.Errorf("caret in the prefix still hides %q", h)
	}
	if h := hiddenText(">tight", parseLineSpans(">tight", -1)); !reflect.DeepEqual(h, []string{">"}) {
		t.Errorf("hidden %q, want the bare >", h)
	}
}

func TestHighlightAndFootnotes(t *testing.T) {
	const line = "a ==marked== b[^1] c"
	sp := parseLineSpans(line, -1)
	if !reflect.DeepEqual(hiddenText(line, sp), []string{"==", "==", "[^", "]"}) {
		t.Errorf("hidden %q", hiddenText(line, sp))
	}
	var hl, ref span
	for _, s := range sp {
		switch s.tag {
		case "highlight":
			hl = s
		case "footref":
			ref = s
		}
	}
	if string([]rune(line)[hl.start:hl.end]) != "marked" || string([]rune(line)[ref.start:ref.end]) != "1" {
		t.Errorf("highlight %v, footnote reference %v", hl, ref)
	}
	// The caret in the highlight shows its markers.
	if h := hiddenText(line, parseLineSpans(line, 5)); !reflect.DeepEqual(h, []string{"[^", "]"}) {
		t.Errorf("caret in highlight hides %q", h)
	}
	// Not highlights: an equals run, a space inside, a lone pair.
	for _, l := range []string{"a === b", "== spaced ==", "x == y"} {
		for _, s := range parseLineSpans(l, -1) {
			if s.tag == "highlight" {
				t.Errorf("%q drew a highlight", l)
			}
		}
	}
	if n := isFootnoteDef("[^note]: text"); n != len("[^note]: ") {
		t.Errorf("footnote definition body at %d", n)
	}
	if isFootnoteDef("[^a b]: no") != 0 || isFootnoteDef("[^1] no colon") != 0 {
		t.Error("a footnote definition was taken for something that is not one")
	}
}

func TestCalloutHead(t *testing.T) {
	cases := []struct {
		line, typ string
		fold      byte
		title     string
		ok        bool
	}{
		{"> [!note] Title", "note", 0, "Title", true},
		{"> [!Warning]- Folded one", "warning", '-', "Folded one", true},
		{"> [!tip]+", "tip", '+', "", true},
		{">[!info]  Spaced", "info", 0, "Spaced", true},
		{"> [!my-type] x", "my-type", 0, "x", true},
		{"> plain quote", "", 0, "", false},
		{"> [!] empty", "", 0, "", false},
		{"> [!note no close", "", 0, "", false},
		{"[!note] no quote", "", 0, "", false},
	}
	for _, c := range cases {
		typ, fold, off, ok := calloutHead(c.line)
		if ok != c.ok || typ != c.typ || fold != c.fold {
			t.Errorf("%q: got %q %q %v", c.line, typ, fold, ok)
			continue
		}
		if ok && strings.TrimSpace(c.line[off:]) != c.title {
			t.Errorf("%q: title %q, want %q", c.line, c.line[off:], c.title)
		}
	}
}

func TestCalloutStylesAndIcons(t *testing.T) {
	for typ, want := range map[string]string{
		"note": "note", "tip": "tip", "hint": "tip", "info": "info", "warning": "warning",
		"caution": "warning", "danger": "danger", "error": "danger", "success": "success",
		"question": "question", "quote": "quote", "example": "example", "todo": "todo",
		"unheard-of": "note", "": "note",
	} {
		if got := calloutClass(typ); got != want {
			t.Errorf("calloutClass(%q) = %q, want %q", typ, got, want)
		}
	}
	seen := map[string]bool{}
	for _, c := range []string{"note", "tip", "info", "warning", "danger", "success", "question", "quote", "example", "todo"} {
		icon := calloutIcon(c)
		if !strings.HasPrefix(icon, "atlasnotes-") || !strings.HasSuffix(icon, "-symbolic") || seen[icon] {
			t.Errorf("style %q has icon %q (missing or shared)", c, icon)
		}
		seen[icon] = true
	}
	if calloutTitle(richBlock{typ: "warning"}) != "Warning" || calloutTitle(richBlock{typ: "x", title: "T"}) != "T" {
		t.Error("callout title")
	}
}

func TestScanBlocks(t *testing.T) {
	text := "# T\n> [!note] Hi\n> body\n> more\n\n> plain\n```go\nx := 1\n> not a quote\n```\n> [!tip]- Folded\n> a"
	lines := strings.Split(text, "\n")
	got := scanBlocks(lines, markup.InCodeFence(text))
	type brief struct {
		kind        blockKind
		first, last int
		typ, lang   string
		closed      bool
	}
	var have []brief
	for _, b := range got {
		have = append(have, brief{b.kind, b.first, b.last, b.typ, b.lang, b.closed})
	}
	want := []brief{
		{kindCallout, 1, 3, "note", "", false},
		{kindQuote, 5, 5, "", "", false},
		{kindCode, 6, 9, "", "go", true},
		{kindCallout, 10, 11, "tip", "", false},
	}
	if !reflect.DeepEqual(have, want) {
		t.Errorf("blocks %+v, want %+v", have, want)
	}
	if got[3].fold != '-' || got[0].key != "> [!note] Hi" {
		t.Errorf("fold %q key %q", got[3].fold, got[0].key)
	}
}

func TestScanFolds(t *testing.T) {
	lines := strings.Split("# A\ntext\n## B\nb\n### C\nc\n## D\nd\n# E\ne", "\n")
	fold := func(names ...string) map[string]bool {
		m := map[string]bool{}
		for _, n := range names {
			m[n] = true
		}
		return m
	}
	hides, heads := scanFolds(lines, nil, fold("## B"))
	if !reflect.DeepEqual(hides, [][2]int{{3, 5}}) || !heads[2] {
		t.Errorf("## B hides %v heads %v", hides, heads)
	}
	// A folded heading swallows the folded ones under it.
	hides, _ = scanFolds(lines, nil, fold("# A", "## B"))
	if !reflect.DeepEqual(hides, [][2]int{{1, 7}}) {
		t.Errorf("# A hides %v", hides)
	}
	// A heading with nothing under it hides nothing, and "#" in code is not one.
	if h, _ := scanFolds([]string{"# A", "# B"}, nil, fold("# A")); len(h) != 0 {
		t.Errorf("empty section hid %v", h)
	}
	fence := []bool{false, true, true, true}
	if h, _ := scanFolds([]string{"# A", "```", "# c", "```"}, fence, fold("# A")); !reflect.DeepEqual(h, [][2]int{{1, 3}}) {
		t.Errorf("code hid %v", h)
	}
	if foldEnd(lines, nil, 2) != 5 || foldEnd(lines, nil, 0) != 7 || foldEnd(lines, nil, 8) != 9 {
		t.Error("foldEnd")
	}
}

func TestRichStateLookups(t *testing.T) {
	s := richState{
		blocks: []richBlock{{kind: kindCallout, first: 2, last: 4, folded: true}, {kind: kindQuote, first: 6, last: 7}},
		hides:  [][2]int{{10, 12}},
	}
	for n, want := range map[int]bool{1: false, 2: false, 3: true, 4: true, 5: false, 7: false, 10: true, 12: true, 13: false} {
		if s.foldedAt(n) != want {
			t.Errorf("foldedAt(%d) = %v", n, !want)
		}
	}
	if s.blockAt(5) != nil || s.blockAt(3) == nil || s.blockAt(7).kind != kindQuote || s.blockAt(8) != nil {
		t.Error("blockAt")
	}
}

func TestFenceLanguage(t *testing.T) {
	for line, want := range map[string]string{"```go": "go", "``` python ": "python", "```": "", "~~~sh": "sh"} {
		if got := fenceLang(line); got != want {
			t.Errorf("fenceLang(%q) = %q, want %q", line, got, want)
		}
	}
}

func TestParseEmbed(t *testing.T) {
	cases := map[string]embedRef{
		"![[Note]]":              {"Note", ""},
		"  ![[Work/Todo#Plan]] ": {"Work/Todo", "Plan"},
		"![[Note|alias]]":        {"Note", ""},
	}
	for line, want := range cases {
		if got, ok := parseEmbed(line); !ok || got != want {
			t.Errorf("parseEmbed(%q) = %+v %v", line, got, ok)
		}
	}
	for _, line := range []string{"![[pic.png]]", "text ![[Note]]", "![[Note]] text", "![[]]", "[[Note]]", "![[a]] ![[b]]"} {
		if _, ok := parseEmbed(line); ok {
			t.Errorf("parseEmbed(%q) was taken for an embed", line)
		}
	}
}

func TestSectionOf(t *testing.T) {
	text := "intro\n# One\na\n## Sub\nb\n# Two\nc"
	if got, ok := sectionOf(text, "one"); !ok || got != "a\n## Sub\nb" {
		t.Errorf("section One = %q %v", got, ok)
	}
	if got, ok := sectionOf(text, "Sub"); !ok || got != "b" {
		t.Errorf("section Sub = %q %v", got, ok)
	}
	if got, ok := sectionOf(text, ""); !ok || got != text {
		t.Error("no heading is the whole note")
	}
	if _, ok := sectionOf(text, "Missing"); ok {
		t.Error("found a heading that is not there")
	}
}

func TestEmbedMarkupIsCappedAndFlat(t *testing.T) {
	got, more := embedMarkup("# Head\nsome **bold**\n![[Other]]\n")
	if more || !strings.Contains(got, "<b>Head</b>") || !strings.Contains(got, "<b>bold</b>") {
		t.Errorf("markup %q", got)
	}
	// An embed in an embedded note comes out as a link, not as another embed.
	if strings.Contains(got, "![[") || !strings.Contains(got, "Other") {
		t.Errorf("nested embed came out as %q", got)
	}
	long := strings.Repeat("line\n", 100)
	got, more = embedMarkup(long)
	if !more || strings.Count(got, "\n")+1 != maxEmbedLines {
		t.Errorf("long note: %d lines, more=%v", strings.Count(got, "\n")+1, more)
	}
	if n := utf8.RuneCountInString(got); n > maxEmbedChars+100 {
		t.Errorf("markup is %d characters", n)
	}
}
