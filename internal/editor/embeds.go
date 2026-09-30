package editor

import (
	"errors"
	"html"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

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

// embedTable draws the rows of a table as aligned text, which is all an embed
// has room for: the delimiter row goes, and each column is padded to its widest
// cell.
func embedTable(rows []string) string {
	var cells [][]string
	var widths []int
	for _, r := range rows {
		if _, ok := parseDelimiter(r); ok {
			continue
		}
		c := splitRow(r)
		for i := range c {
			c[i] = cleanText(c[i])
			if i >= len(widths) {
				widths = append(widths, 0)
			}
			widths[i] = max(widths[i], utf8.RuneCountInString(c[i]))
		}
		cells = append(cells, c)
	}
	var b strings.Builder
	for n, c := range cells {
		var row strings.Builder
		for i, cell := range c {
			row.WriteString(cell)
			if i < len(c)-1 {
				row.WriteString(strings.Repeat(" ", widths[i]-utf8.RuneCountInString(cell)+3))
			}
		}
		if n > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("<tt>" + html.EscapeString(row.String()) + "</tt>")
	}
	return b.String()
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

// embedMarkup turns the text of a note (or section) into what an embed shows, as
// Pango markup: headings bold, inline emphasis kept, at most maxEmbedLines lines.
// A line that is itself an embed is shown as a link, which is what stops an embed
// from containing embeds. more says text was left out.
func embedMarkup(text string) (out string, more bool) {
	var b strings.Builder
	n := 0
	lines := strings.Split(strings.TrimSpace(clipRunes(text, maxEmbedBytes)), "\n")
	for i := 0; i < len(lines); i++ {
		l := clipRunes(lines[i], maxEmbedLine)
		if strings.HasPrefix(strings.TrimSpace(l), "![[") {
			l = strings.Replace(l, "![[", "[[", 1)
		}
		if strings.TrimSpace(l) == "" && b.Len() == 0 {
			continue
		}
		if n >= maxEmbedLines || b.Len() >= maxEmbedChars {
			return strings.TrimRight(b.String(), "\n"), true
		}
		switch {
		case isTableRow(l) && i+1 < len(lines) && isTableDelimiter(lines[i+1]):
			// A table: the run of rows it has, as aligned text.
			j := i
			for j+1 < len(lines) && isTableRow(lines[j+1]) {
				j++
			}
			rows := lines[i : j+1]
			b.WriteString(embedTable(rows))
			n += len(rows)
			i = j
		case headingLevel(l) > 0:
			b.WriteString("<b>" + cellMarkup(l[headingLevel(l)+1:]) + "</b>")
			n++
		case strings.HasPrefix(l, "> "):
			b.WriteString(cellMarkup(l[2:]))
			n++
		case isFenceLine(l) || strings.HasPrefix(strings.TrimSpace(l), "<!--"):
			continue
		default:
			b.WriteString(cellMarkup(l))
			n++
		}
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n"), false
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
	body string // the markup the body was last dressed with
}

// embedBox is the widget for one embed, kept for reuse (see imageBox).
type embedBox struct {
	box   *gtk.Box
	title *gtk.Label
	body  *gtk.Label
	ref   embedRef // what a click on the title opens
}

type embedState struct {
	items []*embedItem
	hits  []embedHit
	pool  []*embedBox
}

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
			e.view.Remove(it.box.box)
		}
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
	b.body = gtk.NewLabel("")
	b.body.SetXAlign(0)
	b.body.SetWrap(true)
	b.body.SetWrapMode(pango.WrapWordChar)
	b.body.SetUseMarkup(true)
	b.body.AddCSSClass("atlas-embed-body")
	b.box.Append(b.title)
	b.box.Append(b.body)
	return b
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
	body, more := "", false
	switch sec, ok := sectionOf(text, it.ref.heading); {
	case errors.Is(err, ErrProtected):
		body = "<i>Protected note</i>"
	case err != nil || e.ReadNote == nil:
		body = "<i>Note not found</i>"
	case !ok:
		body = "<i>Section not found</i>"
	default:
		body, more = embedMarkup(sec)
		if strings.TrimSpace(body) == "" {
			body = "<i>Empty</i>"
		}
	}
	if more {
		body += "\n…"
	}
	if body != it.body || b.body.Text() == "" {
		it.body = body
		b.body.SetMarkup(body)
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
		e.view.AddOverlay(bx, 0, tableParkY)
		it.shown, it.x, it.y = true, 0, tableParkY
	}
	bx.SetVisible(true)
	bx.SetSizeRequest(avail, -1)
	_, h, _, _ = bx.Measure(gtk.OrientationVertical, avail)
	bx.SetSizeRequest(avail, h)
	return avail, h
}
