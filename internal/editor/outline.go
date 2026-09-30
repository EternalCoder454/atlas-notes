package editor

import (
	"slices"
	"sort"
	"strings"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
)

// This file draws the outline rail: a thin column of small marks at the right edge
// of the page, one per heading and indented by level, with the section being read
// in the accent colour. Hovering it opens a list of the headings' names, and
// clicking one scrolls there, opening the fold it is in.
//
// The rail is laid over the page, inside the editor's own area, and is not part of
// the text. The headings are found while the render pass has the note's text at
// hand (see scanOutline), and the widgets are touched only when that list has
// changed, so typing in a paragraph costs a walk over the note's line starts and
// nothing more.

const (
	// outlineMin is how many headings a note needs before it has a rail.
	outlineMin = 3
	// outlineMaxMarks is the most marks the rail draws. A note with more headings
	// than that shows only its shallower ones; the list still holds them all.
	outlineMaxMarks = 60
	// outlineHoverMs is how long the pointer rests on the rail before the list
	// opens, and outlineLeaveMs how long it may be away from both before it closes.
	outlineHoverMs = 120
	outlineLeaveMs = 250
)

// markWidths is how wide the mark of each heading level is: the deeper the
// heading, the shorter its mark, so the marks step in from the page's edge.
var markWidths = [...]int{18, 14, 11, 9, 8, 7}

// heading is one heading of the note.
type heading struct {
	line, level int
	text        string
}

// outlineState is the note's headings and the rail's widgets.
type outlineState struct {
	heads   []heading
	scratch []heading // where the next scan collects, swapped in when it differs
	rail    *outlineRail
	// cur is the heading being read (an index into heads, -1 before the first).
	cur    int
	queued bool // a look at the scroll position is waiting for an idle moment
}

// outlineItem is one entry of the list.
type outlineItem struct {
	btn *gtk.Button
	lbl *gtk.Label
}

// outlineRail is the rail's widgets.
type outlineRail struct {
	frame *gtk.Overlay
	box   *gtk.Box
	marks []*gtk.Box
	shown []int // the heading each visible mark stands for
	lit   *gtk.Box
	pop   *gtk.Popover
	list  *gtk.Box
	items []outlineItem
	// listStale says the list holds other headings than the note has now.
	listStale     bool
	inRail, inPop bool
	pinned        bool // the list was opened by the command, not by hovering
	hoverGen      uint64
	levelsOfMarks []int
}

// scanOutline finds the note's headings, from the text of the whole note. It runs
// when the text has changed (see refreshFence). Only a heading list that differs
// from the last one touches the rail.
func (e *Editor) scanOutline(raw string) {
	s := &e.outline
	next := s.scratch[:0]
	for pos, line := 0, 0; pos <= len(raw); line++ {
		end := len(raw)
		if j := strings.IndexByte(raw[pos:], '\n'); j >= 0 {
			end = pos + j
		}
		if raw[pos:end] != "" && raw[pos] == '#' {
			l := raw[pos:end]
			if lvl := headingLevel(l); lvl > 0 && !inFence(e.fence, line) && !e.inFront(line) {
				if t := headingText(l[lvl+1:]); t != "" {
					next = append(next, heading{line, lvl, t})
				}
			}
		}
		pos = end + 1
	}
	s.scratch = next
	if slices.Equal(next, s.heads) {
		return
	}
	s.heads, s.scratch = next, s.heads
	e.outlineChanged()
}

// patchOutline brings the heading list up to date after an edit inside line ln,
// whose text is now l, when no other line changed: the same result as scanOutline,
// for the cost of one line. The caller has seen that the line is not in a code
// block or the front matter.
func (e *Editor) patchOutline(ln int, l string) {
	s := &e.outline
	i, found := sort.Find(len(s.heads), func(i int) int { return ln - s.heads[i].line })
	var h heading
	ok := false
	if l != "" && l[0] == '#' {
		if lvl := headingLevel(l); lvl > 0 {
			if t := headingText(l[lvl+1:]); t != "" {
				h, ok = heading{ln, lvl, t}, true
			}
		}
	}
	if !found && !ok {
		return
	}
	if found && ok && s.heads[i] == h {
		return
	}
	next := append(s.scratch[:0], s.heads...)
	switch {
	case found && ok:
		next[i] = h
	case found:
		next = append(next[:i], next[i+1:]...)
	default:
		next = append(next, heading{})
		copy(next[i+1:], next[i:])
		next[i] = h
	}
	s.heads, s.scratch = next, s.heads
	e.outlineChanged()
}

var headingClean = strings.NewReplacer("**", "", "__", "", "`", "", "[[", "", "]]", "")

// headingText is what a heading says, without its closing "#"s and the emphasis
// marks that would only clutter a list.
func headingText(s string) string {
	s = strings.TrimSpace(s)
	if t := strings.TrimRight(s, "#"); t != s && (t == "" || t[len(t)-1] == ' ') {
		s = strings.TrimSpace(t)
	}
	return strings.TrimSpace(headingClean.Replace(s))
}

// clearOutline forgets the headings. Opening another note replaces the text.
func (e *Editor) clearOutline() {
	s := &e.outline
	if len(s.heads) == 0 {
		return
	}
	s.heads = s.heads[:0]
	e.outlineChanged()
}

// installOutline builds the rail and puts the editor's page in the frame it is
// laid over. It runs once, from New.
func (e *Editor) installOutline() {
	r := &outlineRail{}
	e.outline.rail = r
	e.outline.cur = -1
	r.frame = gtk.NewOverlay()
	r.frame.SetChild(e.scroll)
	r.frame.SetHExpand(true)
	r.frame.SetVExpand(true)

	r.box = gtk.NewBox(gtk.OrientationVertical, 0)
	r.box.AddCSSClass("atlas-outline-rail")
	r.box.SetHAlign(gtk.AlignEnd)
	r.box.SetVAlign(gtk.AlignCenter)
	r.box.SetMarginEnd(18) // clear of the overlay scrollbar, which must stay draggable
	r.box.SetVisible(false)
	r.frame.AddOverlay(r.box)

	r.pop = gtk.NewPopover()
	r.pop.SetParent(r.box)
	r.pop.SetPosition(gtk.PosLeft)
	r.pop.SetHasArrow(false)
	r.pop.SetAutohide(false)
	r.pop.AddCSSClass("atlas-outline-pop")
	sw := gtk.NewScrolledWindow()
	sw.SetPropagateNaturalHeight(true)
	sw.SetPropagateNaturalWidth(true)
	sw.SetMaxContentHeight(380)
	sw.SetMaxContentWidth(320)
	sw.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	r.list = gtk.NewBox(gtk.OrientationVertical, 0)
	sw.SetChild(r.list)
	r.pop.SetChild(sw)

	m := gtk.NewEventControllerMotion()
	m.ConnectEnter(func(_, _ float64) {
		r.inRail = true
		e.openSoon()
	})
	m.ConnectLeave(func() {
		r.inRail = false
		e.closeSoon()
	})
	r.box.AddController(m)
	pm := gtk.NewEventControllerMotion()
	pm.ConnectEnter(func(_, _ float64) { r.inPop = true })
	pm.ConnectLeave(func() {
		r.inPop = false
		e.closeSoon()
	})
	sw.AddController(pm)

	// The popover is parented to the rail, and GTK complains of a child left behind
	// when that is finalized, so it is let go of first.
	r.frame.ConnectDestroy(func() {
		r.pop.Unparent()
	})

	e.scroll.VAdjustment().NotifyProperty("value", e.queueOutlineCurrent)
}

// outlineChanged brings the rail in line with the headings.
func (e *Editor) outlineChanged() {
	s := &e.outline
	r := s.rail
	if r == nil {
		return
	}
	r.listStale = true
	if len(s.heads) < outlineMin {
		r.box.SetVisible(false)
		r.pop.Popdown()
		r.pinned = false
		r.shown, r.levelsOfMarks = r.shown[:0], r.levelsOfMarks[:0]
		s.cur = -1
		return
	}
	// Which headings get a mark: all of them, or when there are too many the
	// shallowest ones that fit.
	maxLevel := 6
	for ; maxLevel > 1; maxLevel-- {
		n := 0
		for _, h := range s.heads {
			if h.level <= maxLevel {
				n++
			}
		}
		if n <= outlineMaxMarks {
			break
		}
	}
	shown := r.shown[:0]
	var levels []int
	for i, h := range s.heads {
		if h.level <= maxLevel {
			shown = append(shown, i)
			levels = append(levels, h.level)
		}
	}
	r.shown = shown
	if !slices.Equal(levels, r.levelsOfMarks) {
		r.levelsOfMarks = levels
		for len(r.marks) < len(shown) {
			mk := gtk.NewBox(gtk.OrientationHorizontal, 0)
			mk.AddCSSClass("atlas-outline-mark")
			mk.SetHAlign(gtk.AlignEnd)
			r.marks = append(r.marks, mk)
			r.box.Append(mk)
		}
		for i, mk := range r.marks {
			if i >= len(shown) {
				mk.SetVisible(false)
				continue
			}
			mk.SetVisible(true)
			mk.SetSizeRequest(markWidths[min(levels[i], 6)-1], -1)
		}
	}
	r.box.SetVisible(true)
	if r.pop.IsVisible() {
		e.fillOutlineList()
	}
	s.cur = -2 // not known: the next look sets it whatever it finds
	e.queueOutlineCurrent()
}

// queueOutlineCurrent looks at where the page is scrolled to, once things have
// settled, and lights the mark of the section there.
func (e *Editor) queueOutlineCurrent() {
	s := &e.outline
	if s.queued || len(s.heads) < outlineMin {
		return
	}
	s.queued = true
	coreglib.IdleAdd(func() bool {
		s.queued = false
		e.updateOutlineCurrent()
		return false
	})
}

// updateOutlineCurrent works out which section the top of the page is in.
func (e *Editor) updateOutlineCurrent() {
	s := &e.outline
	r := s.rail
	if r == nil || len(s.heads) < outlineMin {
		return
	}
	y := int(e.scroll.VAdjustment().Value()) + 16
	line := 0
	if it, _ := e.view.IterAtLocation(0, y); it != nil {
		line = it.Line()
	}
	cur := -1
	for i, h := range s.heads {
		if h.line > line {
			break
		}
		cur = i
	}
	// The end of the note is in the last section, and a page too short to scroll
	// never leaves the first one.
	if adj := e.scroll.VAdjustment(); adj.Value()+adj.PageSize() >= adj.Upper()-2 && adj.Value() > 0 {
		cur = len(s.heads) - 1
	}
	if cur == s.cur {
		return
	}
	s.cur = cur
	if r.lit != nil {
		r.lit.RemoveCSSClass("current")
		r.lit = nil
	}
	// The mark of the heading, or of the last one that has a mark before it.
	for k := len(r.shown) - 1; k >= 0; k-- {
		if r.shown[k] <= cur {
			r.lit = r.marks[k]
			r.lit.AddCSSClass("current")
			break
		}
	}
	if r.pop.IsVisible() {
		e.markOutlineItem()
	}
}

// fillOutlineList gives the list a button for each heading. The buttons are kept
// and reused; the ones the note has no heading for are hidden.
func (e *Editor) fillOutlineList() {
	s := &e.outline
	r := s.rail
	r.listStale = false
	for len(r.items) < len(s.heads) {
		i := len(r.items)
		btn := gtk.NewButton()
		btn.AddCSSClass("flat")
		btn.AddCSSClass("atlas-outline-item")
		btn.SetFocusOnClick(false)
		lbl := gtk.NewLabel("")
		lbl.SetXAlign(0)
		lbl.SetEllipsize(pango.EllipsizeEnd)
		lbl.SetMaxWidthChars(36)
		btn.SetChild(lbl)
		btn.ConnectClicked(func() {
			e.gotoHeading(i)
			r.pop.Popdown()
			r.pinned = false
		})
		r.items = append(r.items, outlineItem{btn, lbl})
		r.list.Append(btn)
	}
	for i, it := range r.items {
		if i >= len(s.heads) {
			it.btn.SetVisible(false)
			continue
		}
		h := s.heads[i]
		it.btn.SetVisible(true)
		it.lbl.SetText(h.text)
		it.lbl.SetMarginStart((h.level - 1) * 12)
		it.btn.SetTooltipText(h.text)
	}
	e.markOutlineItem()
}

// markOutlineItem shows which entry of the list is the section being read.
func (e *Editor) markOutlineItem() {
	s := &e.outline
	for i, it := range s.rail.items {
		if i == s.cur {
			it.btn.AddCSSClass("current")
		} else {
			it.btn.RemoveCSSClass("current")
		}
	}
}

// openSoon opens the list once the pointer has rested on the rail.
func (e *Editor) openSoon() {
	r := e.outline.rail
	r.hoverGen++
	gen := r.hoverGen
	coreglib.TimeoutAdd(outlineHoverMs, func() bool {
		if gen == r.hoverGen && r.inRail && !r.pop.IsVisible() && len(e.outline.heads) >= outlineMin {
			e.popOutline(false)
		}
		return false
	})
}

// closeSoon closes a list that hovering opened, a moment after the pointer has
// left both the rail and the list. It crosses from one to the other, and the list
// must not vanish under it on the way.
func (e *Editor) closeSoon() {
	r := e.outline.rail
	r.hoverGen++
	gen := r.hoverGen
	coreglib.TimeoutAdd(outlineLeaveMs, func() bool {
		if gen == r.hoverGen && !r.inRail && !r.inPop && !r.pinned {
			r.pop.Popdown()
		}
		return false
	})
}

// popOutline opens the list. One that a command opened takes the keyboard and
// closes with Escape or a click elsewhere; one that hovering opened does neither.
func (e *Editor) popOutline(pinned bool) {
	r := e.outline.rail
	if r.listStale {
		e.fillOutlineList()
	} else {
		e.markOutlineItem()
	}
	if pinned != r.pinned {
		r.pop.Popdown()
	}
	r.pinned = pinned
	r.pop.SetAutohide(pinned)
	r.pop.Popup()
	if pinned {
		cur := max(e.outline.cur, 0)
		if cur < len(r.items) {
			r.items[cur].btn.GrabFocus()
		}
	}
}

// ShowOutline opens the list of the note's headings. It reports false when the
// note has too few of them to have an outline.
func (e *Editor) ShowOutline() bool {
	if len(e.outline.heads) < outlineMin {
		return false
	}
	e.popOutline(true)
	return true
}

// gotoHeading scrolls to the i'th heading, opening any fold it is in or under.
func (e *Editor) gotoHeading(i int) {
	s := &e.outline
	if i < 0 || i >= len(s.heads) {
		return
	}
	line := s.heads[i].line
	unfolded := false
	for _, h := range e.rich.hides {
		if line >= h[0] && line <= h[1] {
			e.setMark(&e.rich.headMarks, h[0]-1, 0)
			unfolded = true
		}
	}
	if e.rich.heads[line] {
		e.setMark(&e.rich.headMarks, line, 0)
		unfolded = true
	}
	scroll := func() bool {
		if it, ok := e.buffer.IterAtLine(line); ok {
			e.view.ScrollToIter(it, 0, true, 0, 0.06)
		}
		return false
	}
	if !unfolded {
		scroll()
		return
	}
	// The lines are laid out again once the fold is open (a render pass, then
	// GTK's own), and where they are is not known before.
	e.foldChanged()
	coreglib.TimeoutAdd(reparseDebounceMs+80, scroll)
}
