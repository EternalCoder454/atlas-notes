package editor

import (
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-notes/internal/frontmatter"
)

// This file draws a note's YAML front matter as a card of properties.
//
// Like a table, the card is not part of the text. The lines between the two "---"
// stay in the buffer exactly as written, so the saved note and the index never
// learn about the card. Away from the caret they are hidden and a card is laid
// over the text view where they were, with blank space under the first line so the
// note's text starts below it (the placement is the tables' own, see block and
// placeBlocks). The caret in the front matter shows the raw lines and the card
// goes away, as with a table.
//
// Editing a value in the card writes back only the lines of that property, as one
// undo step (see editProp); everything else in the block, the other keys and their
// order, a comment, a nested map, is untouched. The reading and writing of the
// YAML is in package frontmatter, which needs no GTK.

// propsGap is the blank space left under the card.
const propsGap = 10

// propsItem is the card on show, as the block the placement pass puts under a
// line: the overlay bookkeeping for the first line, and how many lines the front
// matter takes.
type propsItem struct {
	overlay
	lines int
	w     gtk.Widgetter // the card's widget, nil until it is built
}

// propsState is the editor's front matter bookkeeping.
type propsState struct {
	doc *frontmatter.Doc // the note's front matter as of the last scan, or nil
	// hit is set when a render pass drew the front matter hidden, which is the card's
	// cue to be on show; on says it is. stale says the card shows something other
	// than doc.
	hit, on, stale bool
	item           propsItem
	card           *propsCard // built the first time it is needed
}

// inFront reports whether buffer line n is one of the front matter's lines, the
// fences included.
func (e *Editor) inFront(n int) bool {
	d := e.props.doc
	return d != nil && n >= 0 && n <= d.End
}

// sameProps reports whether two front matters would show the same card. Where the
// properties sit in the note does not matter to it, only what they say.
func sameProps(a, b []frontmatter.Prop) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if x.Key != y.Key || x.KeyRaw != y.KeyRaw || x.Kind != y.Kind || x.Value != y.Value ||
			x.Flow != y.Flow || !reflect.DeepEqual(x.Items, y.Items) ||
			(x.Kind == frontmatter.ReadOnly && !reflect.DeepEqual(x.Lines, y.Lines)) {
			return false
		}
	}
	return true
}

// scanProps reads the note's front matter again, from the text of the whole note.
// It runs when the text has changed (see refreshFence), and costs one comparison
// for a note without front matter. moved reports that the block's lines are not
// where they were, and hi how far the lines to draw again must reach.
func (e *Editor) scanProps(raw string) (hi int, moved bool) {
	s := &e.props
	old := s.doc
	var doc *frontmatter.Doc
	if frontmatter.Has(raw) {
		if d, ok := frontmatter.Parse(raw); ok {
			doc = d
		}
	}
	if old == nil && doc == nil {
		return 0, false
	}
	s.doc = doc
	oldEnd, newEnd := -1, -1
	if old != nil {
		oldEnd = old.End
	}
	if doc != nil {
		newEnd = doc.End
	}
	if old == nil || doc == nil || !sameProps(old.Props, doc.Props) {
		s.stale = true
	}
	return max(oldEnd, newEnd), oldEnd != newEnd
}

// widenForProps widens the lines a pass covers to the whole front matter when it
// touches any of it: the card is shown or hidden as one.
func (e *Editor) widenForProps(from, to int) (int, int) {
	if d := e.props.doc; d != nil && from <= d.End {
		return 0, max(to, d.End)
	}
	return from, to
}

// tagFrontLine tags a line of the front matter. With the caret in it, it is the
// raw text, in code. Without, it is folded away, and the first line notes the card
// for syncProps and gets its spacing back, which the pass has just stripped. at is
// the caret's character offset in the line, or -1.
func (e *Editor) tagFrontLine(lineNum int, line string, reveal bool, at int, key *caretKey) {
	s := &e.props
	if reveal {
		if n := utf8.RuneCountInString(line); n > 0 {
			e.applyTag("code", lineNum, 0, n)
		}
		if key != nil {
			// What caretPassNeeded will ask about this line, so that a caret moving
			// in the raw text does not draw it again for nothing.
			parseLine(line, at, key)
			linkSpansKey(line, at, key)
		}
		return
	}
	e.tagFolded(lineNum, line)
	if lineNum == 0 {
		s.hit = true
		if s.on && s.item.pad != "" {
			e.applyPad(&s.item.overlay, 0, s.item.pad)
		}
	}
}

// syncProps runs after the render pass has tagged lines from on. The card is
// shown when the pass drew the front matter hidden, taken away when it drew it as
// raw text, and dressed again when what it says has changed. A pass that did not
// reach the first line says nothing about the card.
func (e *Editor) syncProps(from int) {
	s := &e.props
	hit := s.hit
	s.hit = false
	switch {
	case s.doc == nil:
		if s.on {
			e.dropCard()
		}
		return
	case from > 0:
		if s.on && s.stale {
			e.dressCard()
		}
		return
	case !hit:
		if s.on {
			e.dropCard()
		}
		return
	}
	if !s.on {
		e.showCard()
	} else if s.stale {
		e.dressCard()
	}
	s.item.lines = s.doc.End + 1
	e.queuePlace()
}

// showCard puts the card on show for the front matter's first line.
func (e *Editor) showCard() {
	s := &e.props
	iter, ok := e.buffer.IterAtLine(0)
	if !ok {
		return
	}
	e.cardWidgets() // built on first use
	// Left gravity keeps the mark in front of anything typed at the start of the
	// line, so it stays on this line.
	s.item.mark = e.buffer.CreateMark("", iter, true)
	s.item.pad = ""
	s.on = true
	e.dressCard()
}

// dropCard takes the card off show. Its widget stays in the view, out of sight,
// for the next front matter.
func (e *Editor) dropCard() {
	s := &e.props
	s.on = false
	if s.card != nil {
		s.card.root.SetVisible(false)
	}
	if s.item.mark != nil && !s.item.mark.Deleted() {
		e.buffer.DeleteMark(s.item.mark)
	}
	s.item.mark, s.item.pad = nil, ""
}

// clearProps forgets the front matter. Opening another note replaces the text.
func (e *Editor) clearProps() {
	s := &e.props
	s.doc, s.hit, s.stale = nil, false, false
	if s.on {
		e.dropCard()
	}
}

// ---- Size and position -------------------------------------------------------

func (it *propsItem) over() *overlay { return &it.overlay }

func (it *propsItem) ready(avail int) bool { return avail > 0 }

func (it *propsItem) resize(e *Editor, avail int) { it.size(e, avail) }

func (it *propsItem) widget() gtk.Widgetter { return it.w }

// wantPad is the space the first line needs under it: the card's height, less what
// the front matter's other, hidden lines already take.
func (it *propsItem) wantPad(e *Editor, line, avail int) string {
	if avail <= 0 {
		return ""
	}
	_, h := it.size(e, avail)
	if h <= 0 {
		return ""
	}
	name, _ := tablePad(h + propsGap - e.linesBelow(line, it.lines))
	return name
}

// size fits the card to the view and gives it that size, as a table's grid is.
// The card is measured in the view, where its style is known, so it waits out of
// sight the first time.
func (it *propsItem) size(e *Editor, avail int) (w, h int) {
	root := it.w
	if !it.shown {
		e.view.AddOverlay(root, 0, tableParkY)
		it.shown, it.x, it.y = true, 0, tableParkY
	}
	b := gtk.BaseWidget(root)
	b.SetVisible(true)
	b.SetSizeRequest(-1, -1)
	minW, _, _, _ := b.Measure(gtk.OrientationHorizontal, -1)
	w = max(avail, minW, 1)
	_, h, _, _ = b.Measure(gtk.OrientationVertical, w)
	b.SetSizeRequest(w, h)
	return w, h
}

// ---- Editing -------------------------------------------------------------------

// editProp changes one property of the front matter: the one at index idx, which
// must still be called key. mutate is given it as it is in the buffer now, and the
// lines it comes to are written in place of the lines it had. Nothing else in the
// block is touched, and nothing is written when the lines come out the same.
func (e *Editor) editProp(idx int, key string, mutate func(p *frontmatter.Prop)) {
	doc, ok := frontmatter.Parse(e.rawText())
	if !ok || idx < 0 || idx >= len(doc.Props) || doc.Props[idx].Key != key || !doc.Props[idx].Editable() {
		return
	}
	p := doc.Props[idx]
	mutate(&p)
	lines := p.Render()
	if reflect.DeepEqual(lines, doc.Props[idx].Lines) {
		return
	}
	e.replaceLines(doc.Props[idx].Line, doc.Props[idx].End, lines, doc.EOL)
}

// removeProp takes a property out of the front matter.
func (e *Editor) removeProp(idx int, key string) {
	doc, ok := frontmatter.Parse(e.rawText())
	if !ok || idx < 0 || idx >= len(doc.Props) || doc.Props[idx].Key != key {
		return
	}
	e.replaceLines(doc.Props[idx].Line, doc.Props[idx].End, nil, doc.EOL)
}

// addProp adds a property at the end of the front matter, above its closing "---".
// It reports whether it did: a key that is there already is not added twice.
func (e *Editor) addProp(p frontmatter.Prop) bool {
	doc, ok := frontmatter.Parse(e.rawText())
	if !ok {
		return false
	}
	for _, q := range doc.Props {
		if strings.EqualFold(q.Key, p.Key) {
			return false
		}
	}
	e.replaceLines(doc.End, doc.End-1, p.Render(), doc.EOL)
	return true
}

// replaceLines writes lines in place of buffer lines first through last, both
// included (nothing is replaced when last < first, and the lines go in before
// first; no lines removes them), joined by eol, as a single step of the undo history. The lines'
// own text is swapped and the line breaks around it kept, so a caret on the line
// after them does not move.
func (e *Editor) replaceLines(first, last int, lines []string, eol string) {
	e.buffer.BeginUserAction()
	defer e.buffer.EndUserAction()
	switch {
	case last < first: // an insertion
		if at, ok := e.buffer.IterAtLine(first); ok {
			e.buffer.Insert(at, strings.Join(lines, eol)+eol)
		}
	case len(lines) == 0: // a removal, line breaks and all
		start, ok1 := e.buffer.IterAtLine(first)
		end, ok2 := e.buffer.IterAtLine(last + 1)
		if ok1 && ok2 {
			e.buffer.Delete(start, end)
		}
	default:
		n, ok := e.lineCharLen(last)
		start, ok1 := e.buffer.IterAtLineOffset(first, 0)
		end, ok2 := e.buffer.IterAtLineOffset(last, n)
		if !ok || !ok1 || !ok2 {
			return
		}
		e.buffer.Delete(start, end)
		if at, ok := e.buffer.IterAtLineOffset(first, 0); ok {
			text := strings.Join(lines, eol)
			if eol == "\r\n" {
				text += "\r" // the line's own ending, which is part of what was swapped
			}
			e.buffer.Insert(at, text)
		}
	}
}

// showFrontSource puts the caret in the front matter, which shows its raw text.
func (e *Editor) showFrontSource() {
	if e.props.doc == nil {
		return
	}
	line := min(1, e.props.doc.End)
	if iter, ok := e.buffer.IterAtLine(line); ok {
		e.buffer.PlaceCursor(iter)
		e.view.GrabFocus()
	}
}

// cardFocused reports whether the keyboard focus is in the card. Its entries live
// inside the text view, so the view's own key handlers, which run first, must
// leave those keys alone.
func (e *Editor) cardFocused() bool {
	c := e.props.card
	return e.props.on && c != nil && gtk.BaseWidget(c.root).FocusChild() != nil
}
