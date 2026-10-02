package mcp

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// This file holds the text helpers the tools share: splitting a note into
// numbered lines, finding its headings and sections, and showing a changed
// region so the model can check an edit without reading the note again.

// maxViewLines caps the lines a change view shows, whatever changed.
const maxViewLines = 40

// maxLineRunes is the longest line shown whole in a numbered listing.
const maxLineRunes = 400

// splitLines is a note's lines without their line breaks. A note that ends
// with a newline does not end with an empty line, and an empty note has none.
func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for i, l := range lines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}
	return lines
}

// numbered is lines from..to (counting from 1, both included) as "  12| text".
func numbered(lines []string, from, to int) string {
	var b strings.Builder
	for i := max(from, 1); i <= min(to, len(lines)); i++ {
		fmt.Fprintf(&b, "%4d| %s\n", i, clip(lines[i-1], maxLineRunes))
	}
	return b.String()
}

// span is a stretch of a note's text, in bytes, that an edit put there.
type span struct{ start, end int }

// replaceAt puts repl[j] in place of each stretch old[j] of text (ascending,
// not overlapping), in one pass, and moves the spans already noted so they
// still point at the same text. The new text gets spans too.
func replaceAt(text string, old []span, repl []string, spans []span) (string, []span) {
	// shift[j] is how far text after the first j stretches moves, and at[j]
	// where the jth replacement starts in the new text.
	shift := make([]int, len(old)+1)
	at := make([]int, len(old))
	for j, o := range old {
		at[j] = o.start + shift[j]
		shift[j+1] = shift[j] + len(repl[j]) - (o.end - o.start)
	}
	// where is a point of the old text in the new. A point inside a replaced
	// stretch goes to the start of what replaced it, or to its end.
	where := func(x int, end bool) int {
		c := sort.Search(len(old), func(j int) bool { return old[j].end > x })
		if c < len(old) && old[c].start < x {
			p := at[c]
			if end {
				p += len(repl[c])
			}
			return p
		}
		return x + shift[c]
	}
	out := make([]span, 0, len(spans)+len(old))
	for _, sp := range spans {
		out = append(out, span{where(sp.start, false), where(sp.end, true)})
	}
	var b strings.Builder
	b.Grow(len(text) + max(shift[len(old)], 0))
	prev := 0
	for j, o := range old {
		b.WriteString(text[prev:o.start])
		b.WriteString(repl[j])
		prev = o.end
		out = append(out, span{at[j], at[j] + len(repl[j])})
	}
	b.WriteString(text[prev:])
	return b.String(), out
}

// findText is where each non-overlapping copy of old is in text. A line break
// in old matches \n or \r\n, so text with mixed line ends still matches
// what read_note showed, which has no \r.
func findText(text, old string) []span {
	old = strings.ReplaceAll(old, "\r\n", "\n")
	if !strings.Contains(old, "\n") {
		var out []span
		for _, a := range occurrences(text, old) {
			out = append(out, span{a, a + len(old)})
		}
		return out
	}
	parts := strings.Split(old, "\n")
	// end is where a copy that has parts[0] ending at p ends, if one does.
	end := func(p int) (int, bool) {
		for _, part := range parts[1:] {
			switch {
			case strings.HasPrefix(text[p:], "\r\n"):
				p += 2
			case strings.HasPrefix(text[p:], "\n"):
				p++
			default:
				return 0, false
			}
			if !strings.HasPrefix(text[p:], part) {
				return 0, false
			}
			p += len(part)
		}
		return p, true
	}
	var out []span
	for i := 0; i < len(text); {
		j := strings.Index(text[i:], parts[0])
		if j < 0 {
			break
		}
		start := i + j
		if e, ok := end(start + len(parts[0])); ok {
			out = append(out, span{start, e})
			i = e
			continue
		}
		i = start + 1
	}
	return out
}

// crlfNote is whether most of text's line breaks are \r\n, so text added to it
// is written that way too.
func crlfNote(text string) bool {
	return strings.Count(text, "\r\n")*2 > strings.Count(text, "\n")
}

// endsLike is repl with the line breaks of the text it replaces, or those of
// the note when that text has none.
func endsLike(repl, matched string, crlf bool) string {
	if strings.Contains(matched, "\n") {
		crlf = strings.Contains(matched, "\r\n")
	}
	if crlf {
		return toCRLF(repl)
	}
	return strings.ReplaceAll(repl, "\r\n", "\n")
}

// changeView shows where spans of text changed: each as numbered lines with one
// line of context either side, overlapping ones joined, and no more than
// maxViewLines in all. A span with nothing in it is a deletion, and shows the
// lines on either side of where the text was.
func changeView(text string, spans []span) string {
	lines := splitLines(text)
	if len(lines) == 0 {
		return "(the note is now empty)\n"
	}
	// Where each line starts, so a span's line is a search, not a count.
	starts := []int{0}
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			starts = append(starts, i+1)
		}
	}
	lineAt := func(off int) int {
		off = min(max(off, 0), len(text))
		n := sort.Search(len(starts), func(i int) bool { return starts[i] > off })
		return min(max(n, 1), len(lines))
	}
	type region struct{ from, to int }
	var regions []region
	for _, sp := range spans {
		from := lineAt(sp.start)
		to := from
		if sp.end > sp.start {
			to = lineAt(sp.end - 1)
		}
		regions = append(regions, region{max(from-1, 1), min(to+1, len(lines))})
	}
	sort.Slice(regions, func(i, j int) bool { return regions[i].from < regions[j].from })
	var merged []region
	for _, r := range regions {
		if n := len(merged); n > 0 && r.from <= merged[n-1].to+1 {
			merged[n-1].to = max(merged[n-1].to, r.to)
			continue
		}
		merged = append(merged, r)
	}

	var b strings.Builder
	shown := 0
	for i, r := range merged {
		if shown >= maxViewLines {
			rest := 0
			for _, m := range merged[i:] {
				rest += m.to - m.from + 1
			}
			fmt.Fprintf(&b, "(… %d more changed lines)\n", rest)
			break
		}
		if i > 0 {
			b.WriteString("     ...\n")
		}
		to := r.to
		if left := maxViewLines - shown; to-r.from+1 > left {
			b.WriteString(numbered(lines, r.from, r.from+left-1))
			rest := to - (r.from + left - 1)
			for _, m := range merged[i+1:] {
				rest += m.to - m.from + 1
			}
			fmt.Fprintf(&b, "(… %d more changed lines)\n", rest)
			break
		}
		b.WriteString(numbered(lines, r.from, to))
		shown += to - r.from + 1
	}
	return b.String()
}

// heading is one Markdown heading of a note.
type heading struct {
	line  int // counting from 1
	level int
	title string
	text  string // the whole line, "## Title"
}

// headingsOf lists a note's headings. A line starting with # inside a fenced
// code block is code, not a heading.
func headingsOf(lines []string) []heading {
	var out []heading
	fence := ""
	for i, l := range lines {
		t := strings.TrimLeft(l, " ")
		if len(l)-len(t) > 3 {
			continue // indented code
		}
		if f := fenceOf(t); f != "" {
			switch {
			case fence == "":
				fence = f
			case f[0] == fence[0] && len(f) >= len(fence) && strings.TrimSpace(t[len(f):]) == "":
				fence = ""
			}
			continue
		}
		if fence != "" || !strings.HasPrefix(t, "#") {
			continue
		}
		level := len(t) - len(strings.TrimLeft(t, "#"))
		rest := t[level:]
		if level > 6 || (rest != "" && rest[0] != ' ' && rest[0] != '\t') {
			continue
		}
		out = append(out, heading{line: i + 1, level: level, title: titleOf(rest), text: strings.TrimRight(l, " \t")})
	}
	return out
}

// fenceOf is the run of ``` or ~~~ a line opens or closes a code block with.
func fenceOf(t string) string {
	for _, c := range []string{"`", "~"} {
		n := len(t) - len(strings.TrimLeft(t, c))
		if n >= 3 {
			return strings.Repeat(c, n)
		}
	}
	return ""
}

// sectionOf finds the section a heading query names: case does not matter and
// leading #s and spaces are ignored. It returns the first line and the last
// line of the section (the line before the next heading of the same or a
// higher level), how many headings matched, and whether any did.
func sectionOf(lines []string, query string) (from, to, matches int, ok bool) {
	want := headingQuery(query)
	if want == "" {
		return
	}
	hs := headingsOf(lines)
	for i, h := range hs {
		if !strings.EqualFold(h.title, want) {
			continue
		}
		matches++
		if matches > 1 {
			continue
		}
		from, to = h.line, len(lines)
		for _, next := range hs[i+1:] {
			if next.level <= h.level {
				to = next.line - 1
				break
			}
		}
		ok = true
	}
	return
}

// headingQuery is the title a heading query names: "## Plan ##", as an
// outline shows it, names "Plan".
func headingQuery(query string) string {
	return titleOf(strings.TrimLeft(strings.TrimSpace(query), "#"))
}

// listHeadings is the note's headings as "  12| ## Title", cut at a sensible
// number so a note full of them cannot flood the answer.
func listHeadings(lines []string) string {
	hs := headingsOf(lines)
	if len(hs) == 0 {
		return "(the note has no headings)\n"
	}
	var b strings.Builder
	for i, h := range hs {
		if i == 100 {
			fmt.Fprintf(&b, "(… %d more headings)\n", len(hs)-i)
			break
		}
		fmt.Fprintf(&b, "%4d| %s\n", h.line, clip(h.text, maxLineRunes))
	}
	return b.String()
}

// errNoHeading is the answer for a heading that is not in the note. It lists
// the ones there are, so the next call can name one.
func errNoHeading(rel, query string, lines []string) error {
	return fmt.Errorf("there is no heading %q in %q. Its headings are:\n%s", query, rel, listHeadings(lines))
}

// lineNumbers is the line numbers as "3, 9, 12", no more than most of them.
func lineNumbers(nums []int, most int) string {
	var parts []string
	for i, n := range nums {
		if i == most {
			parts = append(parts, fmt.Sprintf("and %d more", len(nums)-most))
			break
		}
		parts = append(parts, fmt.Sprint(n))
	}
	return strings.Join(parts, ", ")
}

// occurrences is where each non-overlapping copy of sub starts in text.
func occurrences(text, sub string) []int {
	var out []int
	for i := 0; sub != ""; {
		j := strings.Index(text[i:], sub)
		if j < 0 {
			break
		}
		out = append(out, i+j)
		i += j + len(sub)
	}
	return out
}

// squash is text with its spacing made uniform: line breaks as \n, runs of
// spaces and tabs as one space, nothing trailing a line. It also returns where
// each line starts in the result, so a match can be traced to its line.
func squash(text string) (string, []int) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	var b strings.Builder
	var starts []int
	for _, l := range strings.Split(text, "\n") {
		starts = append(starts, b.Len())
		var out []rune
		space := false
		for _, r := range l {
			if r == ' ' || r == '\t' {
				space = true
				continue
			}
			if space {
				out = append(out, ' ')
				space = false
			}
			out = append(out, r)
		}
		b.WriteString(string(out))
		b.WriteString("\n")
	}
	s := b.String()
	return s[:len(s)-1], starts
}

// whyNotFound explains an old_text that is not in the note, in words that help
// the model try again. It never offers to apply anything itself.
func whyNotFound(rel, text, old string) string {
	lines := splitLines(text)
	// The same text with different spacing.
	{
		nt, starts := squash(text)
		no, _ := squash(old)
		no = strings.TrimSpace(no)
		if hits := occurrences(nt, no); len(hits) == 1 && no != "" {
			lineAt := func(off int) int {
				return sort.Search(len(starts), func(i int) bool { return starts[i] > off })
			}
			from, to := lineAt(hits[0]), min(lineAt(hits[0]+len(no)-1), len(lines))
			shown := numbered(lines, from, min(to, from+maxViewLines-1))
			if to-from+1 > maxViewLines {
				shown += fmt.Sprintf("(… %d more lines)\n", to-from+1-maxViewLines)
			}
			return fmt.Sprintf("old_text is not in %q exactly, but matches at %s when spacing is ignored. Nothing was changed. The text as stored is:\n%sCopy old_text from these lines.",
				rel, where(from, to), shown)
		}
	}
	// The first line of it, which often is there.
	for _, l := range strings.Split(old, "\n") {
		first := strings.TrimSpace(l)
		if first == "" {
			continue
		}
		var at []int
		for i, nl := range lines {
			if strings.Contains(nl, first) {
				at = append(at, i+1)
			}
		}
		if len(at) > 0 {
			return fmt.Sprintf("old_text is not in %q. Its first line, %q, is at line %s. The text from there is:\n%sold_text has to match exactly, spaces and line breaks included.",
				rel, clip(first, 80), lineNumbers(at, 5), numbered(lines, at[0], at[0]+4))
		}
		break
	}
	return fmt.Sprintf("old_text is not in %q. It has to match exactly, spaces and line breaks included; read_note shows the current text", rel)
}

func where(from, to int) string {
	if from == to {
		return fmt.Sprintf("line %d", from)
	}
	return fmt.Sprintf("lines %d-%d", from, to)
}

// clip cuts s to n runes.
func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

// titleOf is a heading's words without the spaces round them or a closing run
// of #, so "## Plan ##" is "Plan".
func titleOf(rest string) string {
	t := strings.TrimSpace(rest)
	if r := strings.TrimRight(t, "#"); r != t && (r == "" || r[len(r)-1] == ' ' || r[len(r)-1] == '\t') {
		t = strings.TrimSpace(r)
	}
	return t
}

// cutBytes is s cut to at most n bytes, not in the middle of a character.
func cutBytes(s string, n int) string {
	if n >= len(s) {
		return s
	}
	n = max(n, 0)
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// toCRLF is text with every line break a carriage return and a line feed, for
// a note that is written that way.
func toCRLF(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "\r\n")
}
