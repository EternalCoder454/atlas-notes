// Package diagram reads Mermaid flowcharts, lays them out and describes how to
// draw them. It knows nothing about GTK: the text is measured through a function
// the caller gives, and the drawing is a list of shapes (a Scene) that the editor
// paints with Cairo and the exporters write as SVG.
package diagram

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// Shape is the outline of a box.
type Shape int

const (
	ShapeRect Shape = iota
	ShapeRound
	ShapeStadium
	ShapeDiamond
	ShapeCircle
)

// Line is one line of a box's text.
type Line struct {
	Text string
	Bold bool
}

// Item is a box, or a group (a subgraph) holding other items. The root group
// has no ID and is the page itself.
type Item struct {
	ID       string
	Group    bool
	Lines    []Line // a box's text
	Title    string // a group's heading
	Shape    Shape
	Accent   bool
	Bare     bool // named in an edge and never given a shape or text
	Dir      string
	Parent   *Item
	Children []*Item

	// Set by Layout. X and Y are on the page; W and H are the size.
	X, Y, W, H float64

	index  int
	rx, ry float64 // inside the parent
	dl     []drawnLine
	depth  int
}

// Edge is an arrow, or a plain line, between two items.
type Edge struct {
	From, To  string
	Label     string
	Dotted    bool
	Thick     bool
	Head      bool // arrowhead at To
	Tail      bool // arrowhead at From
	Invisible bool // keeps two boxes in a row and is not drawn

	from, to *Item
	lca      *Item
	lf, lt   *Item
	skip     bool
	gap      float64
}

// Graph is a parsed flowchart.
type Graph struct {
	Dir   string // TD, LR, BT or RL
	Root  *Item
	Items map[string]*Item
	Order []*Item
	Edges []*Edge
}

// UnsupportedError says what kind of diagram was not drawn, or what in it.
type UnsupportedError struct{ Msg string }

func (e *UnsupportedError) Error() string { return e.Msg }

const (
	maxSource = 64 << 10
	maxItems  = 200
	maxEdges  = 500
	maxDepth  = 4
)

type parser struct {
	g       *Graph
	stack   []*Item
	classes map[string]bool
	pending []pendingClass
	lineNo  int
}

type pendingClass struct {
	ids   []string
	class string
}

func errf(line int, format string, a ...any) error {
	return &UnsupportedError{Msg: fmt.Sprintf("line %d: %s", line, fmt.Sprintf(format, a...))}
}

var diagramKinds = map[string]string{
	"sequencediagram": "sequence diagrams", "gantt": "Gantt charts", "classdiagram": "class diagrams",
	"statediagram": "state diagrams", "statediagram-v2": "state diagrams", "erdiagram": "entity diagrams",
	"pie": "pie charts", "journey": "journeys", "gitgraph": "git graphs", "mindmap": "mind maps",
	"timeline": "timelines", "quadrantchart": "quadrant charts", "requirementdiagram": "requirement diagrams",
	"c4context": "C4 diagrams", "sankey-beta": "Sankey diagrams", "xychart-beta": "charts",
	"block-beta": "block diagrams", "architecture-beta": "architecture diagrams", "kanban": "kanban boards",
}

// Parse reads a Mermaid flowchart. Anything that is not a flowchart, or uses
// something that is not supported, comes back as an *UnsupportedError saying
// what.
func Parse(src string) (g *Graph, err error) {
	if len(src) > maxSource {
		return nil, &UnsupportedError{Msg: "the diagram is too large"}
	}
	defer func() {
		if r := recover(); r != nil {
			g, err = nil, &UnsupportedError{Msg: "the diagram could not be read"}
		}
	}()
	root := &Item{Group: true, Dir: "TD"}
	p := &parser{g: &Graph{Dir: "TD", Root: root, Items: map[string]*Item{}}, classes: map[string]bool{}}
	p.stack = []*Item{root}
	header := false
	for i, raw := range strings.Split(strings.ReplaceAll(src, "\r", ""), "\n") {
		p.lineNo = i + 1
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "%%") {
			continue
		}
		for _, st := range splitStatements(line) {
			st = strings.TrimSpace(st)
			if st == "" {
				continue
			}
			if !header {
				if err := p.header(st); err != nil {
					return nil, err
				}
				header = true
				continue
			}
			if err := p.statement(st); err != nil {
				return nil, err
			}
		}
	}
	if !header {
		return nil, &UnsupportedError{Msg: "the diagram is empty"}
	}
	if len(p.stack) > 1 {
		return nil, &UnsupportedError{Msg: "a subgraph is never closed with end"}
	}
	p.finish()
	if len(p.g.Order) == 0 {
		return nil, &UnsupportedError{Msg: "the diagram has no boxes"}
	}
	return p.g, nil
}

// splitStatements cuts a line at the semicolons that are outside brackets and
// quotes.
func splitStatements(s string) []string {
	var out []string
	depth, start := 0, 0
	inQ := false
	for i, r := range s {
		switch {
		case r == '"':
			inQ = !inQ
		case inQ:
		case r == '[' || r == '(' || r == '{':
			depth++
		case r == ']' || r == ')' || r == '}':
			if depth > 0 {
				depth--
			}
		case r == ';' && depth == 0:
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

func normDir(d string) string {
	switch strings.ToUpper(d) {
	case "TD", "TB":
		return "TD"
	case "LR", "RL", "BT":
		return strings.ToUpper(d)
	}
	return ""
}

func (p *parser) header(s string) error {
	f := strings.Fields(s)
	word := strings.ToLower(f[0])
	if word != "flowchart" && word != "graph" {
		if k, ok := diagramKinds[word]; ok {
			return &UnsupportedError{Msg: "only flowcharts are drawn, not " + k}
		}
		return &UnsupportedError{Msg: "only flowcharts are drawn"}
	}
	dir := "TD"
	if len(f) > 1 {
		if dir = normDir(f[1]); dir == "" {
			return errf(p.lineNo, "unknown direction %q", f[1])
		}
		if len(f) > 2 {
			return errf(p.lineNo, "unexpected text after the direction")
		}
	}
	p.g.Dir = dir
	p.g.Root.Dir = dir
	return nil
}

func (p *parser) cur() *Item { return p.stack[len(p.stack)-1] }

func keyword(s, kw string) (string, bool) {
	if len(s) >= len(kw) && strings.EqualFold(s[:len(kw)], kw) && (len(s) == len(kw) || s[len(kw)] == ' ' || s[len(kw)] == '\t') {
		return strings.TrimSpace(s[len(kw):]), true
	}
	return "", false
}

func (p *parser) statement(s string) error {
	if rest, ok := keyword(s, "subgraph"); ok {
		return p.subgraph(rest)
	}
	if strings.EqualFold(s, "end") {
		if len(p.stack) == 1 {
			return errf(p.lineNo, "end without a subgraph")
		}
		p.stack = p.stack[:len(p.stack)-1]
		return nil
	}
	if rest, ok := keyword(s, "direction"); ok {
		d := normDir(rest)
		if d == "" {
			return errf(p.lineNo, "unknown direction %q", rest)
		}
		p.cur().Dir = d
		return nil
	}
	if rest, ok := keyword(s, "style"); ok {
		f := strings.SplitN(rest, " ", 2)
		if len(f) == 2 {
			p.styleItem(strings.TrimSpace(f[0]), f[1])
		}
		return nil
	}
	if rest, ok := keyword(s, "classDef"); ok {
		f := strings.SplitN(rest, " ", 2)
		if len(f) == 2 {
			for _, name := range strings.Split(f[0], ",") {
				p.classes[name] = accentSpec(name, f[1])
			}
		}
		return nil
	}
	if rest, ok := keyword(s, "class"); ok {
		f := strings.SplitN(rest, " ", 2)
		if len(f) == 2 {
			p.pending = append(p.pending, pendingClass{strings.Split(f[0], ","), strings.TrimSpace(f[1])})
		}
		return nil
	}
	for _, kw := range []string{"linkStyle", "click", "accTitle", "accDescr", "title", "interpolate"} {
		if _, ok := keyword(s, kw); ok {
			return nil
		}
	}
	return p.chain(s)
}

func (p *parser) subgraph(rest string) error {
	if len(p.stack) > maxDepth {
		return errf(p.lineNo, "subgraphs are nested too deeply")
	}
	id, title := rest, rest
	if i := strings.IndexByte(rest, '['); i >= 0 && strings.HasSuffix(rest, "]") {
		id = strings.TrimSpace(rest[:i])
		title = strings.TrimSpace(rest[i+1 : len(rest)-1])
		if len(title) >= 2 && title[0] == '"' && title[len(title)-1] == '"' {
			title = title[1 : len(title)-1]
		}
	}
	id = strings.Trim(id, `"`)
	if id == "" {
		return errf(p.lineNo, "a subgraph needs a name")
	}
	if id == title {
		title = strings.Trim(title, `"`)
	}
	if old, ok := p.g.Items[id]; ok && (old.Group || !old.Bare) {
		return errf(p.lineNo, "%q is used twice", id)
	} else if ok {
		p.drop(old)
	}
	if len(p.g.Order) >= maxItems {
		return &UnsupportedError{Msg: "the diagram is too large"}
	}
	it := &Item{ID: id, Group: true, Title: plain(title), Parent: p.cur(), Dir: "", index: len(p.g.Order)}
	it.Dir = "" // inherits until a direction line says otherwise
	p.add(it)
	p.stack = append(p.stack, it)
	return nil
}

func (p *parser) add(it *Item) {
	p.g.Items[it.ID] = it
	p.g.Order = append(p.g.Order, it)
	it.Parent.Children = append(it.Parent.Children, it)
}

// drop removes a bare box that turned out to be the name of a subgraph.
func (p *parser) drop(it *Item) {
	delete(p.g.Items, it.ID)
	par := it.Parent
	for i, c := range par.Children {
		if c == it {
			par.Children = append(par.Children[:i:i], par.Children[i+1:]...)
			break
		}
	}
	for i, c := range p.g.Order {
		if c == it {
			p.g.Order = append(p.g.Order[:i:i], p.g.Order[i+1:]...)
			break
		}
	}
}

func (p *parser) styleItem(id, spec string) {
	if accentSpec("", spec) {
		p.pending = append(p.pending, pendingClass{[]string{id}, "\x00accent"})
	}
}

func (p *parser) finish() {
	for _, pc := range p.pending {
		on := pc.class == "\x00accent" || p.classes[pc.class]
		if !on {
			continue
		}
		for _, id := range pc.ids {
			if it := p.g.Items[strings.TrimSpace(id)]; it != nil && !it.Group {
				it.Accent = true
			}
		}
	}
	// A bare name that is also a subgraph was a reference to the subgraph.
	for _, e := range p.g.Edges {
		e.from, e.to = p.g.Items[e.From], p.g.Items[e.To]
	}
	setDirs(p.g.Root, p.g.Dir, 0)
}

func setDirs(c *Item, inherited string, depth int) {
	if c.Dir == "" {
		c.Dir = inherited
	}
	c.depth = depth
	for _, k := range c.Children {
		if k.Group {
			setDirs(k, c.Dir, depth+1)
		}
	}
}

// accentSpec says whether a style or class definition asks for a coloured
// box. The colour itself is not used: the app's accent is, so the box stays
// readable in every theme.
func accentSpec(name, spec string) bool {
	ln := strings.ToLower(name)
	for _, w := range []string{"accent", "highlight", "active", "focus", "primary", "important"} {
		if strings.Contains(ln, w) {
			return true
		}
	}
	for _, kv := range strings.Split(spec, ",") {
		k, v, ok := strings.Cut(kv, ":")
		if !ok {
			continue
		}
		k, v = strings.ToLower(strings.TrimSpace(k)), strings.ToLower(strings.TrimSpace(v))
		if (k == "stroke" || k == "fill") && coloured(v) {
			return true
		}
	}
	return false
}

func coloured(v string) bool {
	switch v {
	case "", "none", "transparent", "white", "black", "gray", "grey", "#fff", "#000", "#ffffff", "#000000":
		return false
	}
	if strings.HasPrefix(v, "#") {
		h := v[1:]
		if len(h) == 3 {
			h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
		}
		if len(h) < 6 {
			return false
		}
		var c [3]int
		for i := 0; i < 3; i++ {
			if _, err := fmt.Sscanf(h[2*i:2*i+2], "%x", &c[i]); err != nil {
				return false
			}
		}
		lo, hi := c[0], c[0]
		for _, x := range c {
			lo, hi = min(lo, x), max(hi, x)
		}
		return hi-lo > 24
	}
	return true
}

// ---- statements with boxes and arrows ----

type scanner struct {
	s   string
	pos int
}

func (sc *scanner) rest() string { return sc.s[sc.pos:] }
func (sc *scanner) eof() bool    { return sc.pos >= len(sc.s) }
func (sc *scanner) space() {
	for sc.pos < len(sc.s) && (sc.s[sc.pos] == ' ' || sc.s[sc.pos] == '\t') {
		sc.pos++
	}
}

var (
	rePlain   = regexp.MustCompile(`^(<)?(-{2,}>|-{3,}|-\.+->|-\.+-|={2,}>|={3,}|~{3,})`)
	reDash    = regexp.MustCompile(`^--\s+(.+?)\s+(-{2,}>|-{3,})`)
	reThick   = regexp.MustCompile(`^==\s+(.+?)\s+(={2,}>|={3,})`)
	reDot     = regexp.MustCompile(`^-\.\s+(.+?)\s+(\.+->|\.+-)`)
	reBreak   = regexp.MustCompile(`(?i)<\s*br\s*/?\s*>|\\n`)
	reBold    = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	reHTMLTag = regexp.MustCompile(`</?(?:b|strong|i|em|u|span)[^>]*>`)
)

type link struct {
	label                                string
	dotted, thick, head, tail, invisible bool
}

func (sc *scanner) link() (link, error) {
	r := sc.rest()
	var l link
	if m := rePlainMatch(r); m != nil {
		tok := m[0]
		l.tail = m[1] != ""
		l.head = strings.HasSuffix(tok, ">")
		l.dotted = strings.Contains(tok, ".")
		l.thick = strings.Contains(tok, "=")
		l.invisible = strings.Contains(tok, "~")
		sc.pos += len(tok)
		sc.space()
		if strings.HasPrefix(sc.rest(), "|") {
			end := strings.IndexByte(sc.rest()[1:], '|')
			if end < 0 {
				return l, fmt.Errorf("the label of an arrow is never closed")
			}
			l.label = plainLabel(sc.rest()[1 : 1+end])
			sc.pos += end + 2
		}
		return l, nil
	}
	for _, c := range []struct {
		re     *regexp.Regexp
		dotted bool
		thick  bool
	}{{reDash, false, false}, {reThick, false, true}, {reDot, true, false}} {
		if m := c.re.FindStringSubmatch(r); m != nil {
			l.label = plainLabel(m[1])
			l.dotted, l.thick = c.dotted, c.thick
			l.head = strings.HasSuffix(m[2], ">")
			sc.pos += len(m[0])
			return l, nil
		}
	}
	return l, fmt.Errorf("cannot read the arrow at %q", clip(r, 12))
}

func rePlainMatch(r string) []string { return rePlain.FindStringSubmatch(r) }

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

func plainLabel(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	return plain(s)
}

func plain(s string) string {
	s = reBreak.ReplaceAllString(s, " ")
	s = reHTMLTag.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "**", "")
	return strings.Join(strings.Fields(s), " ")
}

func isIDRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) || r > 127
}

func (sc *scanner) id() string {
	start := sc.pos
	rs := []rune(sc.s[sc.pos:])
	n, bytes := 0, 0
	for n < len(rs) {
		r := rs[n]
		if isIDRune(r) {
		} else if r == '-' && n > 0 && n+1 < len(rs) && isIDRune(rs[n+1]) {
		} else {
			break
		}
		bytes += len(string(r))
		n++
	}
	sc.pos = start + bytes
	return sc.s[start:sc.pos]
}

var shapeOpen = []struct {
	open, close string
	shape       Shape
	bad         bool
}{
	{"(((", "", 0, true}, {"[[", "", 0, true}, {"[(", "", 0, true}, {"{{", "", 0, true},
	{"((", "))", ShapeCircle, false}, {"([", "])", ShapeStadium, false},
	{"[", "]", ShapeRect, false}, {"(", ")", ShapeRound, false}, {"{", "}", ShapeDiamond, false},
}

// node reads one box: its name, and when it has them a shape, text and class.
func (p *parser) node(sc *scanner) (string, error) {
	sc.space()
	id := sc.id()
	if id == "" {
		return "", fmt.Errorf("expected a box name at %q", clip(sc.rest(), 12))
	}
	shape, hasText, text := ShapeRect, false, ""
	r := sc.rest()
	for _, so := range shapeOpen {
		if !strings.HasPrefix(r, so.open) {
			continue
		}
		if so.bad {
			return "", fmt.Errorf("the shape %s is not supported", so.open)
		}
		body := r[len(so.open):]
		var content string
		if t := strings.TrimLeft(body, " "); strings.HasPrefix(t, `"`) {
			end := strings.IndexByte(t[1:], '"')
			if end < 0 {
				return "", fmt.Errorf("a quote is never closed")
			}
			content = t[1 : 1+end]
			after := strings.TrimLeft(t[end+2:], " ")
			if !strings.HasPrefix(after, so.close) {
				return "", fmt.Errorf("box %s is never closed", id)
			}
			sc.pos += len(r) - len(after) + len(so.close)
		} else {
			end := strings.Index(body, so.close)
			if end < 0 {
				return "", fmt.Errorf("box %s is never closed", id)
			}
			content = body[:end]
			sc.pos += len(so.open) + end + len(so.close)
		}
		shape, hasText, text = so.shape, true, content
		break
	}
	class := ""
	if strings.HasPrefix(sc.rest(), ":::") {
		sc.pos += 3
		class = sc.id()
	}
	if err := p.touch(id, shape, hasText, text); err != nil {
		return "", err
	}
	if class != "" {
		p.pending = append(p.pending, pendingClass{[]string{id}, class})
	}
	return id, nil
}

func (p *parser) touch(id string, shape Shape, hasText bool, text string) error {
	it := p.g.Items[id]
	if it != nil && it.Group {
		if hasText {
			return fmt.Errorf("%q is a subgraph and cannot be given text", id)
		}
		return nil
	}
	if it == nil {
		if len(p.g.Order) >= maxItems {
			return &UnsupportedError{Msg: "the diagram is too large"}
		}
		it = &Item{ID: id, Shape: shape, Bare: !hasText, Parent: p.cur(), index: len(p.g.Order)}
		it.Lines = []Line{{Text: id}}
		p.add(it)
	}
	if hasText {
		it.Shape, it.Bare = shape, false
		it.Lines = makeLines(text)
		if len(it.Lines) == 0 {
			it.Lines = []Line{{Text: id}}
		}
	}
	return nil
}

// makeLines splits a box's text at <br> and \n. A line written in **bold** is
// bold; when none is, the first of several lines is the title and is drawn
// bold, and the layout draws the rest smaller.
func makeLines(text string) []Line {
	parts := reBreak.Split(text, -1)
	var out []Line
	for _, part := range parts {
		part = reHTMLTag.ReplaceAllString(part, "")
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		bold := false
		if reBold.MatchString(part) {
			bold = true
			part = reBold.ReplaceAllString(part, "$1")
		}
		part = strings.Join(strings.Fields(strings.ReplaceAll(part, "**", "")), " ")
		out = append(out, Line{Text: part, Bold: bold})
	}
	return out
}

// hasExplicitBold reports whether the box's own text asked for bold anywhere.
func hasExplicitBold(ls []Line) bool {
	for _, l := range ls {
		if l.Bold {
			return true
		}
	}
	return false
}

func (p *parser) nodeGroup(sc *scanner) ([]string, error) {
	var ids []string
	for {
		id, err := p.node(sc)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
		sc.space()
		if strings.HasPrefix(sc.rest(), "&") {
			sc.pos++
			continue
		}
		return ids, nil
	}
}

func (p *parser) chain(s string) error {
	sc := &scanner{s: s}
	left, err := p.nodeGroup(sc)
	if err != nil {
		return errf(p.lineNo, "%v", err)
	}
	for {
		sc.space()
		if sc.eof() {
			return nil
		}
		l, err := sc.link()
		if err != nil {
			return errf(p.lineNo, "%v", err)
		}
		right, err := p.nodeGroup(sc)
		if err != nil {
			return errf(p.lineNo, "%v", err)
		}
		for _, a := range left {
			for _, b := range right {
				if len(p.g.Edges) >= maxEdges {
					return &UnsupportedError{Msg: "the diagram is too large"}
				}
				p.g.Edges = append(p.g.Edges, &Edge{From: a, To: b, Label: l.label, Dotted: l.dotted,
					Thick: l.thick, Head: l.head, Tail: l.tail, Invisible: l.invisible})
			}
		}
		left = right
	}
}
