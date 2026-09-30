package app

import (
	"fmt"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
)

// The "Changes" side of the version history: the selected version set against
// the note as it is now, line by line. The comparison itself is in diff.go.

// changesRowCap is the most rows the view draws. Each is a widget, and a note
// that changed throughout would otherwise build tens of thousands of them and
// stall the dialog; the rest are counted.
const changesRowCap = 1500

// changesView is the scrolled list of changed lines, with a summary above it.
type changesView struct {
	widget  *gtk.Box
	summary *gtk.Label
	rows    *gtk.Box

	version, current string // what the page compares, once it is shown
	visible, stale   bool
}

func newChangesView() *changesView {
	c := &changesView{}
	c.summary = gtk.NewLabel("")
	c.summary.SetXAlign(0)
	c.summary.SetWrap(true)
	c.summary.AddCSSClass("dim-label")
	c.summary.AddCSSClass("caption")
	c.summary.SetMarginTop(8)
	c.summary.SetMarginBottom(8)
	c.summary.SetMarginStart(14)
	c.summary.SetMarginEnd(14)

	c.rows = gtk.NewBox(gtk.OrientationVertical, 0)
	c.rows.AddCSSClass("diff-view")
	scroll := gtk.NewScrolledWindow()
	scroll.SetChild(c.rows)
	scroll.SetPolicy(gtk.PolicyAutomatic, gtk.PolicyAutomatic)
	scroll.SetHExpand(true)
	scroll.SetVExpand(true)

	c.widget = gtk.NewBox(gtk.OrientationVertical, 0)
	c.widget.Append(c.summary)
	c.widget.Append(gtk.NewSeparator(gtk.OrientationHorizontal))
	c.widget.Append(scroll)
	return c
}

func (c *changesView) clear() {
	for w := c.rows.FirstChild(); w != nil; w = c.rows.FirstChild() {
		c.rows.Remove(w)
	}
}

// message replaces the view's content with a sentence, for a version that could
// not be read.
func (c *changesView) message(text string) {
	c.stale = false
	c.clear()
	c.summary.SetText(text)
}

// show remembers what to compare. The comparison is made when the Changes page
// is on screen, and again for each version chosen while it is: people open the
// dialog to read a version, and a note of ten thousand lines should not be
// compared with every one they click through.
func (c *changesView) show(version, current string) {
	c.version, c.current, c.stale = version, current, true
	c.clear()
	if c.visible {
		c.render()
	}
}

// setVisible is told when the Changes page is shown or hidden.
func (c *changesView) setVisible(v bool) {
	c.visible = v
	if v && c.stale {
		c.render()
	}
}

// render draws the comparison of the remembered texts.
func (c *changesView) render() {
	c.stale = false
	c.clear()
	if c.version == c.current {
		c.summary.SetText("This version is the same as the note now.")
		return
	}
	lines, precise := diffLines(c.version, c.current)
	added, removed := diffCounts(lines)
	if added == 0 && removed == 0 {
		// The strings differ but no line does: only how the lines end.
		c.summary.SetText("This version differs from the note now only in its line endings or a final newline.")
		return
	}
	summary := fmt.Sprintf("Compared with the note now: %s only in this version (red), %s only in the note now (green).",
		plural(removed, "line"), plural(added, "line"))
	if !precise {
		summary = "These two are too different to compare line by line, so the whole of each is shown. " + summary
	}
	c.summary.SetText(summary)

	rows := foldDiff(lines)
	drawn := rows
	if len(drawn) > changesRowCap {
		drawn = drawn[:changesRowCap]
	}
	for _, r := range drawn {
		c.rows.Append(diffRowWidget(r))
	}
	if len(rows) > len(drawn) {
		c.rows.Append(diffRowWidget(diffRow{Folded: -(len(rows) - len(drawn))}))
	}
}

// diffRowWidget draws one row. A folded row of a negative count is the note
// that rows were left out at the end.
func diffRowWidget(r diffRow) *gtk.Box {
	row := gtk.NewBox(gtk.OrientationHorizontal, 8)
	row.AddCSSClass("diff-row")

	gutter := gtk.NewLabel("")
	gutter.SetXAlign(0.5)
	gutter.SetYAlign(0)
	gutter.SetWidthChars(2)
	gutter.AddCSSClass("diff-gutter")

	text := gtk.NewLabel("")
	text.SetXAlign(0)
	text.SetHExpand(true)
	text.SetWrap(true)
	text.SetWrapMode(pango.WrapWordChar)
	text.SetSelectable(true)
	text.AddCSSClass("diff-text")

	switch {
	case r.Folded < 0:
		row.AddCSSClass("diff-fold")
		text.SetText(fmt.Sprintf("%d more rows are not shown", -r.Folded))
		text.SetSelectable(false)
	case r.Folded > 0:
		row.AddCSSClass("diff-fold")
		text.SetText(plural(r.Folded, "unchanged line"))
		text.SetSelectable(false)
	case r.Op == diffAdd:
		row.AddCSSClass("diff-add")
		gutter.SetText("+")
		text.SetText(r.Text)
	case r.Op == diffDel:
		row.AddCSSClass("diff-del")
		gutter.SetText("-")
		text.SetText(r.Text)
	default:
		text.SetText(r.Text)
	}
	row.Append(gutter)
	row.Append(text)
	return row
}
