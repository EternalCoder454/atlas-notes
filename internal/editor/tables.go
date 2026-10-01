package editor

import (
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/graphene"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
)

// This file draws Markdown tables as real tables.
//
// A table is not part of the text. Its lines stay in the buffer exactly as
// written, so the saved note, the index and undo never learn about it. Away from
// the caret the lines are hidden (shrunk to nothing, as an image line is) and a
// grid of labels is laid over the text view where they were, with blank space
// under the table's first line so the text after it does not run underneath. The
// caret on any line of the table shows the whole thing as raw Markdown, in the
// monospace style so that the pipes line up, and the grid goes away. It is the
// same choice pictures make (see images.go): a table is one thing, so it shows
// all of its Markdown or none.
//
// The placement (sizes, spacing, positions) is the pictures' own, shared through
// the block interface. What is here is finding tables, drawing their cells, and
// the parts of a table that need no GTK (parsing, spotting spans in a document,
// widening a pass to whole tables, escaping cell text for Pango), which are plain
// functions and are tested on their own.

const (
	// maxTableCells is the largest table that is drawn. A cell is a label, and
	// labels the pool has no room for are ones the bindings never free, so an
	// enormous table stays plain text rather than turn every open of the note into
	// a leak. It is a few times what a table in a note holds.
	maxTableCells = 1000
	// maxPooledLabels caps the labels kept for reuse, the same size as the picture
	// boxes' pool and for the same reason.
	maxPooledLabels = 512
	// maxPooledGrids caps the grids kept for reuse.
	maxPooledGrids = 32
	// tableGap is the blank space left under a table.
	tableGap = 8
	// tablePadStep is what the space under a table is rounded up to before it
	// becomes a spacing tag, so that tables of many heights do not each make a tag.
	tablePadStep = 4
	// tablePadPrefix starts the name of a table's spacing tag.
	tablePadPrefix = "table-pad-"
	// tableParkY is where a table waits while it is measured. A widget has to be in
	// the view for its style to count, and it has to be in the view before the
	// place it goes to is known, so it waits out of sight.
	tableParkY = -1 << 16
	// maxInlineDepth is how deep the emphasis in a cell may nest.
	maxInlineDepth = 4
)

// ---- Parsing ---------------------------------------------------------------

// tableAlign is how a column's text sits in its cells.
type tableAlign uint8

const (
	alignDefault tableAlign = iota // "---": left, like every column without a colon
	alignLeft                      // ":--"
	alignCenter                    // ":-:"
	alignRight                     // "--:"
)

// table is a parsed Markdown table.
type table struct {
	// rows holds the header row first and then the body rows. Every row has one
	// cell per column: a row that had fewer cells is padded and one that had more
	// is cut, as GitHub does.
	rows  [][]string
	align []tableAlign // one per column
	lines int          // how many lines the table takes: the header, the delimiter row and the body
}

// isTableRow reports whether a line could be a row of a table: it has a pipe. A
// task line starts with its checkbox's anchor and is never one. This is all that
// is asked to find where a table might reach, so it is kept cheap.
func isTableRow(line string) bool {
	return strings.IndexByte(line, '|') >= 0 && !strings.HasPrefix(line, anchorChar)
}

// splitRow cuts a row into its cells, without the outer pipes and with the
// whitespace around each cell trimmed. A pipe splits a cell unless it is escaped
// ("\|", which is a pipe in the cell's text) or is inside a code span (`a|b`). Other
// backslashes are kept, for the inline pass to read.
func splitRow(line string) []string {
	s := strings.TrimSpace(line)
	i := 0
	if strings.HasPrefix(s, "|") {
		i = 1
	}
	var cells []string
	var cur strings.Builder
	// A leading pipe is a separator that has already been passed, so a row of
	// nothing else has no cells.
	endedOnPipe := i == 1
	for i < len(s) {
		endedOnPipe = false
		switch c := s[i]; c {
		case '\\':
			if i+1 < len(s) && s[i+1] == '|' {
				cur.WriteByte('|')
			} else if i+1 < len(s) {
				cur.WriteByte('\\')
				cur.WriteByte(s[i+1])
			} else {
				cur.WriteByte('\\')
			}
			i += 2
		case '`':
			run := runLen(s, i, '`')
			if j := closingRun(s, i+run, run); j >= 0 {
				// A code span is copied whole. GitHub still reads "\|" inside one as a
				// pipe, so that is done here too.
				cur.WriteString(strings.ReplaceAll(s[i:j+run], `\|`, "|"))
				i = j + run
			} else {
				cur.WriteString(s[i : i+run])
				i += run
			}
		case '|':
			cells = append(cells, strings.TrimSpace(cur.String()))
			cur.Reset()
			endedOnPipe = true
			i++
		default:
			cur.WriteByte(c)
			i++
		}
	}
	if !endedOnPipe && s != "" {
		cells = append(cells, strings.TrimSpace(cur.String()))
	}
	return cells
}

// runLen is how many times ch repeats in s from position i.
func runLen(s string, i int, ch byte) int {
	n := 0
	for i+n < len(s) && s[i+n] == ch {
		n++
	}
	return n
}

// closingRun finds the start of the next run of exactly run backticks in s at or
// after from, which is what closes a code span, or -1.
func closingRun(s string, from, run int) int {
	for k := from; k < len(s); {
		if s[k] != '`' {
			k++
			continue
		}
		n := runLen(s, k, '`')
		if n == run {
			return k
		}
		k += n
	}
	return -1
}

// parseDelimiter reads a delimiter row: cells made only of dashes with an
// optional colon at either end. It gives the columns' alignments. The row needs a
// pipe, so that a line of dashes stays a rule and a heading underline.
func parseDelimiter(line string) ([]tableAlign, bool) {
	if strings.IndexByte(line, '|') < 0 {
		return nil, false
	}
	cells := splitRow(line)
	if len(cells) == 0 {
		return nil, false
	}
	align := make([]tableAlign, len(cells))
	for i, c := range cells {
		left := strings.HasPrefix(c, ":")
		right := len(c) > 1 && strings.HasSuffix(c, ":")
		dashes := strings.TrimSuffix(strings.TrimPrefix(c, ":"), ":")
		if dashes == "" || strings.Trim(dashes, "-") != "" {
			return nil, false
		}
		switch {
		case left && right:
			align[i] = alignCenter
		case left:
			align[i] = alignLeft
		case right:
			align[i] = alignRight
		}
	}
	return align, true
}

// parseTable reads the table that starts at lines[0]: a header row, a delimiter
// row with as many cells, and then as many body rows as follow (every line with a
// pipe, up to the first that has none). It is not a table when the first two lines
// are not those, and the delimiter row alone is not one either.
func parseTable(lines []string) (table, bool) {
	if len(lines) < 2 || !isTableRow(lines[0]) {
		return table{}, false
	}
	align, ok := parseDelimiter(lines[1])
	if !ok {
		return table{}, false
	}
	head := splitRow(lines[0])
	if len(head) != len(align) {
		return table{}, false
	}
	rows := [][]string{head}
	i := 2
	for ; i < len(lines) && isTableRow(lines[i]); i++ {
		rows = append(rows, fitRow(splitRow(lines[i]), len(head)))
	}
	return table{rows: rows, align: align, lines: i}, true
}

// fitRow pads a row with empty cells, or cuts it, to n cells.
func fitRow(cells []string, n int) []string {
	if len(cells) > n {
		return cells[:n]
	}
	for len(cells) < n {
		cells = append(cells, "")
	}
	return cells
}

// tableAt is a table found in a document: the line its header is on.
type tableAt struct {
	line int
	table
}

// findTables finds the tables among lines, which are the document's lines from
// line first on. fence says which lines of the document are in a fenced code
// block, as the editor keeps it (nil when there is none): a table is never made
// of those, and never crosses them. A table too big to draw (see maxTableCells)
// is left as text.
func findTables(lines []string, first int, fence []bool) []tableAt {
	var found []tableAt
	segEnd := 0 // where the stretch of lines outside a fence that i is in ends
	for i := 0; i < len(lines); {
		if i >= segEnd {
			if inFence(fence, first+i) {
				i++
				continue
			}
			segEnd = i
			for segEnd < len(lines) && !inFence(fence, first+segEnd) {
				segEnd++
			}
		}
		// Most lines have no pipe, and are passed over here.
		if strings.IndexByte(lines[i], '|') < 0 {
			i++
			continue
		}
		t, ok := parseTable(lines[i:segEnd])
		if !ok {
			i++
			continue
		}
		if len(t.rows)*len(t.align) <= maxTableCells {
			found = append(found, tableAt{first + i, t})
		}
		i += t.lines
	}
	return found
}

// widenToTables widens the pass range from..to, of a document whose last line is
// last, so that a table it touches is in it whole. A table reaches past an end of
// the range only through lines that look like rows, so each end is followed while
// they do; an end that is not on such a line has no table crossing it. isRow says
// whether a line looks like a row.
func widenToTables(from, to, last int, isRow func(line int) bool) (int, int) {
	rowFrom := isRow(from)
	rowTo := rowFrom
	if to != from {
		rowTo = isRow(to)
	}
	if rowFrom {
		for from > 0 && isRow(from-1) {
			from--
		}
	}
	if rowTo {
		for to < last && isRow(to+1) {
			to++
		}
	}
	return from, to
}

// ---- Cell text -------------------------------------------------------------

// escapeMarkup escapes text for Pango markup. Everything that ends up in a label's
// markup goes through here or through cellMarkup, which is built on it.
var escapeMarkup = strings.NewReplacer(
	"&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;",
).Replace

// cleanText makes text safe for markup: bytes that are not text become the
// replacement character, and control characters, which markup cannot hold, become
// spaces.
func cleanText(s string) string {
	s = strings.ToValidUTF8(s, "�")
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
}

// cellMarkup turns the Markdown in a table cell into Pango markup: bold, italic,
// strikethrough and code become tags, and a link becomes its text. Everything else
// is escaped, and the tags are the only markup in the result, each opened and
// closed by this function, so the result is always well formed.
func cellMarkup(cell string) string {
	var b strings.Builder
	writeInline(&b, cleanText(cell), 0, false)
	return b.String()
}

// linkedMarkup is cellMarkup for text read in a page rather than a grid: a link's
// text and a web address are drawn in the link colour, the way the editor draws
// them.
func linkedMarkup(text string) string {
	var b strings.Builder
	writeInline(&b, cleanText(text), 0, true)
	return b.String()
}

// urlRun finds a web address in plain text.
var urlRun = regexp.MustCompile(`https?://[^\s<>]+`)

// writeLinked writes plain text with its web addresses in the link colour.
func writeLinked(b *strings.Builder, s string) {
	at := 0
	for _, m := range urlRun.FindAllStringIndex(s, -1) {
		end := m[0] + len(strings.TrimRight(s[m[0]:m[1]], ".,;:!?)"))
		b.WriteString(escapeMarkup(s[at:m[0]]))
		b.WriteString(`<span foreground="` + linkColor + `" underline="single">` + escapeMarkup(s[m[0]:end]) + `</span>`)
		at = end
	}
	b.WriteString(escapeMarkup(s[at:]))
}

// writeInline writes the markup for s to b. links draws the links in colour.
func writeInline(b *strings.Builder, s string, depth int, links bool) {
	start := 0 // where the text not yet written begins
	flush := func(end int) {
		if end <= start {
			return
		}
		if links {
			writeLinked(b, s[start:end])
			return
		}
		b.WriteString(escapeMarkup(s[start:end]))
	}
	i := 0
	for i < len(s) {
		switch c := s[i]; c {
		case '\\':
			// A backslash before punctuation makes it plain. The punctuation stays in
			// the text still to be written, and is stepped over.
			if i+1 < len(s) && isPunct(s[i+1]) {
				flush(i)
				start = i + 1
				i += 2
				continue
			}
		case '`':
			run := runLen(s, i, '`')
			if j := closingRun(s, i+run, run); j >= 0 {
				flush(i)
				b.WriteString("<tt>")
				b.WriteString(escapeMarkup(s[i+run : j]))
				b.WriteString("</tt>")
				i = j + run
				start = i
				continue
			}
			i += run
			continue
		case '*', '_', '~':
			if depth < maxInlineDepth {
				if tag, n, end := emphasis(s, i); end >= 0 {
					flush(i)
					b.WriteString("<" + tag + ">")
					writeInline(b, s[i+n:end], depth+1, links)
					b.WriteString("</" + tag + ">")
					i = end + n
					start = i
					continue
				}
			}
		case '!', '[':
			at := i
			if c == '!' {
				at++
			}
			if at < len(s) && s[at] == '[' {
				if text, end, ok := linkText(s, at); ok {
					flush(i)
					if links {
						b.WriteString(`<span foreground="` + linkColor + `">`)
					}
					if depth < maxInlineDepth {
						writeInline(b, text, depth+1, false)
					} else {
						b.WriteString(escapeMarkup(text))
					}
					if links {
						b.WriteString("</span>")
					}
					i = end
					start = i
					continue
				}
			}
		}
		i++
	}
	flush(len(s))
}

// isPunct reports whether c is ASCII punctuation, which a backslash may escape.
func isPunct(c byte) bool {
	return strings.IndexByte("!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~", c) >= 0
}

// isWordByte reports whether c could be part of a word. Any byte of a multi-byte
// character counts, since such characters are letters more often than not.
func isWordByte(c byte) bool {
	return c >= 0x80 || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// emphasis reads bold, italic or strikethrough opened at s[i]. It gives the Pango
// tag, the length of the marker, and where the closing marker starts, which is -1
// when there is no emphasis here. As in Markdown, the text inside may not start or
// end with a space, and an underscore does not open or close inside a word, so
// snake_case stays as it is.
func emphasis(s string, i int) (tag string, n, end int) {
	c := s[i]
	n = 1
	if i+1 < len(s) && s[i+1] == c {
		n = 2
	}
	// A longer try is made first, and when it finds no end a single marker is
	// still tried, so "**a*" reads as a star and an italic a. Strikethrough has no
	// single form.
	least := 1
	if c == '~' {
		least = 2
	}
	for ; n >= least; n-- {
		if c == '_' && i > 0 && isWordByte(s[i-1]) {
			return "", 0, -1
		}
		if i+n >= len(s) || s[i+n] == ' ' {
			continue
		}
		if j := findClose(s, i+n, c, n); j >= 0 {
			switch {
			case c == '~':
				return "s", n, j
			case n == 2:
				return "b", n, j
			default:
				return "i", n, j
			}
		}
	}
	return "", 0, -1
}

// findClose finds the marker that closes an emphasis whose text starts at from:
// n repeats of ch. Escaped characters and code spans are stepped over, and a
// single marker does not close on half of a doubled one.
func findClose(s string, from int, ch byte, n int) int {
	for k := from; k < len(s); {
		switch s[k] {
		case '\\':
			k += 2
			continue
		case '`':
			run := runLen(s, k, '`')
			if j := closingRun(s, k+run, run); j >= 0 {
				k = j + run
			} else {
				k += run
			}
			continue
		case ch:
			run := runLen(s, k, ch)
			switch {
			case n == 1 && run >= 2:
				k += run // the middle of something doubled, which is for the text inside
				continue
			case run >= n && k > from && s[k-1] != ' ' &&
				(ch != '_' || k+n >= len(s) || !isWordByte(s[k+n])):
				return k
			}
			k += run
			continue
		}
		k++
	}
	return -1
}

// linkText reads a link or image at s[at] (which is a "["): "[text](target)" or
// "[[target|alias]]". It gives the text to show, and where the link ends.
func linkText(s string, at int) (text string, end int, ok bool) {
	if strings.HasPrefix(s[at:], "[[") {
		j := strings.Index(s[at+2:], "]]")
		if j <= 0 {
			return "", 0, false
		}
		name := s[at+2 : at+2+j]
		if k := strings.LastIndexByte(name, '|'); k >= 0 {
			name = name[k+1:]
		}
		return name, at + 2 + j + 2, true
	}
	depth := 0
	for k := at; k < len(s); k++ {
		switch s[k] {
		case '\\':
			k++
		case '[':
			depth++
		case ']':
			depth--
			if depth > 0 {
				continue
			}
			if k+1 >= len(s) || s[k+1] != '(' {
				return "", 0, false
			}
			parens := 0
			for m := k + 1; m < len(s); m++ {
				switch s[m] {
				case '\\':
					m++
				case '(':
					parens++
				case ')':
					parens--
					if parens == 0 {
						return s[at+1 : k], m + 1, true
					}
				}
			}
			return "", 0, false
		}
	}
	return "", 0, false
}

// ---- Spacing ---------------------------------------------------------------

// tablePad names the spacing tag for a table's line and says how many pixels of
// space it puts there: px rounded up to a step. Nothing is needed for a table
// that fits in the lines it covers, and that is a name of "".
func tablePad(px int) (name string, n int) {
	if px <= 0 {
		return "", 0
	}
	n = (px + tablePadStep - 1) / tablePadStep * tablePadStep
	return tablePadPrefix + strconv.Itoa(n), n
}

// ---- State -----------------------------------------------------------------

// tableHit is a table the render pass has just drawn as a table.
type tableHit struct {
	line int
	t    table
}

// tableItem is one table on show: the overlay bookkeeping for its first line,
// what it holds, and its grid.
type tableItem struct {
	overlay
	t    table
	grid *tableGrid
}

// tableGrid is the widget for one table, kept together so it can be reused for
// another one, for the reason given at imageBox. Its click handler holds the
// editor and a number, never the widget itself.
type tableGrid struct {
	serial int
	grid   *gtk.Grid
	cells  []tableCell // the labels in the grid now
}

// tableCell is a label in a table's grid, where it sits and what it was last
// set to show. A grid keeps its labels between tables, so the next table it
// shows sets only the cells that differ, and opening a note full of tables
// does not take every cell apart and build it again.
type tableCell struct {
	l        *gtk.Label
	row, col int
	set      bool // the fields below say what l shows; false for a label from the pool
	markup   string
	align    tableAlign
	head     bool
}

// tableState is the editor's table bookkeeping.
type tableState struct {
	items  []*tableItem
	hits   []tableHit         // tables drawn by the render pass in progress
	byLine map[int]*tableItem // items by line, built when a pass needs it
	grids  []*tableGrid       // grids kept for reuse
	labels []*gtk.Label       // labels kept for reuse
	serial int                // last grid number handed out
}

// clearTables forgets every table. Opening another note replaces the text.
func (e *Editor) clearTables() {
	s := &e.tbl
	// Last first: the pool hands out the grid it took last, so the next note's
	// first table gets this note's first grid, the likeliest to have its shape.
	for i := len(s.items) - 1; i >= 0; i-- {
		e.dropTable(s.items[i])
	}
	s.items = nil
	s.hits = s.hits[:0]
	s.byLine = nil
}

// ---- The render pass -------------------------------------------------------

// isRowLine reports whether buffer line n could be a row of a table.
func (e *Editor) isRowLine(n int) bool {
	if inFence(e.fence, n) {
		return false
	}
	text, ok := e.lineText(n)
	return ok && isTableRow(text)
}

// widenForTables widens the range of a render pass to hold every table it
// touches, whole: the tables on show, which the range may have changed, and any
// run of rows it reaches into, which an edit may have made a table (or not one
// any more). A note with no pipe in it, and so no table, pays nothing.
func (e *Editor) widenForTables(from, to, lastLine int) (int, int) {
	if !e.hasPipe && len(e.tbl.items) == 0 {
		return from, to
	}
	for again := true; again; {
		again = false
		for _, it := range e.tbl.items {
			l := e.markLine(it.mark)
			if l < 0 {
				continue
			}
			end := min(l+it.t.lines-1, lastLine)
			if l <= to && end >= from && (l < from || end > to) {
				from, to = min(from, l), max(to, end)
				again = true
			}
		}
	}
	if e.hasPipe {
		from, to = widenToTables(from, to, lastLine, e.isRowLine)
	}
	return from, to
}

// findTablesIn finds the tables among the lines of a render pass, which start at
// line first. A line drawn as a bullet is shown to the parser as the list item it
// stands for.
func (e *Editor) findTablesIn(lines []string, first int, bullets map[int]byte) []tableAt {
	if len(bullets) > 0 {
		src := make([]string, len(lines))
		copy(src, lines)
		for n, m := range bullets {
			if i := n - first; i >= 0 && i < len(src) {
				src[i] = restoreMarker(src[i], m)
			}
		}
		lines = src
	}
	return findTables(lines, first, e.fence)
}

// tagTableLine tags a line of a table. With the caret in the table it is the raw
// Markdown, in code. Without, it is hidden and the table's first line notes the
// table for syncTables and gets its spacing back, which the pass has just
// stripped.
func (e *Editor) tagTableLine(lineNum int, line string, tb *tableAt, reveal bool) {
	n := utf8.RuneCountInString(line)
	if reveal {
		e.applyTag("code", lineNum, 0, n)
		return
	}
	// The same choice the other markers make: hidden outright, or dimmed when
	// ATLAS_NO_HIDE is set.
	name := "invisible"
	if !hideMarkers {
		name = "marker"
	}
	e.applyTag(name, lineNum, 0, n)
	if lineNum != tb.line {
		return
	}
	e.tbl.hits = append(e.tbl.hits, tableHit{lineNum, tb.table})
	if it := e.tableItemAt(lineNum); it != nil && it.pad != "" {
		e.applyPad(&it.overlay, lineNum, it.pad)
	}
}

// tableItemAt finds the table whose first line is line.
func (e *Editor) tableItemAt(line int) *tableItem {
	s := &e.tbl
	if len(s.items) == 0 {
		return nil
	}
	if s.byLine == nil {
		s.byLine = make(map[int]*tableItem, len(s.items))
		for _, it := range s.items {
			if l := e.markLine(it.mark); l >= 0 {
				s.byLine[l] = it
			}
		}
	}
	return s.byLine[line]
}

// inTable reports whether a line is one of the hidden lines of a table on show.
// Its text is not what the person sees there, so it holds no link.
func (e *Editor) inTable(line int) bool {
	for _, it := range e.tbl.items {
		if l := e.markLine(it.mark); l >= 0 && line >= l && line < l+it.t.lines {
			return true
		}
	}
	return false
}

// syncTables runs after the render pass has tagged lines [from, to]. A table on
// show in that range that is not drawn as a table any more (its lines changed,
// or the caret is in it) goes away, one whose content changed is filled again,
// and a table newly drawn gets a grid. A table outside the range was not looked
// at and is left alone.
func (e *Editor) syncTables(from, to int) {
	s := &e.tbl
	hits := s.hits
	s.hits = s.hits[:0]
	s.byLine = nil
	if len(s.items) == 0 && len(hits) == 0 {
		return
	}
	hitAt := func(line int) *tableHit {
		for i := range hits {
			if hits[i].line == line {
				return &hits[i]
			}
		}
		return nil
	}
	seen := map[int]bool{}
	kept := s.items[:0]
	for _, it := range s.items {
		line := e.markLine(it.mark)
		h := hitAt(line)
		inRange := line >= from && line <= to
		// Deleting text leaves the marks that were inside it piled on one line; only
		// the first of them is kept.
		if line < 0 || seen[line] || (inRange && h == nil) {
			e.dropTable(it)
			continue
		}
		seen[line] = true
		if h != nil && !reflect.DeepEqual(h.t, it.t) {
			it.t = h.t
			e.dressGrid(it.grid, it.t)
		}
		kept = append(kept, it)
	}
	for i := len(kept); i < len(s.items); i++ {
		s.items[i] = nil
	}
	s.items = kept

	for _, h := range hits {
		if !seen[h.line] {
			e.addTable(h)
		}
	}
	e.queuePlace()
}

// addTable starts a table for a run of lines drawn as one.
func (e *Editor) addTable(h tableHit) {
	iter, ok := e.buffer.IterAtLine(h.line)
	if !ok {
		return
	}
	// Left gravity keeps the mark in front of anything typed at the start of the
	// line, so it stays on this line.
	it := &tableItem{overlay: overlay{mark: e.buffer.CreateMark("", iter, true)}, t: h.t}
	it.grid = e.takeGrid()
	e.dressGrid(it.grid, it.t)
	e.tbl.items = append(e.tbl.items, it)
}

// dropTable lets go of a table's widgets and mark.
func (e *Editor) dropTable(it *tableItem) {
	if it.grid != nil {
		if it.shown {
			e.dropOverlay(it.grid.grid)
		}
		e.giveGrid(it.grid)
		it.grid = nil
	}
	if !it.mark.Deleted() {
		e.buffer.DeleteMark(it.mark)
	}
}

// ---- Widgets ---------------------------------------------------------------

// takeGrid hands out a grid, from the pool when it holds one.
func (e *Editor) takeGrid() *tableGrid {
	s := &e.tbl
	if n := len(s.grids); n > 0 {
		g := s.grids[n-1]
		s.grids = s.grids[:n-1]
		return g
	}
	s.serial++
	g := &tableGrid{serial: s.serial, grid: gtk.NewGrid()}
	// The borders and the header's background come from the stylesheet. Only the
	// spacing is set here, because a cell's borders would otherwise be pulled apart.
	g.grid.AddCSSClass("md-table")
	g.grid.SetRowSpacing(0)
	g.grid.SetColumnSpacing(0)

	// A press on a table is claimed, so the text view underneath never sees it, and
	// puts the caret in the Markdown at the cell that was pressed, where it was
	// pressed, so the table opens for editing right there. It used to go to the
	// table's first line, wherever the press was, and the cell wanted had to be
	// found again in the source. The handler is handed the gesture rather than
	// closing over the grid, for the reason given at tableGrid.
	click := gtk.NewGestureClick()
	click.SetButton(1)
	serial := g.serial
	click.Connect("pressed", func(c *gtk.GestureClick, nPress int, x, y float64) {
		c.SetState(gtk.EventSequenceClaimed)
		row, col, at := -1, -1, 0
		if grid, ok := c.Widget().(*gtk.Grid); ok {
			row, col, at = pressedCell(grid, x, y)
		}
		e.editTable(serial, row, col, at)
	})
	g.grid.AddController(click)
	return g
}

// giveGrid takes a grid back into the pool with its cells still in it, for the
// next table to reuse, or lets it go when the pool is full. The labels a pooled
// grid holds are not counted against maxPooledLabels, so after a note of many
// large tables up to maxPooledGrids grids' worth stay alive. They would anyway:
// a label the pool cannot hold is never freed (see maxTableCells), and these
// are at least used again.
func (e *Editor) giveGrid(g *tableGrid) {
	if len(e.tbl.grids) < maxPooledGrids {
		e.tbl.grids = append(e.tbl.grids, g)
		return
	}
	e.emptyGrid(g)
}

// emptyGrid takes the labels out of a grid, into the pool.
func (e *Editor) emptyGrid(g *tableGrid) {
	for i := range g.cells {
		e.poolCell(g, g.cells[i].l)
		g.cells[i] = tableCell{}
	}
	g.cells = g.cells[:0]
}

// poolCell takes one label out of a grid, into the pool.
func (e *Editor) poolCell(g *tableGrid, l *gtk.Label) {
	g.grid.Remove(l)
	l.SetText("")
	if len(e.tbl.labels) < maxPooledLabels {
		e.tbl.labels = append(e.tbl.labels, l)
	}
}

// takeLabel hands out a cell, from the pool when it holds one.
func (e *Editor) takeLabel() *gtk.Label {
	s := &e.tbl
	if n := len(s.labels); n > 0 {
		l := s.labels[n-1]
		s.labels = s.labels[:n-1]
		return l
	}
	l := gtk.NewLabel("")
	l.AddCSSClass("md-table-cell")
	l.SetWrap(true)
	l.SetWrapMode(pango.WrapWordChar)
	l.SetSelectable(false)
	// The label fills its cell, so its borders are the cell's, and the text is
	// kept to the top of it when a neighbour is taller.
	l.SetYAlign(0)
	return l
}

// dressGrid fills a grid with a table's cells. A label already in the grid at a
// cell's place shows that cell; only the places the grid lacks get a label, and
// only the labels the table has no place for leave.
func (e *Editor) dressGrid(g *tableGrid, t table) {
	old := g.cells
	at := make(map[[2]int]int, len(old))
	for i, c := range old {
		at[[2]int{c.row, c.col}] = i
	}
	kept := make([]bool, len(old))
	var cells []tableCell
	for r, row := range t.rows {
		for c, text := range row {
			var cell tableCell
			if i, ok := at[[2]int{r, c}]; ok {
				cell, kept[i] = old[i], true
			} else {
				cell = tableCell{l: e.takeLabel(), row: r, col: c}
				g.grid.Attach(cell.l, c, r, 1, 1)
			}
			cell.dress(text, t.align[c], r == 0)
			cells = append(cells, cell)
		}
	}
	for i, c := range old {
		if !kept[i] {
			e.poolCell(g, c.l)
		}
	}
	g.cells = cells
}

// dress sets the cell's label to show text, touching only what differs from
// what it shows already.
func (c *tableCell) dress(text string, a tableAlign, head bool) {
	m := cellMarkup(text)
	if head {
		m = "<b>" + m + "</b>"
	}
	if c.set && m == c.markup && a == c.align && head == c.head {
		return
	}
	if !c.set || m != c.markup || head != c.head {
		dressCell(c.l, text, m, a, head)
	} else {
		alignCell(c.l, a)
	}
	c.set, c.markup, c.align, c.head = true, m, a, head
}

// dressCell sets a label to show a cell, whose markup cellMarkup has made (in
// <b> for the header, which is bold).
func dressCell(l *gtk.Label, cell, m string, a tableAlign, head bool) {
	if head {
		l.AddCSSClass("md-table-head")
	} else {
		l.RemoveCSSClass("md-table-head")
	}
	// Markup that is only the cell's text escaped shows that text, and setting it
	// as text spares Pango parsing it: most cells are plain words. Otherwise only
	// markup this file built goes in, and the text in it is escaped.
	if plain := cleanText(cell); !head && m == escapeMarkup(plain) {
		l.SetText(plain)
	} else {
		l.SetMarkup(m)
	}
	alignCell(l, a)
}

// alignCell lines a cell's text up the way its column says.
func alignCell(l *gtk.Label, a tableAlign) {
	switch a {
	case alignCenter:
		l.SetXAlign(0.5)
		l.SetJustify(gtk.JustifyCenter)
	case alignRight:
		l.SetXAlign(1)
		l.SetJustify(gtk.JustifyRight)
	default:
		l.SetXAlign(0)
		l.SetJustify(gtk.JustifyLeft)
	}
}

// pressedCell finds the cell of a table's grid under a press, and how many
// characters into its text the press was. -1 for the row and column when it was
// on no cell (a border).
func pressedCell(grid *gtk.Grid, x, y float64) (row, col, at int) {
	w := grid.Pick(x, y, gtk.PickDefault)
	var l *gtk.Label
	for w != nil {
		b := gtk.BaseWidget(w)
		if lbl, ok := w.(*gtk.Label); ok {
			if p := b.Parent(); p != nil && coreglib.InternObject(p).Native() == coreglib.InternObject(grid).Native() {
				l = lbl
				break
			}
		}
		if coreglib.InternObject(w).Native() == coreglib.InternObject(grid).Native() {
			break
		}
		w = b.Parent()
	}
	if l == nil {
		return -1, -1, 0
	}
	col, row, _, _ = grid.QueryChild(l)
	// Where in the label's text: the point in the label's own coordinates, less
	// where its layout is drawn, asked of the layout.
	pt, ok := gtk.BaseWidget(grid).ComputePoint(l, graphene.NewPointAlloc().Init(float32(x), float32(y)))
	if !ok {
		return row, col, 0
	}
	ox, oy := l.LayoutOffsets()
	layout := l.Layout()
	idx, trailing, _ := layout.XYToIndex(int(pt.X()-float32(ox))*pango.SCALE, int(pt.Y()-float32(oy))*pango.SCALE)
	text := layout.Text()
	if idx > len(text) {
		idx = len(text)
	}
	at = utf8.RuneCountInString(text[:idx]) + trailing
	return row, col, at
}

// cellSpans finds where each cell's text starts and ends in a table row's line,
// in bytes, with the whitespace around it left out. It reads the line as
// splitRow does: an escaped pipe or one in a code span does not end a cell.
func cellSpans(line string) [][2]int {
	start := len(line) - len(strings.TrimLeft(line, " \t"))
	i := start
	if i < len(line) && line[i] == '|' {
		i++
	}
	var spans [][2]int
	cellStart := i
	end := len(strings.TrimRight(line, " \t"))
	closeCell := func(to int) {
		a, b := cellStart, to
		for a < b && (line[a] == ' ' || line[a] == '\t') {
			a++
		}
		for b > a && (line[b-1] == ' ' || line[b-1] == '\t') {
			b--
		}
		spans = append(spans, [2]int{a, b})
	}
	for i < end {
		switch line[i] {
		case '\\':
			i += 2
		case '`':
			run := runLen(line, i, '`')
			if j := closingRun(line, i+run, run); j >= 0 {
				i = j + run
			} else {
				i += run
			}
		case '|':
			closeCell(i)
			i++
			cellStart = i
		default:
			i++
		}
	}
	if cellStart < end {
		closeCell(end)
	}
	return spans
}

// editTable puts the caret in the Markdown of the table whose grid has this
// number, so that it shows for editing: in the cell that was pressed (row and
// col, the header being row 0), at characters into its text when the cell's
// source reads the same as what was drawn, and at the cell's start when the
// source has markup the drawing hid. With no cell, on the table's first line.
func (e *Editor) editTable(serial, row, col, at int) {
	for _, it := range e.tbl.items {
		if it.grid == nil || it.grid.serial != serial {
			continue
		}
		line := e.markLine(it.mark)
		if line < 0 {
			return
		}
		iter, ok := e.buffer.IterAtLine(line)
		if row >= 0 && col >= 0 {
			if pos, found := e.cellPosition(line, row, col, at, it.t); found {
				iter, ok = pos, true
			}
		}
		if ok {
			e.buffer.PlaceCursor(iter)
			e.view.GrabFocus()
		}
		return
	}
}

// cellPosition is where in the buffer a cell of the table starting on line
// first begins, plus at characters when the cell's source is the text drawn.
func (e *Editor) cellPosition(first, row, col, at int, t table) (*gtk.TextIter, bool) {
	ln := first
	if row > 0 {
		ln = first + row + 1 // the delimiter row sits under the header
	}
	text, ok := e.lineText(ln)
	if !ok {
		return nil, false
	}
	spans := cellSpans(text)
	if col >= len(spans) {
		return nil, false
	}
	off := spans[col][0]
	src := text[spans[col][0]:spans[col][1]]
	if row < len(t.rows) && col < len(t.rows[row]) && t.rows[row][col] == src {
		// Drawn as written: the press's place in the drawing is its place in the
		// source too.
		n := 0
		for i := range src {
			if n == at {
				off = spans[col][0] + i
				break
			}
			n++
			off = spans[col][1]
		}
		if at == 0 {
			off = spans[col][0]
		}
	}
	iter, ok := e.buffer.IterAtLineIndex(ln, off)
	if !ok {
		return nil, false
	}
	return iter, true
}

// ---- Size and position -----------------------------------------------------

func (t *tableItem) over() *overlay { return &t.overlay }

func (t *tableItem) widget() gtk.Widgetter {
	if t.grid == nil {
		return nil
	}
	return t.grid.grid
}

func (t *tableItem) ready(avail int) bool { return avail > 0 }

func (t *tableItem) resize(e *Editor, avail int) { t.size(e, avail) }

// wantPad is the space the table's first line needs under it. The table's other
// lines are hidden, but each still takes a sliver of height, and the grid is laid
// over them, so what they take is not asked for twice.
func (t *tableItem) wantPad(e *Editor, line, avail int) string {
	if avail <= 0 {
		return "" // the view has no width to fit a table to
	}
	_, h := t.size(e, avail)
	if h <= 0 {
		return ""
	}
	name, _ := tablePad(h + tableGap - e.linesBelow(line, t.t.lines))
	return name
}

// size fits the grid to the view and gives it that size: the width its cells
// want, but never more than the view has, and the height its labels come to once
// they have wrapped to that width. A grid narrower than its cells can be made
// still gets as much as that. It measures the grid in the view, where its style is
// known, so a grid not yet in it waits out of sight first.
func (t *tableItem) size(e *Editor, avail int) (w, h int) {
	g := t.grid.grid
	if !t.shown {
		e.addOverlay(g, 0, tableParkY)
		t.shown, t.x, t.y = true, 0, tableParkY
	}
	g.SetVisible(true)
	// What was asked for last time would hold the measure at that size.
	g.SetSizeRequest(-1, -1)
	minW, natW, _, _ := g.Measure(gtk.OrientationHorizontal, -1)
	w = max(min(natW, avail), minW, 1)
	_, h, _, _ = g.Measure(gtk.OrientationVertical, w)
	g.SetSizeRequest(w, h)
	return w, h
}

// linesBelow is the height of a table's lines after its first, which is n lines
// long and starts at line.
func (e *Editor) linesBelow(line, n int) int {
	last := line + n - 1
	if n < 2 || last >= e.buffer.LineCount() {
		return 0
	}
	first, ok1 := e.buffer.IterAtLine(line)
	end, ok2 := e.buffer.IterAtLine(last)
	if !ok1 || !ok2 {
		return 0
	}
	ft, fh := e.view.LineYrange(first)
	lt, lh := e.view.LineYrange(end)
	return max(lt+lh-(ft+fh), 0)
}
