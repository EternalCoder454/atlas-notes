package editor

import (
	"strings"
	"time"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"atlas-notes/internal/frontmatter"
)

// The widgets of the properties card (see props.go). Like the pictures' and the
// tables' they are reused rather than rebuilt: the bindings keep every widget
// they wrap until the process ends, so a row made per property would grow memory
// with every note opened. The card is built once, its rows are made as a note
// needs them and only ever grow, and each row holds every widget a value can be
// shown with, showing the one its kind wants. The handlers hold the editor and the
// row, which are never freed either, and look the property up again in the buffer
// when they fire, so a stale row can never write over the wrong lines.

// propsCard is the card's widgets.
type propsCard struct {
	root *gtk.Box
	rows *gtk.Box
	list []*propRow
	form addForm
}

// propRow is one property: its icon, its name, its value, and the button that
// takes it away.
type propRow struct {
	e    *Editor
	idx  int    // the property's place in the front matter
	key  string // and its key, which an edit is checked against
	kind frontmatter.Kind
	// value is what the row shows as a scalar, for putting it back after an entry
	// was given something it cannot take.
	value string
	busy  bool // the row is being dressed, so what it fires is not the person's

	box  *gtk.Box
	icon *gtk.Image
	name *gtk.Label
	del  *gtk.Button

	entry *gtk.Entry
	sw    *gtk.Switch
	ro    *gtk.Label

	dateBtn *gtk.MenuButton
	dateLbl *gtk.Label
	cal     *gtk.Calendar
	nav     bool // the calendar was just moved to another month

	flow  *gtk.FlowBox
	chips []*propChip
	adder *gtk.Entry
}

// propChip is one entry of a list.
type propChip struct {
	wrap gtk.Widgetter // the flow box's own wrapper of it, which is what is hidden
	box  *gtk.Box
	text *gtk.Label
	rm   *gtk.Button
	item string // the entry as written, which a click checks
}

// addForm is the popover that adds a property.
type addForm struct {
	btn   *gtk.MenuButton
	pop   *gtk.Popover
	name  *gtk.Entry
	types *gtk.DropDown
}

// propTypes are the kinds the form offers, in the order it lists them.
var propTypes = []struct {
	label string
	kind  frontmatter.Kind
}{
	{"Text", frontmatter.Text},
	{"Number", frontmatter.Number},
	{"Date", frontmatter.Date},
	{"Checkbox", frontmatter.Bool},
	{"List", frontmatter.List},
	{"Tags", frontmatter.Tags},
}

// propIcon is the icon a kind is drawn with.
func propIcon(k frontmatter.Kind) string {
	switch k {
	case frontmatter.Number:
		return "atlasnotes-property-number-symbolic"
	case frontmatter.Date:
		return "atlasnotes-property-date-symbolic"
	case frontmatter.Bool:
		return "atlasnotes-task-symbolic"
	case frontmatter.List, frontmatter.Aliases:
		return "atlasnotes-property-list-symbolic"
	case frontmatter.Tags:
		return "atlasnotes-tag-symbolic"
	}
	return "atlasnotes-property-text-symbolic"
}

// cardWidgets builds the card the first time it is asked for.
func (e *Editor) cardWidgets() *propsCard {
	s := &e.props
	if s.card != nil {
		return s.card
	}
	c := &propsCard{}
	s.card = c
	c.root = gtk.NewBox(gtk.OrientationVertical, 2)
	c.root.AddCSSClass("atlas-props")

	head := gtk.NewBox(gtk.OrientationHorizontal, 4)
	title := gtk.NewLabel("Properties")
	title.SetXAlign(0)
	title.SetHExpand(true)
	title.AddCSSClass("atlas-props-title")
	src := gtk.NewButtonFromIconName("atlasnotes-code-symbolic")
	src.AddCSSClass("flat")
	src.AddCSSClass("atlas-props-source")
	src.SetFocusOnClick(false)
	src.SetTooltipText("Edit as text")
	src.ConnectClicked(e.showFrontSource)
	head.Append(title)
	head.Append(src)
	c.root.Append(head)

	c.rows = gtk.NewBox(gtk.OrientationVertical, 0)
	c.root.Append(c.rows)

	e.buildAddForm(c)
	foot := gtk.NewBox(gtk.OrientationHorizontal, 0)
	foot.Append(c.form.btn)
	c.root.Append(foot)

	s.item.w = c.root
	return c
}

// buildAddForm makes the "Add property" button and its popover.
func (e *Editor) buildAddForm(c *propsCard) {
	f := &c.form
	f.btn = gtk.NewMenuButton()
	f.btn.AddCSSClass("flat")
	f.btn.AddCSSClass("atlas-props-add")
	f.btn.SetFocusOnClick(false)
	inner := gtk.NewBox(gtk.OrientationHorizontal, 6)
	inner.Append(gtk.NewImageFromIconName("atlasnotes-add-symbolic"))
	inner.Append(gtk.NewLabel("Add property"))
	f.btn.SetChild(inner)

	f.pop = gtk.NewPopover()
	form := gtk.NewBox(gtk.OrientationVertical, 8)
	form.SetMarginTop(10)
	form.SetMarginBottom(10)
	form.SetMarginStart(10)
	form.SetMarginEnd(10)
	f.name = gtk.NewEntry()
	f.name.SetPlaceholderText("Name")
	labels := make([]string, len(propTypes))
	for i, t := range propTypes {
		labels[i] = t.label
	}
	f.types = gtk.NewDropDownFromStrings(labels)
	ok := gtk.NewButtonWithLabel("Add")
	ok.AddCSSClass("suggested-action")
	form.Append(f.name)
	form.Append(f.types)
	form.Append(ok)
	f.pop.SetChild(form)
	f.btn.SetPopover(f.pop)
	f.pop.ConnectShow(func() {
		f.name.RemoveCSSClass("error")
		f.name.GrabFocus()
	})
	submit := func() { e.submitAddForm(f) }
	ok.ConnectClicked(submit)
	f.name.ConnectActivate(submit)
}

// submitAddForm adds the property the form describes.
func (e *Editor) submitAddForm(f *addForm) {
	name := strings.TrimSpace(f.name.Text())
	i := int(f.types.Selected())
	if i < 0 || i >= len(propTypes) {
		return
	}
	kind := propTypes[i].kind
	if name == "" && kind == frontmatter.Tags {
		name = "tags"
	}
	if name == "" {
		f.name.AddCSSClass("error")
		return
	}
	p := frontmatter.NewProp(name, kind)
	if p.Kind == frontmatter.Date {
		p.Value = time.Now().Format("2006-01-02")
	}
	if !e.addProp(p) {
		f.name.AddCSSClass("error") // that key is already there
		return
	}
	f.name.RemoveCSSClass("error")
	f.name.SetText("")
	f.pop.Popdown()
}

// dressCard fills the card with the front matter's properties.
func (e *Editor) dressCard() {
	s := &e.props
	c := e.cardWidgets()
	s.stale = false
	if s.doc == nil {
		return
	}
	props := s.doc.Props
	for len(c.list) < len(props) {
		r := e.newPropRow()
		c.list = append(c.list, r)
		c.rows.Append(r.box)
	}
	for i, r := range c.list {
		if i < len(props) {
			r.box.SetVisible(true)
			r.dress(i, props[i])
		} else {
			r.box.SetVisible(false)
		}
	}
	e.queuePlace()
}

// newPropRow builds a row with every kind of value widget in it.
func (e *Editor) newPropRow() *propRow {
	r := &propRow{e: e}
	r.box = gtk.NewBox(gtk.OrientationHorizontal, 10)
	r.box.AddCSSClass("atlas-prop-row")

	r.icon = gtk.NewImage()
	r.icon.SetPixelSize(16)
	r.icon.AddCSSClass("atlas-prop-icon")
	r.name = gtk.NewLabel("")
	r.name.SetXAlign(0)
	r.name.SetWidthChars(14)
	r.name.SetMaxWidthChars(14)
	r.name.SetEllipsize(pango.EllipsizeEnd)
	r.name.AddCSSClass("atlas-prop-name")
	r.box.Append(r.icon)
	r.box.Append(r.name)

	val := gtk.NewBox(gtk.OrientationHorizontal, 0)
	val.SetHExpand(true)
	val.SetVAlign(gtk.AlignCenter)
	r.box.Append(val)

	// A scalar: text or a number.
	r.entry = gtk.NewEntry()
	r.entry.SetHExpand(true)
	r.entry.SetWidthChars(1)
	r.entry.SetHasFrame(false)
	r.entry.SetPlaceholderText("Empty")
	r.entry.AddCSSClass("atlas-prop-entry")
	r.entry.ConnectActivate(r.commitEntry)
	focus := gtk.NewEventControllerFocus()
	focus.ConnectLeave(r.commitEntry)
	r.entry.AddController(focus)
	val.Append(r.entry)

	// A checkbox.
	r.sw = gtk.NewSwitch()
	r.sw.SetVAlign(gtk.AlignCenter)
	r.sw.ConnectStateSet(func(state bool) bool {
		if !r.busy {
			r.set(func(p *frontmatter.Prop) {
				p.Value = "false"
				if state {
					p.Value = "true"
				}
			})
		}
		return false // the switch changes as it always does
	})
	val.Append(r.sw)

	// A date, picked from a calendar.
	r.dateBtn = gtk.NewMenuButton()
	r.dateBtn.AddCSSClass("flat")
	r.dateBtn.AddCSSClass("atlas-prop-date")
	r.dateBtn.SetFocusOnClick(false)
	r.dateBtn.SetVAlign(gtk.AlignCenter)
	r.dateBtn.SetHAlign(gtk.AlignStart)
	r.dateLbl = gtk.NewLabel("")
	r.dateBtn.SetChild(r.dateLbl)
	pop := gtk.NewPopover()
	r.cal = gtk.NewCalendar()
	pop.SetChild(r.cal)
	r.dateBtn.SetPopover(pop)
	// A month or a year arrow moves the selected day along with it, and GTK says
	// so with a day-selected of its own; only a day that was picked counts. The
	// arrow's own signal is what tells the two apart, so the day waits a moment.
	move := func() { r.nav = true }
	r.cal.ConnectNextMonth(move)
	r.cal.ConnectPrevMonth(move)
	r.cal.ConnectNextYear(move)
	r.cal.ConnectPrevYear(move)
	r.cal.ConnectDaySelected(func() {
		if r.busy {
			return
		}
		coreglib.IdleAdd(func() bool {
			if r.nav {
				r.nav = false
				return false
			}
			d := r.cal.Date()
			if d == nil {
				return false
			}
			day := time.Date(d.Year(), time.Month(d.Month()), d.DayOfMonth(), 0, 0, 0, 0, time.Local).Format("2006-01-02")
			pop.Popdown()
			r.set(func(p *frontmatter.Prop) { p.Value = day })
			return false
		})
	})
	val.Append(r.dateBtn)

	// A list, as chips with an entry to add another.
	r.flow = gtk.NewFlowBox()
	r.flow.SetSelectionMode(gtk.SelectionNone)
	r.flow.SetHExpand(true)
	r.flow.SetHAlign(gtk.AlignFill)
	r.flow.SetColumnSpacing(6)
	r.flow.SetRowSpacing(4)
	r.flow.SetMaxChildrenPerLine(64)
	r.adder = gtk.NewEntry()
	r.adder.SetWidthChars(8)
	r.adder.SetMaxWidthChars(16)
	r.adder.SetHasFrame(false)
	r.adder.AddCSSClass("atlas-prop-add-chip")
	r.adder.ConnectActivate(r.commitChip)
	af := gtk.NewEventControllerFocus()
	af.ConnectLeave(r.commitChip)
	r.adder.AddController(af)
	r.flow.Append(r.adder)
	r.noFocus(r.adder)
	val.Append(r.flow)

	// A value that is not one to edit.
	r.ro = gtk.NewLabel("")
	r.ro.SetXAlign(0)
	r.ro.SetHExpand(true)
	r.ro.SetWidthChars(1)
	r.ro.SetEllipsize(pango.EllipsizeEnd)
	r.ro.AddCSSClass("atlas-prop-readonly")
	val.Append(r.ro)

	r.del = gtk.NewButtonFromIconName("atlasnotes-close-symbolic")
	r.del.AddCSSClass("flat")
	r.del.AddCSSClass("atlas-prop-remove")
	r.del.SetFocusOnClick(false)
	r.del.SetVAlign(gtk.AlignCenter)
	r.del.SetTooltipText("Remove property")
	r.del.ConnectClicked(func() { e.removeProp(r.idx, r.key) })
	r.box.Append(r.del)
	return r
}

// noFocus keeps the flow box's wrapper of a child from taking keyboard focus of
// its own, which would put a stop between the chips that does nothing.
func (r *propRow) noFocus(child gtk.Widgetter) {
	if p := gtk.BaseWidget(child).Parent(); p != nil {
		gtk.BaseWidget(p).SetFocusable(false)
	}
}

// set edits the row's property (see editProp).
func (r *propRow) set(mutate func(p *frontmatter.Prop)) {
	r.e.editProp(r.idx, r.key, mutate)
}

// commitEntry writes the entry's text as the property's value. A number entry
// given something that is not a number is put back as it was.
func (r *propRow) commitEntry() {
	if r.busy || (r.kind != frontmatter.Text && r.kind != frontmatter.Number) {
		return
	}
	text := strings.TrimSpace(r.entry.Text())
	if r.kind == frontmatter.Number && text != "" && !frontmatter.IsNumber(text) {
		r.entry.SetText(r.value)
		return
	}
	if text == r.value {
		return
	}
	r.set(func(p *frontmatter.Prop) { p.Value = text })
}

// commitChip adds what the entry holds to the list.
func (r *propRow) commitChip() {
	if r.busy {
		return
	}
	item := strings.TrimSpace(r.adder.Text())
	if r.kind == frontmatter.Tags {
		item = strings.ReplaceAll(strings.TrimLeft(item, "# "), " ", "-")
	}
	if item == "" {
		return
	}
	r.adder.SetText("")
	r.set(func(p *frontmatter.Prop) {
		for _, it := range p.Items {
			if strings.EqualFold(it, item) {
				return
			}
		}
		p.Items = append(p.Items, item)
	})
}

// removeChip takes the entry a chip shows out of the list.
func (r *propRow) removeChip(c *propChip) {
	r.set(func(p *frontmatter.Prop) {
		for i, it := range p.Items {
			if it == c.item {
				p.Items = append(p.Items[:i:i], p.Items[i+1:]...)
				return
			}
		}
	})
}

// clickChip opens the notes carrying a tag.
func (r *propRow) clickChip(c *propChip) {
	if r.kind == frontmatter.Tags && r.e.OnOpenTag != nil {
		r.e.OnOpenTag(strings.TrimLeft(c.item, "#"))
	}
}

// newChip builds a chip and puts it before the entry that adds another.
func (r *propRow) newChip(at int) *propChip {
	c := &propChip{}
	c.box = gtk.NewBox(gtk.OrientationHorizontal, 0)
	c.box.AddCSSClass("atlas-chip")
	c.text = gtk.NewLabel("")
	c.text.SetEllipsize(pango.EllipsizeEnd)
	c.text.SetMaxWidthChars(28)
	c.text.AddCSSClass("atlas-chip-text")
	click := gtk.NewGestureClick()
	click.ConnectReleased(func(_ int, _, _ float64) { r.clickChip(c) })
	c.text.AddController(click)
	c.rm = gtk.NewButtonFromIconName("atlasnotes-close-symbolic")
	c.rm.AddCSSClass("flat")
	c.rm.AddCSSClass("atlas-chip-remove")
	c.rm.SetFocusOnClick(false)
	c.rm.SetTooltipText("Remove")
	c.rm.ConnectClicked(func() { r.removeChip(c) })
	c.box.Append(c.text)
	c.box.Append(c.rm)
	r.flow.Insert(c.box, at)
	c.wrap = gtk.BaseWidget(c.box).Parent()
	r.noFocus(c.box)
	return c
}

// dress shows a property in the row.
func (r *propRow) dress(idx int, p frontmatter.Prop) {
	r.busy = true
	defer func() { r.busy = false }()
	r.idx, r.key, r.kind, r.value = idx, p.Key, p.Kind, p.Value

	r.icon.SetFromIconName(propIcon(p.Kind))
	r.name.SetText(p.Key)
	r.name.SetTooltipText(p.Key)
	r.del.SetVisible(p.Editable())

	scalar := p.Kind == frontmatter.Text || p.Kind == frontmatter.Number
	list := p.Kind == frontmatter.List || p.Kind == frontmatter.Tags || p.Kind == frontmatter.Aliases
	r.entry.SetVisible(scalar)
	r.sw.SetVisible(p.Kind == frontmatter.Bool)
	r.dateBtn.SetVisible(p.Kind == frontmatter.Date)
	r.flow.SetVisible(list)
	r.ro.SetVisible(p.Kind == frontmatter.ReadOnly)

	switch {
	case scalar:
		if r.entry.Text() != p.Value {
			r.entry.SetText(p.Value)
		}
	case p.Kind == frontmatter.Bool:
		if r.sw.Active() != (p.Value == "true") {
			r.sw.SetActive(p.Value == "true")
		}
	case p.Kind == frontmatter.Date:
		r.dateLbl.SetText(p.Value)
		if t, err := time.Parse("2006-01-02", p.Value); err == nil {
			r.cal.SelectDay(glib.NewDateTimeLocal(t.Year(), int(t.Month()), t.Day(), 0, 0, 0))
		}
	case list:
		r.dressChips(p)
	default:
		r.ro.SetText(readOnlyText(p))
		r.ro.SetTooltipText(strings.Join(p.Lines, "\n"))
	}
}

// dressChips shows a list's entries as chips.
func (r *propRow) dressChips(p frontmatter.Prop) {
	for len(r.chips) < len(p.Items) {
		r.chips = append(r.chips, r.newChip(len(r.chips)))
	}
	for i, c := range r.chips {
		show := i < len(p.Items)
		gtk.BaseWidget(c.wrap).SetVisible(show)
		if !show {
			continue
		}
		c.item = p.Items[i]
		c.text.SetText(strings.TrimLeft(c.item, "#"))
		if p.Kind == frontmatter.Tags {
			c.text.SetCursorFromName("pointer")
			c.text.SetTooltipText("Show notes with this tag")
		} else {
			c.text.SetCursor(nil)
			c.text.SetTooltipText("")
		}
	}
	if p.Kind == frontmatter.Tags {
		r.adder.SetPlaceholderText("Add tag")
	} else {
		r.adder.SetPlaceholderText("Add")
	}
}

// readOnlyText is what a property the card cannot edit is summed up as: its value
// when it is on one line, and its lines run together when it is not.
func readOnlyText(p frontmatter.Prop) string {
	if len(p.Lines) == 0 {
		return ""
	}
	if len(p.Lines) == 1 {
		_, v, _ := strings.Cut(p.Lines[0], ":")
		return strings.TrimSpace(v)
	}
	var parts []string
	for _, l := range p.Lines[1:] {
		if l = strings.TrimSpace(l); l != "" {
			parts = append(parts, l)
		}
	}
	if v := strings.TrimSpace(p.Lines[0][min(len(p.KeyRaw)+1, len(p.Lines[0])):]); v != "" {
		parts = append([]string{v}, parts...)
	}
	return strings.Join(parts, "  ")
}
