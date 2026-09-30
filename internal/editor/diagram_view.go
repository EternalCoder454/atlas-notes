package editor

import (
	"math"
	"strings"
	"unicode/utf8"

	"github.com/diamondburned/gotk4/pkg/cairo"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"atlas-notes/internal/diagram"
)

// This file draws Mermaid flowcharts in place of their code blocks, the way
// tables.go draws tables: the lines stay in the buffer as written (autosave,
// undo and the index never learn about the picture), they are hidden while the
// caret is away, and a widget is laid over the text where they were with blank
// space under the block's first line. The caret in the block shows the source.
// A block the diagram package cannot read stays the code block it is, with a
// note in its label of what could not be drawn.

const (
	// diagramMinScale is the smallest a wide diagram is shrunk to before it
	// scrolls sideways instead, so that its text stays readable.
	diagramMinScale = 0.62
	// diagramGap is the blank space left under a diagram.
	diagramGap = 10
	// maxPooledDiagrams caps the drawing areas kept for reuse, for the reason
	// given at imageBox.
	maxPooledDiagrams = 16
)

// diagramHit is a diagram the render pass has just hidden the lines of.
type diagramHit struct {
	line, lines int
	src         string
}

// diagramBox is the widget for one diagram, kept for reuse.
type diagramBox struct {
	serial int
	scroll *gtk.ScrolledWindow
	area   *gtk.DrawingArea
	font   *diagramFont
	src    string // what scene was laid out from
	scene  *diagram.Scene
	scale  float64
}

type diagramItem struct {
	overlay
	src   string
	lines int
	box   *diagramBox
}

type diagramParse struct {
	ok  bool
	msg string
}

type diagramState struct {
	items  []*diagramItem
	hits   []diagramHit
	byLine map[int]*diagramItem
	pool   []*diagramBox
	serial int
	memo   map[int]bool // per block start: the pass decided it is drawn
	parsed map[string]diagramParse
}

// diagramSource is the text between the fences of a mermaid block.
func (e *Editor) diagramSource(b *richBlock) string {
	var sb strings.Builder
	for l := b.first + 1; l < b.last; l++ {
		t, _ := e.lineText(l)
		sb.WriteString(t)
		sb.WriteByte('\n')
	}
	return sb.String()
}

// diagramCheck parses a source, remembering the answer.
func (e *Editor) diagramCheck(src string) diagramParse {
	s := &e.dia
	if r, ok := s.parsed[src]; ok {
		return r
	}
	if s.parsed == nil || len(s.parsed) > 64 {
		s.parsed = map[string]diagramParse{}
	}
	var r diagramParse
	if _, err := diagram.Parse(src); err != nil {
		r.msg = err.Error()
	} else {
		r.ok = true
	}
	s.parsed[src] = r
	return r
}

// drawsDiagram reports whether the block is drawn as a diagram: a closed
// mermaid block the caret is not in, whose text is a flowchart that can be drawn.
func (e *Editor) drawsDiagram(b *richBlock, cursorLine int) bool {
	if b.kind != kindCode || !b.closed || b.last < b.first+2 || !strings.EqualFold(b.lang, "mermaid") || revealed(b, cursorLine) {
		return false
	}
	s := &e.dia
	if v, ok := s.memo[b.first]; ok {
		return v
	}
	if s.memo == nil {
		s.memo = map[int]bool{}
	}
	v := e.diagramCheck(e.diagramSource(b)).ok
	s.memo[b.first] = v
	return v
}

// tagDiagramLine hides a line of a drawn diagram. The first line notes the
// diagram for syncDiagrams and gets its spacing back.
func (e *Editor) tagDiagramLine(lineNum int, line string, b *richBlock) {
	n := utf8.RuneCountInString(line)
	name := "invisible"
	if !hideMarkers {
		name = "marker"
	}
	if n == 0 {
		e.tagNewline(name, lineNum)
	} else {
		e.applyTag(name, lineNum, 0, n)
	}
	if lineNum != b.first {
		return
	}
	e.dia.hits = append(e.dia.hits, diagramHit{lineNum, b.last - b.first + 1, e.diagramSource(b)})
	if it := e.diagramItemAt(lineNum); it != nil && it.pad != "" {
		e.applyPad(&it.overlay, lineNum, it.pad)
	}
}

func (e *Editor) diagramItemAt(line int) *diagramItem {
	s := &e.dia
	if len(s.items) == 0 {
		return nil
	}
	if s.byLine == nil {
		s.byLine = make(map[int]*diagramItem, len(s.items))
		for _, it := range s.items {
			if l := e.markLine(it.mark); l >= 0 {
				s.byLine[l] = it
			}
		}
	}
	return s.byLine[line]
}

// diagramDrawnAt reports whether a diagram is on show for the block at line.
func (e *Editor) diagramDrawnAt(line int) bool { return e.diagramItemAt(line) != nil }

func (e *Editor) clearDiagrams() {
	for _, it := range e.dia.items {
		e.dropDiagram(it)
	}
	e.dia.items = nil
	e.dia.hits = e.dia.hits[:0]
	e.dia.byLine = nil
	e.dia.memo = nil
}

// syncDiagrams runs after the render pass, as syncTables does.
func (e *Editor) syncDiagrams(from, to int) {
	s := &e.dia
	hits := s.hits
	s.hits = s.hits[:0]
	s.byLine = nil
	s.memo = nil
	if len(s.items) == 0 && len(hits) == 0 {
		return
	}
	seen := map[int]bool{}
	kept := s.items[:0]
	for _, it := range s.items {
		line := e.markLine(it.mark)
		var h *diagramHit
		for i := range hits {
			if hits[i].line == line {
				h = &hits[i]
			}
		}
		inRange := line >= from && line <= to
		if line < 0 || seen[line] || (inRange && h == nil) {
			e.dropDiagram(it)
			continue
		}
		seen[line] = true
		if h != nil {
			it.lines = h.lines
			if h.src != it.src {
				it.src = h.src // laid out again when it is next sized
				it.box.area.QueueDraw()
			}
		}
		kept = append(kept, it)
	}
	for i := len(kept); i < len(s.items); i++ {
		s.items[i] = nil
	}
	s.items = kept
	for _, h := range hits {
		if seen[h.line] {
			continue
		}
		iter, ok := e.buffer.IterAtLine(h.line)
		if !ok {
			continue
		}
		it := &diagramItem{overlay: overlay{mark: e.buffer.CreateMark("", iter, true)}, src: h.src, lines: h.lines}
		it.box = e.takeDiagramBox()
		it.box.src, it.box.scene = "", nil
		s.items = append(s.items, it)
	}
	e.queuePlace()
}

func (e *Editor) dropDiagram(it *diagramItem) {
	if it.box != nil {
		if it.shown {
			e.view.Remove(it.box.scroll)
		}
		if len(e.dia.pool) < maxPooledDiagrams {
			e.dia.pool = append(e.dia.pool, it.box)
		}
		it.box = nil
	}
	if !it.mark.Deleted() {
		e.buffer.DeleteMark(it.mark)
	}
}

// diagramPalette reads the colours a diagram is drawn in from the widget's style.
func diagramPalette(w gtk.Widgetter) diagram.Palette {
	wd := gtk.BaseWidget(w)
	sc := wd.StyleContext()
	look := func(names ...string) (diagram.RGBA, bool) {
		for _, n := range names {
			if c, ok := sc.LookupColor(n); ok && c != nil {
				return diagram.RGBA{R: float64(c.Red()), G: float64(c.Green()), B: float64(c.Blue()), A: float64(c.Alpha())}, true
			}
		}
		return diagram.RGBA{}, false
	}
	fgc := wd.Color()
	fg := diagram.RGBA{R: float64(fgc.Red()), G: float64(fgc.Green()), B: float64(fgc.Blue()), A: 1}
	bg, ok := look("view_bg_color", "window_bg_color")
	if !ok {
		bg = diagram.RGBA{R: 1, G: 1, B: 1, A: 1}
		if fg.R > 0.5 {
			bg = diagram.RGBA{R: 0.14, G: 0.14, B: 0.16, A: 1}
		}
	}
	acc, ok := look("accent_color", "accent_bg_color")
	if !ok {
		acc = diagram.RGBA{R: 0.21, G: 0.52, B: 0.89, A: 1}
	}
	mix := func(k float64) diagram.RGBA {
		return diagram.RGBA{R: bg.R + (fg.R-bg.R)*k, G: bg.G + (fg.G-bg.G)*k, B: bg.B + (fg.B-bg.B)*k, A: 1}
	}
	var p diagram.Palette
	p[diagram.RoleBg] = bg
	p[diagram.RoleFg] = fg
	p[diagram.RoleDim] = mix(0.62)
	p[diagram.RoleAccent] = acc
	p[diagram.RoleAccentTint] = diagram.RGBA{R: acc.R, G: acc.G, B: acc.B, A: 0.16}
	p[diagram.RoleBox] = mix(0.07)
	p[diagram.RoleBoxStroke] = mix(0.30)
	p[diagram.RoleGroup] = mix(0.035)
	p[diagram.RoleGroupStroke] = mix(0.18)
	p[diagram.RoleLine] = mix(0.60)
	return p
}

func (e *Editor) takeDiagramBox() *diagramBox {
	s := &e.dia
	if n := len(s.pool); n > 0 {
		b := s.pool[n-1]
		s.pool = s.pool[:n-1]
		return b
	}
	s.serial++
	b := &diagramBox{serial: s.serial, scale: 1}
	b.area = gtk.NewDrawingArea()
	b.area.AddCSSClass("atlas-diagram")
	b.font = &diagramFont{}
	b.scroll = gtk.NewScrolledWindow()
	b.scroll.SetPolicy(gtk.PolicyAutomatic, gtk.PolicyNever)
	b.scroll.SetChild(b.area)
	b.scroll.AddCSSClass("atlas-diagram-scroll")
	b.area.SetDrawFunc(func(_ *gtk.DrawingArea, cr *cairo.Context, w, h int) {
		if b.scene == nil {
			return
		}
		pal := diagramPalette(b.area)
		cr.Scale(b.scale, b.scale)
		paintScene(cr, b.scene, &pal, b.font)
	})
	// A press puts the caret in the block, where the source shows for editing.
	click := gtk.NewGestureClick()
	click.SetButton(1)
	serial := b.serial
	click.Connect("pressed", func(c *gtk.GestureClick, nPress int) {
		c.SetState(gtk.EventSequenceClaimed)
		e.editDiagram(serial)
	})
	b.area.AddController(click)
	return b
}

func (e *Editor) editDiagram(serial int) {
	for _, it := range e.dia.items {
		if it.box == nil || it.box.serial != serial {
			continue
		}
		if line := e.markLine(it.mark); line >= 0 {
			if iter, ok := e.buffer.IterAtLine(line); ok {
				e.buffer.PlaceCursor(iter)
				e.view.GrabFocus()
			}
		}
		return
	}
}

// ---- Size and position (the block interface) ---------------------------------

func (it *diagramItem) over() *overlay { return &it.overlay }

func (it *diagramItem) widget() gtk.Widgetter {
	if it.box == nil {
		return nil
	}
	return it.box.scroll
}

func (it *diagramItem) ready(avail int) bool { return avail > 0 }

func (it *diagramItem) resize(e *Editor, avail int) { it.size(e, avail) }

func (it *diagramItem) wantPad(e *Editor, line, avail int) string {
	if avail <= 0 {
		return ""
	}
	_, h := it.size(e, avail)
	if h <= 0 {
		return ""
	}
	name, _ := tablePad(h + diagramGap - e.linesBelow(line, it.lines))
	return name
}

// size lays the diagram out when its source has changed, fits it to the view
// (shrunk down to diagramMinScale, then scrolled) and gives the widget that size.
func (it *diagramItem) size(e *Editor, avail int) (w, h int) {
	b := it.box
	if !it.shown {
		e.view.AddOverlay(b.scroll, 0, tableParkY)
		it.shown, it.x, it.y = true, 0, tableParkY
	}
	if b.scene == nil || b.src != it.src {
		g, err := diagram.Parse(it.src)
		if err != nil {
			return 0, 0
		}
		if b.font.layout == nil {
			pc := b.area.CreatePangoContext()
			b.font = newDiagramFont(pango.NewLayout(pc), pc.FontDescription().Family())
		}
		b.scene, b.src = diagram.Layout(g, b.font.measure), it.src
		b.area.QueueDraw()
	}
	sw, sh := b.scene.W, b.scene.H
	b.scale = 1
	if sw > float64(avail) {
		b.scale = math.Max(float64(avail)/sw, diagramMinScale)
	}
	cw, ch := int(math.Ceil(sw*b.scale)), int(math.Ceil(sh*b.scale))
	b.area.SetContentWidth(cw)
	b.area.SetContentHeight(ch)
	w = min(cw, avail)
	h = ch
	if cw > avail {
		h += 12 // room for the scroll bar
	}
	gtk.BaseWidget(b.scroll).SetSizeRequest(w, h)
	return w, h
}

// diagramNote is what the label of a mermaid block that is not drawn says.
func (e *Editor) diagramNote(b richBlock) string {
	if !b.closed || b.last < b.first+2 {
		return "Mermaid diagram"
	}
	r := e.diagramCheck(e.diagramSource(&b))
	if r.ok {
		return "Mermaid diagram"
	}
	return "Mermaid diagram, not drawn: " + r.msg
}
