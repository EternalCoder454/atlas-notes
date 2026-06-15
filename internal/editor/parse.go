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
// otherwise they receive the "invisible" tag so the rendered text reads cleanly.
func parseLineSpans(line string, reveal bool) []span {
	runes := []rune(line)
	n := len(runes)

	var spans []span
	add := func(tag string, s, e int) { spans = append(spans, span{tag, s, e}) }
	hide := func(s, e int) {
		if !reveal {
			add("invisible", s, e)
		}
	}

	switch {
	case strings.HasPrefix(line, "### "):
		add("heading3", 0, n)
		hide(0, 4)
	case strings.HasPrefix(line, "## "):
		add("heading2", 0, n)
		hide(0, 3)
	case strings.HasPrefix(line, "# "):
		add("heading1", 0, n)
		hide(0, 2)
	}

	// Hide trailing HTML-comment metadata (e.g. checklist priority/due).
	if b := strings.Index(line, "<!--"); b >= 0 {
		if rel := strings.Index(line[b:], "-->"); rel >= 0 {
			cs := utf8.RuneCountInString(line[:b])
			ce := utf8.RuneCountInString(line[:b+rel+len("-->")])
			hide(cs, ce)
		}
	}

	i := 0
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
		case runes[i] == '*' && i+1 < n && runes[i+1] == '*':
			if j := indexDoubleStar(runes, i+2); j >= 0 {
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

func indexFrom(runes []rune, ch rune, from int) int {
	for k := from; k < len(runes); k++ {
		if runes[k] == ch {
			return k
		}
	}
	return -1
}

// indexDoubleStar returns the index of the first '*' of the next "**" at or
// after from, or -1.
func indexDoubleStar(runes []rune, from int) int {
	for k := from; k+1 < len(runes); k++ {
		if runes[k] == '*' && runes[k+1] == '*' {
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
