package editor

import (
	"strings"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// The widgets that go with render.go's blocks. Like pictures and tables they are
// laid over the text view, and like them they are reused rather than rebuilt: the
// bindings keep every widget they wrap until the process ends, so a widget made
// per block would grow memory for as long as the app runs. Each kind has a list
// that only ever grows, and the blocks of a note take the first of them.

const (
	// calloutPad is the room inside a callout's card, left of its text, for the
	// icon; calloutIndent is the text's left margin, the view's own margin (16)
	// and that.
	calloutPad    = 40
	calloutIndent = 16 + calloutPad
	// quoteBarGap is how far the bar of a quote is left of its text.
	quoteBarGap = 10
)

// part is one widget of a deco and where it was last put.
type part struct {
	w     gtk.Widgetter
	shown bool // it has been added to the view as an overlay
	vis   bool // and is visible now
	x, y  int
	sw    int // the size it was last given
	sh    int
}

func (p *part) setVisible(v bool) {
	if p.w == nil || !p.shown || p.vis == v {
		return
	}
	gtk.BaseWidget(p.w).SetVisible(v)
	p.vis = v
}

// deco is the widgets of one block. main is a callout's card, a quote's bar or a
// code block's label and copy button; head is the callout's header, which is
// what a click folds it by.
type deco struct {
	e            *Editor
	kind         blockKind
	blk          richBlock
	reveal       bool
	hidden       bool
	main, head   part
	class        string
	icon, chevro *gtk.Image
	title, lang  *gtk.Label
	dressed      bool // blk and reveal are what the widgets were last dressed with
}

func (d *deco) setVisible(v bool) {
	d.main.setVisible(v)
	d.head.setVisible(v)
}

// takeDeco hands out the nth deco of a kind, making it when there are not that
// many.
func (e *Editor) takeDeco(k blockKind, n int) *deco {
	list := e.rich.decos[k]
	if n < len(list) {
		return list[n]
	}
	d := &deco{e: e, kind: k}
	switch k {
	case kindQuote:
		bar := gtk.NewBox(gtk.OrientationVertical, 0)
		bar.AddCSSClass("atlas-quote-bar")
		bar.SetCanTarget(false)
		d.main.w = bar
	case kindCallout:
		card := gtk.NewBox(gtk.OrientationVertical, 0)
		card.AddCSSClass("atlas-callout")
		card.SetCanTarget(false)
		d.main.w = card
		head := gtk.NewBox(gtk.OrientationHorizontal, 0)
		head.AddCSSClass("atlas-callout-head")
		d.icon = gtk.NewImage()
		d.icon.SetPixelSize(16)
		d.icon.SetMarginStart(calloutPad - 26)
		d.icon.AddCSSClass("atlas-callout-icon")
		d.title = gtk.NewLabel("")
		d.title.SetXAlign(0)
		d.title.SetHExpand(true)
		d.title.SetMarginStart(10)
		d.title.AddCSSClass("atlas-callout-title")
		d.chevro = gtk.NewImage()
		d.chevro.SetPixelSize(14)
		d.chevro.SetMarginEnd(14)
		d.chevro.AddCSSClass("atlas-callout-chevron")
		head.Append(d.icon)
		head.Append(d.title)
		head.Append(d.chevro)
		head.SetCursorFromName("pointer")
		click := gtk.NewGestureClick()
		// The press is claimed, so the view does not also put the caret in the title
		// (which would show its markers) for the click that folded the callout.
		click.ConnectPressed(func(_ int, _, _ float64) { click.SetState(gtk.EventSequenceClaimed) })
		click.ConnectReleased(func(_ int, _, _ float64) { e.toggleCallout(d.blk.first) })
		head.AddController(click)
		d.head.w = head
	case kindCode:
		box := gtk.NewBox(gtk.OrientationHorizontal, 4)
		box.AddCSSClass("atlas-code-tools")
		d.lang = gtk.NewLabel("")
		d.lang.AddCSSClass("atlas-code-lang")
		btn := gtk.NewButton()
		btn.SetIconName("atlasnotes-copy-symbolic")
		btn.AddCSSClass("flat")
		btn.AddCSSClass("atlas-code-copy")
		btn.SetFocusOnClick(false)
		btn.SetTooltipText("Copy code")
		btn.ConnectClicked(func() { e.copyBlockText(d.blk.first) })
		box.Append(d.lang)
		box.Append(btn)
		d.main.w = box
	}
	e.rich.decos[k] = append(list, d)
	return d
}

// dress gives a deco the block it is to draw.
func (d *deco) dress() {
	b := d.blk
	switch d.kind {
	case kindCallout:
		class := calloutClass(b.typ)
		if class != d.class {
			w := gtk.BaseWidget(d.main.w)
			if d.class != "" {
				w.RemoveCSSClass("callout-" + d.class)
				gtk.BaseWidget(d.head.w).RemoveCSSClass("callout-" + d.class)
			}
			w.AddCSSClass("callout-" + class)
			gtk.BaseWidget(d.head.w).AddCSSClass("callout-" + class)
			d.class = class
			d.icon.SetFromIconName(calloutIcon(class))
		}
		if b.title == "" {
			d.title.SetText(calloutTitle(b))
		} else {
			d.title.SetText("")
		}
		if b.folded {
			d.chevro.SetFromIconName("atlasnotes-chevron-right-symbolic")
		} else {
			d.chevro.SetFromIconName("atlasnotes-chevron-down-symbolic")
		}
		// With the caret in it the callout shows its markers and is being edited, so
		// its header lets clicks through to the text.
		gtk.BaseWidget(d.head.w).SetCanTarget(!d.reveal)
	case kindCode:
		// A long language name is cut short, so it cannot push the copy button out
		// of the block.
		lang := []rune(b.lang)
		if strings.EqualFold(b.lang, "mermaid") {
			lang = []rune("Mermaid diagram") // there is no renderer, so say what it is
		}
		if len(lang) > 16 {
			lang = append(lang[:15], '…')
		}
		d.lang.SetText(string(lang))
	}
}

// syncRich runs after the render pass: every block gets a deco, and the extra
// ones are put away.
func (e *Editor) syncRich(revealLine int) {
	s := &e.rich
	s.active = s.active[:0]
	var used [3]int
	for i := range s.blocks {
		b := s.blocks[i]
		d := e.takeDeco(b.kind, used[b.kind])
		used[b.kind]++
		reveal := revealed(&b, revealLine)
		d.hidden = s.hiddenAt(b.first)
		// The widgets are dressed again only when what they show has changed, not
		// on every pass: setting an icon or a label is a call into GTK each.
		if !d.dressed || d.blk != b || d.reveal != reveal {
			d.blk, d.reveal, d.dressed = b, reveal, true
			d.dress()
		}
		s.active = append(s.active, d)
	}
	for k, list := range s.decos {
		for j := used[k]; j < len(list); j++ {
			list[j].setVisible(false)
		}
	}
	e.queuePlace()
	e.refreshChevron()
}

func (e *Editor) putPart(p *part, x, y, w, h int) {
	if w != p.sw || h != p.sh {
		if w > 0 || h > 0 {
			gtk.BaseWidget(p.w).SetSizeRequest(w, h)
		}
		p.sw, p.sh = w, h
	}
	switch {
	case !p.shown:
		e.view.AddOverlay(p.w, x, y)
		p.shown, p.vis, p.x, p.y = true, true, x, y
	case x != p.x || y != p.y:
		e.view.MoveOverlay(p.w, x, y)
		p.x, p.y = x, y
	}
	p.setVisible(true)
}

// nearLines is the range of lines in or near the viewport.
func (e *Editor) nearLines() (l0, l1 int) {
	adj := e.scroll.VAdjustment()
	vTop := int(adj.Value())
	vEnd := vTop + int(adj.PageSize())
	near := 800
	l0, l1 = 0, e.buffer.LineCount()
	if it, _ := e.view.IterAtLocation(0, max(vTop-near, 0)); it != nil {
		l0 = it.Line()
	}
	if it, _ := e.view.IterAtLocation(0, vEnd+near); it != nil {
		l1 = it.Line()
	}
	return l0, l1
}

// placeDecor puts every block's widgets where the text has put the block. It
// reports whether the layout was not ready, so that the pass is tried again.
func (e *Editor) placeDecor() (unsettled bool) {
	s := &e.rich
	if len(s.active) == 0 {
		return false
	}
	width := e.view.Width()
	if width <= 0 {
		return true
	}
	// Only blocks near the viewport are put in place: laying out a long note's
	// every callout on each pass is work for widgets nobody is looking at. A
	// scroll places the ones that have come near (see installRender).
	l0, l1 := e.nearLines()
	for _, d := range s.active {
		if d.hidden || (d.kind == kindCode && d.reveal) {
			d.setVisible(false)
			continue
		}
		if d.blk.last < l0 || d.blk.first > l1 {
			continue
		}
		fi, ok1 := e.buffer.IterAtLine(d.blk.first)
		li, ok2 := e.buffer.IterAtLine(d.blk.last)
		if !ok1 || !ok2 {
			continue
		}
		top, fh := e.view.LineYrange(fi)
		ltop, lh := e.view.LineYrange(li)
		if fh <= 0 || lh <= 0 {
			unsettled = true
			continue
		}
		bottom := ltop + lh
		xt := e.view.IterLocation(fi).X()
		switch d.kind {
		case kindQuote:
			e.putPart(&d.main, xt-quoteBarGap, top, 3, bottom-top)
		case kindCallout:
			if d.blk.folded {
				bottom = top + fh
			}
			x, w := xt-calloutPad, width-2*(xt-calloutPad)
			e.putPart(&d.main, x, top, w, bottom-top)
			e.putPart(&d.head, x, top, w, fh)
		case kindCode:
			_, nat, _, _ := gtk.BaseWidget(d.main.w).Measure(gtk.OrientationHorizontal, -1)
			e.putPart(&d.main, width-e.view.RightMargin()-nat-4, top+2, 0, 0)
		}
	}
	return unsettled
}

// copyBlockText puts the text of the code block that starts at line first, without
// its fences, on the clipboard. The block is found again as it is now: the note
// may have been edited since the button was drawn. The buffer's text is used, not
// its slice, so a checkbox's placeholder character cannot come along.
func (e *Editor) copyBlockText(first int) {
	b := e.rich.blockAt(first)
	if b == nil || b.kind != kindCode {
		return
	}
	last := b.last
	if b.closed {
		last--
	}
	if last < b.first+1 {
		return
	}
	start, ok1 := e.buffer.IterAtLine(b.first + 1)
	end, ok2 := e.buffer.IterAtLine(last)
	if !ok1 || !ok2 {
		return
	}
	if !end.EndsLine() {
		end.ForwardToLineEnd()
	}
	e.view.Clipboard().SetText(e.buffer.Text(start, end, false))
}

// ---- The heading chevron ---------------------------------------------------

// chevron is the button beside a heading that folds it. There is one, moved to
// the heading the pointer is over.
type chevron struct {
	btn    *gtk.Button
	part   part
	line   int
	folded bool
	over   bool // the pointer is on the button
	inView bool // the pointer is over the text
}

func (c *chevron) hide() {
	c.part.setVisible(false)
	c.line = -1
}

// installRender connects what the block widgets need from the view: the pointer
// over a heading, and the caret landing in folded text.
func (e *Editor) installRender() {
	s := &e.rich
	e.scroll.VAdjustment().NotifyProperty("value", func() {
		if (len(s.active) == 0 && len(e.items) == 0) || s.decoQueued {
			return
		}
		s.decoQueued = true
		coreglib.IdleAdd(func() bool {
			s.decoQueued = false
			e.placeDecor()
			e.placeChips()
			return false
		})
	})
	c := &chevron{line: -1}
	s.chev = c
	c.btn = gtk.NewButton()
	c.btn.AddCSSClass("flat")
	c.btn.AddCSSClass("atlas-fold-chevron")
	c.btn.SetFocusOnClick(false)
	c.btn.SetTooltipText("Fold or unfold")
	c.btn.ConnectClicked(func() {
		if c.line >= 0 {
			e.toggleHeading(c.line)
		}
	})
	c.part.w = c.btn
	bm := gtk.NewEventControllerMotion()
	bm.ConnectEnter(func(_, _ float64) { c.over = true })
	bm.ConnectLeave(func() {
		c.over = false
		e.leaveHeading()
	})
	c.btn.AddController(bm)

	m := gtk.NewEventControllerMotion()
	m.ConnectMotion(func(x, y float64) {
		c.inView = true
		bx, by := e.view.WindowToBufferCoords(gtk.TextWindowWidget, int(x), int(y))
		// The second result is not reliable for this call, so only the iterator is
		// looked at.
		iter, _ := e.view.IterAtLocation(bx, by)
		if iter == nil {
			return
		}
		e.hoverHeading(iter.Line(), false)
	})
	m.ConnectLeave(func() {
		c.inView = false
		e.leaveHeading()
	})
	e.view.AddController(m)
}

// hoverHeading shows the chevron beside line when it is a heading, and takes it
// away when it is not.
func (e *Editor) hoverHeading(line int, force bool) {
	c := e.rich.chev
	if !force && (c.line == line || c.over) {
		return
	}
	text, ok := e.lineText(line)
	if !ok || headingLevel(text) == 0 || inFence(e.fence, line) {
		c.hide()
		return
	}
	iter, ok := e.buffer.IterAtLine(line)
	if !ok {
		return
	}
	top, h := e.view.LineYrange(iter)
	c.line = line
	c.folded = e.rich.heads[line]
	if c.folded {
		c.btn.SetIconName("atlasnotes-chevron-right-symbolic")
	} else {
		c.btn.SetIconName("atlasnotes-chevron-down-symbolic")
	}
	e.putPart(&c.part, 0, top+max(h-22, 0), 18, 22)
}

// refreshChevron draws the chevron again for the heading it is beside, after a
// fold has changed its direction or the lines have moved.
func (e *Editor) refreshChevron() {
	if c := e.rich.chev; c != nil && c.line >= 0 {
		e.hoverHeading(c.line, true)
	}
}

// leaveHeading takes the chevron away a moment after the pointer has left both
// the text and the button. The pointer crosses from one to the other, and the
// button must not vanish under it on the way.
func (e *Editor) leaveHeading() {
	c := e.rich.chev
	coreglib.TimeoutAdd(150, func() bool {
		if !c.over && !c.inView {
			c.hide()
		}
		return false
	})
}

// hideFoldedRows keeps the checklist rows of folded lines out of sight: a row is
// a widget in the text, which shrinking the line's text does nothing to.
func (e *Editor) hideFoldedRows() {
	s := &e.rich
	active := len(s.hides) > 0
	for _, b := range s.blocks {
		active = active || b.folded
	}
	if !active && !s.rowsHidden {
		return
	}
	s.rowsHidden = active
	for _, it := range e.items {
		if it.anchor == nil || it.row == nil || it.anchor.Deleted() {
			continue
		}
		if iter := e.buffer.IterAtChildAnchor(it.anchor); iter != nil {
			it.row.box.SetVisible(!s.foldedAt(iter.Line()))
		}
	}
}
