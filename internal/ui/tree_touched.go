package ui

import (
	"time"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// A note changed from outside the window, by Claude Code, pulses in the panel:
// its row lights up in a warm tint that fades (the "touched" class in
// style.css), so work happening across the vault can be followed at a glance.
// Notes changed again while their row is still lit keep the one pulse; the
// next change after it has faded starts another, so a note being worked on
// steadily pulses steadily rather than glowing without end.

// touchFor is how long a row keeps the class. The animation itself is a little
// shorter, so the class is never taken off a row that is still fading.
const touchFor = 2200 * time.Millisecond

// touchRebind is how soon after a change a row bound again still pulses.
const touchRebind = 400 * time.Millisecond

// Touched lights up the rows of notes that have just been changed elsewhere.
func (t *Tree) Touched(rels []string) {
	if len(rels) == 0 {
		return
	}
	if t.touched == nil {
		t.touched = map[string]time.Time{}
	}
	now := time.Now()
	fresh := false
	for _, rel := range rels {
		if until, ok := t.touched[rel]; ok && now.Before(until) {
			continue // still lit from the last change
		}
		t.touched[rel] = now.Add(touchFor)
		fresh = true
	}
	if !fresh {
		return
	}
	t.paintTouched()
	if !t.touchSweep {
		t.touchSweep = true
		coreglib.TimeoutAdd(uint(touchFor.Milliseconds())+50, t.sweepTouched)
	}
}

// sweepTouched lets go of rows whose pulse is over, and comes back for the
// rest while any are still lit.
func (t *Tree) sweepTouched() bool {
	now := time.Now()
	for rel, until := range t.touched {
		if !now.Before(until) {
			delete(t.touched, rel)
		}
	}
	t.paintTouched()
	if len(t.touched) > 0 {
		return true
	}
	t.touchSweep = false
	return false
}

// paintTouched brings the rows on screen into line with what is lit.
func (t *Tree) paintTouched() {
	for c := t.listView.FirstChild(); c != nil; {
		w := gtk.BaseWidget(c)
		c = w.NextSibling()
		if exp, ok := w.FirstChild().(*gtk.TreeExpander); ok {
			if n := nodeFromExpander(exp); n != nil {
				t.paintTouch(exp, n)
			}
		}
	}
}

// paintTouch sets one row's class. A row bound again while lit, as the one for
// a note just rewritten is when the panel refreshes, picks the class up here.
func (t *Tree) paintTouch(exp *gtk.TreeExpander, n *node) {
	row := rowOf(exp)
	if row == nil {
		return
	}
	until, ok := t.touched[n.rel]
	now := time.Now()
	switch {
	case !ok || n.isFolder || !now.Before(until):
		row.RemoveCSSClass("touched")
	case row.HasCSSClass("touched"):
		// Already pulsing.
	case now.Sub(until.Add(-touchFor)) < touchRebind:
		// A row bound again just after its note changed, as the panel's
		// refresh does to it, starts the pulse again at once, which looks like
		// one pulse. Later on, starting it again would flash a row that had
		// nearly faded, so it is left plain.
		row.AddCSSClass("touched")
	}
}
