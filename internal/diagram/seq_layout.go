package diagram

import (
	"math"
	"sort"
	"strconv"
	"strings"
)

const (
	seqPad      = 10.0
	seqHeadSize = 13.0
	seqBoxH     = 32.0
	seqActorH   = 66.0
	seqMinBoxW  = 84.0
	seqGap      = 28.0
	seqMsgWrap  = 240.0
	seqNoteWrap = 220.0
	seqBarW     = 10.0
	seqBarStep  = 4.0
	seqSelfW    = 32.0
	seqSelfH    = 20.0
)

type seqFrame struct {
	lo, hi float64 // the x extent of what is inside
	y0     float64
	kind   string
	labels []string
	divs   []float64 // the y of each label after the first
}

type seqLayout struct {
	s      *Sequence
	m      Measure
	wrapW  float64
	th     float64 // the height of a line of text
	cx     []float64
	boxW   []float64
	act    [][]float64 // per participant, the y where each open bar started
	y      float64
	gMin   float64
	gMax   float64
	num    int
	frames []*seqFrame

	rects, framePrims, lifelines, bars, msgs, notes, heads []Prim
}

func (l *seqLayout) ext(a, b float64) {
	l.gMin, l.gMax = math.Min(l.gMin, a), math.Max(l.gMax, b)
	if n := len(l.frames); n > 0 {
		f := l.frames[n-1]
		f.lo, f.hi = math.Min(f.lo, a), math.Max(f.hi, b)
	}
}

func (l *seqLayout) lines(text string, size, limit float64) ([]string, float64) {
	if text == "" {
		return nil, 0
	}
	ls := wrap(text, size, false, l.m, limit)
	var w float64
	for i, s := range ls {
		ls[i] = fitWidth(s, size, false, l.m, limit)
		tw, _ := l.m(ls[i], size, false)
		w = math.Max(w, tw)
	}
	return ls, w
}

// Scene lays the diagram out for a page avail wide (0 for a natural width).
// Messages wrap their text narrower, step by step, until it fits.
func (s *Sequence) Scene(m Measure, avail float64) *Scene {
	if m == nil {
		m = ApproxMeasure
	}
	var sc *Scene
	for _, w := range []float64{seqMsgWrap, 180, 140, 110} {
		sc = s.layout(m, w)
		if avail <= 0 || sc.W <= avail {
			break
		}
	}
	return sc
}

type seqNeed struct {
	i, j int
	w    float64
}

func (l *seqLayout) needs(evs []*SeqEvent, out *[]seqNeed) {
	n := len(l.s.Parts)
	for _, e := range evs {
		switch e.Kind {
		case evMsg:
			num := ""
			if l.s.AutoNumber {
				num = "99. "
			}
			_, tw := l.lines(num+e.Text, labelSize, l.wrapW)
			if e.A == e.B {
				if e.A < n-1 {
					*out = append(*out, seqNeed{e.A, e.A + 1, math.Max(seqSelfW+8, tw+12) + 16})
				}
				continue
			}
			i, j := min(e.A, e.B), max(e.A, e.B)
			*out = append(*out, seqNeed{i, j, tw + 28})
		case evNote:
			_, tw := l.lines(e.Text, labelSize, seqNoteWrap)
			w := math.Max(tw+16, 60)
			switch {
			case e.Side == 'l' && e.A > 0:
				*out = append(*out, seqNeed{e.A - 1, e.A, w + 20})
			case e.Side == 'r' && e.A < n-1:
				*out = append(*out, seqNeed{e.A, e.A + 1, w + 20})
			case e.Side == 'o' && e.A == e.B:
				if e.A > 0 {
					*out = append(*out, seqNeed{e.A - 1, e.A, w/2 + 12})
				}
				if e.A < n-1 {
					*out = append(*out, seqNeed{e.A, e.A + 1, w/2 + 12})
				}
			case e.Side == 'o':
				*out = append(*out, seqNeed{e.A, e.B, tw})
			}
		case evBlock:
			for _, p := range e.Block.Parts {
				l.needs(p.Events, out)
			}
		}
	}
}

func (s *Sequence) layout(m Measure, wrapW float64) *Scene {
	l := &seqLayout{s: s, m: m, wrapW: wrapW, gMin: math.Inf(1), gMax: math.Inf(-1)}
	_, l.th = m("Ag", labelSize, false)
	n := len(s.Parts)
	anyActor := false
	l.boxW = make([]float64, n)
	for i, p := range s.Parts {
		w, _ := m(p.Label, seqHeadSize, true)
		l.boxW[i] = math.Max(seqMinBoxW, math.Min(w, 200)+28)
		anyActor = anyActor || p.Actor
	}
	gaps := make([]float64, max(n-1, 0))
	for i := range gaps {
		gaps[i] = (l.boxW[i]+l.boxW[i+1])/2 + 16
	}
	var needs []seqNeed
	l.needs(s.Events, &needs)
	sort.SliceStable(needs, func(a, b int) bool { return needs[a].j-needs[a].i < needs[b].j-needs[b].i })
	for _, nd := range needs {
		var sum float64
		for k := nd.i; k < nd.j; k++ {
			sum += gaps[k]
		}
		if sum < nd.w {
			add := (nd.w - sum) / float64(nd.j-nd.i)
			for k := nd.i; k < nd.j; k++ {
				gaps[k] += add
			}
		}
	}
	l.cx = make([]float64, n)
	for i := 1; i < n; i++ {
		l.cx[i] = l.cx[i-1] + gaps[i-1]
	}
	l.act = make([][]float64, n)

	headH := seqBoxH
	if anyActor {
		headH = seqActorH
	}
	y0 := seqPad
	var titleH float64
	if s.Title != "" {
		_, th := m(s.Title, titleSize, true)
		titleH = th + 8
	}
	y0 += titleH
	ly0 := y0 + headH
	l.y = ly0 + 14
	l.events(s.Events)
	l.y += 6
	ly1 := l.y
	for i := range l.act {
		for len(l.act[i]) > 0 {
			l.popBar(i, ly1-4)
		}
	}
	for i, p := range s.Parts {
		l.ext(l.cx[i]-l.boxW[i]/2, l.cx[i]+l.boxW[i]/2)
		l.lifelines = append(l.lifelines, Prim{Kind: PrimPath, Pts: []Pt{{l.cx[i], ly0}, {l.cx[i], ly1}}, Stroke: RoleBoxStroke, StrokeW: 1, Dash: true})
		l.head(i, p, y0, headH, true)
		l.head(i, p, ly1, headH, false)
	}
	H := ly1 + headH + seqPad

	// Everything so far is placed from the first lifeline; slide it right so the
	// leftmost thing is a margin from the edge.
	if s.Title != "" {
		tw, _ := m(s.Title, titleSize, true)
		l.gMax = math.Max(l.gMax, l.gMin+tw)
	}
	dx := seqPad - l.gMin
	W := l.gMax - l.gMin + 2*seqPad
	sc := &Scene{W: W, H: H, Label: "Sequence diagram"}
	if s.Title != "" {
		sc.Prims = append(sc.Prims, Prim{Kind: PrimText, X: seqPad, Y: seqPad, Text: s.Title, Size: titleSize, Bold: true, Left: true, Fill: RoleFg})
	}
	start := len(sc.Prims)
	for _, p := range [][]Prim{l.rects, l.framePrims, l.lifelines, l.bars, l.msgs, l.notes, l.heads} {
		sc.Prims = append(sc.Prims, p...)
	}
	for i := start; i < len(sc.Prims); i++ {
		p := &sc.Prims[i]
		p.X += dx
		if len(p.Pts) > 0 {
			pts := make([]Pt, len(p.Pts))
			for k, q := range p.Pts {
				pts[k] = Pt{q.X + dx, q.Y}
			}
			p.Pts = pts
		}
	}
	var names []string
	for _, p := range s.Parts {
		names = append(names, p.Label)
	}
	d := "Participants " + strings.Join(names, ", ") + ", " + strconv.Itoa(s.nMsgs) + " messages"
	if s.Title != "" {
		d = s.Title + ", " + d
	}
	sc.Desc = truncate(d, 300)
	return sc
}

// head draws a participant's box or figure, at the top of the page or the bottom.
func (l *seqLayout) head(i int, p *Participant, y, rowH float64, top bool) {
	cx, w := l.cx[i], l.boxW[i]
	if !p.Actor {
		by := y
		if top {
			by = y + rowH - seqBoxH
		}
		l.heads = append(l.heads,
			Prim{Kind: PrimBox, X: cx - w/2, Y: by, W: w, H: seqBoxH, Radius: 4, Fill: RoleBox, Stroke: RoleBoxStroke, StrokeW: 1},
			Prim{Kind: PrimText, X: cx, Y: by + (seqBoxH-l.headTextH())/2, Text: fitWidth(p.Label, seqHeadSize, true, l.m, w-16), Size: seqHeadSize, Bold: true, Fill: RoleFg})
		return
	}
	// A figure with its name beneath, the same at the top and the bottom.
	hy := y + 7
	lw := 1.5
	l.heads = append(l.heads,
		Prim{Kind: PrimBox, X: cx - 7, Y: hy - 7, W: 14, H: 14, Shape: ShapeCircle, Fill: RoleBox, Stroke: RoleLine, StrokeW: lw},
		Prim{Kind: PrimPath, Pts: []Pt{{cx, hy + 7}, {cx, hy + 26}}, Stroke: RoleLine, StrokeW: lw},
		Prim{Kind: PrimPath, Pts: []Pt{{cx - 11, hy + 15}, {cx + 11, hy + 15}}, Stroke: RoleLine, StrokeW: lw},
		Prim{Kind: PrimPath, Pts: []Pt{{cx - 9, hy + 38}, {cx, hy + 26}, {cx + 9, hy + 38}}, Stroke: RoleLine, StrokeW: lw},
		Prim{Kind: PrimText, X: cx, Y: y + 48, Text: fitWidth(p.Label, seqHeadSize, true, l.m, w-8), Size: seqHeadSize, Bold: true, Fill: RoleFg})
}

func (l *seqLayout) headTextH() float64 {
	_, h := l.m("Ag", seqHeadSize, true)
	return h
}

func (l *seqLayout) depth(k int) int { return len(l.act[k]) }

// inset is how far an arrow stops short of a lifeline, for the bars on it.
func (l *seqLayout) inset(k int) float64 {
	d := l.depth(k)
	if d == 0 {
		return 0
	}
	return seqBarW/2 + seqBarStep*float64(d-1)
}

func (l *seqLayout) pushBar(k int, y float64) { l.act[k] = append(l.act[k], y) }

func (l *seqLayout) popBar(k int, y float64) {
	d := len(l.act[k]) - 1
	y0 := l.act[k][d]
	l.act[k] = l.act[k][:d]
	if y < y0 {
		y = y0
	}
	x := l.cx[k] - seqBarW/2 + seqBarStep*float64(d)
	l.bars = append(l.bars, Prim{Kind: PrimBox, X: x, Y: y0, W: seqBarW, H: y - y0, Fill: RoleBox, Stroke: RoleLine, StrokeW: 1})
	l.ext(x, x+seqBarW)
}

func (l *seqLayout) events(evs []*SeqEvent) {
	for _, e := range evs {
		switch e.Kind {
		case evMsg:
			l.message(e)
		case evNote:
			l.note(e)
		case evActivate:
			l.pushBar(e.A, l.y-4)
		case evDeactivate:
			l.popBar(e.A, l.y-6)
		case evBlock:
			l.block(e.Block)
		}
	}
}

func (l *seqLayout) message(e *SeqEvent) {
	text := e.Text
	if l.s.AutoNumber {
		l.num++
		text = strconv.Itoa(l.num) + ". " + text
	}
	ls, tw := l.lines(text, labelSize, l.wrapW)
	lh := l.th + 1
	textH := float64(len(ls)) * lh
	ay := l.y + textH + 8
	if len(ls) == 0 {
		ay = l.y + 10
	}
	if e.Activate {
		l.pushBar(e.B, ay)
	}
	cxa, cxb := l.cx[e.A], l.cx[e.B]
	stroke := 1.5
	if e.A == e.B {
		x := cxa + l.inset(e.A)
		pts := []Pt{{x, ay}, {x + seqSelfW, ay}, {x + seqSelfW, ay + seqSelfH}, {x, ay + seqSelfH}}
		l.msgs = append(l.msgs, Prim{Kind: PrimPath, Pts: pts, Stroke: RoleLine, StrokeW: stroke, Dash: e.Dashed, Corner: 6})
		l.ends(e, pts[3], pts[2])
		for i, s := range ls {
			l.msgs = append(l.msgs, Prim{Kind: PrimText, X: x + 6, Y: l.y + float64(i)*lh, Text: s, Size: labelSize, Left: true, Fill: RoleFg})
		}
		l.ext(cxa-4, math.Max(x+seqSelfW+4, x+6+tw))
		l.y = ay + seqSelfH + 12
		if e.Deactivate {
			l.popBar(e.A, ay+seqSelfH)
		}
		return
	}
	dir := 1.0
	if cxb < cxa {
		dir = -1
	}
	a := Pt{cxa + dir*l.inset(e.A), ay}
	b := Pt{cxb - dir*l.inset(e.B), ay}
	l.msgs = append(l.msgs, Prim{Kind: PrimPath, Pts: []Pt{a, b}, Stroke: RoleLine, StrokeW: stroke, Dash: e.Dashed})
	l.ends(e, b, a)
	mid := (a.X + b.X) / 2
	for i, s := range ls {
		l.msgs = append(l.msgs, Prim{Kind: PrimText, X: mid, Y: l.y + float64(i)*lh, Text: s, Size: labelSize, Fill: RoleFg})
	}
	l.ext(math.Min(a.X, b.X)-6, math.Max(a.X, b.X)+6)
	l.y = ay + 14
	if e.Deactivate {
		l.popBar(e.A, ay)
	}
}

// ends draws the head of a message at its receiving end (and the sender's, for a
// two-way one). tip is where it lands; from is a point on the line behind it.
func (l *seqLayout) ends(e *SeqEvent, tip, from Pt) {
	draw := func(tip, from Pt) {
		dx, dy := tip.X-from.X, tip.Y-from.Y
		n := math.Hypot(dx, dy)
		if n == 0 {
			dx, dy, n = 1, 0, 1
		}
		dx, dy = dx/n, dy/n
		switch e.Head {
		case '>':
			l.msgs = append(l.msgs, arrowhead(from, tip))
		case ')':
			const k, h = 9.0, 5.0
			bx, by := tip.X-dx*k, tip.Y-dy*k
			l.msgs = append(l.msgs, Prim{Kind: PrimPath, Pts: []Pt{{bx - dy*h, by + dx*h}, tip, {bx + dy*h, by - dx*h}}, Stroke: RoleLine, StrokeW: 1.5})
		case 'x':
			const r = 5.0
			c := Pt{tip.X - dx*r, tip.Y - dy*r}
			l.msgs = append(l.msgs,
				Prim{Kind: PrimPath, Pts: []Pt{{c.X - r, c.Y - r}, {c.X + r, c.Y + r}}, Stroke: RoleLine, StrokeW: 1.5},
				Prim{Kind: PrimPath, Pts: []Pt{{c.X - r, c.Y + r}, {c.X + r, c.Y - r}}, Stroke: RoleLine, StrokeW: 1.5})
		}
	}
	draw(tip, from)
	if e.Both && e.Head != 0 {
		draw(from, tip)
	}
}

func (l *seqLayout) note(e *SeqEvent) {
	ls, tw := l.lines(e.Text, labelSize, seqNoteWrap)
	if len(ls) == 0 {
		ls = []string{""}
	}
	lh := l.th + 1
	h := float64(len(ls))*lh + 10
	w := math.Max(tw+16, 60)
	var x float64
	switch e.Side {
	case 'l':
		x = l.cx[e.A] - 10 - w
	case 'r':
		x = l.cx[e.A] + 10
	default:
		if e.A == e.B {
			x = l.cx[e.A] - w/2
		} else {
			span := l.cx[e.B] - l.cx[e.A] + 20
			w = math.Max(w, span)
			x = (l.cx[e.A]+l.cx[e.B])/2 - w/2
		}
	}
	l.notes = append(l.notes, Prim{Kind: PrimBox, X: x, Y: l.y, W: w, H: h, Radius: 3, Fill: RoleAccentTint, Stroke: RoleAccent, StrokeW: 1})
	for i, s := range ls {
		l.notes = append(l.notes, Prim{Kind: PrimText, X: x + w/2, Y: l.y + 5 + float64(i)*lh, Text: s, Size: labelSize, Fill: RoleFg})
	}
	l.ext(x, x+w)
	l.y += h + 10
}

func (l *seqLayout) block(b *SeqBlock) {
	f := &seqFrame{lo: math.Inf(1), hi: math.Inf(-1), y0: l.y, kind: b.Kind}
	rect := b.Kind == "rect"
	hdr := l.th + 10
	l.frames = append(l.frames, f)
	if rect {
		l.y += 6
	}
	for i, p := range b.Parts {
		f.labels = append(f.labels, p.Label)
		if !rect {
			if i > 0 {
				f.divs = append(f.divs, l.y)
			}
			l.y += hdr
		}
		l.events(p.Events)
		if !rect && len(p.Events) == 0 {
			l.y += 4
		}
	}
	l.frames = l.frames[:len(l.frames)-1]
	y1 := l.y + 6
	if math.IsInf(f.lo, 1) { // nothing inside: span the participants
		f.lo, f.hi = l.cx[0]-10, l.cx[len(l.cx)-1]+10
	}
	pad := 10.0
	lo, hi := f.lo-pad, f.hi+pad
	if !rect {
		tw, _ := l.m(b.Kind, labelSize, true)
		need := tw + 16 + 8
		for _, s := range f.labels {
			if s != "" {
				cw, _ := l.m("["+s+"]", labelSize, false)
				need = math.Max(need, tw+16+8+cw+12)
			}
		}
		hi = math.Max(hi, lo+need)
	}
	l.ext(lo, hi) // also grows the frame this one is in
	if rect {
		l.rects = append(l.rects, Prim{Kind: PrimBox, X: lo, Y: f.y0, W: hi - lo, H: y1 - f.y0, Radius: 3, Fill: RoleAccentTint})
	} else {
		l.framePrims = append(l.framePrims, Prim{Kind: PrimBox, X: lo, Y: f.y0, W: hi - lo, H: y1 - f.y0, Radius: 3, Stroke: RoleLine, StrokeW: 1.2})
		tw, _ := l.m(b.Kind, labelSize, true)
		l.framePrims = append(l.framePrims,
			Prim{Kind: PrimBox, X: lo, Y: f.y0, W: tw + 16, H: l.th + 6, Radius: 3, Fill: RoleGroup, Stroke: RoleLine, StrokeW: 1.2},
			Prim{Kind: PrimText, X: lo + 8, Y: f.y0 + 3, Text: b.Kind, Size: labelSize, Bold: true, Left: true, Fill: RoleFg})
		if f.labels[0] != "" {
			l.framePrims = append(l.framePrims, Prim{Kind: PrimText, X: lo + tw + 16 + 8, Y: f.y0 + 3, Text: "[" + f.labels[0] + "]", Size: labelSize, Left: true, Fill: RoleDim})
		}
		for i, dy := range f.divs {
			l.framePrims = append(l.framePrims, Prim{Kind: PrimPath, Pts: []Pt{{lo, dy}, {hi, dy}}, Stroke: RoleLine, StrokeW: 1, Dash: true})
			if s := f.labels[i+1]; s != "" {
				l.framePrims = append(l.framePrims, Prim{Kind: PrimText, X: lo + 8, Y: dy + 3, Text: "[" + s + "]", Size: labelSize, Left: true, Fill: RoleDim})
			}
		}
	}
	l.y = y1 + 10
}
