package editor

import (
	"os"
	"strings"
	"unicode/utf8"
)

// hideMarkers is whether markdown markers are hidden outright rather than
// dimmed. See the note in parseLineSpans.
var hideMarkers = os.Getenv("ATLAS_NO_HIDE") == ""

// span is a tag application over a character range within a single line.
type span struct {
	tag   string
	start int // inclusive character offset within the line
	end   int // exclusive character offset within the line
}

// caretKey says which constructs the caret reveals on its line: the line
// itself, the byte range of the code, emphasis or strike construct it is in, the
// byte range of the link it is in, and whether it is in a heading's or quote's
// prefix. A construct always ends past its start, so the zero range means none.
//
// Two carets with equal keys leave the line looking the same, which is what lets
// a caret move within a line skip the render pass (see caretPassNeeded).
type caretKey struct {
	line   int
	inline [2]int
	link   [2]int
	prefix bool
}

// parseLineSpans returns the formatting spans for one markdown line. caret is
// the caret's character offset within the line, or -1 when it is not on this
// line. A marker is left visible only while the caret is in the construct it
// belongs to, its markers included, so "**bold** and `code`" shows the stars
// with the caret in the first and the backticks with it in the second. A
// heading's "#" and a quote's ">" show while the caret is in or right after that
// prefix. Every other marker is dimmed or hidden so the rendered text reads
// cleanly. A task's trailing metadata comment is hidden whatever the caret does:
// it is not text anyone edits, and snapCaret keeps the caret out of it.
//
// The scan runs over bytes. Every marker markdown uses is ASCII, and a UTF-8
// continuation byte can never be mistaken for one, so a byte scan is exact —
// and it avoids converting each line to a rune slice on every render pass,
// which was the largest allocation in the editor's hot path. Offsets are
// converted to characters at the end, and only when the line actually contains
// multi-byte text.
func parseLineSpans(line string, caret int) []span {
	return parseLine(line, caret, nil)
}

// parseLine is parseLineSpans, and also fills in key (when it is not nil) with
// the constructs the caret is in.
func parseLine(line string, caret int, key *caretKey) []span {
	n := len(line)
	if n == 0 {
		return nil
	}
	cb := caretByte(line, caret)

	var spans []span
	add := func(tag string, s, e int) { spans = append(spans, span{tag, s, e}) }
	// hide covers a marker unless shown says its construct has the caret.
	hide := func(s, e int, shown bool) {
		if shown {
			return
		}
		// Markers are normally hidden outright. GTK's invisible-text support
		// is documented as incomplete, and hit-testing a line that carries it
		// converts a layout byte offset back to a buffer position — the call
		// that has been aborting this app with "byte index off the end of the
		// line" while selecting text. ATLAS_NO_HIDE dims the markers instead
		// of hiding them, which keeps the buffer's visible length equal to its
		// real length and takes that machinery out of the picture.
		if hideMarkers {
			add("invisible", s, e)
			return
		}
		add("marker", s, e)
	}
	// dim leaves a marker visible but recessive. It is used for list bullets, where
	// hiding the character would change the text's shape. A shown quote bar needs
	// no dimming: the quote's own gray already covers it.
	dim := func(s, e int) { add("marker", s, e) }
	// inside says whether the caret is in the inline construct spanning bytes s to
	// e, or right against either end of it.
	inside := func(s, e int) bool {
		if cb < s || cb > e {
			return false
		}
		if key != nil {
			key.inline = [2]int{s, e}
		}
		return true
	}
	// inPrefix says whether the caret is in a block prefix of that many
	// characters, or right after it. The prefixes are ASCII, so characters and
	// bytes agree.
	inPrefix := func(prefix int) bool {
		if caret < 0 || caret > prefix {
			return false
		}
		if key != nil {
			key.prefix = true
		}
		return true
	}

	body := 0 // byte offset where the line's prose starts (after block markers)

	switch {
	case strings.HasPrefix(line, "#### "):
		add("heading4", 0, n)
		hide(0, 5, inPrefix(5))
		body = 5
	case strings.HasPrefix(line, "### "):
		add("heading3", 0, n)
		hide(0, 4, inPrefix(4))
		body = 4
	case strings.HasPrefix(line, "## "):
		add("heading2", 0, n)
		hide(0, 3, inPrefix(3))
		body = 3
	case strings.HasPrefix(line, "# "):
		add("heading1", 0, n)
		hide(0, 2, inPrefix(2))
		body = 2
	case isDivider(line):
		add("divider", 0, n)
		return charSpans(line, spans)
	case strings.HasPrefix(line, "> "):
		add("quote", 0, n)
		hide(0, 2, inPrefix(2))
		body = 2
	default:
		if m := bulletPrefix(line); m > 0 {
			add("listitem", 0, n)
			dim(m-2, m-1) // the "-"/"*" itself
			body = m
		} else if m := numberPrefix(line); m > 0 {
			// The number stays as it is: it is the item's label, not markup.
			add("listitem", 0, n)
			body = m
		}
	}

	// Hide trailing HTML-comment metadata (e.g. checklist priority/due).
	if b := strings.Index(line, "<!--"); b >= 0 {
		if rel := strings.Index(line[b:], "-->"); rel >= 0 {
			hide(b, b+rel+len("-->"), false)
			n = b // don't scan inline markup inside the metadata comment
		}
	}

	// Prose is the common case: a line with no inline marker at all needs no
	// character-by-character scan. IndexAny is a vectorized search, so this is
	// far cheaper than running the state machine over the line.
	if !strings.ContainsAny(line[body:n], "*`~") {
		return charSpans(line, spans)
	}

	i := body
	for i < n {
		switch {
		case line[i] == '`':
			if j := indexByteFrom(line, '`', i+1, n); j > i {
				shown := inside(i, j+1)
				add("code", i+1, j)
				hide(i, i+1, shown)
				hide(j, j+1, shown)
				i = j + 1
				continue
			}
		case line[i] == '~' && i+1 < n && line[i+1] == '~':
			if j := indexDouble(line, '~', i+2, n); j >= 0 {
				shown := inside(i, j+2)
				add("strike", i+2, j)
				hide(i, i+2, shown)
				hide(j, j+2, shown)
				i = j + 2
				continue
			}
		case line[i] == '*' && i+1 < n && line[i+1] == '*':
			if j := indexDouble(line, '*', i+2, n); j >= 0 {
				shown := inside(i, j+2)
				add("bold", i+2, j)
				hide(i, i+2, shown)
				hide(j, j+2, shown)
				i = j + 2
				continue
			}
		case line[i] == '*':
			if j := indexSingleStar(line, i+1, n); j > i {
				shown := inside(i, j+1)
				add("italic", i+1, j)
				hide(i, i+1, shown)
				hide(j, j+1, shown)
				i = j + 1
				continue
			}
		}
		i++
	}
	return charSpans(line, spans)
}

// caretByte converts the caret's character offset in line to a byte offset. A
// caret before the line (-1, meaning it is elsewhere) stays -1, and one past the
// end is taken to be at the end. A line of pure ASCII, nearly all of them, needs
// no conversion.
func caretByte(line string, caret int) int {
	if caret <= 0 {
		return max(caret, -1)
	}
	if isASCII(line) {
		return caret
	}
	if b, ok := byteOffset(line, caret); ok {
		return b
	}
	return len(line)
}

// snapCaret moves a caret that is inside or after a line's trailing metadata
// comment to just before it, and before the spaces that lead up to it. The
// comment is hidden, so a caret past it would be typing into text nobody can
// see, and the end of a task line is exactly where a caret lands when the line
// is clicked or arrowed into. The caret and the result are character offsets.
//
// A comment with text after it is not task metadata and does not trap the
// caret: it could never reach that text otherwise.
func snapCaret(line string, caret int) int {
	b := strings.Index(line, "<!--")
	if b < 0 {
		return caret
	}
	rel := strings.Index(line[b:], "-->")
	if rel < 0 || strings.TrimSpace(line[b+rel+len("-->"):]) != "" {
		return caret
	}
	stop := utf8.RuneCountInString(strings.TrimRight(line[:b], " \t"))
	if caret > stop {
		return stop
	}
	return caret
}

// charSpans converts byte offsets to character offsets, which is what the text
// buffer's iterators use. A line of pure ASCII — nearly all of them — needs no
// conversion at all.
func charSpans(line string, spans []span) []span {
	if len(spans) == 0 || isASCII(line) {
		return spans
	}
	for i := range spans {
		spans[i].start = utf8.RuneCountInString(line[:clamp(spans[i].start, len(line))])
		spans[i].end = utf8.RuneCountInString(line[:clamp(spans[i].end, len(line))])
	}
	return spans
}

// isASCII reports whether s is free of multi-byte characters.
func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// bulletPrefix returns the byte offset just past a "- " / "* " / "+ " list
// marker (including its indentation), or 0 when the line isn't a list item.
// Task lines ("- [ ] ") are excluded: they render as checkbox widgets.
func bulletPrefix(line string) int {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	if i >= len(line) || (line[i] != '-' && line[i] != '*' && line[i] != '+') {
		return 0
	}
	if i+1 >= len(line) || line[i+1] != ' ' {
		return 0
	}
	if i+2 < len(line) && line[i+2] == '[' {
		return 0 // a task line
	}
	return i + 2
}

// numberPrefix returns the byte offset just past a "1. " or "12) " list marker
// (including its indentation), or 0 when the line isn't a numbered item. The
// space after the dot is required, so "1.5 is a number" is prose, and the digits
// are capped at nine, as in CommonMark, so a long figure is not taken for a list.
func numberPrefix(line string) int {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	digits := i
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	if i == digits || i-digits > 9 {
		return 0
	}
	if i+1 >= len(line) || (line[i] != '.' && line[i] != ')') || line[i+1] != ' ' {
		return 0
	}
	return i + 2
}

// isDivider reports whether the line is a markdown horizontal rule.
func isDivider(line string) bool {
	t := strings.TrimSpace(line)
	if len(t) < 3 {
		return false
	}
	c := t[0]
	if c != '-' && c != '*' && c != '_' {
		return false
	}
	return strings.Count(t, string(c)) == len(t)
}

func indexByteFrom(s string, ch byte, from, end int) int {
	for k := from; k < end; k++ {
		if s[k] == ch {
			return k
		}
	}
	return -1
}

// indexDouble returns the index of the first of a doubled ch ("**", "~~") at or
// after from, or -1.
func indexDouble(s string, ch byte, from, end int) int {
	for k := from; k+1 < end; k++ {
		if s[k] == ch && s[k+1] == ch {
			return k
		}
	}
	return -1
}

// indexSingleStar returns the next standalone '*' (not part of "**"), or -1.
func indexSingleStar(s string, from, end int) int {
	for k := from; k < end; k++ {
		if s[k] == '*' {
			if k+1 < end && s[k+1] == '*' {
				k++ // skip the pair
				continue
			}
			return k
		}
	}
	return -1
}
