package diagram

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// Role names a colour by what it is for. The editor fills each one from the
// theme's colours when it draws, so a diagram follows light, dark and the app's
// themes; the exporters use fixed palettes.
type Role int

const (
	RoleNone Role = iota
	RoleBg
	RoleFg
	RoleDim
	RoleAccent
	RoleAccentTint
	RoleBox
	RoleBoxStroke
	RoleGroup
	RoleGroupStroke
	RoleLine
	RoleAccentStrong
	RoleError
	RoleErrorTint
	RoleCount
)

// RGBA is a colour with parts from 0 to 1.
type RGBA struct{ R, G, B, A float64 }

// Palette gives a colour for every Role.
type Palette [RoleCount]RGBA

// LightPalette and DarkPalette are for output that has no theme to read.
var LightPalette = Palette{
	RoleBg: {1, 1, 1, 1}, RoleFg: {0.14, 0.14, 0.16, 1}, RoleDim: {0.42, 0.42, 0.46, 1},
	RoleAccent: {0.21, 0.52, 0.89, 1}, RoleAccentTint: {0.21, 0.52, 0.89, 0.12},
	RoleBox: {0.97, 0.97, 0.98, 1}, RoleBoxStroke: {0.74, 0.74, 0.78, 1},
	RoleGroup: {0.95, 0.95, 0.96, 1}, RoleGroupStroke: {0.80, 0.80, 0.83, 1}, RoleLine: {0.40, 0.40, 0.44, 1},
	RoleAccentStrong: {0.21, 0.52, 0.89, 0.40}, RoleError: {0.75, 0.11, 0.16, 1}, RoleErrorTint: {0.75, 0.11, 0.16, 0.14},
}

var DarkPalette = Palette{
	RoleBg: {0.14, 0.14, 0.16, 1}, RoleFg: {0.92, 0.92, 0.94, 1}, RoleDim: {0.66, 0.66, 0.70, 1},
	RoleAccent: {0.47, 0.68, 0.96, 1}, RoleAccentTint: {0.47, 0.68, 0.96, 0.16},
	RoleBox: {0.19, 0.19, 0.22, 1}, RoleBoxStroke: {0.36, 0.36, 0.40, 1},
	RoleGroup: {0.17, 0.17, 0.19, 1}, RoleGroupStroke: {0.30, 0.30, 0.34, 1}, RoleLine: {0.66, 0.66, 0.70, 1},
	RoleAccentStrong: {0.47, 0.68, 0.96, 0.45}, RoleError: {1, 0.42, 0.40, 1}, RoleErrorTint: {1, 0.42, 0.40, 0.18},
}

// PrimKind is what a Prim is.
type PrimKind int

const (
	PrimBox   PrimKind = iota // a rectangle, rounded rectangle, diamond or ellipse
	PrimText                  // one line of text, centred on X, top at Y (or starting at X when Left)
	PrimPath                  // a line, straight or with rounded corners
	PrimArrow                 // a filled arrowhead
)

// Pt is a point on the page.
type Pt struct{ X, Y float64 }

// Prim is one thing to draw.
type Prim struct {
	Kind       PrimKind
	X, Y, W, H float64
	Shape      Shape
	Radius     float64
	Fill       Role
	Stroke     Role
	StrokeW    float64
	Dash       bool
	Text       string
	Size       float64
	Bold, Left bool
	Pts        []Pt // a path's corners, or an arrowhead's three points
	Corner     float64
}

// Scene is a laid-out diagram: W by H, and the shapes back to front.
type Scene struct {
	W, H  float64
	Label string // what kind of picture it is, for a screen reader: "Flowchart", "Gantt chart"
	Desc  string // the box titles, for a screen reader
	Prims []Prim
	// Routes is where each drawn arrow runs, for the editor to hit-test.
	Routes []Route
}

// Route is the path one arrow was drawn along, and where its label sits.
type Route struct {
	Edge  *Edge
	Pts   []Pt
	Label Pt
}

func shapeRadius(it *Item) float64 {
	switch it.Shape {
	case ShapeRound:
		return 10
	case ShapeStadium:
		return it.H / 2
	}
	return 4
}

func buildScene(g *Graph, m Measure) *Scene {
	sc := &Scene{W: g.Root.W, H: g.Root.H, Label: "Flowchart"}
	var titles []string
	for _, it := range g.Order {
		if !it.Group && len(it.Lines) > 0 {
			titles = append(titles, it.Lines[0].Text)
		}
	}
	sc.Desc = truncate(strings.Join(titles, ", "), 300)
	// Groups, outermost first.
	var groups []*Item
	for _, it := range g.Order {
		if it.Group {
			groups = append(groups, it)
		}
	}
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].depth < groups[j].depth })
	for _, it := range groups {
		sc.Prims = append(sc.Prims, Prim{Kind: PrimBox, X: it.X, Y: it.Y, W: it.W, H: it.H, Radius: 10,
			Fill: RoleGroup, Stroke: RoleGroupStroke, StrokeW: 1})
		if it.Title != "" {
			sc.Prims = append(sc.Prims, Prim{Kind: PrimText, X: it.X + 14, Y: it.Y + 8, Text: it.Title,
				Size: headSize, Bold: true, Fill: RoleDim, Left: true})
		}
	}
	sc.routeEdges(g, m)
	for _, it := range g.Order {
		if it.Group {
			continue
		}
		p := Prim{Kind: PrimBox, X: it.X, Y: it.Y, W: it.W, H: it.H, Shape: it.Shape, Radius: shapeRadius(it),
			Fill: RoleBox, Stroke: RoleBoxStroke, StrokeW: 1}
		if it.Accent {
			p.Fill, p.Stroke, p.StrokeW = RoleAccentTint, RoleAccent, 2
		}
		sc.Prims = append(sc.Prims, p)
		var th float64
		for _, l := range it.dl {
			th += l.h
		}
		if len(it.dl) > 1 {
			th += 2
		}
		y := it.Y + (it.H-th)/2
		for i, l := range it.dl {
			role := RoleFg
			if l.dim {
				role = RoleDim
			}
			sc.Prims = append(sc.Prims, Prim{Kind: PrimText, X: it.X + it.W/2, Y: y, Text: l.text, Size: l.size, Bold: l.bold, Fill: role})
			y += l.h
			if i == 0 && len(it.Lines) > 1 && it.dl[0].size != it.dl[len(it.dl)-1].size {
				y += 2
			}
		}
	}
	return sc
}

type port struct {
	e     *Edge
	end   int // 0 at the source, 1 at the target
	item  *Item
	side  int // 0 top, 1 right, 2 bottom, 3 left
	other float64
	coord float64
}

func rectSide(it *Item, side int, c float64) Pt {
	switch side {
	case 0:
		return Pt{c, it.Y}
	case 1:
		return Pt{it.X + it.W, c}
	case 2:
		return Pt{c, it.Y + it.H}
	}
	return Pt{it.X, c}
}

// sides chooses where an arrow leaves the source and enters the target.
func sides(e *Edge) (int, int, bool) {
	a, b := e.lf, e.lt
	if e.free {
		a, b = e.from, e.to // the boxes themselves, where they were put
	}
	const eps = 0.5
	below := b.Y >= a.Y+a.H-eps
	above := b.Y+b.H <= a.Y+eps
	right := b.X >= a.X+a.W-eps
	left := b.X+b.W <= a.X+eps
	vert := vertical(e.lca.Dir)
	if e.free {
		dx := (b.X + b.W/2) - (a.X + a.W/2)
		dy := (b.Y + b.H/2) - (a.Y + a.H/2)
		vert = math.Abs(dy) >= math.Abs(dx)
	}
	tryV := func() (int, int, bool) {
		if below {
			return 2, 0, true
		}
		if above {
			return 0, 2, true
		}
		return 0, 0, false
	}
	tryH := func() (int, int, bool) {
		if right {
			return 1, 3, true
		}
		if left {
			return 3, 1, true
		}
		return 0, 0, false
	}
	var s, t int
	var ok bool
	if vert {
		if s, t, ok = tryV(); !ok {
			s, t, ok = tryH()
		}
	} else if s, t, ok = tryH(); !ok {
		s, t, ok = tryV()
	}
	if !ok && e.free {
		// Overlapping boxes: a straight way along the longer distance.
		dx := (b.X + b.W/2) - (a.X + a.W/2)
		dy := (b.Y + b.H/2) - (a.Y + a.H/2)
		switch {
		case vert && dy >= 0:
			return 2, 0, true
		case vert:
			return 0, 2, true
		case dx >= 0:
			return 1, 3, true
		}
		return 3, 1, true
	}
	return s, t, ok
}

func (sc *Scene) routeEdges(g *Graph, m Measure) {
	type routed struct {
		e      *Edge
		ss, ts int
	}
	var list []routed
	byKey := map[[2]int][]*port{}
	itemID := map[*Item]int{}
	for i, it := range g.Order {
		itemID[it] = i
	}
	var ports []*port
	for _, e := range g.Edges {
		if e.skip || e.Invisible || e.lf == e.lt {
			continue
		}
		ss, ts, ok := sides(e)
		if !ok {
			continue
		}
		list = append(list, routed{e, ss, ts})
		p0 := &port{e: e, end: 0, item: e.from, side: ss}
		p1 := &port{e: e, end: 1, item: e.to, side: ts}
		p0.other = center(e.to, ss)
		p1.other = center(e.from, ts)
		ports = append(ports, p0, p1)
		byKey[[2]int{itemID[p0.item], ss}] = append(byKey[[2]int{itemID[p0.item], ss}], p0)
		byKey[[2]int{itemID[p1.item], ts}] = append(byKey[[2]int{itemID[p1.item], ts}], p1)
	}
	for k, ps := range byKey {
		it := g.Order[k[0]]
		side := k[1]
		n := len(ps)
		sort.SliceStable(ps, func(i, j int) bool { return ps[i].other < ps[j].other })
		spread := !it.Group && (it.Shape == ShapeRect || it.Shape == ShapeRound || it.Shape == ShapeStadium) || it.Group
		for i, p := range ps {
			lo, ln := it.X, it.W
			if side == 1 || side == 3 {
				lo, ln = it.Y, it.H
			}
			if spread && n > 1 {
				// Keep off the rounded corners.
				in := math.Min(14, ln/4)
				p.coord = lo + in + (ln-2*in)*float64(i+1)/float64(n+1)
			} else {
				p.coord = lo + ln/2
			}
		}
	}
	var labels []Prim
	plateOf := func(e *Edge) Role {
		if e.lca != nil && e.lca.Parent != nil {
			return RoleGroup // in a group, which is tinted
		}
		return RoleBg
	}
	for _, r := range list {
		e := r.e
		var a, b Pt
		for _, p := range ports {
			if p.e != e {
				continue
			}
			if p.end == 0 {
				a = rectSide(p.item, p.side, p.coord)
			} else {
				b = rectSide(p.item, p.side, p.coord)
			}
		}
		pts := []Pt{a}
		var lab Pt
		if detour := sc.detour(e, r.ss, r.ts, a, b); detour != nil {
			pts = detour
			n := len(pts)
			lab = Pt{(pts[n-3].X + pts[n-2].X) / 2, pts[n-2].Y}
			if r.ss == 1 || r.ss == 3 {
				lab = Pt{pts[n-2].X, (pts[n-3].Y + pts[n-2].Y) / 2}
			}
			pts = append(pts[:n-1:n-1], b)
		} else if r.ss == 0 || r.ss == 2 {
			gap := math.Max(e.gap, 0)
			var near float64
			if r.ss == 2 {
				near = e.lt.Y - math.Min(gap/2, (e.lt.Y-(e.lf.Y+e.lf.H))/2)
			} else {
				near = e.lt.Y + e.lt.H + math.Min(gap/2, (e.lf.Y-(e.lt.Y+e.lt.H))/2)
			}
			if gap == 0 {
				near = (b.Y + a.Y) / 2
			}
			if math.Abs(a.X-b.X) > 0.5 {
				pts = append(pts, Pt{a.X, near}, Pt{b.X, near})
				lab = Pt{(a.X + b.X) / 2, near}
			} else {
				lab = Pt{a.X, (a.Y + b.Y) / 2}
				if gap > 0 {
					lab.Y = near
				}
			}
		} else {
			var near float64
			if e.free {
				near = (a.X + b.X) / 2
			} else if r.ss == 1 {
				near = e.lt.X - math.Min(math.Max(e.gap, rankGapH)/2, (e.lt.X-(e.lf.X+e.lf.W))/2)
			} else {
				near = e.lt.X + e.lt.W + math.Min(math.Max(e.gap, rankGapH)/2, (e.lf.X-(e.lt.X+e.lt.W))/2)
			}
			if math.Abs(a.Y-b.Y) > 0.5 {
				pts = append(pts, Pt{near, a.Y}, Pt{near, b.Y})
				lab = Pt{near, (a.Y + b.Y) / 2}
			} else {
				lab = Pt{(a.X + b.X) / 2, a.Y}
			}
		}
		pts = append(pts, b)
		// No zero-length steps, so the arrowhead follows the real last segment.
		tidy := pts[:1:1]
		for _, q := range pts[1:] {
			if math.Hypot(q.X-tidy[len(tidy)-1].X, q.Y-tidy[len(tidy)-1].Y) > 0.5 || len(tidy) < 2 {
				tidy = append(tidy, q)
			}
		}
		pts = tidy
		w := 1.5
		if e.Thick {
			w = 3
		}
		sc.Routes = append(sc.Routes, Route{Edge: e, Pts: pts, Label: lab})
		sc.Prims = append(sc.Prims, Prim{Kind: PrimPath, Pts: pts, Stroke: RoleLine, StrokeW: w, Dash: e.Dotted, Corner: 8})
		if e.Head {
			sc.Prims = append(sc.Prims, arrowhead(pts[len(pts)-2], b))
		}
		if e.Tail {
			sc.Prims = append(sc.Prims, arrowhead(pts[1], a))
		}
		if e.Label != "" {
			plate := plateOf(e)
			tw, th := m(e.Label, labelSize, false)
			labels = append(labels,
				Prim{Kind: PrimBox, X: lab.X - tw/2 - 6, Y: lab.Y - th/2 - 2, W: tw + 12, H: th + 4, Radius: 4, Fill: plate},
				Prim{Kind: PrimText, X: lab.X, Y: lab.Y - th/2, Text: e.Label, Size: labelSize, Fill: RoleDim})
		}
	}
	sc.Prims = append(sc.Prims, labels...)
}

func center(it *Item, side int) float64 {
	if side == 1 || side == 3 {
		return it.Y + it.H/2
	}
	return it.X + it.W/2
}

func arrowhead(from, tip Pt) Prim {
	dx, dy := tip.X-from.X, tip.Y-from.Y
	l := math.Hypot(dx, dy)
	if l == 0 {
		dx, dy, l = 0, 1, 1
	}
	dx, dy = dx/l, dy/l
	const length, half = 10.0, 4.5
	bx, by := tip.X-dx*length, tip.Y-dy*length
	return Prim{Kind: PrimArrow, Fill: RoleLine, Pts: []Pt{tip, {bx - dy*half, by + dx*half}, {bx + dy*half, by - dx*half}}}
}

// Seg is one step of a path: 'M' or 'L' with P[0], or 'C' with all three.
type Seg struct {
	Op byte
	P  [3]Pt
}

// PathSegs turns corners into a path whose bends are rounded off with radius r.
func PathSegs(pts []Pt, r float64) []Seg {
	if len(pts) == 0 {
		return nil
	}
	segs := []Seg{{Op: 'M', P: [3]Pt{pts[0]}}}
	for i := 1; i < len(pts)-1; i++ {
		p0, p1, p2 := pts[i-1], pts[i], pts[i+1]
		d0, d1 := math.Hypot(p1.X-p0.X, p1.Y-p0.Y), math.Hypot(p2.X-p1.X, p2.Y-p1.Y)
		rr := math.Min(r, math.Min(d0, d1)/2)
		if rr < 0.5 {
			segs = append(segs, Seg{Op: 'L', P: [3]Pt{p1}})
			continue
		}
		a := Pt{p1.X + (p0.X-p1.X)/d0*rr, p1.Y + (p0.Y-p1.Y)/d0*rr}
		b := Pt{p1.X + (p2.X-p1.X)/d1*rr, p1.Y + (p2.Y-p1.Y)/d1*rr}
		segs = append(segs, Seg{Op: 'L', P: [3]Pt{a}}, Seg{Op: 'C', P: [3]Pt{p1, p1, b}})
	}
	return append(segs, Seg{Op: 'L', P: [3]Pt{pts[len(pts)-1]}})
}

// SVG writes the scene as a standalone SVG picture in the given colours.
func (sc *Scene) SVG(pal Palette) string {
	var b strings.Builder
	w, h := math.Ceil(sc.W), math.Ceil(sc.H)
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%.0f" height="%.0f" viewBox="0 0 %.0f %.0f" font-family="sans-serif" role="img" aria-label="%s" style="max-width:100%%;height:auto"><title>%s: %s</title>`, w, h, w, h, xmlEscape(sc.Label), xmlEscape(sc.Label), xmlEscape(sc.Desc))
	col := func(r Role) string {
		if r == RoleNone {
			return "none"
		}
		c := pal[r]
		return fmt.Sprintf("rgb(%.0f,%.0f,%.0f)", c.R*255, c.G*255, c.B*255)
	}
	op := func(r Role) string {
		if r == RoleNone || pal[r].A >= 0.999 {
			return ""
		}
		return fmt.Sprintf(` fill-opacity="%.2f"`, pal[r].A)
	}
	for _, p := range sc.Prims {
		switch p.Kind {
		case PrimBox:
			attrs := fmt.Sprintf(`fill="%s"%s stroke="%s" stroke-width="%.1f"`, col(p.Fill), op(p.Fill), col(p.Stroke), p.StrokeW)
			switch p.Shape {
			case ShapeDiamond:
				fmt.Fprintf(&b, `<polygon points="%.1f,%.1f %.1f,%.1f %.1f,%.1f %.1f,%.1f" %s/>`,
					p.X+p.W/2, p.Y, p.X+p.W, p.Y+p.H/2, p.X+p.W/2, p.Y+p.H, p.X, p.Y+p.H/2, attrs)
			case ShapeCircle:
				fmt.Fprintf(&b, `<ellipse cx="%.1f" cy="%.1f" rx="%.1f" ry="%.1f" %s/>`, p.X+p.W/2, p.Y+p.H/2, p.W/2, p.H/2, attrs)
			default:
				fmt.Fprintf(&b, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" rx="%.1f" %s/>`, p.X, p.Y, p.W, p.H, p.Radius, attrs)
			}
		case PrimText:
			anchor, x := "middle", p.X
			if p.Left {
				anchor = "start"
			}
			weight := "normal"
			if p.Bold {
				weight = "bold"
			}
			fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" font-size="%.0f" font-weight="%s" text-anchor="%s" dominant-baseline="central" fill="%s">%s</text>`,
				x, p.Y+p.Size*0.65, p.Size, weight, anchor, col(p.Fill), xmlEscape(p.Text))
		case PrimPath:
			var d strings.Builder
			for _, s := range PathSegs(p.Pts, p.Corner) {
				switch s.Op {
				case 'M', 'L':
					fmt.Fprintf(&d, "%c%.1f %.1f ", s.Op, s.P[0].X, s.P[0].Y)
				case 'C':
					fmt.Fprintf(&d, "C%.1f %.1f %.1f %.1f %.1f %.1f ", s.P[0].X, s.P[0].Y, s.P[1].X, s.P[1].Y, s.P[2].X, s.P[2].Y)
				}
			}
			dash := ""
			if p.Dash {
				dash = ` stroke-dasharray="5 4"`
			}
			fmt.Fprintf(&b, `<path d="%s" fill="none" stroke="%s" stroke-width="%.1f"%s/>`, strings.TrimSpace(d.String()), col(p.Stroke), p.StrokeW, dash)
		case PrimArrow:
			fmt.Fprintf(&b, `<polygon points="%.1f,%.1f %.1f,%.1f %.1f,%.1f" fill="%s"/>`,
				p.Pts[0].X, p.Pts[0].Y, p.Pts[1].X, p.Pts[1].Y, p.Pts[2].X, p.Pts[2].Y, col(p.Fill))
		}
	}
	b.WriteString("</svg>")
	return b.String()
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}

// detour routes an arrow that runs past more than one rank round the side of
// the group holding them, in the gaps between ranks and a channel at the edge,
// so that it does not cross the boxes between. It returns nil for the arrows
// that need no such way. The last point it returns is the target's border.
func (sc *Scene) detour(e *Edge, ss, ts int, a, b Pt) []Pt {
	// The detour runs through the gaps between rank rows. An arrow to or from
	// a box the person placed by hand has no rows to follow: its span still
	// counts the ranks of the automatic layout, and a path through those gaps
	// ended short of the placed box. It takes the direct elbow instead.
	if e.free || (e.span > -2 && e.span < 2) {
		return nil
	}
	vert := vertical(e.lca.Dir)
	if vert != (ss == 0 || ss == 2) || ss == ts {
		return nil
	}
	// Where the rank rows of the two ends are, on the page.
	origin := func(it *Item) Pt { return Pt{it.X - it.rx, it.Y - it.ry} }
	oa, ob := origin(e.lf), origin(e.lt)
	const off = 14.0
	pad := groupPad
	if e.lca.Parent == nil {
		pad = pagePad
	}
	var ma, mb float64 // main-axis positions of the two horizontal runs
	var ca, cb float64 // where the ends are across the flow
	var chanL, chanR float64
	forward := ss == 2 || ss == 1
	if vert {
		ca, cb = a.X, b.X
		if forward {
			ma, mb = oa.Y+e.lf.rowHi+off, ob.Y+e.lt.rowLo-off
		} else {
			ma, mb = oa.Y+e.lf.rowLo-off, ob.Y+e.lt.rowHi+off
		}
		chanL, chanR = e.lca.X+pad/2, e.lca.X+e.lca.W-pad/2
	} else {
		ca, cb = a.Y, b.Y
		if forward {
			ma, mb = oa.X+e.lf.rowHi+off, ob.X+e.lt.rowLo-off
		} else {
			ma, mb = oa.X+e.lf.rowLo-off, ob.X+e.lt.rowHi+off
		}
		chanL, chanR = e.lca.Y+pad/2, e.lca.Y+e.lca.H-pad/2
	}
	ch := chanL
	if math.Abs(chanR-(ca+cb)/2) < math.Abs(chanL-(ca+cb)/2) {
		ch = chanR
	}
	mk := func(main, cross float64) Pt {
		if vert {
			return Pt{cross, main}
		}
		return Pt{main, cross}
	}
	return []Pt{a, mk(ma, ca), mk(ma, ch), mk(mb, ch), mk(mb, cb), b}
}
