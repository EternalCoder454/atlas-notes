package editor

import (
	"math"
	"sort"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/cairo"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"

	"atlas-notes/internal/diagram"
)

// The flowchart editor: a dialog with a canvas where boxes are dragged about,
// joined with arrows and renamed, drawn by the same Scene renderer as the note.
// Nothing in the note changes until Done, which rewrites the block's text as one
// undo step. Where boxes were put is kept in "%% atlas:pos" comments.

const (
	deGrid   = 20.0 // the grid boxes snap to, in diagram pixels
	dePad    = 16.0 // space round the canvas inside the scrolled window
	deHandle = 5.0  // radius of a connection handle
	deGrow   = 200.0
	deMinW   = 900.0
	deMinH   = 560.0
)

type deMode int

const (
	deIdle deMode = iota
	deMove
	deConnect
	deBand
)

type measureKey struct {
	t    string
	s    float64
	bold bool
}

type diagramEditor struct {
	e           *Editor
	dialog      *adw.Dialog
	scroll      *gtk.ScrolledWindow
	area        *gtk.DrawingArea
	font        *diagramFont
	memo        map[measureKey][2]float64
	first, last int // the fences of the block being edited
	orig        string
	g           *diagram.Graph
	scene       *diagram.Scene
	undo, redo  []*diagram.Graph
	changed     bool
	zoom        float64
	cw, ch      float64 // the canvas
	cw0, ch0    float64
	sel         map[string]bool
	selEdge     *diagram.Edge
	hover       *diagram.Item
	hoverSide   int
	// the drag in progress
	mode        deMode
	start       diagram.Pt // where it began, on the canvas
	cur         diagram.Pt
	snap        *diagram.Graph
	startPos    map[string]diagram.Pt
	startRects  map[string][4]float64
	direct      []*diagram.Item
	moved       bool
	connectFrom *diagram.Item
	shapeDD     *gtk.DropDown
	styleDD     *gtk.DropDown
	hiBtn       *gtk.ToggleButton
	quiet       bool
	pop         *gtk.Popover
}

func (d *diagramEditor) measure(text string, size float64, bold bool) (float64, float64) {
	k := measureKey{text, size, bold}
	if v, ok := d.memo[k]; ok {
		return v[0], v[1]
	}
	if len(d.memo) > 4000 {
		d.memo = map[measureKey][2]float64{}
	}
	w, h := d.font.measure(text, size, bold)
	d.memo[k] = [2]float64{w, h}
	return w, h
}

// layout lays the graph out again and grows the canvas to hold it.
func (d *diagramEditor) layout() {
	d.scene = d.g.Scene(d.measure, 0)
	d.cw = math.Max(d.cw, math.Max(d.g.CanvasW, math.Max(d.scene.W+deGrow, deMinW)))
	d.ch = math.Max(d.ch, math.Max(d.g.CanvasH, math.Max(d.scene.H+deGrow/2, deMinH)))
	d.cw, d.ch = math.Min(d.cw, diagram.MaxSize), math.Min(d.ch, diagram.MaxSize)
	d.area.SetContentWidth(int(d.cw*d.zoom + 2*dePad))
	d.area.SetContentHeight(int(d.ch*d.zoom + 2*dePad))
	d.area.QueueDraw()
}

// ---- Opening ---------------------------------------------------------------

// mermaidBlock finds the mermaid block the line is in: its fence lines.
func (e *Editor) mermaidBlock(line int) (first, last int, ok bool) {
	fence := func(l int) (string, bool) {
		t, _ := e.lineText(l)
		if t = strings.TrimSpace(t); strings.HasPrefix(t, "```") {
			return strings.TrimSpace(t[3:]), true
		}
		return "", false
	}
	first = -1
	for l := line; l >= 0; l-- {
		if lang, f := fence(l); f {
			if lang == "" && l == line {
				continue // the closing fence itself
			}
			first = l
			break
		}
	}
	if first < 0 {
		return 0, 0, false
	}
	if lang, _ := fence(first); !strings.EqualFold(lang, "mermaid") {
		return 0, 0, false
	}
	for l := first + 1; l < e.buffer.LineCount(); l++ {
		if lang, f := fence(l); f && lang == "" {
			return first, l, l >= line
		}
	}
	return 0, 0, false
}

func (e *Editor) caretLine() int {
	return e.buffer.IterAtMark(e.buffer.GetInsert()).Line()
}

// EditDiagramAtCaret opens the flowchart editor on the mermaid block the caret
// is in. It reports false when the caret is not in a flowchart.
func (e *Editor) EditDiagramAtCaret() bool { return e.EditDiagramAt(e.caretLine()) }

// EditDiagramAt opens the flowchart editor on the mermaid block at a line.
func (e *Editor) EditDiagramAt(line int) bool {
	first, last, ok := e.mermaidBlock(line)
	if !ok || last < first+2 {
		return false
	}
	src := e.diagramSource(&richBlock{first: first, last: last})
	if diagram.Kind(src) != "flowchart" {
		return false
	}
	g, err := diagram.Parse(src)
	if err != nil {
		return false
	}
	e.openDiagramEditor(first, last, src, g)
	return true
}

// EditFirstDiagram opens the editor on the first flowchart in the note.
func (e *Editor) EditFirstDiagram() bool {
	for l := 0; l < e.buffer.LineCount(); l++ {
		if t, _ := e.lineText(l); strings.HasPrefix(strings.TrimSpace(t), "```mermaid") && e.EditDiagramAt(l) {
			return true
		}
	}
	return false
}

func (e *Editor) openDiagramEditor(first, last int, src string, g *diagram.Graph) {
	d := &diagramEditor{e: e, first: first, last: last, orig: src, g: g, zoom: 1, sel: map[string]bool{}, hoverSide: -1,
		memo: map[measureKey][2]float64{}}
	d.area = gtk.NewDrawingArea()
	d.area.SetFocusable(true)
	d.area.AddCSSClass("atlas-diagram-editor")
	d.area.UpdateProperty([]gtk.AccessibleProperty{gtk.AccessiblePropertyLabel},
		[]coreglib.Value{*coreglib.NewValue("Flowchart canvas. Drag boxes to move them, drag from a handle to connect, double-click to edit text.")})
	pc := d.area.CreatePangoContext()
	d.font = newDiagramFont(pango.NewLayout(pc), pc.FontDescription().Family())
	d.scroll = gtk.NewScrolledWindow()
	d.scroll.SetChild(d.area)
	d.scroll.SetHExpand(true)
	d.scroll.SetVExpand(true)
	d.layout()
	d.cw0, d.ch0 = d.cw, d.ch
	d.area.SetDrawFunc(func(_ *gtk.DrawingArea, cr *cairo.Context, w, h int) { d.draw(cr) })
	d.wire()

	bar := d.toolbar()
	hb := adw.NewHeaderBar()
	hb.SetShowStartTitleButtons(false)
	hb.SetShowEndTitleButtons(false)
	hb.SetTitleWidget(adw.NewWindowTitle("Edit diagram", ""))
	cancel := gtk.NewButtonWithLabel("Cancel")
	cancel.SetTooltipText("Close without changing the note")
	cancel.ConnectClicked(func() { d.close() })
	done := gtk.NewButtonWithLabel("Done")
	done.AddCSSClass("suggested-action")
	done.SetTooltipText("Apply the changes to the note")
	done.ConnectClicked(func() { d.done() })
	hb.PackStart(cancel)
	hb.PackEnd(done)
	tv := adw.NewToolbarView()
	tv.AddTopBar(hb)
	tv.AddTopBar(bar)
	tv.SetContent(d.scroll)
	d.dialog = adw.NewDialog()
	d.dialog.SetTitle("Edit diagram")
	d.dialog.SetContentWidth(1180)
	d.dialog.SetContentHeight(780)
	d.dialog.SetChild(tv)
	d.dialog.SetCanClose(false)
	d.dialog.ConnectCloseAttempt(func() { d.close() })
	d.dialog.Present(e.view)
	d.area.GrabFocus()
	d.fit()
}

func (d *diagramEditor) button(label, tip string, fn func()) *gtk.Button {
	b := gtk.NewButtonWithLabel(label)
	b.SetTooltipText(tip)
	b.AddCSSClass("flat")
	b.SetFocusOnClick(false)
	b.ConnectClicked(func() { fn(); d.area.GrabFocus() })
	return b
}

func (d *diagramEditor) toolbar() gtk.Widgetter {
	box := gtk.NewBox(gtk.OrientationHorizontal, 4)
	box.SetMarginStart(8)
	box.SetMarginEnd(8)
	box.SetMarginTop(4)
	box.SetMarginBottom(4)
	sep := func() { box.Append(gtk.NewSeparator(gtk.OrientationVertical)) }
	box.Append(d.button("Add box", "Add a box in the middle of the view. Double-click empty space to add one there", func() { d.addBox(-1, -1) }))
	d.shapeDD = gtk.NewDropDownFromStrings([]string{"Rectangle", "Rounded", "Stadium", "Diamond", "Circle"})
	d.shapeDD.SetTooltipText("Shape of the selected boxes")
	d.shapeDD.SetFocusOnClick(false)
	d.shapeDD.NotifyProperty("selected", func() {
		if d.quiet {
			return
		}
		s := diagram.Shape(d.shapeDD.Selected())
		d.mutate(false, func() {
			for _, it := range d.boxes() {
				it.Shape = s
			}
		})
	})
	box.Append(d.shapeDD)
	d.hiBtn = gtk.NewToggleButtonWithLabel("Highlight")
	d.hiBtn.AddCSSClass("flat")
	d.hiBtn.SetFocusOnClick(false)
	d.hiBtn.SetTooltipText("Draw the selected boxes in the accent colour")
	d.hiBtn.ConnectToggled(func() {
		if d.quiet {
			return
		}
		on := d.hiBtn.Active()
		d.mutate(false, func() {
			for _, it := range d.boxes() {
				it.Accent = on
			}
		})
	})
	box.Append(d.hiBtn)
	sep()
	box.Append(d.button("Group", "Put the selected boxes in a titled group", d.group))
	box.Append(d.button("Ungroup", "Remove the selected group and keep its boxes", d.ungroup))
	box.Append(d.button("Delete", "Delete the selection (Delete key)", d.remove))
	sep()
	d.styleDD = gtk.NewDropDownFromStrings([]string{"Solid", "Dotted", "Thick"})
	d.styleDD.SetTooltipText("Style of the selected arrow")
	d.styleDD.SetFocusOnClick(false)
	d.styleDD.NotifyProperty("selected", func() {
		if d.quiet || d.selEdge == nil {
			return
		}
		s := d.styleDD.Selected()
		e := d.selEdge
		d.mutate(false, func() { e.Dotted, e.Thick = s == 1, s == 2 })
	})
	box.Append(d.styleDD)
	box.Append(d.button("Label", "Edit the label of the selected arrow (or double-click it)", func() {
		if d.selEdge != nil {
			d.editEdge(d.selEdge)
		}
	}))
	sep()
	box.Append(d.button("Undo", "Undo (Ctrl+Z)", d.doUndo))
	box.Append(d.button("Redo", "Redo (Ctrl+Shift+Z)", d.doRedo))
	sep()
	box.Append(d.button("−", "Zoom out", func() { d.setZoom(d.zoom / 1.25) }))
	box.Append(d.button("+", "Zoom in (or Ctrl+scroll)", func() { d.setZoom(d.zoom * 1.25) }))
	box.Append(d.button("Fit", "Fit the whole canvas in the view", d.fit))
	sc := gtk.NewScrolledWindow()
	sc.SetPolicy(gtk.PolicyAutomatic, gtk.PolicyNever)
	sc.SetChild(box)
	return sc
}

// ---- Changing the graph ----------------------------------------------------

func (d *diagramEditor) push(before *diagram.Graph) {
	d.undo = append(d.undo, before)
	if len(d.undo) > 100 {
		d.undo = d.undo[1:]
	}
	d.redo = nil
	d.changed = true
}

// mutate runs a change as one undo step. A change that can move the boxes the
// layout places (freeze) first keeps them where they are.
func (d *diagramEditor) mutate(freeze bool, fn func()) {
	before := d.g.Clone()
	if freeze {
		d.pinAll()
	}
	fn()
	d.push(before)
	d.layout()
	d.syncTools()
}

// pinAll stores where every box is now, so that a change to the diagram does
// not send the boxes the layout places somewhere else.
func (d *diagramEditor) pinAll() {
	for _, it := range d.g.Order {
		if _, ok := d.g.Pos[it.ID]; !it.Group && !ok {
			d.g.SetPos(it.ID, it.X, it.Y)
		}
	}
}

func (d *diagramEditor) doUndo() {
	if n := len(d.undo); n > 0 {
		d.redo = append(d.redo, d.g)
		d.g, d.undo = d.undo[n-1], d.undo[:n-1]
		d.afterSwap()
	}
}

func (d *diagramEditor) doRedo() {
	if n := len(d.redo); n > 0 {
		d.undo = append(d.undo, d.g)
		d.g, d.redo = d.redo[n-1], d.redo[:n-1]
		d.afterSwap()
	}
}

func (d *diagramEditor) afterSwap() {
	d.changed = true
	d.selEdge, d.hover = nil, nil
	for id := range d.sel {
		if d.g.Items[id] == nil {
			delete(d.sel, id)
		}
	}
	d.layout()
	d.syncTools()
}

// boxes are the boxes (not groups) selected.
func (d *diagramEditor) boxes() []*diagram.Item {
	var out []*diagram.Item
	for _, it := range d.g.Order {
		if d.sel[it.ID] && !it.Group {
			out = append(out, it)
		}
	}
	return out
}

// leaves are the boxes selected and those in the groups selected.
func (d *diagramEditor) leaves() []*diagram.Item {
	var out []*diagram.Item
	seen := map[*diagram.Item]bool{}
	var add func(it *diagram.Item)
	add = func(it *diagram.Item) {
		if it.Group {
			for _, k := range it.Children {
				add(k)
			}
		} else if !seen[it] {
			seen[it] = true
			out = append(out, it)
		}
	}
	for _, it := range d.g.Order {
		if d.sel[it.ID] {
			add(it)
		}
	}
	return out
}

func (d *diagramEditor) syncTools() {
	d.quiet = true
	defer func() { d.quiet = false }()
	if bs := d.boxes(); len(bs) > 0 {
		d.shapeDD.SetSelected(uint(bs[0].Shape))
		d.hiBtn.SetActive(bs[0].Accent)
	} else {
		d.hiBtn.SetActive(false)
	}
	if e := d.selEdge; e != nil {
		s := uint(0)
		if e.Dotted {
			s = 1
		} else if e.Thick {
			s = 2
		}
		d.styleDD.SetSelected(s)
	}
}

func (d *diagramEditor) addBox(x, y float64) {
	if x < 0 {
		x = (d.scroll.HAdjustment().Value()+d.scroll.HAdjustment().PageSize()/2-dePad)/d.zoom - 50
		y = (d.scroll.VAdjustment().Value()+d.scroll.VAdjustment().PageSize()/2-dePad)/d.zoom - 20
	}
	x, y = d.snapTo(x), d.snapTo(y)
	var parent *diagram.Item
	for _, it := range d.g.Order {
		if it.Group && x >= it.X && x <= it.X+it.W && y >= it.Y && y <= it.Y+it.H && (parent == nil || depthOf(it) > depthOf(parent)) {
			parent = it
		}
	}
	var nb *diagram.Item
	d.mutate(true, func() { nb = d.g.AddBox("New box", parent, x, y) })
	if nb == nil {
		return
	}
	d.sel = map[string]bool{nb.ID: true}
	d.selEdge = nil
	d.syncTools()
	d.editItem(nb)
}

func depthOf(it *diagram.Item) int {
	n := 0
	for a := it.Parent; a != nil; a = a.Parent {
		n++
	}
	return n
}

func (d *diagramEditor) group() {
	var items []*diagram.Item
	for _, it := range d.g.Order {
		if d.sel[it.ID] {
			items = append(items, it)
		}
	}
	if len(items) == 0 {
		return
	}
	var gr *diagram.Item
	d.mutate(true, func() { gr = d.g.GroupItems(items, "Group") })
	if gr != nil {
		d.sel = map[string]bool{gr.ID: true}
		d.syncTools()
		d.editItem(gr)
	}
}

func (d *diagramEditor) ungroup() {
	var gs []*diagram.Item
	for _, it := range d.g.Order {
		if d.sel[it.ID] && it.Group {
			gs = append(gs, it)
		}
	}
	if len(gs) == 0 {
		return
	}
	d.mutate(true, func() {
		for _, gr := range gs {
			d.g.Ungroup(gr)
			delete(d.sel, gr.ID)
		}
	})
}

func (d *diagramEditor) remove() {
	if d.selEdge == nil && len(d.sel) == 0 {
		return
	}
	e := d.selEdge
	ids := []string{}
	for id := range d.sel {
		ids = append(ids, id)
	}
	d.mutate(true, func() {
		if e != nil {
			d.g.RemoveEdge(e)
		}
		for _, id := range ids {
			if it := d.g.Items[id]; it != nil {
				d.g.Remove(it)
			}
		}
	})
	d.sel, d.selEdge = map[string]bool{}, nil
	d.area.QueueDraw()
}

func (d *diagramEditor) snapTo(v float64) float64 { return math.Round(v/deGrid) * deGrid }

// ---- Zoom ------------------------------------------------------------------

func (d *diagramEditor) setZoom(z float64) {
	d.zoom = math.Min(math.Max(z, 0.2), 3)
	d.layout()
}

func (d *diagramEditor) fit() {
	w, h := float64(d.scroll.AllocatedWidth()), float64(d.scroll.AllocatedHeight())
	if w < 50 || h < 50 {
		w, h = 1140, 640
	}
	d.setZoom(math.Min((w-2*dePad)/d.cw, (h-2*dePad)/d.ch))
}

// ---- Hit testing -----------------------------------------------------------

func (d *diagramEditor) toCanvas(x, y float64) diagram.Pt {
	return diagram.Pt{X: (x - dePad) / d.zoom, Y: (y - dePad) / d.zoom}
}

func inRect(it *diagram.Item, p diagram.Pt, m float64) bool {
	return p.X >= it.X-m && p.X <= it.X+it.W+m && p.Y >= it.Y-m && p.Y <= it.Y+it.H+m
}

func (d *diagramEditor) boxAt(p diagram.Pt, m float64) *diagram.Item {
	for i := len(d.g.Order) - 1; i >= 0; i-- {
		if it := d.g.Order[i]; !it.Group && inRect(it, p, m) {
			return it
		}
	}
	return nil
}

func (d *diagramEditor) groupAt(p diagram.Pt, headerOnly bool) *diagram.Item {
	var best *diagram.Item
	for _, it := range d.g.Order {
		if it.Group && inRect(it, p, 0) && (!headerOnly || p.Y <= it.Y+26) && (best == nil || depthOf(it) > depthOf(best)) {
			best = it
		}
	}
	return best
}

func segDist(p, a, b diagram.Pt) float64 {
	dx, dy := b.X-a.X, b.Y-a.Y
	l := dx*dx + dy*dy
	t := 0.0
	if l > 0 {
		t = math.Min(math.Max(((p.X-a.X)*dx+(p.Y-a.Y)*dy)/l, 0), 1)
	}
	return math.Hypot(p.X-(a.X+t*dx), p.Y-(a.Y+t*dy))
}

func (d *diagramEditor) edgeAt(p diagram.Pt) *diagram.Edge {
	for i := len(d.scene.Routes) - 1; i >= 0; i-- {
		r := d.scene.Routes[i]
		for j := 1; j < len(r.Pts); j++ {
			if segDist(p, r.Pts[j-1], r.Pts[j]) <= 6/d.zoom {
				return r.Edge
			}
		}
	}
	return nil
}

func handlePt(it *diagram.Item, side int) diagram.Pt {
	switch side {
	case 0:
		return diagram.Pt{X: it.X + it.W/2, Y: it.Y}
	case 1:
		return diagram.Pt{X: it.X + it.W, Y: it.Y + it.H/2}
	case 2:
		return diagram.Pt{X: it.X + it.W/2, Y: it.Y + it.H}
	}
	return diagram.Pt{X: it.X, Y: it.Y + it.H/2}
}

func (d *diagramEditor) handleAt(it *diagram.Item, p diagram.Pt) int {
	for s := 0; s < 4; s++ {
		if h := handlePt(it, s); math.Hypot(p.X-h.X, p.Y-h.Y) <= (deHandle+4)/d.zoom {
			return s
		}
	}
	return -1
}

// ---- Input -----------------------------------------------------------------

func (d *diagramEditor) wire() {
	var nPress int
	click := gtk.NewGestureClick()
	click.SetButton(1)
	drag := gtk.NewGestureDrag()
	drag.SetButton(1)
	click.Connect("pressed", func(c *gtk.GestureClick, n int, x, y float64) {
		d.area.GrabFocus()
		nPress = n
		p := d.toCanvas(x, y)
		st := click.CurrentEventState()
		if n >= 2 {
			d.mode = deIdle
			d.doubleClick(p)
			return
		}
		d.press(p, st)
	})
	drag.ConnectDragUpdate(func(dx, dy float64) {
		if nPress >= 2 {
			return
		}
		d.dragTo(diagram.Pt{X: d.start.X + dx/d.zoom, Y: d.start.Y + dy/d.zoom}, drag.CurrentEventState())
	})
	drag.ConnectDragEnd(func(dx, dy float64) {
		if nPress >= 2 {
			return
		}
		d.release(drag.CurrentEventState())
	})
	d.area.AddController(click)
	d.area.AddController(drag)

	motion := gtk.NewEventControllerMotion()
	motion.ConnectMotion(func(x, y float64) {
		if d.mode != deIdle {
			return
		}
		p := d.toCanvas(x, y)
		old, oldSide := d.hover, d.hoverSide
		d.hover, d.hoverSide = d.boxAt(p, 12), -1
		if d.hover != nil {
			d.hoverSide = d.handleAt(d.hover, p)
		}
		if d.hover != old || d.hoverSide != oldSide {
			d.area.QueueDraw()
		}
	})
	motion.ConnectLeave(func() {
		if d.mode == deIdle && d.hover != nil {
			d.hover = nil
			d.area.QueueDraw()
		}
	})
	d.area.AddController(motion)

	scroll := gtk.NewEventControllerScroll(gtk.EventControllerScrollVertical)
	scroll.ConnectScroll(func(dx, dy float64) bool {
		if scroll.CurrentEventState()&gdk.ControlMask == 0 {
			return false
		}
		if dy < 0 {
			d.setZoom(d.zoom * 1.1)
		} else if dy > 0 {
			d.setZoom(d.zoom / 1.1)
		}
		return true
	})
	d.area.AddController(scroll)

	key := gtk.NewEventControllerKey()
	key.ConnectKeyPressed(func(val, code uint, st gdk.ModifierType) bool { return d.key(val, st) })
	d.area.AddController(key)
}

func (d *diagramEditor) key(val uint, st gdk.ModifierType) bool {
	ctrl, shift, alt := st&gdk.ControlMask != 0, st&gdk.ShiftMask != 0, st&gdk.AltMask != 0
	switch {
	case ctrl && (val == gdk.KEY_z || val == gdk.KEY_Z) && !shift:
		d.doUndo()
	case ctrl && (val == gdk.KEY_Z || val == gdk.KEY_z && shift || val == gdk.KEY_y):
		d.doRedo()
	case val == gdk.KEY_Delete || val == gdk.KEY_BackSpace:
		d.remove()
	case val == gdk.KEY_Left || val == gdk.KEY_Right || val == gdk.KEY_Up || val == gdk.KEY_Down:
		step := deGrid
		if alt {
			step = 1
		}
		dx, dy := 0.0, 0.0
		switch val {
		case gdk.KEY_Left:
			dx = -step
		case gdk.KEY_Right:
			dx = step
		case gdk.KEY_Up:
			dy = -step
		default:
			dy = step
		}
		ls := d.leaves()
		if len(ls) == 0 {
			return false
		}
		d.mutate(false, func() {
			for _, it := range ls {
				d.g.SetPos(it.ID, it.X+dx, it.Y+dy)
			}
		})
	case val == gdk.KEY_Escape:
		d.close()
	default:
		return false
	}
	return true
}

func (d *diagramEditor) press(p diagram.Pt, st gdk.ModifierType) {
	d.start, d.cur, d.moved = p, p, false
	shift := st&gdk.ShiftMask != 0
	if d.hover != nil && d.hoverSide >= 0 {
		d.mode, d.connectFrom = deConnect, d.hover
		return
	}
	if it := d.boxAt(p, 0); it != nil {
		d.selEdge = nil
		if shift {
			if d.sel[it.ID] {
				delete(d.sel, it.ID)
			} else {
				d.sel[it.ID] = true
			}
		} else if !d.sel[it.ID] {
			d.sel = map[string]bool{it.ID: true}
		}
		d.beginMove()
		d.syncTools()
		return
	}
	if e := d.edgeAt(p); e != nil {
		d.sel, d.selEdge, d.mode = map[string]bool{}, e, deIdle
		d.syncTools()
		d.area.QueueDraw()
		return
	}
	if gr := d.groupAt(p, true); gr != nil {
		d.selEdge = nil
		if !shift {
			d.sel = map[string]bool{}
		}
		d.sel[gr.ID] = true
		d.beginMove()
		d.syncTools()
		return
	}
	if !shift {
		d.sel = map[string]bool{}
	}
	d.selEdge, d.mode = nil, deBand
	d.area.QueueDraw()
}

func (d *diagramEditor) beginMove() {
	d.mode = deMove
	d.snap = d.g.Clone()
	d.startPos = map[string]diagram.Pt{}
	for _, it := range d.leaves() {
		d.startPos[it.ID] = diagram.Pt{X: it.X, Y: it.Y}
	}
	d.direct = d.boxes()
	d.startRects = map[string][4]float64{}
	for _, it := range d.g.Order {
		if it.Group {
			d.startRects[it.ID] = [4]float64{it.X, it.Y, it.W, it.H}
		}
	}
	d.area.QueueDraw()
}

func (d *diagramEditor) dragTo(p diagram.Pt, st gdk.ModifierType) {
	d.cur = p
	switch d.mode {
	case deMove:
		dx, dy := p.X-d.start.X, p.Y-d.start.Y
		if !d.moved && math.Hypot(dx, dy)*d.zoom < 3 {
			return
		}
		if !d.moved {
			d.moved = true
			d.push(d.snap)
		}
		// The box under the pointer is what snaps; the rest keep their distance.
		ls := d.leaves()
		if len(ls) == 0 {
			return
		}
		sx, sy := 0.0, 0.0
		if st&gdk.AltMask == 0 {
			a := d.startPos[ls[0].ID]
			sx, sy = d.snapTo(a.X+dx)-(a.X+dx), d.snapTo(a.Y+dy)-(a.Y+dy)
		}
		for _, it := range ls {
			s := d.startPos[it.ID]
			nx := math.Min(math.Max(s.X+dx+sx, 0), d.cw-it.W)
			ny := math.Min(math.Max(s.Y+dy+sy, 0), d.ch-it.H)
			if nx+it.W > d.cw-40 && d.cw < diagram.MaxSize {
				d.cw += deGrow
			}
			if ny+it.H > d.ch-40 && d.ch < diagram.MaxSize {
				d.ch += deGrow
			}
			d.g.SetPos(it.ID, nx, ny)
		}
		// A box dropped on a group joins it, and one taken out of it leaves.
		for _, it := range d.direct {
			c := diagram.Pt{X: it.X + it.W/2, Y: it.Y + it.H/2}
			var target *diagram.Item
			for id, r := range d.startRects {
				gr := d.g.Items[id]
				if gr != nil && c.X >= r[0] && c.X <= r[0]+r[2] && c.Y >= r[1] && c.Y <= r[1]+r[3] && (target == nil || depthOf(gr) > depthOf(target)) {
					target = gr
				}
			}
			d.g.Reparent(it, target)
		}
		d.layout()
	case deConnect, deBand:
		d.area.QueueDraw()
	}
}

func (d *diagramEditor) release(st gdk.ModifierType) {
	mode := d.mode
	d.mode = deIdle
	switch mode {
	case deConnect:
		from := d.connectFrom
		d.connectFrom = nil
		if to := d.boxAt(d.cur, 4); to != nil && from != nil && to != from && math.Hypot(d.cur.X-d.start.X, d.cur.Y-d.start.Y) > 6/d.zoom {
			for _, e := range d.g.Edges {
				if e.From == from.ID && e.To == to.ID {
					d.area.QueueDraw()
					return
				}
			}
			var ne *diagram.Edge
			d.mutate(true, func() { ne = d.g.AddEdge(from.ID, to.ID) })
			if ne != nil {
				d.sel, d.selEdge = map[string]bool{}, ne
				d.syncTools()
			}
		}
	case deBand:
		x0, x1 := math.Min(d.start.X, d.cur.X), math.Max(d.start.X, d.cur.X)
		y0, y1 := math.Min(d.start.Y, d.cur.Y), math.Max(d.start.Y, d.cur.Y)
		for _, it := range d.g.Order {
			if !it.Group && it.X < x1 && it.X+it.W > x0 && it.Y < y1 && it.Y+it.H > y0 {
				d.sel[it.ID] = true
			}
		}
		d.syncTools()
	}
	d.area.QueueDraw()
}

func (d *diagramEditor) doubleClick(p diagram.Pt) {
	if it := d.boxAt(p, 0); it != nil {
		d.sel, d.selEdge = map[string]bool{it.ID: true}, nil
		d.editItem(it)
	} else if e := d.edgeAt(p); e != nil {
		d.sel, d.selEdge = map[string]bool{}, e
		d.editEdge(e)
	} else if gr := d.groupAt(p, true); gr != nil {
		d.sel, d.selEdge = map[string]bool{gr.ID: true}, nil
		d.editItem(gr)
	} else {
		d.addBox(p.X-50, p.Y-20)
	}
	d.area.QueueDraw()
}

// ---- Editing text in place -------------------------------------------------

// popoverAt opens a small popover over a rectangle of the canvas.
func (d *diagramEditor) popoverAt(x, y, w, h float64, content gtk.Widgetter) *gtk.Popover {
	if d.pop != nil {
		d.pop.Popdown()
	}
	pop := gtk.NewPopover()
	pop.SetChild(content)
	pop.SetParent(d.area)
	pop.SetAutohide(true)
	r := gdk.NewRectangle(int(dePad+x*d.zoom), int(dePad+y*d.zoom), int(math.Max(w*d.zoom, 1)), int(math.Max(h*d.zoom, 1)))
	pop.SetPointingTo(&r)
	pop.ConnectClosed(func() {
		if d.pop == pop {
			d.pop = nil
		}
		glib.IdleAdd(func() { pop.Unparent(); d.area.GrabFocus() })
	})
	d.pop = pop
	pop.Popup()
	return pop
}

func (d *diagramEditor) editItem(it *diagram.Item) {
	title, sub := diagram.TextOf(it)
	box := gtk.NewBox(gtk.OrientationVertical, 6)
	te := gtk.NewEntry()
	te.SetText(title)
	te.SetMaxLength(80)
	te.SetWidthChars(26)
	te.SetPlaceholderText("Title")
	te.UpdateProperty([]gtk.AccessibleProperty{gtk.AccessiblePropertyLabel}, []coreglib.Value{*coreglib.NewValue("Box title")})
	box.Append(te)
	var se *gtk.Entry
	if !it.Group {
		se = gtk.NewEntry()
		se.SetText(sub)
		se.SetWidthChars(26)
		se.SetPlaceholderText("Subtitle (optional)")
		se.UpdateProperty([]gtk.AccessibleProperty{gtk.AccessiblePropertyLabel}, []coreglib.Value{*coreglib.NewValue("Box subtitle")})
		box.Append(se)
	}
	pop := d.popoverAt(it.X, it.Y, it.W, it.H, box)
	apply := func() {
		nt, ns := te.Text(), ""
		if se != nil {
			ns = se.Text()
		}
		if nt == title && ns == sub {
			return
		}
		d.mutate(false, func() { d.g.SetText(it, nt, ns) })
	}
	te.ConnectActivate(func() {
		if se != nil {
			se.GrabFocus()
		} else {
			pop.Popdown()
		}
	})
	if se != nil {
		se.ConnectActivate(func() { pop.Popdown() })
	}
	pop.ConnectClosed(apply)
	te.GrabFocus()
	te.SelectRegion(0, -1)
}

func (d *diagramEditor) editEdge(e *diagram.Edge) {
	r := d.scene.Routes
	var at diagram.Pt
	for _, rt := range r {
		if rt.Edge == e {
			at = rt.Label
			if at == (diagram.Pt{}) && len(rt.Pts) > 0 {
				at = rt.Pts[len(rt.Pts)/2]
			}
		}
	}
	en := gtk.NewEntry()
	en.SetText(e.Label)
	en.SetMaxLength(80)
	en.SetWidthChars(24)
	en.SetPlaceholderText("Label")
	en.UpdateProperty([]gtk.AccessibleProperty{gtk.AccessiblePropertyLabel}, []coreglib.Value{*coreglib.NewValue("Arrow label")})
	pop := d.popoverAt(at.X-4, at.Y-4, 8, 8, en)
	old := e.Label
	en.ConnectActivate(func() { pop.Popdown() })
	pop.ConnectClosed(func() {
		if nl := diagram.Clean(en.Text()); nl != old {
			d.mutate(false, func() { e.Label = nl })
		}
	})
	en.GrabFocus()
}

// ---- Drawing ---------------------------------------------------------------

func (d *diagramEditor) draw(cr *cairo.Context) {
	pal := diagramPalette(d.area)
	// Outside the canvas, a little off the page colour; the canvas itself in it.
	out := pal[diagram.RoleGroup]
	cr.SetSourceRGBA(out.R, out.G, out.B, 1)
	cr.Paint()
	cr.Translate(dePad, dePad)
	cr.Scale(d.zoom, d.zoom)
	setRole(cr, &pal, diagram.RoleBg)
	cr.Rectangle(0, 0, d.cw, d.ch)
	cr.Fill()
	setRole(cr, &pal, diagram.RoleGroupStroke)
	step := deGrid
	if d.zoom < 0.5 {
		step = deGrid * 2
	}
	for y := step; y < d.ch; y += step {
		for x := step; x < d.cw; x += step {
			cr.Rectangle(x-0.75, y-0.75, 1.5, 1.5)
		}
	}
	cr.Fill()
	setRole(cr, &pal, diagram.RoleBoxStroke)
	cr.SetLineWidth(1 / d.zoom)
	cr.Rectangle(0, 0, d.cw, d.ch)
	cr.Stroke()
	paintScene(cr, d.scene, &pal, d.font)

	acc := func() { setRole(cr, &pal, diagram.RoleAccent) }
	if e := d.selEdge; e != nil {
		for _, r := range d.scene.Routes {
			if r.Edge != e {
				continue
			}
			for _, s := range diagram.PathSegs(r.Pts, 8) {
				switch s.Op {
				case 'M':
					cr.MoveTo(s.P[0].X, s.P[0].Y)
				case 'L':
					cr.LineTo(s.P[0].X, s.P[0].Y)
				case 'C':
					cr.CurveTo(s.P[0].X, s.P[0].Y, s.P[1].X, s.P[1].Y, s.P[2].X, s.P[2].Y)
				}
			}
			setRole(cr, &pal, diagram.RoleAccentStrong)
			cr.SetLineWidth(7)
			cr.Stroke()
		}
	}
	ids := make([]string, 0, len(d.sel))
	for id := range d.sel {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if it := d.g.Items[id]; it != nil {
			roundedRect(cr, it.X-3, it.Y-3, it.W+6, it.H+6, 8)
			acc()
			cr.SetLineWidth(2)
			cr.Stroke()
		}
	}
	handles := d.hover
	if d.mode == deConnect {
		handles = d.connectFrom
	}
	if handles != nil {
		for s := 0; s < 4; s++ {
			h := handlePt(handles, s)
			cr.NewSubPath()
			cr.Arc(h.X, h.Y, deHandle, 0, 2*math.Pi)
			setRole(cr, &pal, diagram.RoleBg)
			cr.FillPreserve()
			acc()
			cr.SetLineWidth(1.5)
			cr.Stroke()
		}
	}
	switch d.mode {
	case deConnect:
		if d.connectFrom != nil {
			if to := d.boxAt(d.cur, 4); to != nil && to != d.connectFrom {
				roundedRect(cr, to.X-3, to.Y-3, to.W+6, to.H+6, 8)
				acc()
				cr.SetLineWidth(2)
				cr.Stroke()
			}
			h := handlePt(d.connectFrom, max(d.hoverSide, 0))
			cr.MoveTo(h.X, h.Y)
			cr.LineTo(d.cur.X, d.cur.Y)
			acc()
			cr.SetDash([]float64{5, 4}, 0)
			cr.SetLineWidth(2)
			cr.Stroke()
			cr.SetDash(nil, 0)
		}
	case deBand:
		x0, y0 := math.Min(d.start.X, d.cur.X), math.Min(d.start.Y, d.cur.Y)
		w, h := math.Abs(d.cur.X-d.start.X), math.Abs(d.cur.Y-d.start.Y)
		cr.Rectangle(x0, y0, w, h)
		setRole(cr, &pal, diagram.RoleAccentTint)
		cr.FillPreserve()
		acc()
		cr.SetLineWidth(1 / d.zoom)
		cr.Stroke()
	}
}

// ---- Done and Cancel -------------------------------------------------------

func (d *diagramEditor) close() {
	if d.pop != nil {
		d.pop.Popdown()
	}
	if !d.changed {
		d.dialog.ForceClose()
		return
	}
	a := adw.NewAlertDialog("Discard your changes?", "The diagram in the note is left as it was.")
	a.AddResponse("keep", "Keep Editing")
	a.AddResponse("discard", "Discard")
	a.SetResponseAppearance("discard", adw.ResponseDestructive)
	a.SetDefaultResponse("keep")
	a.SetCloseResponse("keep")
	a.ConnectResponse(func(r string) {
		if r == "discard" {
			d.dialog.ForceClose()
		}
	})
	a.Present(d.dialog)
}

func (d *diagramEditor) done() {
	if d.pop != nil {
		d.pop.Popdown()
	}
	if !d.changed {
		d.dialog.ForceClose()
		return
	}
	if d.cw > d.cw0 || d.ch > d.ch0 {
		d.g.CanvasW, d.g.CanvasH = d.cw, d.ch
	}
	text := d.g.Mermaid()
	if !d.g.Reads(text) {
		a := adw.NewAlertDialog("This diagram cannot be saved", "Something in it, such as a box named like a Mermaid keyword, would be written back as a different diagram. The note is unchanged.")
		a.AddResponse("ok", "OK")
		a.Present(d.dialog)
		return
	}
	d.e.replaceDiagramBlock(d.first, d.last, text)
	d.dialog.ForceClose()
}

// replaceDiagramBlock swaps the text between a block's fences for text, as one
// undo step, and moves the caret out of the block so the picture is drawn.
func (e *Editor) replaceDiagramBlock(first, last int, text string) {
	start, ok1 := e.buffer.IterAtLine(first + 1)
	end, ok2 := e.buffer.IterAtLine(last)
	if !ok1 || !ok2 {
		return
	}
	caret := e.buffer.IterAtMark(e.buffer.GetInsert()).Offset()
	startOff, endOff := start.Offset(), end.Offset()
	e.buffer.BeginUserAction()
	e.buffer.Delete(start, end)
	ins := e.buffer.IterAtOffset(startOff)
	e.buffer.Insert(ins, text)
	e.buffer.EndUserAction()
	newEnd := startOff + len([]rune(text))
	switch {
	case caret < startOff:
	case caret >= endOff:
		caret += (newEnd - startOff) - (endOff - startOff)
	default:
		caret = -1
	}
	if caret >= 0 {
		e.buffer.PlaceCursor(e.buffer.IterAtOffset(caret))
	} else if it, ok := e.buffer.IterAtLine(first + 1 + strings.Count(text, "\n") + 1); ok && first+2+strings.Count(text, "\n") < e.buffer.LineCount() {
		e.buffer.PlaceCursor(it)
	} else if it, ok := e.buffer.IterAtLine(max(first-1, 0)); ok {
		e.buffer.PlaceCursor(it)
	}
	e.Reparse()
}
