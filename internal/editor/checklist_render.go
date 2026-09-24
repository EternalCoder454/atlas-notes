package editor

import (
	"fmt"
	"strings"
	"time"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
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

// releaseRow unparents a row and keeps it for the next task line.
func (e *Editor) releaseRow(row *itemRow) {
	if row == nil {
		return
	}
	e.view.Remove(row.box)
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
// ItemsCreated counts checklist rows actually built (as opposed to reused); the
// bench harness reports it so widget churn stays visible.
var ItemsCreated int

func (e *Editor) newRow() *itemRow {
	ItemsCreated++
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

// installItemMenu puts the per-task context menu on the text view itself, so
// right-clicking anywhere on a task line opens it. One controller for the whole
// document replaces one per row — fewer objects, no per-row references, and a
// larger target than the checkbox.
func (e *Editor) installItemMenu() {
	gesture := gtk.NewGestureClick()
	gesture.SetButton(3) // secondary (right) click
	gesture.ConnectPressed(func(_ int, x, y float64) {
		bx, by := e.view.WindowToBufferCoords(gtk.TextWindowWidget, int(x), int(y))
		iter, ok := e.view.IterAtLocation(bx, by)
		if !ok || iter == nil {
			return
		}
		ln := iter.Line()
		if line, ok := e.lineText(ln); !ok || !strings.HasPrefix(line, anchorChar) {
			return // not a rendered task line
		}
		gesture.SetState(gtk.EventSequenceClaimed)
		e.showItemPopover(ln, x, y)
	})
	e.view.AddController(gesture)
}

// ShowItemMenu opens a task line's context menu programmatically. It exists for
// the screenshot tooling; the menu is normally opened by right-clicking.
func (e *Editor) ShowItemMenu(ln int) { e.showItemPopover(ln, 40, 40) }

// showItemPopover presents the per-item menu for a task line: priority, due
// date, remove.
func (e *Editor) showItemPopover(ln int, x, y float64) {
	pop := gtk.NewPopover()
	pop.SetAutohide(true)
	rect := gdk.NewRectangle(int(x), int(y), 1, 1)
	pop.SetPointingTo(&rect)
	pop.SetParent(e.view)
	pop.ConnectClosed(func() { pop.Unparent() })

	content := gtk.NewBox(gtk.OrientationVertical, 6)
	content.SetMarginTop(8)
	content.SetMarginBottom(8)
	content.SetMarginStart(8)
	content.SetMarginEnd(8)

	content.Append(sectionLabel("Priority"))
	prow := gtk.NewBox(gtk.OrientationHorizontal, 4)
	addPriority := func(label string, p checklist.Priority) {
		b := gtk.NewButtonWithLabel(label)
		b.AddCSSClass("flat")
		b.ConnectClicked(func() {
			e.updateItem(ln, func(i *checklist.Item) { i.Priority = p })
			pop.Popdown()
		})
		prow.Append(b)
	}
	addPriority("High", checklist.PriorityHigh)
	addPriority("Medium", checklist.PriorityMedium)
	addPriority("Low", checklist.PriorityLow)
	addPriority("None", checklist.PriorityNone)
	content.Append(prow)

	content.Append(sectionLabel("Due date"))
	cal := gtk.NewCalendar()
	content.Append(cal)

	dueRow := gtk.NewBox(gtk.OrientationHorizontal, 6)
	setDue := gtk.NewButtonWithLabel("Set Due Date")
	setDue.ConnectClicked(func() {
		due := fmt.Sprintf("%04d-%02d-%02d", cal.Year(), cal.Month()+1, cal.Day())
		e.updateItem(ln, func(i *checklist.Item) { i.DueDate = due })
		pop.Popdown()
	})
	clearDue := gtk.NewButtonWithLabel("Clear")
	clearDue.AddCSSClass("flat")
	clearDue.ConnectClicked(func() {
		e.updateItem(ln, func(i *checklist.Item) { i.DueDate = "" })
		pop.Popdown()
	})
	dueRow.Append(setDue)
	dueRow.Append(clearDue)
	content.Append(dueRow)

	remove := gtk.NewButtonWithLabel("Remove Item")
	remove.AddCSSClass("destructive-action")
	remove.ConnectClicked(func() {
		e.removeItem(ln)
		pop.Popdown()
	})
	content.Append(remove)

	pop.SetChild(content)
	pop.Popup()
}

// updateItem rewrites the metadata of task line ln, applying mut, and rebuilds
// its row so the priority color and due badge match.
func (e *Editor) updateItem(ln int, mut func(*checklist.Item)) {
	line, ok := e.lineText(ln)
	if !ok || !strings.HasPrefix(line, anchorChar) {
		return
	}
	it, ok := checklist.ParseLine("- [ ] " + strings.TrimPrefix(line, anchorChar))
	if !ok {
		return
	}
	it.Checked = e.anchorChecked(ln)
	it.Order = 0
	mut(&it)

	e.replaceLineRaw(ln, it.Marshal())
	e.markDirty(ln, ln)
	e.reapItems()                  // the rewritten line dropped its old anchor
	e.renderChecklists(ln, ln, -1) // …and gets a row built from the new metadata
	e.markEdited()
}

// removeItem deletes a whole task line, widget and all.
func (e *Editor) removeItem(ln int) {
	start, ok1 := e.buffer.IterAtLineOffset(ln, 0)
	if !ok1 {
		return
	}
	end, ok2 := e.buffer.IterAtLineOffset(ln+1, 0)
	if !ok2 {
		_, end = e.buffer.Bounds() // last line: delete to buffer end
	}
	e.withLoading(func() { e.buffer.Delete(start, end) })
	e.markDirty(ln, ln)
	e.reapItems()
	e.markEdited()
}

// replaceLineRaw swaps a whole buffer line — anchor included — for raw markdown.
func (e *Editor) replaceLineRaw(ln int, text string) {
	start, ok := e.buffer.IterAtLine(ln)
	if !ok {
		return
	}
	end, ok := e.buffer.IterAtLine(ln)
	if !ok {
		return
	}
	if !end.EndsLine() {
		end.ForwardToLineEnd()
	}
	e.withLoading(func() {
		e.buffer.Delete(start, end)
		if at, ok := e.buffer.IterAtLine(ln); ok {
			e.buffer.Insert(at, text)
		}
	})
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

func sectionLabel(text string) *gtk.Label {
	l := gtk.NewLabel(text)
	l.SetXAlign(0)
	l.AddCSSClass("dim-label")
	return l
}

// coreglib is used by the editor's timers; keep the import anchored here so the
// build doesn't drift when handlers move between files.
var _ = coreglib.SourceHandle(0)
