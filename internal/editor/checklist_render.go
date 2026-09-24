package editor

import (
	"strings"
	"time"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-notes/internal/checklist"
)

// anchorChar is the Unicode object-replacement character GTK places where a
// child anchor lives in the buffer.
const anchorChar = "￼"

// itemSpacing separates the priority bar from the checkbox inside a row. The
// gap between the row and the task's text is a margin in style.css, in text
// units, so it scales with the editor's font.
const itemSpacing = 5

// anchoredItem is one checkbox widget embedded in the buffer, paired with the
// anchor that positions it.
//
// Two things about GTK 4 make this bookkeeping necessary, and both of them leak
// a widget per task line without it:
//
//   - Deleting the anchor from the buffer does not destroy the widget. It stays
//     parented to the text view, invisible and alive, until the view is asked
//     to remove it (reapItems below).
//   - A signal handler that captures the very widget it is attached to keeps a
//     Go reference to it alive, which keeps the C object alive, which keeps the
//     handler alive. So nothing here captures its own widget: the row's menu
//     lives on the text view and works from the clicked line number instead.
type anchoredItem struct {
	anchor *gtk.TextChildAnchor
	row    *itemRow
}

// itemRow is one checklist row's widgets, kept together so a row can be reused
// for a different task instead of being rebuilt.
//
// Reuse is not a micro-optimization here. Every GTK object the bindings wrap is
// held by a strong entry until its toggle reference flips, so a widget that is
// created and dropped keeps roughly 8 KB alive for the rest of the process.
// Rebuilding a row per task line per note opened therefore grew memory without
// bound — measured at ~160 KB for every open of a ten-task note. Rows are now
// created once and re-dressed, so the number of widgets is bounded by the
// longest checklist ever shown rather than by how long the app has been used.
type itemRow struct {
	box  *gtk.Box
	bar  *gtk.Box
	cb   *gtk.CheckButton
	chip *gtk.Label
}

// renderChecklists replaces the "- [x] " prefix of each raw task line in
// [from, to] with an embedded checkbox widget (via a GtkTextChildAnchor). It is
// idempotent: lines already starting with an anchor are skipped, so it is safe
// to call repeatedly, and a render pass only needs to cover the lines that
// changed.
func (e *Editor) renderChecklists(from, to, excludeLine int) {
	if to < from {
		return
	}
	prev := e.loading
	e.loading = true
	defer func() { e.loading = prev }()

	for ln := from; ln <= to; ln++ {
		if ln == excludeLine {
			continue // leave the line being edited as raw markdown
		}
		line, ok := e.lineText(ln)
		if !ok || line == "" {
			continue
		}
		if strings.HasPrefix(line, anchorChar) {
			e.hasAnchors = true
			continue // already rendered
		}
		it, ok := checklist.ParseLine(line)
		if !ok {
			continue
		}
		off := checklist.TextOffset(line)
		if off < 0 {
			continue
		}
		start, ok1 := e.buffer.IterAtLineOffset(ln, 0)
		prefixEnd, ok2 := e.buffer.IterAtLineOffset(ln, off)
		if !ok1 || !ok2 {
			continue
		}
		e.buffer.Delete(start, prefixEnd)
		at, ok3 := e.buffer.IterAtLineOffset(ln, 0)
		if !ok3 {
			continue
		}
		anchor := e.buffer.CreateChildAnchor(at)
		row := e.takeRow(it)
		e.view.AddChildAtAnchor(row.box, anchor)
		e.items = append(e.items, anchoredItem{anchor: anchor, row: row})
		e.hasAnchors = true
	}
}

// reapItems removes the widgets whose anchor has been deleted from the buffer —
// a line the user deleted, a task turned back into plain text, or a note that
// was replaced. Without this they accumulate for the whole session.
func (e *Editor) reapItems() {
	if len(e.items) == 0 {
		return
	}
	kept := e.items[:0]
	for _, it := range e.items {
		if it.anchor.Deleted() {
			e.releaseRow(it.row)
			continue
		}
		kept = append(kept, it)
	}
	for i := len(kept); i < len(e.items); i++ {
		e.items[i] = anchoredItem{} // drop the references so they can be collected
	}
	e.items = kept
	e.hasAnchors = len(e.items) > 0
}

// clearItems removes every embedded widget. Used when the whole document is
// replaced and every anchor disappears at once.
func (e *Editor) clearItems() {
	for _, it := range e.items {
		e.releaseRow(it.row)
	}
	e.items = nil
	e.hasAnchors = false
}

// maxPooledRows caps the pool so a one-off enormous checklist does not pin
// thousands of widgets for the rest of the session.
const maxPooledRows = 512

// takeRow returns a row dressed for it: one from the pool when possible.
func (e *Editor) takeRow(it checklist.Item) *itemRow {
	if n := len(e.rowPool); n > 0 {
		row := e.rowPool[n-1]
		e.rowPool[n-1] = nil
		e.rowPool = e.rowPool[:n-1]
		e.dressRow(row, it)
		return row
	}
	row := e.newRow()
	e.dressRow(row, it)
	return row
}

// releaseRow unparents a row and keeps it for the next task line. The parent
// check matters: a row can reach here after GTK has already taken it off the
// view, and removing it twice is a warning.
func (e *Editor) releaseRow(row *itemRow) {
	if row == nil {
		return
	}
	if row.box.Parent() != nil {
		e.view.Remove(row.box)
	}
	if len(e.rowPool) < maxPooledRows {
		e.rowPool = append(e.rowPool, row)
	}
}

// dressRow points an existing row at a different task.
func (e *Editor) dressRow(row *itemRow, it checklist.Item) {
	applyPriorityClass(row.bar, it.Priority)
	// Setting the state must not look like the user clicking the box.
	e.withLoading(func() { row.cb.SetActive(it.Checked) })
	dressDueChip(row.chip, it.DueDate)
}

// lineText returns the text of one buffer line, including any anchor character,
// without copying the rest of the document.
func (e *Editor) lineText(ln int) (string, bool) {
	start, ok := e.buffer.IterAtLine(ln)
	if !ok {
		return "", false
	}
	end, ok := e.buffer.IterAtLine(ln)
	if !ok {
		return "", false
	}
	if !end.EndsLine() {
		end.ForwardToLineEnd()
	}
	return e.buffer.Slice(start, end, true), true
}

// newRow builds a checklist row: the priority bar, the checkbox, and a due-date
// badge (hidden until the task has a date).
//
// Its only signal handler captures the editor, never the widgets — a handler
// that captures its own widget keeps a Go reference to it alive, which keeps
// the C object alive, which keeps the handler alive.
// rowsBuilt counts the checklist rows actually built, as opposed to taken from
// the pool. RowsBuilt exposes it for the measurement harness, where widget
// churn is the thing being watched.
var rowsBuilt int

// RowsBuilt reports how many checklist rows have been constructed this session.
func RowsBuilt() int { return rowsBuilt }

func (e *Editor) newRow() *itemRow {
	rowsBuilt++
	box := gtk.NewBox(gtk.OrientationHorizontal, itemSpacing)
	box.AddCSSClass("checklist-item")
	// No vertical alignment is set here on purpose: the text layout pins an
	// embedded widget's bottom to the line's baseline, so alignment is decided
	// by the widget's height (see the checklist rules in style.css).

	bar := gtk.NewBox(gtk.OrientationVertical, 0)
	bar.SetSizeRequest(3, -1)
	bar.AddCSSClass("priority-bar")

	cb := gtk.NewCheckButton()
	cb.SetVAlign(gtk.AlignFill)
	cb.SetTooltipText("Toggle task · right-click the line for priority and due date")
	cb.ConnectToggled(func() {
		if e.loading {
			return
		}
		// Re-tag the document so a finished task's text is struck through.
		// This deliberately does not look up which row fired: holding a
		// reference to it here is what used to pin the row in memory.
		e.markAllDirty()
		e.scheduleReparse()
		if e.OnChanged != nil {
			e.OnChanged()
		}
	})

	chip := gtk.NewLabel("")
	chip.AddCSSClass("due-chip")
	chip.SetVAlign(gtk.AlignEnd) // bottom-aligned, like the box, on the baseline
	chip.SetVisible(false)

	box.Append(bar)
	box.Append(cb)
	box.Append(chip)
	return &itemRow{box: box, bar: bar, cb: cb, chip: chip}
}

// anchorChecked reports whether the checkbox embedded on a line is ticked.
func (e *Editor) anchorChecked(line int) bool {
	it, ok := e.buffer.IterAtLineOffset(line, 0)
	if !ok {
		return false
	}
	anchor := it.ChildAnchor()
	if anchor == nil {
		return false
	}
	for _, w := range anchor.Widgets() {
		if cb := checkButtonIn(w); cb != nil {
			return cb.Active()
		}
	}
	return false
}

func (e *Editor) markEdited() {
	e.scheduleReparse()
	if e.OnChanged != nil {
		e.OnChanged()
	}
}

func applyPriorityClass(bar *gtk.Box, p checklist.Priority) {
	for _, c := range []string{"priority-high", "priority-medium", "priority-low"} {
		bar.RemoveCSSClass(c)
	}
	switch p {
	case checklist.PriorityHigh:
		bar.AddCSSClass("priority-high")
	case checklist.PriorityMedium:
		bar.AddCSSClass("priority-medium")
	case checklist.PriorityLow:
		bar.AddCSSClass("priority-low")
	}
}

// checkButtonIn finds the GtkCheckButton inside an anchor's hosted widget.
func checkButtonIn(w gtk.Widgetter) *gtk.CheckButton {
	if cb, ok := w.(*gtk.CheckButton); ok {
		return cb
	}
	box, ok := w.(*gtk.Box)
	if !ok {
		return nil
	}
	for c := box.FirstChild(); c != nil; {
		if cb, ok := c.(*gtk.CheckButton); ok {
			return cb
		}
		ns, ok := c.(interface{ NextSibling() gtk.Widgetter })
		if !ok {
			break
		}
		c = ns.NextSibling()
	}
	return nil
}

// dressDueChip shows a task's due date on the row's badge, or hides it when
// there is no date. The badge turns red once the date has passed and amber when
// it is today.
func dressDueChip(chip *gtk.Label, due string) {
	for _, c := range []string{"due-today", "due-overdue"} {
		chip.RemoveCSSClass(c)
	}
	t, err := time.Parse("2006-01-02", due)
	if due == "" || err != nil {
		chip.SetVisible(false)
		chip.SetText("")
		return
	}
	chip.SetText(t.Format("Jan 2"))
	chip.SetVisible(true)
	today := time.Now().Truncate(24 * time.Hour)
	switch day := t.Truncate(24 * time.Hour); {
	case day.Before(today):
		chip.AddCSSClass("due-overdue")
		chip.SetTooltipText("Overdue · " + t.Format("Mon, Jan 2 2006"))
	case day.Equal(today):
		chip.AddCSSClass("due-today")
		chip.SetTooltipText("Due today")
	default:
		chip.SetTooltipText("Due " + t.Format("Mon, Jan 2 2006"))
	}
}
