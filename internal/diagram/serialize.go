package diagram

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// Mermaid writes the graph as the text of a mermaid block (without the fences).
// It starts from the text the graph was read from: every line the editor did
// not change stays as it was, in its place, and the statements it does not model
// (classDef, linkStyle, click, other comments) are carried through. What changed
// is written in around them. When that does not read back as the same graph, the
// whole text is written afresh instead, keeping only those carried lines.
func (g *Graph) Mermaid() string {
	if s, ok := g.patch(); ok {
		return s
	}
	return g.canonical()
}

func posLine(id string, p Pt) string {
	return fmt.Sprintf("%%%% %s %s %d %d", posPrefix, id, int(math.Round(p.X)), int(math.Round(p.Y)))
}

func sizeLine(w, h float64) string {
	return fmt.Sprintf("%%%% %s %d %d", sizePrefix, int(math.Round(w)), int(math.Round(h)))
}

// lineKind says what a source line is: "blank", "comment", "pos", "size",
// "sub", "end", "dir", "style", "class", "classdef", "raw" or "chain", and
// "complex" for a line that holds a statement of one of the first kinds among
// others.
func lineKind(line string) string {
	t := strings.TrimSpace(line)
	if t == "" {
		return "blank"
	}
	if strings.HasPrefix(t, "%%") {
		f := strings.Fields(strings.TrimPrefix(t, "%%"))
		if len(f) > 0 && f[0] == posPrefix {
			return "pos"
		}
		if len(f) > 0 && f[0] == sizePrefix {
			return "size"
		}
		return "comment"
	}
	var kinds []string
	for _, st := range splitStatements(t) {
		st = strings.TrimSpace(st)
		if st == "" {
			continue
		}
		k := "chain"
		switch {
		case func() bool { _, ok := keyword(st, "subgraph"); return ok }():
			k = "sub"
		case strings.EqualFold(st, "end"):
			k = "end"
		case func() bool { _, ok := directive(st, "direction"); return ok }():
			k = "dir"
		case func() bool { _, ok := directive(st, "style"); return ok }():
			k = "style"
		case func() bool { _, ok := directive(st, "classDef"); return ok }():
			k = "classdef"
		case func() bool { _, ok := directive(st, "class"); return ok }():
			k = "class"
		default:
			for _, kw := range []string{"linkStyle", "click", "accTitle", "accDescr", "title", "interpolate"} {
				if _, ok := directive(st, kw); ok {
					k = "raw"
				}
			}
		}
		kinds = append(kinds, k)
	}
	if len(kinds) == 0 {
		return "blank"
	}
	if len(kinds) > 1 {
		for _, k := range kinds {
			if k != "chain" {
				return "complex"
			}
		}
	}
	return kinds[0]
}

func leadWS(s string) string { return s[:len(s)-len(strings.TrimLeft(s, " \t"))] }

func sourceLines(src string) []string {
	raw := strings.Split(strings.ReplaceAll(src, "\r", ""), "\n")
	if n := len(raw); n > 0 && raw[n-1] == "" {
		raw = raw[:n-1]
	}
	return raw
}

// fragment reads one line of the source on its own: the boxes it names in
// order, and its arrows.
func fragment(line string) (nodes []*Item, edges []*Edge, err error) {
	root := &Item{Group: true, Dir: "TD"}
	p := &parser{g: &Graph{Dir: "TD", Root: root, Items: map[string]*Item{}}, classes: map[string]bool{}}
	p.stack = []*Item{root}
	for _, st := range splitStatements(line) {
		if st = strings.TrimSpace(st); st == "" {
			continue
		}
		if err := p.chain(st); err != nil {
			return nil, nil, err
		}
	}
	return p.g.Order, p.g.Edges, nil
}

func sameLines(a, b []Line) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// sameGraph says whether two graphs draw the same: the same boxes (text, shape,
// highlight) in the same groups, the same arrows, and the same stored places.
func sameGraph(a, b *Graph) bool {
	if a.Dir != b.Dir || len(a.Items) != len(b.Items) || len(a.Edges) != len(b.Edges) {
		return false
	}
	for id, x := range a.Items {
		y := b.Items[id]
		if y == nil || x.Group != y.Group || x.Parent.ID != y.Parent.ID {
			return false
		}
		if x.Group {
			if x.Title != y.Title {
				return false
			}
		} else if x.Shape != y.Shape || x.Accent != y.Accent || !sameLines(x.Lines, y.Lines) {
			return false
		}
	}
	var ka, kb []string
	for _, e := range a.Edges {
		ka = append(ka, edgeKey(e))
	}
	for _, e := range b.Edges {
		kb = append(kb, edgeKey(e))
	}
	sort.Strings(ka)
	sort.Strings(kb)
	for i := range ka {
		if ka[i] != kb[i] {
			return false
		}
	}
	pos := func(g *Graph) map[string]Pt {
		m := map[string]Pt{}
		for id, p := range g.Pos {
			if it := g.Items[id]; it != nil && !it.Group {
				m[id] = Pt{math.Round(p.X), math.Round(p.Y)}
			}
		}
		return m
	}
	pa, pb := pos(a), pos(b)
	if len(pa) != len(pb) {
		return false
	}
	for id, p := range pa {
		if pb[id] != p {
			return false
		}
	}
	return true
}

type frame struct {
	it   *Item
	keep bool
	ind  string
}

func (g *Graph) patch() (string, bool) {
	orig, err := Parse(g.src)
	if err != nil {
		return "", false
	}
	raw := sourceLines(g.src)
	ind := "  "
	headerAt := -1
	for i, l := range raw {
		k := lineKind(l)
		if k == "complex" {
			return "", false
		}
		if headerAt < 0 {
			if k != "blank" && k != "comment" && k != "pos" && k != "size" {
				headerAt = i
				if len(strings.Fields(strings.ReplaceAll(l, ";", " "))) > 2 {
					return "", false // more than the header on its line
				}
			}
		} else if ws := leadWS(l); ws != "" && strings.TrimSpace(l) != "" {
			ind = ws // the first indented line sets the unit
			break
		}
	}
	classAccent := map[string]bool{}
	for _, l := range raw {
		if lineKind(l) == "classdef" {
			rest, _ := directive(strings.TrimSpace(l), "classDef")
			if f := strings.SplitN(rest, " ", 2); len(f) == 2 {
				for _, n := range strings.Split(f[0], ",") {
					classAccent[n] = accentSpec(n, f[1])
				}
			}
		}
	}
	edgeLeft := map[string]int{}
	for _, e := range g.Edges {
		edgeLeft[edgeKey(e)]++
	}
	seen, defd, emitted := map[string]bool{}, map[string]bool{}, map[string]bool{}
	posDone := map[string]bool{}
	var out []string
	var stack []frame
	surviving := func() (*Item, string) {
		for i := len(stack) - 1; i >= 0; i-- {
			if stack[i].keep {
				return stack[i].it, stack[i].ind
			}
		}
		return g.Root, ""
	}
	var adds func(c *Item, lead string) []string
	adds = func(c *Item, lead string) []string {
		var res []string
		for _, k := range c.Children {
			if !k.Group && (!seen[k.ID] || (!k.Bare && !defd[k.ID])) {
				res = append(res, lead+nodeOut(k))
				seen[k.ID] = true
				defd[k.ID] = !k.Bare
			}
		}
		for _, k := range c.Children {
			if k.Group && !emitted[k.ID] {
				emitted[k.ID] = true
				res = append(res, lead+groupOut(k))
				res = append(res, adds(k, lead+ind)...)
				res = append(res, lead+"end")
			}
		}
		return res
	}
	for i, line := range raw {
		if i == headerAt {
			if h := strings.Fields(strings.TrimSpace(line)); len(h) > 0 && g.Dir != orig.Dir {
				line = leadWS(line) + h[0] + " " + g.Dir
			}
			out = append(out, line)
			continue
		}
		t := strings.TrimSpace(line)
		switch k := lineKind(line); k {
		case "blank", "comment", "raw", "classdef":
			out = append(out, line)
		case "pos":
			f := strings.Fields(strings.TrimPrefix(t, "%%"))
			if len(f) == 4 {
				it, p := g.Items[f[1]], g.Pos[f[1]]
				if _, ok := g.Pos[f[1]]; ok && it != nil && !it.Group && !posDone[f[1]] {
					posDone[f[1]] = true
					out = append(out, leadWS(line)+posLine(f[1], p))
				}
			}
		case "size":
			if g.CanvasW > 0 && !posDone["\x00size"] {
				posDone["\x00size"] = true
				out = append(out, leadWS(line)+sizeLine(g.CanvasW, g.CanvasH))
			}
		case "sub":
			rest, _ := keyword(t, "subgraph")
			id := strings.Trim(rest, `"`)
			title := id
			if j := strings.IndexByte(rest, '['); j >= 0 && strings.HasSuffix(rest, "]") {
				id = strings.Trim(strings.TrimSpace(rest[:j]), `"`)
				title = truncate(plain(strings.Trim(strings.TrimSpace(rest[j+1:len(rest)-1]), `"`)), maxLabel)
			}
			m := g.Items[id]
			cur, _ := surviving()
			if m != nil && m.Group && m.Parent == cur && !emitted[id] {
				emitted[id] = true
				if m.Title != title {
					line = leadWS(line) + groupOut(m)
				}
				out = append(out, line)
				stack = append(stack, frame{m, true, leadWS(line)})
			} else {
				stack = append(stack, frame{})
			}
		case "end":
			if len(stack) == 0 {
				return "", false
			}
			f := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if f.keep {
				out = append(out, adds(f.it, f.ind+ind)...)
				out = append(out, line)
			}
		case "dir":
			if len(stack) > 0 && stack[len(stack)-1].keep {
				out = append(out, line)
			}
		case "style":
			rest, _ := directive(t, "style")
			f := strings.SplitN(rest, " ", 2)
			if len(f) == 2 {
				m := g.Items[strings.TrimSpace(f[0])]
				if m == nil || (accentSpec("", f[1]) && !m.Accent) {
					continue
				}
			}
			out = append(out, line)
		case "class":
			rest, _ := directive(t, "class")
			f := strings.SplitN(rest, " ", 2)
			if len(f) == 2 && classAccent[strings.TrimSpace(f[1])] {
				any := false
				for _, id := range strings.Split(f[0], ",") {
					if m := g.Items[strings.TrimSpace(id)]; m != nil && m.Accent {
						any = true
					}
				}
				if !any {
					continue
				}
			}
			out = append(out, line)
		case "chain":
			nodes, edges, err := fragment(t)
			if err != nil {
				return "", false
			}
			cur, _ := surviving()
			ok := true
			for _, n := range nodes {
				m := g.Items[n.ID]
				if m == nil {
					ok = false
					break
				}
				if m.Group {
					continue
				}
				if !seen[n.ID] && m.Parent != cur {
					ok = false
				}
				if !n.Bare && (n.Shape != m.Shape || !sameLines(n.Lines, m.Lines)) {
					ok = false
				}
				if o := orig.Items[n.ID]; o != nil && strings.Contains(t, ":::") && o.Accent != m.Accent {
					ok = false
				}
			}
			used := map[string]int{}
			for _, e := range edges {
				used[edgeKey(e)]++
				if edgeLeft[edgeKey(e)] < used[edgeKey(e)] {
					ok = false
				}
			}
			if !ok {
				continue
			}
			for _, n := range nodes {
				seen[n.ID] = true
				if !n.Bare {
					defd[n.ID] = true
				}
			}
			for k, n := range used {
				edgeLeft[k] -= n
			}
			out = append(out, line)
		}
	}
	if len(stack) != 0 {
		return "", false
	}
	out = append(out, adds(g.Root, ind)...)
	for _, e := range g.Edges {
		if k := edgeKey(e); edgeLeft[k] > 0 {
			edgeLeft[k]--
			out = append(out, ind+edgeOut(e))
		}
	}
	out = append(out, g.tailLines(posDone, ind)...)
	// Highlights whose lines went with a changed one are written again.
	text := strings.Join(out, "\n") + "\n"
	p2, err := Parse(text)
	if err != nil {
		return "", false
	}
	var fix []string
	for _, it := range g.Order {
		o := p2.Items[it.ID]
		if o == nil || it.Group {
			continue
		}
		if o.Accent && !it.Accent {
			return "", false
		}
		if it.Accent && !o.Accent {
			fix = append(fix, ind+accentLine(it.ID))
		}
	}
	if len(fix) > 0 {
		text = strings.Join(append(out, fix...), "\n") + "\n"
		if p2, err = Parse(text); err != nil {
			return "", false
		}
	}
	if !sameGraph(g, p2) {
		return "", false
	}
	return text, true
}

func accentLine(id string) string { return "style " + id + " stroke:#3584e4,stroke-width:2px" }

// tailLines are the stored places and page size not written in place.
func (g *Graph) tailLines(done map[string]bool, ind string) []string {
	var out []string
	for _, it := range g.Order {
		if p, ok := g.Pos[it.ID]; ok && !it.Group && !done[it.ID] {
			done[it.ID] = true
			out = append(out, ind+posLine(it.ID, p))
		}
	}
	if g.CanvasW > 0 && !done["\x00size"] {
		out = append(out, ind+sizeLine(g.CanvasW, g.CanvasH))
	}
	return out
}

// canonical writes the graph from nothing, in a fixed order.
func (g *Graph) canonical() string {
	ind := "  "
	head := "flowchart"
	if f := strings.Fields(strings.TrimSpace(firstCode(g.src))); len(f) > 0 && strings.EqualFold(f[0], "graph") {
		head = "graph"
	}
	out := []string{head + " " + g.Dir}
	var walk func(c *Item, lead string)
	walk = func(c *Item, lead string) {
		for _, k := range c.Children {
			if !k.Group {
				out = append(out, lead+nodeOut(k))
			}
		}
		for _, k := range c.Children {
			if k.Group {
				out = append(out, lead+groupOut(k))
				if k.Dir != "" && k.Dir != c.Dir {
					out = append(out, lead+ind+"direction "+k.Dir)
				}
				walk(k, lead+ind)
				out = append(out, lead+"end")
			}
		}
	}
	walk(g.Root, ind)
	for _, e := range g.Edges {
		out = append(out, ind+edgeOut(e))
	}
	for _, it := range g.Order {
		if it.Accent && !it.Group {
			out = append(out, ind+accentLine(it.ID))
		}
	}
	// What the editor does not model is carried through.
	accentClass := map[string]bool{}
	for _, l := range sourceLines(g.src) {
		t := strings.TrimSpace(l)
		switch lineKind(l) {
		case "classdef":
			rest, _ := directive(t, "classDef")
			if f := strings.SplitN(rest, " ", 2); len(f) == 2 {
				for _, n := range strings.Split(f[0], ",") {
					accentClass[n] = accentSpec(n, f[1])
				}
			}
			out = append(out, l)
		case "comment", "raw":
			out = append(out, l)
		case "style":
			rest, _ := directive(t, "style")
			if f := strings.SplitN(rest, " ", 2); len(f) == 2 && g.Items[strings.TrimSpace(f[0])] != nil && !accentSpec("", f[1]) {
				out = append(out, l)
			}
		}
	}
	out = append(out, g.tailLines(map[string]bool{}, ind)...)
	return strings.Join(out, "\n") + "\n"
}

func firstCode(src string) string {
	for _, l := range sourceLines(src) {
		if t := strings.TrimSpace(l); t != "" && !strings.HasPrefix(t, "%%") {
			return t
		}
	}
	return ""
}

// Reads says whether the text Mermaid wrote reads back as this graph. It does
// not for the odd graph with a box named like a keyword ("end", "class"), which
// the editor then refuses to save rather than write something different.
func (g *Graph) Reads(text string) bool {
	g2, err := Parse(text)
	return err == nil && sameGraph(g, g2)
}
