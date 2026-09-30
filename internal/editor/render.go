package editor

import (
	"sort"
	"strings"
	"unicode/utf8"
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
	key      string // the first line's text, which is what remembers a fold
	folded   bool   // a callout that is drawn folded
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
			b := richBlock{kind: kindQuote, first: i, last: j, key: lines[i]}
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
// the set of headings that are folded, by their text. It returns the hidden
// ranges (both ends included, in order, none inside another) and the folded
// headings' own lines.
func scanFolds(lines []string, fence []bool, folded map[string]bool) (hides [][2]int, heads map[int]bool) {
	for i := 0; i < len(lines); {
		lvl := headingLevel(lines[i])
		if lvl == 0 || inFence(fence, i) || !folded[strings.TrimSpace(lines[i])] {
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
	// calloutFlip holds the callouts the person has clicked, by first line: each
	// is drawn the opposite of how its "+" or "-" says. headFolded holds the folded
	// headings, by their text.
	calloutFlip map[string]bool
	headFolded  map[string]bool
	decos       [3][]*deco
	active      []*deco
	chev        *chevron
	rowsHidden  bool
	unsettled   bool
}

// scanRich works out the note's blocks and folded ranges again, from the text of
// the whole note. It runs when the text has changed (see refreshFence, which has
// the text at hand) or when a fold has. A note with no ">" at the start of a line,
// no fence and no fold has nothing to find, and knowing that costs a few
// substring searches.
func (e *Editor) scanRich(raw string) {
	s := &e.rich
	s.blocks, s.hides, s.heads = nil, nil, nil
	quotes := strings.HasPrefix(raw, ">") || strings.Contains(raw, "\n>")
	if !quotes && e.fence == nil && len(s.headFolded) == 0 {
		return
	}
	lines := strings.Split(raw, "\n")
	s.blocks = scanBlocks(lines, e.fence)
	for i := range s.blocks {
		b := &s.blocks[i]
		if b.kind == kindCallout {
			b.folded = (b.fold == '-') != s.calloutFlip[b.key]
		}
	}
	if len(s.headFolded) > 0 {
		s.hides, s.heads = scanFolds(lines, e.fence, s.headFolded)
	}
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
// caret entering or leaving a code block shows or hides its fences. With a
// heading folded, every pass covers the whole note; a fold is rare and the note's
// structure may have changed under it.
func (e *Editor) widenForRich(from, to, lastLine int) (int, int) {
	s := &e.rich
	if len(s.hides) > 0 {
		return 0, lastLine
	}
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
	s.calloutFlip, s.headFolded = nil, nil
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

// createRichTags defines the tags this file's blocks use.
func (e *Editor) createRichTags() {
	e.newTag("highlight", map[string]any{"background": "rgba(255,208,0,0.38)"})
	e.newTag("footref", map[string]any{"scale": 0.72, "rise": 5000, "foreground": "#62a0ea"})
	e.newTag("footdef", map[string]any{"scale": 0.9, "foreground": "#9a9a9a"})
	// A callout's lines sit inside its card: room on the left for the icon.
	e.newTag("callout", map[string]any{"left-margin": calloutIndent, "right-margin": 28, "pixels-below-lines": 2})
	e.newTag("calloutfirst", map[string]any{"pixels-above-lines": 12})
	e.newTag("calloutlast", map[string]any{"pixels-below-lines": 12})
	// A title that is only the type's name has no text to hold the header open, so
	// the line is given the height of the header the type's name is drawn in.
	e.newTag("calloutbare", map[string]any{"pixels-below-lines": 16})
	e.newTag("callouttitle", map[string]any{"weight": 700})
	// A code block's fences shrink away, and the opening one leaves a band for the
	// language label and the copy button.
	e.newTag("fencetop", map[string]any{
		"family": "monospace", "scale": 0.94, "pixels-below-lines": 22,
		"paragraph-background": "rgba(128,128,128,0.14)",
	})
	e.newTag("fenceend", map[string]any{
		"family": "monospace", "scale": 0.94, "pixels-above-lines": 4,
		"paragraph-background": "rgba(128,128,128,0.14)",
	})
	// Folded lines shrink to nothing, spacing included. Not GTK's invisible; see
	// createTags.
	e.newTag("folded", map[string]any{
		"scale": 0.001, "foreground": "rgba(0,0,0,0)",
		"pixels-above-lines": 0, "pixels-below-lines": 0, "pixels-inside-wrap": 0,
		"left-margin": 0, "indent": 0,
	})
	e.newTag("foldhead", map[string]any{"foreground": "#9a9a9a"})
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
	e.fenceStale = true
	e.markAllDirty()
	e.scheduleReparse()
}

// toggleHeading folds or unfolds the heading on a line.
func (e *Editor) toggleHeading(line int) {
	text, ok := e.lineText(line)
	if !ok || headingLevel(text) == 0 {
		return
	}
	s := &e.rich
	key := strings.TrimSpace(text)
	if s.headFolded[key] {
		delete(s.headFolded, key)
	} else {
		if s.headFolded == nil {
			s.headFolded = map[string]bool{}
		}
		s.headFolded[key] = true
	}
	e.foldChanged()
}

// toggleCallout folds or unfolds a callout.
func (e *Editor) toggleCallout(key string) {
	s := &e.rich
	if s.calloutFlip == nil {
		s.calloutFlip = map[string]bool{}
	}
	if s.calloutFlip[key] {
		delete(s.calloutFlip, key)
	} else {
		s.calloutFlip[key] = true
	}
	e.foldChanged()
}

// unfoldAtCaret opens whatever fold the caret has landed in. Text nobody can see
// is no place for a caret: it is where find leaves it on a match, or where an
// arrow key takes it.
func (e *Editor) unfoldAtCaret() {
	s := &e.rich
	if e.loading || (len(s.hides) == 0 && len(s.blocks) == 0) {
		return
	}
	ins := e.buffer.IterAtMark(e.buffer.GetInsert())
	if ins == nil {
		return
	}
	line := ins.Line()
	for _, h := range s.hides {
		if line >= h[0] && line <= h[1] {
			if text, ok := e.lineText(h[0] - 1); ok {
				delete(s.headFolded, strings.TrimSpace(text))
				e.foldChanged()
			}
			return
		}
	}
	if b := s.blockAt(line); b != nil && b.kind == kindCallout && b.folded && line > b.first {
		e.toggleCallout(b.key)
	}
}
