package editor

import (
	"strings"
	"unicode/utf8"
)

// span is a tag application over a rune range within a single line.
type span struct {
	tag   string
	start int // inclusive rune offset within the line
	end   int // exclusive rune offset within the line
}

// parseLineSpans returns the formatting spans for one markdown line. When reveal
// is true (the cursor is on this line) the markdown markers are left visible;
// otherwise they are dimmed or hidden so the rendered text reads cleanly.
func parseLineSpans(line string, reveal bool) []span {
	runes := []rune(line)
	n := len(runes)
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

	body := 0 // rune offset where the line's prose starts (after block markers)

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
		return spans
	case strings.HasPrefix(line, "> "):
		add("quote", 0, n)
		dim(0, 1)
		body = 2
	default:
		if m := bulletPrefix(runes); m > 0 {
			add("listitem", 0, n)
			dim(m-2, m-1) // the "-"/"*" itself
			body = m
		}
	}

	// Hide trailing HTML-comment metadata (e.g. checklist priority/due).
	if b := strings.Index(line, "<!--"); b >= 0 {
		if rel := strings.Index(line[b:], "-->"); rel >= 0 {
			cs := utf8.RuneCountInString(line[:b])
			ce := utf8.RuneCountInString(line[:b+rel+len("-->")])
			hide(cs, ce)
			n = cs // don't scan inline markup inside the metadata comment
		}
	}

	i := body
	for i < n {
		switch {
		case runes[i] == '`':
			if j := indexFrom(runes, '`', i+1); j > i {
				add("code", i+1, j)
				hide(i, i+1)
				hide(j, j+1)
				i = j + 1
				continue
			}
		case runes[i] == '~' && i+1 < n && runes[i+1] == '~':
			if j := indexDouble(runes, '~', i+2); j >= 0 {
				add("strike", i+2, j)
				hide(i, i+2)
				hide(j, j+2)
				i = j + 2
				continue
			}
		case runes[i] == '*' && i+1 < n && runes[i+1] == '*':
			if j := indexDouble(runes, '*', i+2); j >= 0 {
				add("bold", i+2, j)
				hide(i, i+2)
				hide(j, j+2)
				i = j + 2
				continue
			}
		case runes[i] == '*':
			if j := indexSingleStar(runes, i+1); j > i {
				add("italic", i+1, j)
				hide(i, i+1)
				hide(j, j+1)
				i = j + 1
				continue
			}
		}
		i++
	}
	return spans
}

// bulletPrefix returns the rune offset just past a "- " / "* " / "+ " list
// marker (including its indentation), or 0 when the line isn't a list item.
// Task lines ("- [ ] ") are excluded: they render as checkbox widgets.
func bulletPrefix(runes []rune) int {
	i := 0
	for i < len(runes) && (runes[i] == ' ' || runes[i] == '\t') {
		i++
	}
	if i >= len(runes) || (runes[i] != '-' && runes[i] != '*' && runes[i] != '+') {
		return 0
	}
	if i+1 >= len(runes) || runes[i+1] != ' ' {
		return 0
	}
	if i+2 < len(runes) && runes[i+2] == '[' {
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

func indexFrom(runes []rune, ch rune, from int) int {
	for k := from; k < len(runes); k++ {
		if runes[k] == ch {
			return k
		}
	}
	return -1
}

// indexDouble returns the index of the first of a doubled ch ("**", "~~") at or
// after from, or -1.
func indexDouble(runes []rune, ch rune, from int) int {
	for k := from; k+1 < len(runes); k++ {
		if runes[k] == ch && runes[k+1] == ch {
			return k
		}
	}
	return -1
}

// indexSingleStar returns the next standalone '*' (not part of "**"), or -1.
func indexSingleStar(runes []rune, from int) int {
	for k := from; k < len(runes); k++ {
		if runes[k] == '*' {
			if k+1 < len(runes) && runes[k+1] == '*' {
				k++ // skip the pair
				continue
			}
			return k
		}
	}
	return -1
}
