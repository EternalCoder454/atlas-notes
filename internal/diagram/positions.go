package diagram

import (
	"math"
	"strconv"
	"strings"
)

// Where the editor put boxes is kept in Mermaid comments, which every other
// renderer ignores:
//
//	%% atlas:pos <id> <x> <y>    the top left of a box on the page, in pixels
//	%% atlas:size <w> <h>        the page the editor draws on
//
// A box with no atlas:pos line is placed by the layout.

const (
	posPrefix  = "atlas:pos"
	sizePrefix = "atlas:size"
)

var entities = strings.NewReplacer("#quot;", `"`, "#lt;", "<", "#gt;", ">", "#amp;", "&", "#124;", "|", "#35;", "#", "#96;", "`", "#42;", "*")

// unescape reads the entity codes Mermaid text uses for what cannot be written
// as is; escape writes them.
func unescape(s string) string {
	if !strings.Contains(s, "#") {
		return s
	}
	return entities.Replace(s)
}

func escape(s string) string {
	// A "#" that would read as an entity code is written as one.
	for i := 0; i < len(s); i++ {
		if s[i] != '#' {
			continue
		}
		j := i + 1
		for j < len(s) && (s[j] == '_' || s[j] >= 'a' && s[j] <= 'z' || s[j] >= 'A' && s[j] <= 'Z' || s[j] >= '0' && s[j] <= '9') {
			j++
		}
		if j > i+1 && j < len(s) && s[j] == ';' {
			s = s[:i] + "#35;" + s[i+1:]
			i += 3
		}
	}
	s = strings.NewReplacer(`"`, "#quot;", "<", "#lt;", ">", "#gt;", "|", "#124;", "*", "#42;").Replace(s)
	// A backtick at either end would make Mermaid read a Markdown string.
	if strings.HasPrefix(s, "`") {
		s = "#96;" + s[1:]
	}
	if strings.HasSuffix(s, "`") {
		s = s[:len(s)-1] + "#96;"
	}
	return s
}

func hasStr(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func clampCoord(v, lo float64) float64 {
	if math.IsNaN(v) {
		return lo
	}
	return math.Min(math.Max(v, lo), MaxSize)
}

// comment reads a %% line: the editor's own are kept, the others ignored.
func (p *parser) comment(line string) {
	f := strings.Fields(strings.TrimPrefix(line, "%%"))
	if len(f) == 0 {
		return
	}
	num := func(s string) (float64, bool) {
		v, err := strconv.ParseFloat(s, 64)
		return v, err == nil && !math.IsNaN(v) && !math.IsInf(v, 0)
	}
	switch f[0] {
	case posPrefix:
		if len(f) != 4 {
			return
		}
		x, ok1 := num(f[2])
		y, ok2 := num(f[3])
		if !ok1 || !ok2 {
			return
		}
		if p.g.Pos == nil {
			p.g.Pos = map[string]Pt{}
		}
		if _, dup := p.g.Pos[f[1]]; !dup && len(p.g.Pos) >= maxItems {
			return
		}
		p.g.Pos[f[1]] = Pt{clampCoord(x, 0), clampCoord(y, 0)}
	case sizePrefix:
		if len(f) != 3 {
			return
		}
		w, ok1 := num(f[1])
		h, ok2 := num(f[2])
		if ok1 && ok2 {
			p.g.CanvasW, p.g.CanvasH = clampCoord(w, 100), clampCoord(h, 100)
		}
	}
}

// applyPositions moves the boxes that have a stored place there, then fits the
// groups round what is in them. What has no stored place stays where the layout
// put it; arrows to a moved box pick their sides by where it is now.
func (g *Graph) applyPositions(m Measure) {
	if len(g.Pos) == 0 {
		return
	}
	touched := map[*Item]bool{}
	for id, p := range g.Pos {
		it := g.Items[id]
		if it == nil || it.Group {
			continue
		}
		it.X, it.Y = p.X, p.Y
		for a := it; a != nil; a = a.Parent {
			touched[a] = true
		}
	}
	if len(touched) == 0 {
		return
	}
	var fit func(c *Item)
	fit = func(c *Item) {
		for _, k := range c.Children {
			if k.Group {
				fit(k)
			}
		}
		if c.Parent == nil || !touched[c] || len(c.Children) == 0 {
			return
		}
		minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
		for _, k := range c.Children {
			minX, minY = math.Min(minX, k.X), math.Min(minY, k.Y)
			maxX, maxY = math.Max(maxX, k.X+k.W), math.Max(maxY, k.Y+k.H)
		}
		head, titleW := 0.0, 0.0
		if c.Title != "" {
			head = groupHead
			titleW, _ = m(c.Title, headSize, true)
		}
		x, y := math.Max(minX-groupPad, 0), math.Max(minY-groupPad-head, 0)
		c.X, c.Y = x, y
		c.W = math.Max(maxX+groupPad-x, titleW+2*groupPad)
		c.H = maxY + groupPad - y
	}
	fit(g.Root)
	right, bottom := 0.0, 0.0
	for _, it := range g.Order {
		right, bottom = math.Max(right, it.X+it.W), math.Max(bottom, it.Y+it.H)
	}
	g.Root.W = math.Max(g.Root.W, right+pagePad)
	g.Root.H = math.Max(g.Root.H, bottom+pagePad)
	for _, e := range g.Edges {
		if !e.skip && (touched[e.lf] || touched[e.lt]) {
			e.free, e.span, e.gap = true, 0, 0
		}
	}
}
