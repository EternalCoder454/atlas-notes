package ui

import (
	"github.com/diamondburned/gotk4/pkg/core/gioutil"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// How a folder opens and closes in the vault panel. Its rows slide open
// beneath it and fade in one after another, and close up again before the
// folder actually collapses, so the rows below move rather than jump.
//
// Each row's content sits in a revealer, which is what lets a row's height run
// down to nothing: the list's own row widget has a minimum height in the
// stylesheet, so while a row moves it wears the tree-anim class, which hands
// that height to the content inside the revealer. The two sum to the same
// height, so nothing shifts when the class comes off.
//
// Only a folder the user opens or closes moves. One opened by the panel to
// show the note in it, or restored after a refresh, snaps open as before.

const (
	rowOpenMs  = 220 // a folder's rows sliding open
	rowCloseMs = 160 // and closing again
	// rowStagger is how many rows of an opening folder fade in one after
	// another; the rest come in with the last of them.
	rowStagger = 6
	// rowAnimMax caps how many rows of a folder are animated. Only the rows on
	// screen are bound, and these are the ones there could be.
	rowAnimMax = 48
)

// rowParts is what one row widget of the list is made of, kept so that the
// animation can find a row's pieces without walking the widget tree.
type rowParts struct {
	rev *gtk.Revealer
	exp *gtk.TreeExpander
	n   *node  // what the row is bound to now
	gen uint64 // bumped on every bind and close, so a stale timer leaves the row alone
}

// row is the list's own row widget, the one the stylesheet sizes.
func (p *rowParts) row() *gtk.Widget {
	if parent := p.rev.Parent(); parent != nil {
		return gtk.BaseWidget(parent)
	}
	return nil
}

// settle puts a row back at rest: fully open, no animation classes.
func (p *rowParts) settle() {
	p.rev.SetTransitionDuration(0)
	p.rev.SetRevealChild(true)
	p.exp.RemoveCSSClass("row-enter")
	p.exp.RemoveCSSClass("row-leave")
	p.exp.RemoveCSSClass("turning")
	p.exp.RemoveCSSClass("folding")
	for i := 1; i <= rowStagger; i++ {
		p.exp.RemoveCSSClass(staggerClass(i))
	}
	if row := p.row(); row != nil {
		row.RemoveCSSClass("tree-anim")
	}
}

func staggerClass(i int) string { return "row-enter-" + string(rune('0'+i)) }

// rowOf finds the list row a tree expander sits in.
func rowOf(exp *gtk.TreeExpander) *gtk.Widget {
	w := gtk.BaseWidget(exp)
	for i := 0; i < 3 && w != nil; i++ {
		parent := w.Parent()
		if parent == nil {
			return nil
		}
		w = gtk.BaseWidget(parent)
		if w.CSSName() == "row" {
			return w
		}
	}
	return nil
}

// bindParts records what a row is now showing and settles it; if the row is
// one of a folder's that has just been opened, it then plays it in.
func (t *Tree) bindParts(p *rowParts, n *node) {
	if p.n != nil && t.bound[p.n] == p {
		delete(t.bound, p.n)
	}
	p.n = n
	p.gen++
	t.bound[n] = p
	p.settle()
	if i, ok := t.entering[n]; ok {
		delete(t.entering, n)
		t.playEnter(p, i)
	}
}

// unbindParts forgets what a row was showing.
func (t *Tree) unbindParts(p *rowParts) {
	if p.n != nil && t.bound[p.n] == p {
		delete(t.bound, p.n)
	}
	p.n = nil
	p.gen++
	p.settle()
}

// playEnter slides a row open from nothing and fades it in, the i-th of its
// folder's rows.
func (t *Tree) playEnter(p *rowParts, i int) {
	row := p.row()
	if row == nil {
		return
	}
	row.AddCSSClass("tree-anim")
	p.rev.SetRevealChild(false) // at once: the duration is 0 after settle
	p.exp.AddCSSClass("row-enter")
	if d := min(i, rowStagger); d > 0 {
		p.exp.AddCSSClass(staggerClass(d))
	}
	gen := p.gen
	// The revealer only animates once it is on screen, which a row being
	// bound for the first time is not yet; its first frame is.
	p.rev.AddTickCallback(func(gtk.Widgetter, gdk.FrameClocker) bool {
		if p.gen == gen {
			p.rev.SetTransitionDuration(rowOpenMs)
			p.rev.SetRevealChild(true)
		}
		return false
	})
	coreglib.TimeoutAdd(uint(rowOpenMs+40*rowStagger+60), func() bool {
		if p.gen == gen {
			p.settle()
		}
		return false
	})
}

// toggleFolder opens or closes a folder row the user clicked.
func (t *Tree) toggleFolder(row *gtk.TreeListRow) {
	if row.Expanded() {
		t.collapseFolder(row)
	} else {
		t.expandFolder(row)
	}
}

// expandFolder opens a folder and marks its newly shown rows to play in as
// they are bound.
func (t *Tree) expandFolder(row *gtk.TreeListRow) {
	t.turnChevron(row)
	pos := row.Position()
	before := t.selection.NItems()
	row.SetExpanded(true)
	added := t.selection.NItems() - before
	t.expandGen++
	gen := t.expandGen
	clear(t.entering) // an earlier folder's rows that never came on screen
	for i := uint(0); i < added && i < rowAnimMax; i++ {
		if child := t.rowAt(pos + 1 + i); child != nil {
			n := gioutil.ObjectValue[*node](child.Item())
			if n == nil {
				continue
			}
			// The list binds the rows it can see while the folder expands,
			// so most are already bound by now.
			if p := t.bound[n]; p != nil {
				t.playEnter(p, int(i))
			} else {
				t.entering[n] = int(i)
			}
		}
	}
	// A row that scrolled out of view before it was bound would otherwise
	// play in whenever it next appeared.
	coreglib.TimeoutAdd(400, func() bool {
		if t.expandGen == gen {
			clear(t.entering)
		}
		return false
	})
}

// folderClose is a folder part way through closing: the rows it is closing up and
// the folder's own row, each with the gen it had then, so that rows recycled
// for something else in the meantime are left alone.
type folderClose struct {
	rows   []*rowParts
	gens   []uint64
	parent *rowParts
	pgen   uint64
}

// settle puts the rows at rest and the folder's arrow back.
func (c *folderClose) settle() {
	c.settleRows()
	if c.parent != nil && c.parent.gen == c.pgen {
		c.parent.exp.RemoveCSSClass("folding")
	}
}

// settleRows puts the rows at rest, those no later close has taken over.
func (c *folderClose) settleRows() {
	for i, p := range c.rows {
		if p.gen == c.gens[i] {
			p.settle()
		}
	}
}

// reopen runs the rows back open from wherever they have got to.
func (c *folderClose) reopen() {
	if c.parent != nil && c.parent.gen == c.pgen {
		c.parent.exp.RemoveCSSClass("folding")
	}
	for i, p := range c.rows {
		if p.gen != c.gens[i] {
			continue
		}
		p.exp.RemoveCSSClass("row-leave")
		p.rev.SetTransitionDuration(rowOpenMs)
		p.rev.SetRevealChild(true)
	}
	// Only the rows: the arrow was put back above, and a close started since
	// may have turned it again.
	coreglib.TimeoutAdd(rowOpenMs+40, func() bool {
		c.settleRows()
		return false
	})
}

// collapseFolder closes a folder's rows up, then collapses it. Clicked again
// while its rows are closing, the folder stays open.
func (t *Tree) collapseFolder(row *gtk.TreeListRow) {
	folder := gioutil.ObjectValue[*node](row.Item())
	if c := t.closing[folder]; c != nil {
		delete(t.closing, folder) // its timer finds it gone and does nothing
		c.reopen()
		t.turnChevron(row)
		return
	}
	if !animationsOn() {
		row.SetExpanded(false)
		return
	}
	t.turnChevron(row)
	c := &folderClose{}
	pos, depth := row.Position(), row.Depth()
	for i := pos + 1; len(c.rows) < rowAnimMax; i++ {
		child := t.rowAt(i)
		if child == nil || child.Depth() <= depth {
			break
		}
		n := gioutil.ObjectValue[*node](child.Item())
		p := t.bound[n]
		if p == nil {
			continue // off screen: it simply goes with the folder
		}
		p.gen++ // this close takes the row over from any earlier timer
		if r := p.row(); r != nil {
			r.AddCSSClass("tree-anim")
		}
		p.exp.AddCSSClass("row-leave")
		p.rev.SetTransitionDuration(rowCloseMs)
		p.rev.SetRevealChild(false)
		c.rows = append(c.rows, p)
		c.gens = append(c.gens, p.gen)
	}
	if len(c.rows) == 0 {
		row.SetExpanded(false)
		return
	}
	t.closing[folder] = c
	// The arrow turns as the rows start to close, not once they have gone.
	if c.parent = t.bound[folder]; c.parent != nil {
		c.pgen = c.parent.gen
		c.parent.exp.AddCSSClass("folding")
	}
	coreglib.TimeoutAdd(rowCloseMs, func() bool {
		if t.closing[folder] != c {
			return false // clicked again, and opened back up
		}
		delete(t.closing, folder)
		// A refresh in the meantime may have replaced the row; whatever is
		// there now is settled when it is next bound.
		if row.Item() != nil && row.Expanded() {
			row.SetExpanded(false)
		}
		c.settle()
		return false
	})
}

// animationsOn reports whether the desktop wants things to move. With
// animations off the rows would still wait out the timer, hidden, before
// their folder closed.
func animationsOn() bool {
	if st := gtk.SettingsGetDefault(); st != nil {
		if on, ok := st.ObjectProperty("gtk-enable-animations").(bool); ok {
			return on
		}
	}
	return true
}

// turnChevron lets a folder's disclosure arrow turn rather than flip. The
// arrow only turns while the class is on, so a row widget reused for another
// folder as the list scrolls does not spin.
func (t *Tree) turnChevron(row *gtk.TreeListRow) {
	n := gioutil.ObjectValue[*node](row.Item())
	p := t.bound[n]
	if p == nil {
		return
	}
	gen := p.gen
	p.exp.AddCSSClass("turning")
	coreglib.TimeoutAdd(uint(rowOpenMs+80), func() bool {
		if p.gen == gen {
			p.exp.RemoveCSSClass("turning")
		}
		return false
	})
}
