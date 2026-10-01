package editor

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"atlas-notes/internal/checklist"
	"atlas-notes/internal/markup"
)

// This file draws "![[Note]]" and "![[Note#Heading]]": the text of the other note
// (or of one section of it) in a bordered block under the line, read-only, with
// the note's name above it. It is laid over the text the way a picture is (see
// block in images.go), and the line itself stays in the buffer as written.
//
// What is drawn is text, not a nested editor, so an embed inside an embedded note
// is only its name and can never lead to another. The text is capped so that
// embedding a long note does not push everything else off the page.

const (
	// maxEmbedLines and maxEmbedChars cap what an embed shows.
	maxEmbedLines = 30
	maxEmbedChars = 3000
	// maxEmbedLine caps one line of an embedded note, and maxEmbedBytes the text
	// taken from it, so that a note that is one enormous line costs nothing to draw.
	maxEmbedLine  = 1000
	maxEmbedBytes = 64 << 10
)

// ErrProtected is what Editor.ReadNote returns for a note that is locked: an
// embed must not show in one note what the other keeps sealed.
var ErrProtected = errors.New("protected note")

// clipRunes cuts s to at most n bytes, at a character boundary.
func clipRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// embedRef is what an embed line names.
type embedRef struct {
	target  string
	heading string
}

// parseEmbed reads a line that is only "![[Target]]", "![[Target#Heading]]" or
// either with an "|alias", for a note. A picture's is not one: it is drawn as a
// picture (see images.go).
func parseEmbed(line string) (embedRef, bool) {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, "![[") || !strings.HasSuffix(t, "]]") {
		return embedRef{}, false
	}
	inner := t[3 : len(t)-2]
	if strings.ContainsAny(inner, "[]") {
		return embedRef{}, false
	}
	if i := strings.IndexByte(inner, '|'); i >= 0 {
		inner = inner[:i]
	}
	var r embedRef
	r.target, r.heading, _ = strings.Cut(inner, "#")
	r.target, r.heading = strings.TrimSpace(r.target), strings.TrimSpace(r.heading)
	if r.target == "" || markup.IsImagePath(r.target) {
		return embedRef{}, false
	}
	return r, true
}

// sectionOf returns the part of a note under the heading called heading (case
// aside): from the line after it to the next heading of the same or a higher
// level. ok is false when there is no such heading. An empty heading is the whole
// note.
func sectionOf(text, heading string) (string, bool) {
	if heading == "" {
		return text, true
	}
	lines := strings.Split(text, "\n")
	fence := markup.InCodeFence(text)
	for i, l := range lines {
		lvl := headingLevel(l)
		if lvl == 0 || inFence(fence, i) || !strings.EqualFold(strings.TrimSpace(l[lvl:]), heading) {
			continue
		}
		return strings.Join(lines[i+1:foldEnd(lines, fence, i)+1], "\n"), true
	}
	return "", false
}

// embedKind is what one block of an embed's body is.
type embedKind uint8

const (
	ekText  embedKind = iota // a paragraph, or a list item
	ekHead                   // a heading
	ekQuote                  // a quoted line
	ekTask                   // a task: a checkbox, its text and, if it has one, its due date
	ekCode                   // a fenced block, without its fences
	ekTable                  // a table
)

// embedBlock is one thing an embed shows.
type embedBlock struct {
	kind    embedKind
	level   int    // a heading's level
	indent  int    // pixels of indent, for a nested list item
	markup  string // Pango markup for a text, heading, quote or task
	checked bool
	due     string // a task's due date, "" for none
	code    string
	rows    [][]string // a table's rows, the header first
	align   []tableAlign
}

// embedBlocks reads the text of a note (or section) into what an embed shows:
// headings, lists, quotes, tasks (with their metadata read out of the line rather
// than shown), code without its fences, and tables as rows and columns. At most
// maxEmbedLines lines are taken. A line that is itself an embed is shown as a
// link, which is what stops an embed from containing embeds. more says text was
// left out.
func embedBlocks(text string) (out []embedBlock, more bool) {
	n, chars := 0, 0
	lines := strings.Split(strings.TrimSpace(clipRunes(text, maxEmbedBytes)), "\n")
	for i := 0; i < len(lines); i++ {
		l := clipRunes(lines[i], maxEmbedLine)
		if strings.TrimSpace(l) == "" {
			continue
		}
		if n >= maxEmbedLines || chars >= maxEmbedChars {
			return out, true
		}
		if isFenceLine(l) {
			// A code block: the lines up to the closing fence, as they are.
			var code []string
			for i++; i < len(lines) && !isFenceLine(lines[i]); i++ {
				if n >= maxEmbedLines || chars >= maxEmbedChars {
					more = true
					break
				}
				c := clipRunes(lines[i], maxEmbedLine)
				code = append(code, c)
				n++
				chars += len(c)
			}
			for i < len(lines) && !isFenceLine(lines[i]) {
				i++ // the rest of a block cut short is not read
			}
			if len(code) > 0 {
				out = append(out, embedBlock{kind: ekCode, code: strings.Join(code, "\n")})
			}
			if more {
				return out, true
			}
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(l), "![[") {
			l = strings.Replace(l, "![[", "[[", 1)
		}
		if strings.HasPrefix(strings.TrimSpace(l), "<!--") {
			continue
		}
		n++
		chars += len(l)
		if isTableRow(l) && i+1 < len(lines) && isTableDelimiter(lines[i+1]) {
			// A table: the run of rows it has.
			j := i
			for j+1 < len(lines) && isTableRow(lines[j+1]) {
				j++
			}
			if t, ok := parseTable(lines[i : j+1]); ok {
				if room := max(maxEmbedLines-(n-1), 1); len(t.rows) > room {
					t.rows, more = t.rows[:room], true
				}
				n += len(t.rows)
				chars += len(strings.Join(lines[i:j+1], ""))
				out = append(out, embedBlock{kind: ekTable, rows: t.rows, align: t.align})
				i += t.lines - 1
				if more {
					return out, true
				}
				continue
			}
		}
		out = append(out, embedLine(l))
	}
	return out, more
}

// embedLine reads one line that is not part of a code block or a table.
func embedLine(l string) embedBlock {
	body := strings.TrimLeft(l, " \t")
	width := 0
	for _, c := range l[:len(l)-len(body)] {
		if c == '\t' {
			width += 4
		} else {
			width++
		}
	}
	indent := min(width*4, 48)
	if it, ok := checklist.ParseLine(body); ok {
		m := linkedMarkup(it.Text)
		if it.Checked {
			m = "<s>" + m + "</s>"
		}
		return embedBlock{kind: ekTask, indent: indent, markup: m, checked: it.Checked, due: it.DueDate}
	}
	body = stripComments(body)
	switch lvl := headingLevel(body); {
	case lvl > 0:
		return embedBlock{kind: ekHead, level: lvl, markup: linkedMarkup(body[lvl+1:])}
	case strings.HasPrefix(body, ">"):
		return embedBlock{kind: ekQuote, markup: linkedMarkup(strings.TrimLeft(body[1:], " "))}
	}
	if m := bulletPrefix(body); m > 0 {
		return embedBlock{kind: ekText, indent: indent, markup: "•  " + linkedMarkup(body[m:])}
	}
	if m := numberPrefix(body); m > 0 {
		return embedBlock{kind: ekText, indent: indent, markup: escapeMarkup(body[:m]) + linkedMarkup(body[m:])}
	}
	return embedBlock{kind: ekText, markup: linkedMarkup(body)}
}

// stripComments takes HTML comments, where a task keeps its metadata, out of a line.
func stripComments(s string) string {
	for {
		a := strings.Index(s, "<!--")
		if a < 0 {
			return s
		}
		b := strings.Index(s[a:], "-->")
		if b < 0 {
			return s[:a]
		}
		s = strings.TrimRight(s[:a], " \t") + s[a+b+len("-->"):]
	}
}

// isTableDelimiter reports whether a line is a table's "|---|---|" row.
func isTableDelimiter(line string) bool {
	_, ok := parseDelimiter(line)
	return ok
}

// ---- State -----------------------------------------------------------------

type embedHit struct {
	line int
	ref  embedRef
}

// embedItem is one embed on show.
type embedItem struct {
	overlay
	ref  embedRef
	box  *embedBox
	body string // what the body was last dressed with, as text
}

// embedBox is the widget for one embed, kept for reuse (see imageBox).
type embedBox struct {
	box   *gtk.Box
	title *gtk.Label
	parts []*embedPart // the body: one widget per block, under the title
	ref   embedRef     // what a click on the title opens
}

// embedPart is the widget of one block of an embed's body, kept for reuse (see
// imageBox). Only the widget for its kind is set.
type embedPart struct {
	kind  embedKind
	label *gtk.Label // text, heading, quote, code
	class string     // the label's style class for its kind
	task  *embedTask
	grid  *embedGrid
}

// embedTask is a task's row: a checkbox nobody can click, the text, and the due
// date after it.
type embedTask struct {
	box  *gtk.Box
	cb   *gtk.CheckButton
	text *gtk.Label
	chip *gtk.Label
}

// embedGrid is a table's grid, drawn as the editor's own tables are but with no
// click on it.
type embedGrid struct {
	grid  *gtk.Grid
	cells []*gtk.Label
}

type embedState struct {
	items  []*embedItem
	hits   []embedHit
	pool   []*embedBox
	labels []*embedPart // parts kept for reuse, by the widget they hold
	tasks  []*embedPart
	grids  []*embedPart
}

// maxPooledParts caps each list of parts kept for reuse.
const maxPooledParts = 256

// clearEmbeds forgets every embed. Opening another note replaces the text.
func (e *Editor) clearEmbeds() {
	for _, it := range e.emb.items {
		e.dropEmbed(it)
	}
	e.emb.items, e.emb.hits = nil, e.emb.hits[:0]
}

// tagEmbedLine is the render pass's part for a line that may be an embed: away
// from the caret its Markdown is hidden, like a picture's, and the line is noted
// for syncEmbeds.
func (e *Editor) tagEmbedLine(lineNum int, line string, reveal bool) {
	ref, ok := parseEmbed(line)
	if !ok {
		return
	}
	e.emb.hits = append(e.emb.hits, embedHit{lineNum, ref})
	if !reveal {
		e.applyTag(markerTag(), lineNum, 0, utf8.RuneCountInString(line))
	}
	if it := e.embedAt(lineNum); it != nil && it.pad != "" {
		e.applyPad(&it.overlay, lineNum, it.pad)
	}
}

func (e *Editor) embedAt(line int) *embedItem {
	for _, it := range e.emb.items {
		if e.markLine(it.mark) == line {
			return it
		}
	}
	return nil
}

// syncEmbeds runs after the render pass has tagged lines [from, to], as
// syncTables does for tables.
func (e *Editor) syncEmbeds(from, to int) {
	s := &e.emb
	hits := s.hits
	s.hits = s.hits[:0]
	if len(s.items) == 0 && len(hits) == 0 {
		return
	}
	seen := map[int]bool{}
	kept := s.items[:0]
	for _, it := range s.items {
		line := e.markLine(it.mark)
		var h *embedHit
		for i := range hits {
			if hits[i].line == line {
				h = &hits[i]
			}
		}
		if line < 0 || seen[line] || (line >= from && line <= to && h == nil) {
			e.dropEmbed(it)
			continue
		}
		seen[line] = true
		if h != nil && !reflect.DeepEqual(h.ref, it.ref) {
			it.ref = h.ref
			e.dressEmbed(it)
		}
		kept = append(kept, it)
	}
	for i := len(kept); i < len(s.items); i++ {
		s.items[i] = nil
	}
	s.items = kept
	for _, h := range hits {
		if seen[h.line] {
			continue
		}
		iter, ok := e.buffer.IterAtLine(h.line)
		if !ok {
			continue
		}
		it := &embedItem{overlay: overlay{mark: e.buffer.CreateMark("", iter, true)}, ref: h.ref}
		it.box = e.takeEmbedBox()
		e.dressEmbed(it)
		s.items = append(s.items, it)
	}
	e.queuePlace()
}

func (e *Editor) dropEmbed(it *embedItem) {
	if it.box != nil {
		if it.shown {
			e.dropOverlay(it.box.box)
		}
		e.emptyEmbedBox(it.box)
		if len(e.emb.pool) < maxPooledBoxes {
			e.emb.pool = append(e.emb.pool, it.box)
		}
		it.box = nil
	}
	if !it.mark.Deleted() {
		e.buffer.DeleteMark(it.mark)
	}
}

func (e *Editor) takeEmbedBox() *embedBox {
	if n := len(e.emb.pool); n > 0 {
		b := e.emb.pool[n-1]
		e.emb.pool = e.emb.pool[:n-1]
		return b
	}
	b := &embedBox{}
	b.box = gtk.NewBox(gtk.OrientationVertical, 4)
	b.box.AddCSSClass("atlas-embed")
	b.title = gtk.NewLabel("")
	b.title.SetXAlign(0)
	b.title.AddCSSClass("atlas-embed-title")
	b.title.SetCursorFromName("pointer")
	click := gtk.NewGestureClick()
	click.ConnectPressed(func(_ int, _, _ float64) { click.SetState(gtk.EventSequenceClaimed) })
	click.ConnectReleased(func(_ int, _, _ float64) {
		if e.OnOpenNote != nil && b.ref.target != "" {
			e.OnOpenNote(b.ref.target, b.ref.heading)
		}
	})
	b.title.AddController(click)
	b.box.Append(b.title)
	return b
}

// ---- The body's widgets ----------------------------------------------------

// emptyEmbedBox takes the body out of an embed's widget, into the pools.
func (e *Editor) emptyEmbedBox(b *embedBox) {
	s := &e.emb
	for i, p := range b.parts {
		var list *[]*embedPart
		switch {
		case p.task != nil:
			b.box.Remove(p.task.box)
			list = &s.tasks
		case p.grid != nil:
			b.box.Remove(p.grid.grid)
			e.emptyEmbedGrid(p.grid)
			list = &s.grids
		default:
			b.box.Remove(p.label)
			p.label.SetText("")
			list = &s.labels
		}
		if len(*list) < maxPooledParts {
			*list = append(*list, p)
		}
		b.parts[i] = nil
	}
	b.parts = b.parts[:0]
}

// emptyEmbedGrid takes the cells out of a grid, into the tables' pool of labels.
func (e *Editor) emptyEmbedGrid(g *embedGrid) {
	for i, l := range g.cells {
		g.grid.Remove(l)
		l.SetText("")
		if len(e.tbl.labels) < maxPooledLabels {
			e.tbl.labels = append(e.tbl.labels, l)
		}
		g.cells[i] = nil
	}
	g.cells = g.cells[:0]
}

// embedClasses are the style classes a text block's label takes for its kind.
var embedClasses = map[embedKind]string{
	ekQuote: "atlas-embed-quote",
	ekCode:  "atlas-embed-code",
}

// takePart hands out the widget for a block, from the pool when it holds one.
func (e *Editor) takePart(k embedKind) *embedPart {
	s := &e.emb
	list := &s.labels
	switch k {
	case ekTask:
		list = &s.tasks
	case ekTable:
		list = &s.grids
	}
	var p *embedPart
	if n := len(*list); n > 0 {
		p = (*list)[n-1]
		(*list)[n-1] = nil
		*list = (*list)[:n-1]
	} else {
		p = &embedPart{}
		switch k {
		case ekTask:
			t := &embedTask{}
			t.box = gtk.NewBox(gtk.OrientationHorizontal, 6)
			t.box.AddCSSClass("atlas-embed-task")
			t.cb = gtk.NewCheckButton()
			t.cb.SetVAlign(gtk.AlignStart)
			// The box is a picture of the task's state: nothing can be clicked or
			// focused, so an embedded task is never changed from here.
			t.cb.SetCanTarget(false)
			t.cb.SetFocusable(false)
			t.text = newEmbedLabel()
			t.chip = gtk.NewLabel("")
			t.chip.AddCSSClass("due-chip")
			t.chip.SetVAlign(gtk.AlignStart)
			t.box.Append(t.cb)
			t.box.Append(t.text)
			t.box.Append(t.chip)
			p.task = t
		case ekTable:
			g := &embedGrid{grid: gtk.NewGrid()}
			g.grid.AddCSSClass("md-table")
			g.grid.SetRowSpacing(0)
			g.grid.SetColumnSpacing(0)
			g.grid.SetHAlign(gtk.AlignStart)
			p.grid = g
		default:
			p.label = newEmbedLabel()
		}
	}
	p.kind = k
	return p
}

func newEmbedLabel() *gtk.Label {
	l := gtk.NewLabel("")
	l.SetXAlign(0)
	l.SetWrap(true)
	l.SetWrapMode(pango.WrapWordChar)
	l.SetUseMarkup(true)
	l.AddCSSClass("atlas-embed-body")
	return l
}

// dressEmbedLabel sets a text label to show a block.
func dressEmbedLabel(l *gtk.Label, p *embedPart, blk embedBlock) {
	class := embedClasses[blk.kind]
	if blk.kind == ekHead {
		class = "atlas-embed-h" + strconv.Itoa(min(blk.level, 3))
	}
	if class != p.class {
		if p.class != "" {
			l.RemoveCSSClass(p.class)
		}
		if class != "" {
			l.AddCSSClass(class)
		}
		p.class = class
	}
	l.SetMarginStart(blk.indent)
	if blk.kind == ekCode {
		l.SetUseMarkup(false)
		l.SetText(blk.code)
		return
	}
	l.SetUseMarkup(true)
	l.SetMarkup(blk.markup)
}

// setEmbedBody fills an embed's widget with its blocks.
func (e *Editor) setEmbedBody(b *embedBox, blocks []embedBlock) {
	e.emptyEmbedBox(b)
	for _, blk := range blocks {
		p := e.takePart(blk.kind)
		switch {
		case p.task != nil:
			t := p.task
			t.box.SetMarginStart(blk.indent)
			t.cb.SetActive(blk.checked)
			t.text.SetMarkup(blk.markup)
			if blk.checked {
				t.text.AddCSSClass("atlas-embed-done")
			} else {
				t.text.RemoveCSSClass("atlas-embed-done")
			}
			dressDueChip(t.chip, blk.due)
			t.chip.SetVisible(blk.due != "")
			b.box.Append(t.box)
		case p.grid != nil:
			g := p.grid
			for r, row := range blk.rows {
				for c, cell := range row {
					l := e.takeLabel()
					(&tableCell{l: l}).dress(cell, blk.align[c], r == 0)
					g.grid.Attach(l, c, r, 1, 1)
					g.cells = append(g.cells, l)
				}
			}
			b.box.Append(g.grid)
		default:
			dressEmbedLabel(p.label, p, blk)
			b.box.Append(p.label)
		}
		b.parts = append(b.parts, p)
	}
}

// RefreshEmbeds reads the embedded notes again, for when the notes they show may
// have changed (a save, a sync, a rename). The app calls it where it refreshes
// backlinks.
func (e *Editor) RefreshEmbeds() {
	changed := false
	for _, it := range e.emb.items {
		if it.box == nil {
			continue
		}
		before := it.body
		e.dressEmbed(it)
		changed = changed || it.body != before
	}
	if changed {
		e.queuePlace()
	}
}

// dressEmbed fills an embed's widget from the note it names.
func (e *Editor) dressEmbed(it *embedItem) {
	b := it.box
	b.ref = it.ref
	name := it.ref.target
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	if it.ref.heading != "" {
		name += " > " + it.ref.heading
	}
	b.title.SetText(name)
	var text string
	var err error
	if e.ReadNote != nil {
		text, err = e.ReadNote(it.ref.target)
	}
	var blocks []embedBlock
	more := false
	note := func(msg string) { blocks = []embedBlock{{kind: ekText, markup: "<i>" + msg + "</i>"}} }
	switch sec, ok := sectionOf(text, it.ref.heading); {
	case errors.Is(err, ErrProtected):
		note("Protected note")
	case err != nil || e.ReadNote == nil:
		note("Note not found")
	case !ok:
		note("Section not found")
	default:
		blocks, more = embedBlocks(sec)
		if len(blocks) == 0 {
			note("Empty")
		}
	}
	if more {
		blocks = append(blocks, embedBlock{kind: ekText, markup: "…"})
	}
	// What is drawn is compared as text, so that an embed whose note has not changed
	// is not rebuilt.
	if body := fmt.Sprintf("%#v", blocks); body != it.body || len(b.parts) == 0 {
		it.body = body
		e.setEmbedBody(b, blocks)
	}
}

// ---- Size and position -----------------------------------------------------

func (it *embedItem) over() *overlay { return &it.overlay }

func (it *embedItem) widget() gtk.Widgetter {
	if it.box == nil {
		return nil
	}
	return it.box.box
}

func (it *embedItem) ready(avail int) bool { return avail > 0 }

func (it *embedItem) resize(e *Editor, avail int) { it.size(e, avail) }

func (it *embedItem) wantPad(e *Editor, _, avail int) string {
	if avail <= 0 {
		return ""
	}
	_, h := it.size(e, avail)
	name, _ := tablePad(h + tableGap)
	return name
}

// size gives the block the view's width and the height its text comes to when
// wrapped to it. Like a table's grid it is measured in the view, where its style
// is known, so one not yet in it waits out of sight first.
func (it *embedItem) size(e *Editor, avail int) (w, h int) {
	bx := it.box.box
	if !it.shown {
		e.addOverlay(bx, 0, tableParkY)
		it.shown, it.x, it.y = true, 0, tableParkY
	}
	bx.SetVisible(true)
	bx.SetSizeRequest(avail, -1)
	_, h, _, _ = bx.Measure(gtk.OrientationVertical, avail)
	bx.SetSizeRequest(avail, h)
	return avail, h
}
