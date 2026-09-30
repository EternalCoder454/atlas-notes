package diagram

import (
	"math"
	"sort"
	"strings"
)

// Measure gives the size of a line of text drawn at size (in pixels of the
// diagram), bold or not.
type Measure func(text string, size float64, bold bool) (w, h float64)

// ApproxMeasure is a rough Measure for when no font is at hand.
func ApproxMeasure(text string, size float64, bold bool) (float64, float64) {
	k := 0.56
	if bold {
		k = 0.6
	}
	return float64(len([]rune(text))) * size * k, size * 1.3
}

const (
	titleSize  = 14.0
	subSize    = 12.0
	labelSize  = 12.0
	headSize   = 12.0
	boxPadX    = 16.0
	boxPadY    = 10.0
	maxBoxText = 210.0 // the widest a line of a box's text runs before it wraps
	crossGap   = 24.0
	rankGapV   = 44.0
	rankGapH   = 56.0
	groupPad   = 16.0
	groupHead  = 26.0
	pagePad    = 10.0
)

type drawnLine struct {
	text      string
	size      float64
	bold, dim bool
	w, h      float64
}

// Layout sizes and places every box and group and returns what to draw.
func Layout(g *Graph, m Measure) *Scene { return layoutWith(g, m, maxBoxText) }

// LayoutFit is Layout for a page avail wide. Boxes wrap their text narrower, step
// by step, until the diagram fits (or they cannot get narrower), so that a wide
// diagram is taller rather than small.
func LayoutFit(g *Graph, m Measure, avail float64) *Scene {
	var sc *Scene
	for _, w := range []float64{maxBoxText, 170, 140, 115} {
		sc = layoutWith(g, m, w)
		if sc.W <= avail {
			break
		}
	}
	return sc
}

func layoutWith(g *Graph, m Measure, textW float64) *Scene {
	if m == nil {
		m = ApproxMeasure
	}
	for _, it := range g.Order {
		it.X, it.Y, it.W, it.H, it.rx, it.ry = 0, 0, 0, 0, 0, 0
	}
	for _, it := range g.Order {
		if !it.Group {
			sizeBox(it, m, textW)
		}
	}
	for _, e := range g.Edges {
		prepEdge(e)
	}
	layoutGroup(g, g.Root, m, true)
	place(g.Root, 0, 0)
	return buildScene(g, m)
}

func wrap(text string, size float64, bold bool, m Measure, limit float64) []string {
	if w, _ := m(text, size, bold); w <= limit {
		return []string{text}
	}
	var out []string
	cur := ""
	for _, word := range strings.Fields(text) {
		try := word
		if cur != "" {
			try = cur + " " + word
		}
		if w, _ := m(try, size, bold); w > limit && cur != "" {
			out = append(out, cur)
			cur = word
		} else {
			cur = try
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

func sizeBox(it *Item, m Measure, textW float64) {
	it.dl = it.dl[:0]
	explicit := hasExplicitBold(it.Lines)
	multi := len(it.Lines) > 1
	var tw, th float64
	for i, l := range it.Lines {
		size, bold, dim := titleSize, l.Bold, false
		if !explicit && multi {
			if i == 0 {
				bold = true
			} else {
				size, dim = subSize, true
			}
		}
		for _, part := range wrap(l.Text, size, bold, m, textW) {
			w, h := m(part, size, bold)
			it.dl = append(it.dl, drawnLine{text: part, size: size, bold: bold, dim: dim, w: w, h: h})
			tw = math.Max(tw, w)
			th += h
		}
		if i == 0 && multi {
			th += 2
		}
	}
	w, h := tw+2*boxPadX, th+2*boxPadY
	switch it.Shape {
	case ShapeDiamond:
		w, h = w*1.5, h*1.6
	case ShapeCircle:
		d := math.Max(w, h) * 1.2
		w, h = d, d
	case ShapeStadium:
		w += h / 2
	}
	it.W, it.H = math.Max(w, 64), math.Max(h, 40)
}

func contains(a, b *Item) bool {
	for x := b; x != nil; x = x.Parent {
		if x == a {
			return true
		}
	}
	return false
}

func lift(x, c *Item) *Item {
	for x != nil && x.Parent != c {
		x = x.Parent
	}
	return x
}

func prepEdge(e *Edge) {
	e.skip = e.from == nil || e.to == nil || contains(e.from, e.to) || contains(e.to, e.from)
	if e.skip {
		return
	}
	c := e.from.Parent
	for !contains(c, e.to) {
		c = c.Parent
	}
	e.lca = c
	e.lf, e.lt = lift(e.from, c), lift(e.to, c)
}

func vertical(dir string) bool { return dir == "TD" || dir == "BT" }

// layoutGroup places the children of c inside it and sets c's size.
func layoutGroup(g *Graph, c *Item, m Measure, root bool) {
	for _, k := range c.Children {
		if k.Group {
			layoutGroup(g, k, m, false)
		}
	}
	pad, head := groupPad, 0.0
	if root {
		pad = pagePad
	}
	var titleW float64
	if c.Title != "" && !root {
		head = groupHead
		titleW, _ = m(c.Title, headSize, true)
	}
	kids := c.Children
	if len(kids) == 0 {
		c.W, c.H = math.Max(2*pad+40, titleW+2*pad), 2*pad+head+20
		return
	}
	idx := map[*Item]int{}
	for i, k := range kids {
		idx[k] = i
	}
	n := len(kids)
	vert := vertical(c.Dir)

	// Edges between the children, with each pair once.
	type pair struct{ a, b int }
	seen := map[pair]bool{}
	var pairs []pair
	var labelled []*Edge
	for _, e := range g.Edges {
		if e.skip || e.lca != c || e.lf == e.lt {
			continue
		}
		pr := pair{idx[e.lf], idx[e.lt]}
		if !seen[pr] {
			seen[pr] = true
			pairs = append(pairs, pr)
		}
		if e.Label != "" && !e.Invisible {
			labelled = append(labelled, e)
		}
	}
	succ := make([][]int, n)
	for _, p := range pairs {
		succ[p.a] = append(succ[p.a], p.b)
	}
	// Rank by the longest path, dropping the edges that close a cycle.
	state := make([]int, n)
	var order []int
	dag := make([][]int, n)
	var dfs func(u int)
	dfs = func(u int) {
		state[u] = 1
		for _, v := range succ[u] {
			if state[v] == 1 {
				continue
			}
			dag[u] = append(dag[u], v)
			if state[v] == 0 {
				dfs(v)
			}
		}
		state[u] = 2
		order = append(order, u)
	}
	for i := 0; i < n; i++ {
		if state[i] == 0 {
			dfs(i)
		}
	}
	rank := make([]int, n)
	for i := len(order) - 1; i >= 0; i-- {
		u := order[i]
		for _, v := range dag[u] {
			if rank[u]+1 > rank[v] {
				rank[v] = rank[u] + 1
			}
		}
	}
	nr := 0
	for _, r := range rank {
		nr = max(nr, r+1)
	}
	ranks := make([][]int, nr)
	for i := 0; i < n; i++ {
		ranks[rank[i]] = append(ranks[rank[i]], i)
	}
	pred := make([][]int, n)
	for u := range dag {
		for _, v := range dag[u] {
			pred[v] = append(pred[v], u)
		}
	}
	// Order inside the ranks: sweeps of the mean position of the neighbours.
	pos := make([]int, n)
	setPos := func() {
		for _, r := range ranks {
			for j, u := range r {
				pos[u] = j
			}
		}
	}
	setPos()
	sweep := func(r []int, nb [][]int) {
		bary := make(map[int]float64, len(r))
		for _, u := range r {
			if len(nb[u]) == 0 {
				bary[u] = float64(pos[u])
				continue
			}
			s := 0.0
			for _, v := range nb[u] {
				s += float64(pos[v])
			}
			bary[u] = s / float64(len(nb[u]))
		}
		sort.SliceStable(r, func(i, j int) bool { return bary[r[i]] < bary[r[j]] })
		for j, u := range r {
			pos[u] = j
		}
	}
	for it := 0; it < 4; it++ {
		for r := 1; r < nr; r++ {
			sweep(ranks[r], pred)
		}
		for r := nr - 2; r >= 0; r-- {
			sweep(ranks[r], dag)
		}
	}

	// Sizes along the flow (main) and across it (cross).
	mainOf := func(k *Item) float64 {
		if vert {
			return k.H
		}
		return k.W
	}
	crossOf := func(k *Item) float64 {
		if vert {
			return k.W
		}
		return k.H
	}
	th := make([]float64, nr)
	total := make([]float64, nr)
	maxCross := 0.0
	for r, list := range ranks {
		for j, u := range list {
			th[r] = math.Max(th[r], mainOf(kids[u]))
			total[r] += crossOf(kids[u])
			if j > 0 {
				total[r] += crossGap
			}
		}
		maxCross = math.Max(maxCross, total[r])
	}
	gaps := make([]float64, nr)
	for r := range gaps {
		gaps[r] = rankGapV
		if !vert {
			gaps[r] = rankGapH
		}
	}
	for _, e := range labelled {
		w, h := m(e.Label, labelSize, false)
		need := h + 24
		if !vert {
			need = w + 28
		}
		if r := rank[idx[e.lt]]; r > 0 {
			gaps[r-1] = math.Max(gaps[r-1], need)
		}
	}
	for _, e := range labelled {
		if r := rank[idx[e.lt]]; r > 0 {
			e.gap = gaps[r-1]
		}
	}
	mainTotal := 0.0
	starts := make([]float64, nr)
	for r := 0; r < nr; r++ {
		starts[r] = mainTotal
		mainTotal += th[r]
		if r < nr-1 {
			mainTotal += gaps[r]
		}
	}
	bt := c.Dir == "BT" || c.Dir == "RL"
	contentW, contentH := maxCross, mainTotal
	if !vert {
		contentW, contentH = mainTotal, maxCross
	}
	c.W = math.Max(contentW+2*pad, titleW+2*pad)
	c.H = contentH + 2*pad + head
	offX, offY := (c.W-contentW)/2, pad+head
	for r, list := range ranks {
		cr := (maxCross - total[r]) / 2
		for _, u := range list {
			k := kids[u]
			mn := starts[r] + (th[r]-mainOf(k))/2
			if bt {
				mn = mainTotal - mn - mainOf(k)
			}
			if vert {
				k.rx, k.ry = offX+cr, offY+mn
			} else {
				k.rx, k.ry = offX+mn, offY+cr
			}
			cr += crossOf(k) + crossGap
		}
	}
}

func place(c *Item, ox, oy float64) {
	for _, k := range c.Children {
		k.X, k.Y = ox+k.rx, oy+k.ry
		if k.Group {
			place(k, k.X, k.Y)
		}
	}
	if c.Parent == nil {
		c.X, c.Y = ox, oy
	}
}
