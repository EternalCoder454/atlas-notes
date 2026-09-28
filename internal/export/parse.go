// Package export turns a note into a document another program can open:
// Markdown, OpenDocument Text, Word, a web page, or plain text.
//
// It is written for the Markdown Atlas Notes writes, which is a small and
// fairly strict subset: headings, bold, italic, struck-through and code text,
// quotes, bullet and numbered lists, checklist items with their priority and
// due date, fenced code, dividers and links. Anything else comes through as
// the text it is. There is no GTK here and no network, so the desktop app and
// the phone export through the same code and get the same documents.
//
// The formats are built by hand rather than through a converter. A Word or
// OpenDocument file is a zip of a few XML files, and writing the handful of
// elements a note needs is less code, and far less to trust, than a library
// that understands the whole of either format.
package export

import (
	"strconv"
	"strings"
	"time"

	"atlas-notes/internal/checklist"
)

// kind is what a block is.
type kind int

const (
	paragraph kind = iota
	heading
	bullet
	numbered
	task
	quote
	code
	divider
)

// block is one piece of a document: a paragraph, a heading, one list item, a
// fenced code block, or a divider. A paragraph and a quote may hold several
// lines; they are kept as lines because the note kept them as lines, and each
// format turns the breaks between them into its own line break.
type block struct {
	kind    kind
	level   int // heading level 1 to 6, or a list item's depth from 0
	number  int // a numbered item's own number
	checked bool
	detail  string     // a checklist item's priority and due date, in words
	lines   [][]inline // the text, a line at a time
	code    string     // a code block's text, verbatim
	lang    string     // a code block's language, if it named one
}

// inline is a run of text with the same formatting.
type inline struct {
	text                       string
	bold, italic, strike, mono bool
	link                       string // where it points, when it is a link
}

// parse reads a note's Markdown into blocks.
func parse(md string) []block {
	var out []block
	lines := strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// A fenced code block runs to its closing fence, or to the end of the
		// note if it never closes, and nothing inside it is Markdown.
		if fence, lang, ok := openFence(trimmed); ok {
			var body []string
			for i++; i < len(lines); i++ {
				if strings.HasPrefix(strings.TrimSpace(lines[i]), fence) {
					break
				}
				body = append(body, lines[i])
			}
			out = append(out, block{kind: code, code: strings.Join(body, "\n"), lang: lang})
			continue
		}

		if trimmed == "" {
			continue // a blank line ends whatever paragraph came before it
		}
		if isDivider(trimmed) {
			out = append(out, block{kind: divider})
			continue
		}
		if lvl, text, ok := headingLine(line); ok {
			out = append(out, block{kind: heading, level: lvl, lines: [][]inline{parseInline(text)}})
			continue
		}
		if strings.HasPrefix(trimmed, ">") {
			text := strings.TrimPrefix(strings.TrimPrefix(trimmed, ">"), " ")
			if n := len(out); n > 0 && out[n-1].kind == quote && prevWasText(lines, i) {
				out[n-1].lines = append(out[n-1].lines, parseInline(text))
			} else {
				out = append(out, block{kind: quote, lines: [][]inline{parseInline(text)}})
			}
			continue
		}
		depth := indentDepth(line)
		if it, ok := checklist.ParseLine(line); ok {
			out = append(out, block{
				kind: task, level: depth, checked: it.Checked, detail: taskDetail(it),
				lines: [][]inline{parseInline(stripComments(it.Text))},
			})
			continue
		}
		if text, ok := bulletLine(trimmed); ok {
			out = append(out, block{kind: bullet, level: depth, lines: [][]inline{parseInline(text)}})
			continue
		}
		if n, text, ok := numberedLine(trimmed); ok {
			out = append(out, block{kind: numbered, level: depth, number: n, lines: [][]inline{parseInline(text)}})
			continue
		}

		// Prose. A line that follows another line of prose joins its
		// paragraph, with a line break between them, as it has in the editor.
		text := parseInline(stripComments(trimmed))
		if n := len(out); n > 0 && out[n-1].kind == paragraph && prevWasText(lines, i) {
			out[n-1].lines = append(out[n-1].lines, text)
		} else {
			out = append(out, block{kind: paragraph, lines: [][]inline{text}})
		}
	}
	return out
}

// prevWasText reports whether the line before i was part of the same run of
// text, rather than a blank line that ended it.
func prevWasText(lines []string, i int) bool {
	return i > 0 && strings.TrimSpace(lines[i-1]) != ""
}

func openFence(trimmed string) (fence, lang string, ok bool) {
	for _, f := range []string{"```", "~~~"} {
		if strings.HasPrefix(trimmed, f) {
			return f, strings.TrimSpace(strings.TrimLeft(trimmed, f[:1])), true
		}
	}
	return "", "", false
}

func isDivider(trimmed string) bool {
	if len(trimmed) < 3 {
		return false
	}
	c := trimmed[0]
	if c != '-' && c != '*' && c != '_' {
		return false
	}
	for i := 0; i < len(trimmed); i++ {
		if trimmed[i] != c && trimmed[i] != ' ' {
			return false
		}
	}
	return true
}

func headingLine(line string) (int, string, bool) {
	n := 0
	for n < len(line) && n < 6 && line[n] == '#' {
		n++
	}
	if n == 0 || n >= len(line) || line[n] != ' ' {
		return 0, "", false
	}
	return n, strings.TrimSpace(line[n+1:]), true
}

func bulletLine(trimmed string) (string, bool) {
	for _, m := range []string{"- ", "* ", "+ "} {
		if strings.HasPrefix(trimmed, m) {
			return strings.TrimSpace(trimmed[2:]), true
		}
	}
	return "", false
}

func numberedLine(trimmed string) (int, string, bool) {
	i := 0
	for i < len(trimmed) && i < 9 && trimmed[i] >= '0' && trimmed[i] <= '9' {
		i++
	}
	if i == 0 || i+1 >= len(trimmed) || (trimmed[i] != '.' && trimmed[i] != ')') || trimmed[i+1] != ' ' {
		return 0, "", false
	}
	n, _ := strconv.Atoi(trimmed[:i])
	return n, strings.TrimSpace(trimmed[i+2:]), true
}

// indentDepth is how deeply a list item is nested: a tab or two spaces a level.
func indentDepth(line string) int {
	spaces := 0
	for _, r := range line {
		switch r {
		case ' ':
			spaces++
		case '\t':
			spaces += 2
		default:
			return min(spaces/2, 8)
		}
	}
	return 0
}

// taskDetail is a checklist item's priority and due date, written out: the
// note keeps them in a comment the editor hides, and a document has nowhere
// to hide them, so they become words.
func taskDetail(it checklist.Item) string {
	var parts []string
	if it.Priority != checklist.PriorityNone {
		parts = append(parts, string(it.Priority)+" priority")
	}
	if it.DueDate != "" {
		if t, err := time.Parse("2006-01-02", it.DueDate); err == nil {
			parts = append(parts, "due "+t.Format("2 January 2006"))
		} else {
			parts = append(parts, "due "+it.DueDate)
		}
	}
	return strings.Join(parts, ", ")
}

// stripComments removes HTML comments, which is where the editor keeps what it
// does not show. A comment left open runs to the end of the line.
func stripComments(s string) string {
	for {
		i := strings.Index(s, "<!--")
		if i < 0 {
			return strings.TrimRight(s, " ")
		}
		j := strings.Index(s[i:], "-->")
		if j < 0 {
			return strings.TrimRight(s[:i], " ")
		}
		s = s[:i] + s[i+j+3:]
	}
}

// parseInline splits a line into runs of text with the same formatting. Code
// is taken literally; bold, italic and struck-through text may nest; a link's
// text may be formatted too.
func parseInline(s string) []inline {
	var out []inline
	parseRuns(s, inline{}, &out)
	// Adjacent runs that ended up formatted alike are one run.
	merged := out[:0]
	for _, r := range out {
		if n := len(merged); n > 0 && sameStyle(merged[n-1], r) {
			merged[n-1].text += r.text
			continue
		}
		merged = append(merged, r)
	}
	return merged
}

func sameStyle(a, b inline) bool {
	return a.bold == b.bold && a.italic == b.italic && a.strike == b.strike && a.mono == b.mono && a.link == b.link
}

func parseRuns(s string, style inline, out *[]inline) {
	plain := func(t string) {
		if t != "" {
			r := style
			r.text = t
			*out = append(*out, r)
		}
	}
	for len(s) > 0 {
		i := strings.IndexAny(s, "`*~[")
		if i < 0 {
			plain(s)
			return
		}
		plain(s[:i])
		s = s[i:]
		switch {
		case s[0] == '`':
			if j := strings.IndexByte(s[1:], '`'); j >= 0 {
				r := style
				r.text, r.mono = s[1:1+j], true
				*out = append(*out, r)
				s = s[j+2:]
				continue
			}
		case strings.HasPrefix(s, "**"):
			if j := strings.Index(s[2:], "**"); j > 0 {
				inner := style
				inner.bold = true
				parseRuns(s[2:2+j], inner, out)
				s = s[j+4:]
				continue
			}
		case strings.HasPrefix(s, "~~"):
			if j := strings.Index(s[2:], "~~"); j > 0 {
				inner := style
				inner.strike = true
				parseRuns(s[2:2+j], inner, out)
				s = s[j+4:]
				continue
			}
		case s[0] == '*':
			if j := strings.IndexByte(s[1:], '*'); j > 0 && s[1] != ' ' {
				inner := style
				inner.italic = true
				parseRuns(s[1:1+j], inner, out)
				s = s[j+2:]
				continue
			}
		case s[0] == '[':
			if close := strings.Index(s, "]("); close > 0 {
				if end := strings.IndexByte(s[close+2:], ')'); end >= 0 {
					inner := style
					inner.link = s[close+2 : close+2+end]
					parseRuns(s[1:close], inner, out)
					s = s[close+3+end:]
					continue
				}
			}
		}
		// Not the start of anything after all: the character is text.
		plain(s[:1])
		s = s[1:]
	}
}

// plainText is a line's runs as text, the formatting dropped.
func plainText(runs []inline) string {
	var b strings.Builder
	for _, r := range runs {
		b.WriteString(r.text)
	}
	return b.String()
}
