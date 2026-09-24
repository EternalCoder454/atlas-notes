package editor

import (
	"strings"
	"unicode/utf8"
)

// span is a tag application over a character range within a single line.
type span struct {
	tag   string
	start int // inclusive character offset within the line
	end   int // exclusive character offset within the line
}

// parseLineSpans returns the formatting spans for one markdown line. When reveal
// is true (the cursor is on this line) the markdown markers are left visible;
// otherwise they are dimmed or hidden so the rendered text reads cleanly.
//
// The scan runs over bytes. Every marker markdown uses is ASCII, and a UTF-8
// continuation byte can never be mistaken for one, so a byte scan is exact —
// and it avoids converting each line to a rune slice on every render pass,
// which was the largest allocation in the editor's hot path. Offsets are
// converted to characters at the end, and only when the line actually contains
// multi-byte text.
func parseLineSpans(line string, reveal bool) []span {
	n := len(line)
	if n == 0 {
		return nil
	}

	var spans []span
	add := func(tag string, s, e int) { spans = append(spans, span{tag, s, e}) }
	hide := func(s, e int) {
		if !reveal {
			add("invisible", s, e)
		}
	}
	// dim leaves a marker visible but recessive — used for list bullets and
	// quote bars, where hiding the character would change the text's shape.
	dim := func(s, e int) { add("marker", s, e) }

	body := 0 // byte offset where the line's prose starts (after block markers)

	switch {
	case strings.HasPrefix(line, "#### "):
		add("heading4", 0, n)
		hide(0, 5)
		body = 5
	case strings.HasPrefix(line, "### "):
		add("heading3", 0, n)
		hide(0, 4)
		body = 4
	case strings.HasPrefix(line, "## "):
		add("heading2", 0, n)
		hide(0, 3)
		body = 3
	case strings.HasPrefix(line, "# "):
		add("heading1", 0, n)
		hide(0, 2)
		body = 2
	case isDivider(line):
		add("divider", 0, n)
		return charSpans(line, spans)
	case strings.HasPrefix(line, "> "):
		add("quote", 0, n)
		dim(0, 1)
		body = 2
	default:
		if m := bulletPrefix(line); m > 0 {
			add("listitem", 0, n)
			dim(m-2, m-1) // the "-"/"*" itself
			body = m
		}
	}

	// Hide trailing HTML-comment metadata (e.g. checklist priority/due).
	if b := strings.Index(line, "<!--"); b >= 0 {
		if rel := strings.Index(line[b:], "-->"); rel >= 0 {
			hide(b, b+rel+len("-->"))
			n = b // don't scan inline markup inside the metadata comment
		}
	}

	// Prose is the common case: a line with no inline marker at all needs no
	// character-by-character scan. IndexAny is a vectorized search, so this is
	// far cheaper than running the state machine over the line.
	if strings.IndexAny(line[body:n], "*`~") < 0 {
		return charSpans(line, spans)
	}

	i := body
	for i < n {
		switch {
		case line[i] == '`':
			if j := indexByteFrom(line, '`', i+1, n); j > i {
				add("code", i+1, j)
				hide(i, i+1)
				hide(j, j+1)
				i = j + 1
				continue
			}
		case line[i] == '~' && i+1 < n && line[i+1] == '~':
			if j := indexDouble(line, '~', i+2, n); j >= 0 {
				add("strike", i+2, j)
				hide(i, i+2)
				hide(j, j+2)
				i = j + 2
				continue
			}
		case line[i] == '*' && i+1 < n && line[i+1] == '*':
			if j := indexDouble(line, '*', i+2, n); j >= 0 {
				add("bold", i+2, j)
				hide(i, i+2)
				hide(j, j+2)
				i = j + 2
				continue
			}
		case line[i] == '*':
			if j := indexSingleStar(line, i+1, n); j > i {
				add("italic", i+1, j)
				hide(i, i+1)
				hide(j, j+1)
				i = j + 1
				continue
			}
		}
		i++
	}
	return charSpans(line, spans)
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
