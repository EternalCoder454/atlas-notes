package editor

import (
	"fmt"
	"strings"
	"time"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
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

// renderChecklists replaces the "- [x] " prefix of each raw task line in
// [from, to] with an embedded checkbox widget (via a GtkTextChildAnchor). It is
// idempotent: lines already starting with an anchor are skipped, so it is safe
// to call repeatedly, and a render pass only needs to cover the lines that
// changed.
func (e *Editor) renderChecklists(from, to, excludeLine int) {
	if to < from {
		return
	}
	e.loading = true
	defer func() { e.loading = false }()

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
		e.view.AddChildAtAnchor(e.buildChecklistWidget(it), anchor)
		e.hasAnchors = true
	}
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

func (e *Editor) buildChecklistWidget(it checklist.Item) *gtk.Box {
	box := gtk.NewBox(gtk.OrientationHorizontal, itemSpacing)
	box.AddCSSClass("checklist-item")
	// No vertical alignment is set here on purpose: the text layout pins an
	// embedded widget's bottom to the line's baseline, so alignment is decided
	// by the widget's height (see the checklist rules in style.css).

	bar := gtk.NewBox(gtk.OrientationVertical, 0)
	bar.SetSizeRequest(3, -1)
	bar.AddCSSClass("priority-bar")
	applyPriorityClass(bar, it.Priority)

	cb := gtk.NewCheckButton()
	cb.SetVAlign(gtk.AlignFill)
	cb.SetActive(it.Checked) // set before connecting so it doesn't fire OnChanged
	cb.SetTooltipText("Toggle task · right-click for priority and due date")
	cb.ConnectToggled(func() {
		if e.loading {
			return
		}
		if e.OnChanged != nil {
			e.OnChanged()
		}
		if ln := e.lineForBox(box); ln >= 0 {
			e.markDirty(ln, ln) // strike the text through as soon as it is ticked
		}
		e.scheduleReparse()
	})

	box.Append(bar)
	box.Append(cb)
	if chip := dueChip(it.DueDate); chip != nil {
		box.Append(chip)
	}

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
	e.markDirty(ln, ln)
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
	e.markDirty(ln, ln)
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
	total := e.buffer.LineCount()
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

// dueChip renders an item's due date as a small inline badge, so a date set
// from the context menu is visible in the note instead of hidden in metadata.
// It turns red once the date has passed and amber when it is today.
func dueChip(due string) *gtk.Label {
	if due == "" {
		return nil
	}
	t, err := time.Parse("2006-01-02", due)
	if err != nil {
		return nil
	}
	label := gtk.NewLabel(t.Format("Jan 2"))
	label.AddCSSClass("due-chip")
	label.SetVAlign(gtk.AlignEnd) // bottom-aligned, like the box, on the baseline
	today := time.Now().Truncate(24 * time.Hour)
	switch day := t.Truncate(24 * time.Hour); {
	case day.Before(today):
		label.AddCSSClass("due-overdue")
		label.SetTooltipText("Overdue · " + t.Format("Mon, Jan 2 2006"))
	case day.Equal(today):
		label.AddCSSClass("due-today")
		label.SetTooltipText("Due today")
	default:
		label.SetTooltipText("Due " + t.Format("Mon, Jan 2 2006"))
	}
	return label
}

func sectionLabel(text string) *gtk.Label {
	l := gtk.NewLabel(text)
	l.SetXAlign(0)
	l.AddCSSClass("dim-label")
	return l
}
