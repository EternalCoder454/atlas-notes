package diagram

import (
	"regexp"
	"strings"
	"unicode"
)

// The editing side of a flowchart: changes to a parsed Graph that keep it
// consistent (every edge ends at a box, every box is in a group), and Mermaid,
// which writes it back as text.

// Clone copies the graph, for undo.
func (g *Graph) Clone() *Graph {
	n := &Graph{Dir: g.Dir, Items: make(map[string]*Item, len(g.Items)), CanvasW: g.CanvasW, CanvasH: g.CanvasH, src: g.src}
	if g.Pos != nil {
		n.Pos = make(map[string]Pt, len(g.Pos))
		for k, v := range g.Pos {
			n.Pos[k] = v
		}
	}
	cp := make(map[*Item]*Item, len(g.Order)+1)
	var dup func(it, parent *Item) *Item
	dup = func(it, parent *Item) *Item {
		c := *it
		c.Parent, c.Children, c.dl = parent, nil, nil
		c.Lines = append([]Line(nil), it.Lines...)
		cp[it] = &c
		for _, k := range it.Children {
			c.Children = append(c.Children, dup(k, &c))
		}
		return &c
	}
	n.Root = dup(g.Root, nil)
	for _, it := range g.Order {
		n.Order = append(n.Order, cp[it])
		n.Items[it.ID] = cp[it]
	}
	for _, e := range g.Edges {
		c := *e
		n.Edges = append(n.Edges, &c)
	}
	n.relink()
	return n
}

// relink makes the pointers inside the graph agree with the names.
func (g *Graph) relink() {
	for i, it := range g.Order {
		it.index = i
	}
	for _, e := range g.Edges {
		e.from, e.to = g.Items[e.From], g.Items[e.To]
	}
	setDirs(g.Root, g.Dir, 0)
}

var reserved = map[string]bool{"end": true, "subgraph": true, "graph": true, "flowchart": true, "style": true,
	"class": true, "classdef": true, "click": true, "direction": true, "default": true, "linkstyle": true,
	"title": true, "interpolate": true, "accdescr": true, "acctitle": true}

// NewID makes a short name for a new box or group from its title: letters and
// digits only, not one already used, and not a Mermaid keyword.
func (g *Graph) NewID(title string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(title) {
		if r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			b.WriteRune(r)
		} else if b.Len() > 0 && !strings.HasSuffix(b.String(), "_") {
			b.WriteByte('_')
		}
		if b.Len() >= 10 {
			break
		}
	}
	base := strings.Trim(b.String(), "_")
	if base == "" || unicode.IsDigit(rune(base[0])) {
		base = "box" + base
	}
	id := base
	for n := 2; g.Items[id] != nil || reserved[id]; n++ {
		id = base + itoa(n)
	}
	return id
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for ; n > 0; n /= 10 {
		d = append([]byte{byte('0' + n%10)}, d...)
	}
	return string(d)
}

// Clean makes text fit on one line of a box: no line breaks or bold marks, and
// spaces collapsed.
func Clean(s string) string {
	s = strings.ReplaceAll(s, `\n`, " ")
	s = strings.ReplaceAll(s, "**", "")
	return strings.Join(strings.Fields(reBreak.ReplaceAllString(s, " ")), " ")
}

// AddBox adds a box with the given title in parent (the page when nil) and
// puts it at x, y. It returns nil when the diagram is full.
func (g *Graph) AddBox(title string, parent *Item, x, y float64) *Item {
	if len(g.Order) >= maxItems {
		return nil
	}
	if parent == nil || !parent.Group {
		parent = g.Root
	}
	title = Clean(title)
	id := g.NewID(title)
	if title == "" {
		title = id
	}
	it := &Item{ID: id, Shape: ShapeRect, Lines: []Line{{Text: title}}, Parent: parent}
	g.Items[id] = it
	g.Order = append(g.Order, it)
	parent.Children = append(parent.Children, it)
	g.SetPos(id, x, y)
	g.relink()
	return it
}

// SetText gives a box its title and subtitle (either may be empty), or a group
// its heading (the title).
func (g *Graph) SetText(it *Item, title, sub string) {
	title, sub = Clean(title), Clean(sub)
	if it.Group {
		it.Title = truncate(title, maxLabel)
		return
	}
	if title == "" {
		title = it.ID
	}
	it.Lines = []Line{{Text: title}}
	if sub != "" {
		it.Lines = append(it.Lines, Line{Text: sub})
	}
	it.Bare = false
}

// TextOf is the title and subtitle the editor shows for a box: the first line,
// and the rest on one line.
func TextOf(it *Item) (title, sub string) {
	if it.Group {
		return it.Title, ""
	}
	var rest []string
	for i, l := range it.Lines {
		if i == 0 {
			title = l.Text
		} else {
			rest = append(rest, l.Text)
		}
	}
	return title, strings.Join(rest, " / ")
}

// SetPos stores where a box is, rounded to whole pixels.
func (g *Graph) SetPos(id string, x, y float64) {
	if g.Pos == nil {
		g.Pos = map[string]Pt{}
	}
	g.Pos[id] = Pt{float64(int(clampCoord(x, 0) + 0.5)), float64(int(clampCoord(y, 0) + 0.5))}
}

// Remove deletes a box, or a group with everything in it, and the arrows that
// end at any of them.
func (g *Graph) Remove(it *Item) {
	if it == nil || it.Parent == nil {
		return
	}
	gone := map[*Item]bool{}
	var mark func(x *Item)
	mark = func(x *Item) {
		gone[x] = true
		for _, k := range x.Children {
			mark(k)
		}
	}
	mark(it)
	p := it.Parent
	for i, c := range p.Children {
		if c == it {
			p.Children = append(p.Children[:i:i], p.Children[i+1:]...)
			break
		}
	}
	order := g.Order[:0:0]
	for _, o := range g.Order {
		if gone[o] {
			delete(g.Items, o.ID)
			delete(g.Pos, o.ID)
		} else {
			order = append(order, o)
		}
	}
	g.Order = order
	edges := g.Edges[:0:0]
	for _, e := range g.Edges {
		if !gone[g.Items[e.From]] && !gone[g.Items[e.To]] && g.Items[e.From] != nil && g.Items[e.To] != nil {
			edges = append(edges, e)
		}
	}
	g.Edges = edges
	g.relink()
}

// AddEdge joins two items with an arrow. It returns nil when that is not
// possible: the same item twice, a box and the group it is in, or a full diagram.
func (g *Graph) AddEdge(from, to string) *Edge {
	a, b := g.Items[from], g.Items[to]
	named := func(id string) bool { return (&scanner{s: id}).id() == id && !reserved[strings.ToLower(id)] }
	if a == nil || !named(from) || !named(to) || b == nil || a == b || contains(a, b) || contains(b, a) || len(g.Edges) >= maxEdges {
		return nil
	}
	e := &Edge{From: from, To: to, Head: true}
	g.Edges = append(g.Edges, e)
	g.relink()
	return e
}

// RemoveEdge deletes one arrow.
func (g *Graph) RemoveEdge(e *Edge) {
	for i, x := range g.Edges {
		if x == e {
			g.Edges = append(g.Edges[:i:i], g.Edges[i+1:]...)
			return
		}
	}
}

// GroupItems puts the items in a new group with a title, inside their common
// group (the page when they have none). Items inside one another are left out.
func (g *Graph) GroupItems(items []*Item, title string) *Item {
	if len(g.Order) >= maxItems {
		return nil
	}
	var sel []*Item
	for _, it := range items {
		if it == nil || it.Parent == nil {
			continue
		}
		nested := false
		for _, o := range items {
			if o != it && contains(o, it) {
				nested = true
			}
		}
		if !nested {
			sel = append(sel, it)
		}
	}
	if len(sel) == 0 {
		return nil
	}
	parent := sel[0].Parent
	for _, it := range sel {
		if it.Parent != parent {
			parent = g.Root
		}
	}
	if parent != g.Root {
		depth := 0
		for a := parent; a.Parent != nil; a = a.Parent {
			depth++
		}
		if depth >= maxDepth {
			return nil
		}
	}
	title = Clean(title)
	if title == "" {
		title = "Group"
	}
	id := g.NewID(title)
	gr := &Item{ID: id, Group: true, Title: truncate(title, maxLabel), Parent: parent, Dir: parent.Dir}
	g.Items[id] = gr
	g.Order = append(g.Order, gr)
	parent.Children = append(parent.Children, gr)
	for _, it := range sel {
		g.Reparent(it, gr)
	}
	g.relink()
	return gr
}

// Reparent moves an item into a group (the page when nil). A group cannot go
// inside itself.
func (g *Graph) Reparent(it, parent *Item) {
	if parent == nil {
		parent = g.Root
	}
	if it == nil || it.Parent == nil || it.Parent == parent || contains(it, parent) {
		return
	}
	old := it.Parent
	for i, c := range old.Children {
		if c == it {
			old.Children = append(old.Children[:i:i], old.Children[i+1:]...)
			break
		}
	}
	it.Parent = parent
	parent.Children = append(parent.Children, it)
	g.relink()
}

// Ungroup removes a group and leaves what was in it where the group was.
func (g *Graph) Ungroup(gr *Item) {
	if gr == nil || !gr.Group || gr.Parent == nil {
		return
	}
	p := gr.Parent
	var kids []*Item
	kids = append(kids, gr.Children...)
	for _, k := range kids {
		k.Parent = p
	}
	var out []*Item
	for _, c := range p.Children {
		if c == gr {
			out = append(out, kids...)
		} else {
			out = append(out, c)
		}
	}
	p.Children = out
	gr.Children = nil
	order := g.Order[:0:0]
	for _, o := range g.Order {
		if o != gr {
			order = append(order, o)
		}
	}
	g.Order = order
	delete(g.Items, gr.ID)
	// An arrow to the group has nowhere to go.
	edges := g.Edges[:0:0]
	for _, e := range g.Edges {
		if e.From != gr.ID && e.To != gr.ID {
			edges = append(edges, e)
		}
	}
	g.Edges = edges
	g.relink()
}

// ---- Mermaid out ----

var reSafeText = regexp.MustCompile(`^[\p{L}\p{N} ]+$`)
var reEndWord = regexp.MustCompile(`(?i)\bend\b`)

// textOut is text as it goes inside a box or an arrow's label: quoted and
// escaped unless it is plain words.
func textOut(s string) string {
	if reSafeText.MatchString(s) && !reEndWord.MatchString(s) {
		return s
	}
	return `"` + escape(s) + `"`
}

func linesOut(ls []Line) string {
	var parts []string
	for _, l := range ls {
		t := escape(l.Text)
		if l.Bold {
			t = "**" + t + "**"
		}
		parts = append(parts, t)
	}
	s := strings.Join(parts, "<br>")
	if reSafeText.MatchString(s) && !reEndWord.MatchString(s) {
		return s
	}
	return `"` + s + `"`
}

func nodeOut(it *Item) string {
	if it.Bare && !reserved[strings.ToLower(it.ID)] {
		return it.ID // a box named like a keyword is written with its text, so it is read as a box
	}
	open, closer := "[", "]"
	switch it.Shape {
	case ShapeRound:
		open, closer = "(", ")"
	case ShapeStadium:
		open, closer = "([", "])"
	case ShapeDiamond:
		open, closer = "{", "}"
	case ShapeCircle:
		open, closer = "((", "))"
	}
	return it.ID + open + linesOut(it.Lines) + closer
}

func arrowOut(e *Edge) string {
	tail := ""
	if e.Tail {
		tail = "<"
	}
	var s string
	switch {
	case e.Invisible:
		s = "~~~"
	case e.Thick && e.Head:
		s = tail + "==>"
	case e.Thick:
		s = tail + "==="
	case e.Dotted && e.Head:
		s = tail + "-.->"
	case e.Dotted:
		s = tail + "-.-"
	case e.Head:
		s = tail + "-->"
	default:
		s = tail + "---"
	}
	if e.Label != "" {
		s += "|" + textOut(e.Label) + "|"
	}
	return s
}

func edgeOut(e *Edge) string { return e.From + " " + arrowOut(e) + " " + e.To }

func edgeKey(e *Edge) string {
	head, tail := e.Head && !e.Invisible, e.Tail && !e.Invisible // a hidden arrow has no ends
	return strings.Join([]string{e.From, e.To, e.Label, b2s(e.Dotted), b2s(e.Thick), b2s(head), b2s(tail), b2s(e.Invisible)}, "\x00")
}

func b2s(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func groupOut(it *Item) string {
	if it.Title == it.ID && reSafeText.MatchString(it.ID) {
		return "subgraph " + it.ID
	}
	return "subgraph " + it.ID + " [" + textOut(it.Title) + "]"
}
