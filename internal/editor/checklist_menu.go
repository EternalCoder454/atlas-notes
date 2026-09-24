package editor

import (
	"fmt"
	"strings"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-notes/internal/checklist"
)

// The per-task menu: right-clicking a task line sets its priority, gives it a
// due date, or removes it. The controller lives on the text view rather than
// on each row, so a row holds no references back into the UI — see the note on
// anchoredItem in checklist_render.go for why that matters.

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

func sectionLabel(text string) *gtk.Label {
	l := gtk.NewLabel(text)
	l.SetXAlign(0)
	l.AddCSSClass("dim-label")
	return l
}
