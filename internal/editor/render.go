package editor

import (
	"reflect"
	"sort"
	"strings"
	"unicode/utf8"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
)

// This file draws the block constructs Obsidian notes lean on: quotes, callouts,
// fenced code blocks and folded headings.
//
// None of it changes the text. A quote or a callout is still "> " lines in the
// buffer and a fence is still a fence; what the editor adds is tags (the markers
// go away from the caret, callout lines are indented, folded lines shrink to
// nothing) and widgets laid over the text view, like pictures and tables are: a
// bar beside a quote, a tinted card with an icon behind a callout, a language
// label and a copy button on a code block, a chevron beside a heading. Folding
// is view state only. It lives in the editor, is dropped when another note is
// opened, and never touches the file.
//
// What is a block is worked out from the note's text whenever it changes (see
// scanRich), by plain functions that need no GTK and are tested on their own.

// blockKind is what kind of block richBlock describes.
type blockKind uint8

const (
	kindQuote blockKind = iota
	kindCallout
	kindCode
)

// richBlock is a run of lines drawn as one thing.
type richBlock struct {
	kind        blockKind
	first, last int // buffer lines, both included
	// A callout's type as written, lower-cased, its fold mark ('-' folded to start
	// with, '+' unfolded, 0 for none), its title, and where in its first line the
	// title starts. A code block's language and whether its closing fence is there.
	typ      string
	fold     byte
	title    string
	titleOff int
	lang     string
	closed   bool
	folded   bool // a callout that is drawn folded
}

// calloutHead reads "> [!type]- Title". It returns the type (lower-cased), the
// fold mark, the byte offset where the title starts and whether the line is a
// callout's first line at all.
func calloutHead(line string) (typ string, fold byte, titleOff int, ok bool) {
	if !strings.HasPrefix(line, ">") {
		return
	}
	i := 1
	for i < len(line) && line[i] == ' ' {
		i++
	}
	if !strings.HasPrefix(line[i:], "[!") {
		return
	}
	j := i + 2
	for j < len(line) && (line[j] == '-' || line[j] == '_' || line[j] >= '0' && line[j] <= '9' ||
		line[j] >= 'a' && line[j] <= 'z' || line[j] >= 'A' && line[j] <= 'Z') {
		j++
	}
	if j == i+2 || j >= len(line) || line[j] != ']' {
		return
	}
	typ = strings.ToLower(line[i+2 : j])
	j++
	if j < len(line) && (line[j] == '-' || line[j] == '+') {
		fold = line[j]
		j++
	}
	for j < len(line) && line[j] == ' ' {
		j++
	}
	return typ, fold, j, true
}

// calloutClass maps the type a note gives (Obsidian has aliases for each) to the
// look it is drawn with, one of the ten styles. An unknown type is a note.
func calloutClass(typ string) string {
	switch typ {
	case "tip", "hint", "important":
		return "tip"
	case "info":
		return "info"
	case "warning", "caution", "attention":
		return "warning"
	case "danger", "error", "failure", "fail", "missing", "bug":
		return "danger"
	case "success", "check", "done":
		return "success"
	case "question", "help", "faq":
		return "question"
	case "quote", "cite":
		return "quote"
	case "example":
		return "example"
	case "todo":
		return "todo"
	}
	return "note"
}

// calloutIcon is the icon a callout style is drawn with.
func calloutIcon(class string) string {
	switch class {
	case "tip":
		return "atlasnotes-callout-tip-symbolic"
	case "info":
		return "atlasnotes-info-symbolic"
	case "warning":
		return "atlasnotes-warning-symbolic"
	case "danger":
		return "atlasnotes-error-symbolic"
	case "success":
		return "atlasnotes-callout-success-symbolic"
	case "question":
		return "atlasnotes-callout-question-symbolic"
	case "quote":
		return "atlasnotes-quote-symbolic"
	case "example":
		return "atlasnotes-callout-example-symbolic"
	case "todo":
		return "atlasnotes-callout-todo-symbolic"
	}
	return "atlasnotes-callout-note-symbolic"
}

// calloutTitle is what a callout's header says: its title, or the type in words
// when it has none.
func calloutTitle(b richBlock) string {
	if b.title != "" {
		return b.title
	}
	if b.typ == "" {
		return "Note"
	}
	return strings.ToUpper(b.typ[:1]) + b.typ[1:]
}

// fenceLang is the language a fence line names, "" for none.
func fenceLang(line string) string {
	t := strings.TrimLeft(strings.TrimSpace(line), "`~")
	return strings.TrimSpace(t)
}

// isFenceLine reports whether a line starts like a code fence.
func isFenceLine(line string) bool {
	t := strings.TrimSpace(line)
	return strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~")
}

// scanBlocks finds the quotes, callouts and code blocks in a note's lines. fence
// says which lines are in a code block. A quote is a run of lines starting with
// ">", and it is a callout when the first of them names a type.
func scanBlocks(lines []string, fence []bool) []richBlock {
	var out []richBlock
	for i := 0; i < len(lines); {
		switch {
		case inFence(fence, i):
			j := i
			for j+1 < len(lines) && inFence(fence, j+1) {
				j++
			}
			out = append(out, richBlock{kind: kindCode, first: i, last: j, lang: fenceLang(lines[i]),
				closed: j > i && isFenceLine(lines[j])})
			i = j + 1
		case strings.HasPrefix(lines[i], ">"):
			j := i
			for j+1 < len(lines) && strings.HasPrefix(lines[j+1], ">") && !inFence(fence, j+1) {
				j++
			}
			b := richBlock{kind: kindQuote, first: i, last: j}
			if typ, fold, off, ok := calloutHead(lines[i]); ok {
				b.kind, b.typ, b.fold, b.titleOff = kindCallout, typ, fold, off
				b.title = strings.TrimSpace(lines[i][off:])
			}
			out = append(out, b)
			i = j + 1
		default:
			i++
		}
	}
	return out
}

// headingLevel is the level of a "## Heading" line, 0 for any other line.
func headingLevel(line string) int {
	n := 0
	for n < len(line) && line[n] == '#' {
		n++
	}
	if n == 0 || n > 6 || n >= len(line) || line[n] != ' ' {
		return 0
	}
	return n
}

// scanFolds finds the lines a note's folded headings hide: everything under a
// folded heading up to the next heading of the same or a higher level. folded is
// the set of headings that are folded, by line. It returns the hidden
// ranges (both ends included, in order, none inside another) and the folded
// headings' own lines.
func scanFolds(lines []string, fence []bool, folded map[int]bool) (hides [][2]int, heads map[int]bool) {
	for i := 0; i < len(lines); {
		lvl := headingLevel(lines[i])
		if lvl == 0 || inFence(fence, i) || !folded[i] {
			i++
			continue
		}
		j := i + 1
		for j < len(lines) {
			if l := headingLevel(lines[j]); l > 0 && l <= lvl && !inFence(fence, j) {
				break
			}
			j++
		}
		if j > i+1 {
			hides = append(hides, [2]int{i + 1, j - 1})
			if heads == nil {
				heads = map[int]bool{}
			}
			heads[i] = true
		}
		i = j
	}
	return hides, heads
}

// foldEnd is the last line a heading at line i would hide when folded, or i when
// there is nothing under it.
func foldEnd(lines []string, fence []bool, i int) int {
	lvl := headingLevel(lines[i])
	j := i + 1
	for j < len(lines) {
		if l := headingLevel(lines[j]); l > 0 && l <= lvl && !inFence(fence, j) {
			break
		}
		j++
	}
	return j - 1
}

// ---- Editor state ----------------------------------------------------------

// richState is the editor's block bookkeeping.
type richState struct {
	blocks []richBlock
	hides  [][2]int
	heads  map[int]bool
	// flipMarks holds the callouts the person has clicked, each drawn the opposite
	// of how its "+" or "-" says, and headMarks the folded headings. They are text
	// marks at the start of the line, so that a fold follows its block through
	// edits (retyping a title does not undo it, and two blocks with the same text
	// fold on their own).
	flipMarks  []*gtk.TextMark
	headMarks  []*gtk.TextMark
	decos      [3][]*deco
	active     []*deco
	chev       *chevron
	rowsHidden bool
	// scanned says blocks was worked out for a note of scanLines lines, and force
	// that the next scan must run whatever the edit was (a fold changed).
	scanned    bool
	scanLines  int
	force      bool
	decoQueued bool
}

// markLines is the set of lines a list of marks is on. Marks whose text was
// deleted are dropped from the list.
func (e *Editor) markLines(list *[]*gtk.TextMark) map[int]bool {
	var out map[int]bool
	kept := (*list)[:0]
	for _, m := range *list {
		l := e.markLine(m)
		if l < 0 {
			continue
		}
		kept = append(kept, m)
		if out == nil {
			out = map[int]bool{}
		}
		out[l] = true
	}
	*list = kept
	return out
}

// setMark puts a mark on line (mode 1), takes it off (0) or flips it (-1).
func (e *Editor) setMark(list *[]*gtk.TextMark, line, mode int) {
	found := false
	kept := (*list)[:0]
	for _, m := range *list {
		l := e.markLine(m)
		if l < 0 {
			continue
		}
		if l == line {
			found = true
			if mode != 1 {
				e.buffer.DeleteMark(m)
				continue
			}
		}
		kept = append(kept, m)
	}
	*list = kept
	if !found && mode != 0 {
		if it, ok := e.buffer.IterAtLine(line); ok {
			// Left gravity keeps the mark in front of anything typed at the start of
			// the line, so it stays on this line.
			*list = append(*list, e.buffer.CreateMark("", it, true))
		}
	}
}

// editTouchesRich reports whether an edit to lines from to to of raw could change
// what scanRich finds: it is next to a block or a fold that was found, or one of
// the lines now starts like a quote, a heading or a fence. Typing in a paragraph
// is none of these, and then nothing needs scanning. It walks the text for the
// lines without splitting it.
func (s *richState) editTouchesRich(raw string, from, to int) bool {
	for _, b := range s.blocks {
		if b.last >= from-1 && b.first <= to+1 {
			return true
		}
	}
	for _, h := range s.hides {
		if h[1] >= from-1 && h[0] <= to+2 {
			return true
		}
	}
	if to-from > 200 {
		return true
	}
	pos := 0
	for i := 0; i < from; i++ {
		j := strings.IndexByte(raw[pos:], '\n')
		if j < 0 {
			return true
		}
		pos += j + 1
	}
	for i := from; i <= to && pos <= len(raw); i++ {
		end := len(raw)
		if j := strings.IndexByte(raw[pos:], '\n'); j >= 0 {
			end = pos + j
		}
		l := raw[pos:end]
		if strings.HasPrefix(l, ">") || strings.HasPrefix(l, "#") || isFenceLine(l) {
			return true
		}
		pos = end + 1
	}
	return false
}

// scanRich works out the note's blocks and folded ranges again, from the text of
// the whole note, and reports whether the folded ranges moved. It runs when the
// text has changed (see refreshFence, which has the text at hand) or when a fold
// has. An edit inside a paragraph, which is nearly every edit, leaves the blocks
// where they were and costs a walk over a few lines. A note with no ">" at the
// start of a line, no fence and no fold has nothing to find.
func (e *Editor) scanRich(raw string, from, to, lines int) (hidesMoved bool) {
	s := &e.rich
	force := s.force
	s.force = false
	quotes := strings.HasPrefix(raw, ">") || strings.Contains(raw, "\n>")
	if !quotes && e.fence == nil && len(s.headMarks) == 0 {
		hidesMoved = len(s.hides) > 0
		s.blocks, s.hides, s.heads, s.scanned = nil, nil, nil, false
		return hidesMoved
	}
	if !force && s.scanned && lines == s.scanLines && !s.editTouchesRich(raw, from, to) {
		return false
	}
	oldHides, oldHeads := s.hides, s.heads
	s.blocks, s.hides, s.heads = nil, nil, nil
	s.scanned, s.scanLines = true, lines
	split := strings.Split(raw, "\n")
	s.blocks = scanBlocks(split, e.fence)
	flip := e.markLines(&s.flipMarks)
	for i := range s.blocks {
		b := &s.blocks[i]
		if b.kind == kindCallout {
			b.folded = (b.fold == '-') != flip[b.first]
		}
	}
	if len(s.headMarks) > 0 {
		s.hides, s.heads = scanFolds(split, e.fence, e.markLines(&s.headMarks))
	}
	return !reflect.DeepEqual(oldHides, s.hides) || !reflect.DeepEqual(oldHeads, s.heads)
}

// blockAt is the block that line n is in, or nil.
func (s *richState) blockAt(n int) *richBlock {
	i := sort.Search(len(s.blocks), func(i int) bool { return s.blocks[i].last >= n })
	if i < len(s.blocks) && s.blocks[i].first <= n {
		return &s.blocks[i]
	}
	return nil
}

// hiddenAt reports whether line n is under a folded heading.
func (s *richState) hiddenAt(n int) bool {
	for _, h := range s.hides {
		if n >= h[0] && n <= h[1] {
			return true
		}
	}
	return false
}

// foldedAt reports whether line n is hidden, by a folded heading or by being in
// the body of a folded callout.
func (s *richState) foldedAt(n int) bool {
	if s.hiddenAt(n) {
		return true
	}
	b := s.blockAt(n)
	return b != nil && b.kind == kindCallout && b.folded && n > b.first
}

// widenForRich widens the lines a pass covers to whole blocks: a callout is drawn
// as one card, so a pass that touches a line of it retags all of it, and the
// caret entering or leaving a code block shows or hides its fences.
func (e *Editor) widenForRich(from, to, lastLine int) (int, int) {
	s := &e.rich
	for changed := true; changed; {
		changed = false
		for _, b := range s.blocks {
			if b.last < from || b.first > to {
				continue
			}
			if b.first < from {
				from, changed = b.first, true
			}
			if b.last > to {
				to, changed = b.last, true
			}
		}
	}
	return from, to
}

// clearRich forgets the blocks and the folds. Opening another note replaces the
// text, so what was folded in the last one says nothing about this one.
func (e *Editor) clearRich() {
	s := &e.rich
	s.blocks, s.hides, s.heads = nil, nil, nil
	s.scanned = false
	for _, m := range append(s.flipMarks, s.headMarks...) {
		if !m.Deleted() {
			e.buffer.DeleteMark(m)
		}
	}
	s.flipMarks, s.headMarks = nil, nil
	s.active = s.active[:0]
	for k := range s.decos {
		for _, d := range s.decos[k] {
			d.setVisible(false)
		}
	}
	if s.chev != nil {
		s.chev.hide()
	}
}

// ---- Tags ------------------------------------------------------------------

// richTagDefs are the tags this file's blocks use, in the order they are made:
// a later tag wins over an earlier one where both set a property.
var richTagDefs = []struct {
	name  string
	props map[string]any
}{
	// Text tags take a colour, not a CSS name. The yellow is an alpha, so it is a
	// soft mark on a light page and on a dark one alike; the footnote reference
	// takes the theme's accent (see SetLinkColor).
	{"highlight", map[string]any{"background": "rgba(255,208,0,0.30)"}},
	{"footref", map[string]any{"scale": 0.72, "rise": 5000, "foreground": linkColor}},
	{"footdef", map[string]any{"scale": 0.9, "foreground": "#9a9a9a"}},
	{"footnum", map[string]any{"scale": 0.8, "rise": 4000}},
	// Math has no TeX engine behind it: the formula is set in a serif italic, and a
	// display one that is a line by itself is centred, a little larger.
	{"math", map[string]any{"family": "serif", "style": pango.StyleItalic}},
	{"mathblock", map[string]any{"family": "serif", "style": pango.StyleItalic, "scale": 1.15}},
	{"mathline", map[string]any{"justification": gtk.JustifyCenter, "pixels-above-lines": 4, "pixels-below-lines": 4}},
	// A callout's lines sit inside its card: room on the left for the icon.
	{"callout", map[string]any{"left-margin": calloutIndent, "right-margin": 28, "pixels-below-lines": 2}},
	{"calloutfirst", map[string]any{"pixels-above-lines": 12}},
	{"calloutlast", map[string]any{"pixels-below-lines": 12}},
	// A title that is only the type's name has no text to hold the header open, so
	// the line is given the height of the header the type's name is drawn in.
	{"calloutbare", map[string]any{"pixels-below-lines": 16}},
	{"callouttitle", map[string]any{"weight": 700}},
	// A code block's fences shrink away (they are tagged "invisible" too, and these
	// must not set a size, or as later tags they would win it back), and the
	// opening one leaves a band above the code for the label and the copy button.
	{"fencetop", map[string]any{"pixels-below-lines": 22, "paragraph-background": "rgba(128,128,128,0.14)"}},
	{"fenceend", map[string]any{"pixels-above-lines": 4, "paragraph-background": "rgba(128,128,128,0.14)"}},
	// Folded lines shrink to nothing, spacing included. Not GTK's invisible; see
	// createTags.
	{"folded", map[string]any{
		"scale": 0.001, "foreground": "rgba(0,0,0,0)",
		"pixels-above-lines": 0, "pixels-below-lines": 0, "pixels-inside-wrap": 0,
		"left-margin": 0, "indent": 0,
	}},
	{"foldhead", map[string]any{"foreground": "#9a9a9a"}},
}

// createRichTags defines the tags this file's blocks use.
func (e *Editor) createRichTags() {
	for _, d := range richTagDefs {
		e.newTag(d.name, d.props)
	}
}

// markerTag is the tag that takes a marker out of sight.
func markerTag() string {
	if hideMarkers {
		return "invisible"
	}
	return "marker"
}

// tagFolded tags a line that is folded away.
func (e *Editor) tagFolded(lineNum int, line string) {
	if line == "" {
		e.tagNewline("folded", lineNum)
		return
	}
	e.applyTag("folded", lineNum, 0, utf8.RuneCountInString(line))
}

// revealed says whether the caret is in the block, which shows its markers.
func revealed(b *richBlock, cursorLine int) bool {
	return b != nil && cursorLine >= b.first && cursorLine <= b.last
}

// tagCalloutLine tags a line of a callout: its lines are indented into the card,
// the "> " and the "[!type]" go (unless the caret is in the callout), the title is
// bold, and the rest is parsed as text. at is the caret's character offset in the
// line, or -1.
func (e *Editor) tagCalloutLine(lineNum int, line string, b *richBlock, reveal bool, at int, key *caretKey) {
	if b.folded && lineNum > b.first {
		e.tagFolded(lineNum, line)
		return
	}
	n := utf8.RuneCountInString(line)
	e.applyTag("callout", lineNum, 0, n)
	prefix := quotePrefix(line)
	if lineNum == b.first {
		prefix = b.titleOff
		e.applyTag("calloutfirst", lineNum, 0, n)
		if b.title == "" {
			e.applyTag("calloutbare", lineNum, 0, n)
		}
		e.applyTag("callouttitle", lineNum, prefix, n)
	}
	if lineNum == b.last || (b.folded && lineNum == b.first) {
		e.applyTag("calloutlast", lineNum, 0, n)
	}
	if reveal {
		e.applyTag("marker", lineNum, 0, min(prefix, n))
	} else {
		e.applyTag(markerTag(), lineNum, 0, min(prefix, n))
	}
	e.tagLineFrom(lineNum, line, prefix, at, key)
}

// tagLineFrom tags the text of a line from byte offset off, as if it were a line
// of its own: what follows a callout's or a quote's markers. off is in ASCII.
func (e *Editor) tagLineFrom(lineNum int, line string, off, at int, key *caretKey) {
	if off >= len(line) {
		return
	}
	rest := line[off:]
	if at >= 0 {
		at = max(at-off, 0)
	}
	for _, sp := range parseLine(rest, at, key) {
		e.applyTag(sp.tag, lineNum, sp.start+off, sp.end+off)
	}
	for _, sp := range linkSpansKey(rest, at, key) {
		e.applyTag(sp.tag, lineNum, sp.start+off, sp.end+off)
	}
}

// ---- Folding ---------------------------------------------------------------

// foldChanged draws again after a fold was made or undone. The text has not
// changed, but the blocks are worked out from it again and every line is retagged.
func (e *Editor) foldChanged() {
	e.rich.force = true
	e.fenceStale = true
	e.track.bad = true
	e.markAllDirty()
	e.scheduleReparse()
}

// toggleHeading folds or unfolds the heading on a line.
func (e *Editor) toggleHeading(line int) {
	text, ok := e.lineText(line)
	if !ok || headingLevel(text) == 0 {
		return
	}
	e.setMark(&e.rich.headMarks, line, -1)
	e.foldChanged()
}

// toggleCallout folds or unfolds the callout whose first line is line.
func (e *Editor) toggleCallout(line int) {
	e.setMark(&e.rich.flipMarks, line, -1)
	e.foldChanged()
}

// unfoldAtCaret opens whatever fold the caret, or a selection, has reached. Text
// nobody can see is no place for a caret: it is where find leaves it on a match,
// or where an arrow key takes it. And a selection that runs across a fold, with
// its ends on lines that are showing, would delete what was never seen with the
// next key. The insert mark and the selection's other end are both looked at.
func (e *Editor) unfoldAtCaret() {
	s := &e.rich
	if e.loading || (len(s.hides) == 0 && len(s.blocks) == 0) {
		return
	}
	ins := e.buffer.IterAtMark(e.buffer.GetInsert())
	bound := e.buffer.IterAtMark(e.buffer.SelectionBound())
	if ins == nil {
		return
	}
	lo, hi := ins.Line(), ins.Line()
	if bound != nil {
		lo, hi = min(lo, bound.Line()), max(hi, bound.Line())
	}
	changed := false
	for _, h := range s.hides {
		if h[1] >= lo && h[0] <= hi {
			e.setMark(&s.headMarks, h[0]-1, 0)
			changed = true
		}
	}
	for _, b := range s.blocks {
		if b.kind == kindCallout && b.folded && b.last >= lo && b.first+1 <= hi {
			e.setMark(&s.flipMarks, b.first, -1)
			changed = true
		}
	}
	if !changed {
		return
	}
	e.foldChanged()
	// A selection holds the render pass back (see reparse), and the next key could
	// delete what it covers. Once the button is up the fold is opened at once; while
	// a drag is on, the pass runs when it ends, which is before any key can.
	if e.buffer.HasSelection() && !e.buttonDown() {
		coreglib.IdleAdd(func() bool {
			e.forceReparse = true
			e.reparse()
			return false
		})
	}
}
