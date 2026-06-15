package editor

import (
	"fmt"
	"strings"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-notes/internal/checklist"
)

// anchorChar is the Unicode object-replacement character GTK places where a
// child anchor lives in the buffer.
const anchorChar = "￼"

// checkboxTopNudge pushes the embedded checkbox down a few px so it lines up
// vertically with the line's text.
const checkboxTopNudge = 3

// renderChecklists replaces the "- [x] " prefix of each raw task line with an
// embedded checkbox widget (via a GtkTextChildAnchor). It is idempotent: lines
// already starting with an anchor are skipped, so it is safe to call repeatedly.
func (e *Editor) renderChecklists(excludeLine int) {
	lines := strings.Split(e.rawText(), "\n")
	e.loading = true
	defer func() { e.loading = false }()

	for ln, line := range lines {
		if ln == excludeLine {
			continue // leave the line being edited as raw markdown
		}
		if strings.HasPrefix(line, anchorChar) {
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
		e.view.AddChildAtAnchor(e.buildChecklistWidget(it), anchor)
	}
}

func (e *Editor) buildChecklistWidget(it checklist.Item) *gtk.Box {
	box := gtk.NewBox(gtk.OrientationHorizontal, 6)
	box.AddCSSClass("checklist-item")
	box.SetVAlign(gtk.AlignCenter)

	bar := gtk.NewBox(gtk.OrientationVertical, 0)
	bar.SetSizeRequest(4, -1)
	bar.AddCSSClass("priority-bar")
	applyPriorityClass(bar, it.Priority)

	cb := gtk.NewCheckButton()
	cb.SetVAlign(gtk.AlignCenter)
	cb.SetMarginTop(checkboxTopNudge) // drop the box down to center on the text
	cb.SetActive(it.Checked)          // set before connecting so it doesn't fire OnChanged
	cb.ConnectToggled(func() {
		if e.loading {
			return
		}
		if e.OnChanged != nil {
			e.OnChanged()
		}
	})

	box.Append(bar)
	box.Append(cb)

	gesture := gtk.NewGestureClick()
	gesture.SetButton(3) // secondary (right) click
	gesture.ConnectPressed(func(_ int, _, _ float64) {
		e.showItemPopover(box, bar, cb)
	})
	box.AddController(gesture)

	return box
}

// showItemPopover presents the per-item context menu: priority, due date, remove.
func (e *Editor) showItemPopover(box, bar *gtk.Box, cb *gtk.CheckButton) {
	pop := gtk.NewPopover()
	pop.SetAutohide(true)

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
			e.updateItem(box, bar, cb, func(i *checklist.Item) { i.Priority = p })
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
	setDue := gtk.NewButtonWithLabel("Set Due Date")
	setDue.ConnectClicked(func() {
		due := fmt.Sprintf("%04d-%02d-%02d", cal.Year(), cal.Month()+1, cal.Day())
		e.updateItem(box, bar, cb, func(i *checklist.Item) { i.DueDate = due })
		pop.Popdown()
	})
	content.Append(setDue)

	remove := gtk.NewButtonWithLabel("Remove Item")
	remove.AddCSSClass("destructive-action")
	remove.ConnectClicked(func() {
		e.removeItem(box)
		pop.Popdown()
	})
	content.Append(remove)

	pop.SetChild(content)
	pop.SetParent(box)
	pop.ConnectClosed(func() { pop.Unparent() })
	pop.Popup()
}

// updateItem rewrites the metadata of the task line that owns box, applying mut.
func (e *Editor) updateItem(box, bar *gtk.Box, cb *gtk.CheckButton, mut func(*checklist.Item)) {
	ln := e.lineForBox(box)
	if ln < 0 {
		return
	}
	lines := strings.Split(e.rawText(), "\n")
	if ln >= len(lines) {
		return
	}
	body := strings.TrimPrefix(lines[ln], anchorChar)
	it, ok := checklist.ParseLine("- [ ] " + body)
	if !ok {
		return
	}
	it.Checked = cb.Active()
	it.Order = 0
	mut(&it)

	newBody := it.Text
	if meta := it.Meta(); meta != "" {
		newBody += " " + meta
	}
	e.replaceAfterAnchor(ln, len([]rune(lines[ln])), newBody)
	applyPriorityClass(bar, it.Priority)
	e.markEdited()
}

func (e *Editor) removeItem(box *gtk.Box) {
	ln := e.lineForBox(box)
	if ln < 0 {
		return
	}
	start, ok1 := e.buffer.IterAtLineOffset(ln, 0)
	if !ok1 {
		return
	}
	end, ok2 := e.buffer.IterAtLineOffset(ln+1, 0)
	if !ok2 {
		_, end = e.buffer.Bounds() // last line: delete to buffer end
	}
	e.loading = true
	e.buffer.Delete(start, end)
	e.loading = false
	e.markEdited()
}

// replaceAfterAnchor replaces the text after the anchor (offset 1) up to the end
// of the line with newBody.
func (e *Editor) replaceAfterAnchor(ln, rawLineLen int, newBody string) {
	start, ok1 := e.buffer.IterAtLineOffset(ln, 1)
	end, ok2 := e.buffer.IterAtLineOffset(ln, rawLineLen)
	if !ok1 || !ok2 {
		return
	}
	e.loading = true
	e.buffer.Delete(start, end)
	if at, ok := e.buffer.IterAtLineOffset(ln, 1); ok {
		e.buffer.Insert(at, newBody)
	}
	e.loading = false
}

// lineForBox returns the buffer line whose anchor hosts box, or -1.
func (e *Editor) lineForBox(box *gtk.Box) int {
	target := coreglib.BaseObject(box).Native()
	total := strings.Count(e.rawText(), "\n") + 1
	for ln := 0; ln < total; ln++ {
		it, ok := e.buffer.IterAtLineOffset(ln, 0)
		if !ok {
			continue
		}
		anchor := it.ChildAnchor()
		if anchor == nil {
			continue
		}
		for _, w := range anchor.Widgets() {
			if coreglib.BaseObject(w).Native() == target {
				return ln
			}
		}
	}
	return -1
}

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

func sectionLabel(text string) *gtk.Label {
	l := gtk.NewLabel(text)
	l.SetXAlign(0)
	l.AddCSSClass("dim-label")
	return l
}
