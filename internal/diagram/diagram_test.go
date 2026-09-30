package diagram

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

const example = "flowchart TD\n  subgraph sources [Free sources]\n    direction LR\n    yt[YouTube suggestions] ~~~ api[YouTube Data API] ~~~ trends[Google Trends] ~~~ irs[IRS deadlines] ~~~ qbox[Host question box]\n  end\n  subgraph prog [Go program, runs Sunday night]\n    direction LR\n    rank[Collect and rank<br>Finds the 10 best topics each week] --> draft[Draft scripts<br>Ollama by default, Gemini as backup] --> checks[Go checks<br>Length, disclaimer, numbers vs IRS text] --> send[Send drafts<br>Emails the Host and files Drive copies]\n  end\n  yt & api & trends & irs & qbox --> prog\n  send --> email[Email to the Host<br>Script text, sources, and fact-check flags] & drive[Google Drive<br>Script copies and the Posting Log sheet]\n  email -->|The Host reads and replies by email| approve[Host approves<br>Checks every fact before recording]\n  approve --> record[Host records<br>On a phone, in batches of 3 to 4] --> edit[Producer edits<br>Trims the video and schedules the upload] --> clip[Clipper<br>Cuts clips and runs them in Meta Ads]\n  style approve stroke:#3584e4,stroke-width:2px\n"

func mustParse(t *testing.T, s string) *Graph {
	t.Helper()
	g, err := Parse(s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return g
}

func TestParseExample(t *testing.T) {
	g := mustParse(t, example)
	if len(g.Items) != 17 {
		t.Errorf("items = %d", len(g.Items))
	}
	if !g.Items["approve"].Accent || g.Items["send"].Accent {
		t.Error("accent wrong")
	}
	if g.Items["sources"].Dir != "LR" || g.Items["sources"].Title != "Free sources" {
		t.Errorf("sources = %+v", g.Items["sources"])
	}
	if g.Items["yt"].Parent != g.Items["sources"] {
		t.Error("yt not in sources")
	}
	if g.Items["prog"] == nil || g.Items["prog"].Bare {
		t.Error("prog should be a group")
	}
	var labelled int
	for _, e := range g.Edges {
		if e.Label == "The Host reads and replies by email" {
			labelled++
		}
	}
	if labelled != 1 {
		t.Error("label missing")
	}
	if len(g.Items["rank"].Lines) != 2 {
		t.Error("br not split")
	}
}

func TestParseConstructs(t *testing.T) {
	g := mustParse(t, "graph LR\n%% comment\na[A] --> b(B) --- c([C]) -.-> d{D} ==> e((E))\nf -- text --> g\nh -->|lbl| i; i & j --> k\nl[\"Quoted (x)\"]:::hot\nclassDef hot fill:#f96\nstyle zz stroke:#888\nz[**Bold** plain\\nsecond]\nsubgraph s1 [One]\n subgraph s2 [Two]\n x --> y\n end\nend\nx2 --> s1\n")
	want := map[string]Shape{"a": ShapeRect, "b": ShapeRound, "c": ShapeStadium, "d": ShapeDiamond, "e": ShapeCircle}
	for id, sh := range want {
		if g.Items[id].Shape != sh {
			t.Errorf("%s shape %v", id, g.Items[id].Shape)
		}
	}
	if !g.Items["l"].Accent {
		t.Error("class hot")
	}
	if g.Items["l"].Lines[0].Text != "Quoted (x)" {
		t.Errorf("quoted %q", g.Items["l"].Lines[0].Text)
	}
	if z := g.Items["z"].Lines; len(z) != 2 || !z[0].Bold || z[1].Bold || z[0].Text != "Bold plain" {
		t.Errorf("z = %+v", z)
	}
	if g.Items["y"].Parent != g.Items["s2"] || g.Items["s2"].Parent != g.Items["s1"] {
		t.Error("nesting")
	}
	find := func(a, b string) *Edge {
		for _, e := range g.Edges {
			if e.From == a && e.To == b {
				return e
			}
		}
		t.Fatalf("no edge %s %s", a, b)
		return nil
	}
	if e := find("a", "b"); !e.Head {
		t.Error("arrow")
	}
	if e := find("b", "c"); e.Head {
		t.Error("open")
	}
	if e := find("c", "d"); !e.Dotted {
		t.Error("dotted")
	}
	if e := find("d", "e"); !e.Thick {
		t.Error("thick")
	}
	if e := find("f", "g"); e.Label != "text" {
		t.Error("inline label")
	}
	if e := find("h", "i"); e.Label != "lbl" {
		t.Error("pipe label")
	}
	find("i", "k")
	find("j", "k")
	find("x2", "s1")
}

func TestUnsupported(t *testing.T) {
	for _, s := range []string{"sequenceDiagram\nA->>B: hi", "gantt\n title x", "", "flowchart XX\na-->b",
		"flowchart TD\na[", "flowchart TD\na --", "flowchart TD\nsubgraph x\na", "flowchart TD\nend",
		"flowchart TD\na[[x]]", "flowchart TD\na --> ", "pie\n"} {
		g, err := Parse(s)
		if err == nil {
			t.Errorf("%q parsed: %v", s, g)
		} else if _, ok := err.(*UnsupportedError); !ok {
			t.Errorf("%q: wrong error type %T", s, err)
		}
	}
}

func rects(g *Graph) []*Item {
	var out []*Item
	for _, it := range g.Order {
		out = append(out, it)
	}
	return out
}

func TestLayoutInvariants(t *testing.T) {
	for _, src := range []string{example, "graph LR\na-->b-->c\nc-->a\nb-->d{D}-->e", "graph BT\nsubgraph s\ndirection RL\na-->b\nend\nb-->c", "graph TD\na-->a\nx\ny"} {
		g := mustParse(t, src)
		sc := Layout(g, nil)
		if sc.W <= 0 || sc.H <= 0 {
			t.Fatal("empty scene")
		}
		for _, a := range rects(g) {
			if a.X < -0.01 || a.Y < -0.01 || a.X+a.W > sc.W+0.01 || a.Y+a.H > sc.H+0.01 {
				t.Errorf("%s outside the page", a.ID)
			}
			if a.Parent != nil && a.Parent.Parent != nil {
				p := a.Parent
				if a.X < p.X || a.Y < p.Y || a.X+a.W > p.X+p.W || a.Y+a.H > p.Y+p.H {
					t.Errorf("%s outside its group %s", a.ID, p.ID)
				}
			}
			for _, b := range rects(g) {
				if a == b || a.Parent != b.Parent {
					continue
				}
				if a.X < b.X+b.W && b.X < a.X+a.W && a.Y < b.Y+b.H && b.Y < a.Y+a.H {
					t.Errorf("%s overlaps %s", a.ID, b.ID)
				}
			}
		}
		// Arrows end on the border of their target.
		for _, e := range g.Edges {
			if e.skip || e.Invisible || e.to.Shape == ShapeDiamond || e.to.Shape == ShapeCircle {
				continue
			}
			found := false
			for _, p := range sc.Prims {
				if p.Kind != PrimPath {
					continue
				}
				q := p.Pts[len(p.Pts)-1]
				it := e.to
				onX := math.Abs(q.X-it.X) < 0.01 || math.Abs(q.X-it.X-it.W) < 0.01
				onY := math.Abs(q.Y-it.Y) < 0.01 || math.Abs(q.Y-it.Y-it.H) < 0.01
				inX := q.X >= it.X-0.01 && q.X <= it.X+it.W+0.01
				inY := q.Y >= it.Y-0.01 && q.Y <= it.Y+it.H+0.01
				if (onX && inY) || (onY && inX) {
					found = true
				}
			}
			if !found {
				t.Errorf("no arrow ends on %s", e.to.ID)
			}
		}
		again := Layout(mustParse(t, src), nil)
		if again.SVG(LightPalette) != sc.SVG(LightPalette) {
			t.Error("layout is not deterministic")
		}
	}
}

func TestLabelDoesNotOverlapBoxes(t *testing.T) {
	g := mustParse(t, example)
	sc := Layout(g, nil)
	for i, p := range sc.Prims {
		if p.Kind != PrimText || p.Text != "The Host reads and replies by email" {
			continue
		}
		plate := sc.Prims[i-1]
		for _, it := range g.Order {
			if it.Group {
				continue
			}
			if plate.X < it.X+it.W && it.X < plate.X+plate.W && plate.Y < it.Y+it.H && it.Y < plate.Y+plate.H {
				t.Errorf("label overlaps %s", it.ID)
			}
		}
		return
	}
	t.Error("label not drawn")
}

func TestSVG(t *testing.T) {
	sc := Layout(mustParse(t, example), nil)
	s := sc.SVG(LightPalette)
	if !strings.HasPrefix(s, "<svg") || !strings.Contains(s, "Free sources") || !strings.HasSuffix(s, "</svg>") {
		t.Error("bad svg")
	}
}

func FuzzParse(f *testing.F) {
	f.Add(example)
	f.Add("graph LR\na-->b")
	f.Add("flowchart TD\nsubgraph a\nb[\"x\nend")
	f.Fuzz(func(t *testing.T, s string) {
		g, err := Parse(s)
		if err != nil {
			return
		}
		Layout(g, nil).SVG(LightPalette)
	})
}

func TestLongEdgeAvoidsBoxes(t *testing.T) {
	for _, dir := range []string{"TD", "LR", "BT"} {
		g := mustParse(t, "graph "+dir+"\na-->b-->c-->d\na-->d\nsubgraph s\nx-->y-->z\nx-->z\nend\nd-->x")
		sc := Layout(g, nil)
		for _, p := range sc.Prims {
			if p.Kind != PrimPath {
				continue
			}
			for i := 1; i < len(p.Pts); i++ {
				a, b := p.Pts[i-1], p.Pts[i]
				for _, id := range []string{"b", "c", "y"} {
					it := g.Items[id]
					// An arrow that begins or ends at the box may touch it; none may cross it.
					lo, hi := math.Min(a.X, b.X), math.Max(a.X, b.X)
					lo2, hi2 := math.Min(a.Y, b.Y), math.Max(a.Y, b.Y)
					if lo < it.X+it.W-1 && hi > it.X+1 && lo2 < it.Y+it.H-1 && hi2 > it.Y+1 {
						// Only arrows whose own ends are this box may be in it.
						own := false
						for _, e := range g.Edges {
							if e.From == id || e.To == id {
								own = own || endsAt(p, e)
							}
						}
						if !own {
							t.Errorf("%s: an arrow crosses %s", dir, id)
						}
					}
				}
			}
		}
	}
}

func endsAt(p Prim, e *Edge) bool {
	q := p.Pts[len(p.Pts)-1]
	it := e.to
	return q.X >= it.X-0.01 && q.X <= it.X+it.W+0.01 && q.Y >= it.Y-0.01 && q.Y <= it.Y+it.H+0.01
}

func TestPathologicalInputs(t *testing.T) {
	cases := map[string]string{
		"many ids":    "flowchart TD\n" + strings.Repeat("a&", 30000) + "a",
		"many nodes":  "flowchart TD\n" + strings.Repeat("n1 --> n2\n", 100000),
		"long label":  "flowchart TD\na[" + strings.Repeat("word ", 5000) + "] --> b",
		"long word":   "flowchart TD\na[" + strings.Repeat("x", 50000) + "]",
		"lines":       "flowchart TD\na[" + strings.Repeat("l<br>", 5000) + "]",
		"deep":        strings.Repeat("flowchart TD\nsubgraph s\n", 40),
		"unclosed":    "flowchart TD\na[" + strings.Repeat("(", 20000),
		"many arrows": "flowchart TD\na" + strings.Repeat(" --> a", 5000),
	}
	for name, src := range cases {
		done := make(chan struct{})
		go func() {
			defer close(done)
			if g, err := Parse(src); err == nil {
				sc := Layout(g, nil)
				for _, p := range sc.Prims {
					if p.Kind == PrimBox && (p.W > 1000 || p.H > 1000) && p.Fill != RoleGroup {
						t.Errorf("%s: a box %.0f by %.0f", name, p.W, p.H)
					}
				}
				sc.SVG(LightPalette)
			}
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: took more than 5 seconds", name)
		}
	}
}

func TestTooLargeIsRefused(t *testing.T) {
	var b strings.Builder
	b.WriteString("flowchart TD\n")
	for i := 0; i < 190; i++ {
		fmt.Fprintf(&b, "n%d --> n%d\n", i, i+1)
	}
	if _, err := ParseDrawable(b.String()); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Errorf("a very tall chain was accepted: %v", err)
	}
	if _, err := ParseDrawable(example); err != nil {
		t.Error(err)
	}
}

func TestKeywordsAsNames(t *testing.T) {
	g := mustParse(t, "flowchart TD\nstyle --> class\nclick[Go] --> title(T)\nclass --> end2\nstyle a stroke:#f00\na[A]")
	for _, id := range []string{"style", "class", "click", "title"} {
		if g.Items[id] == nil {
			t.Errorf("%s was taken for a directive", id)
		}
	}
	if !g.Items["a"].Accent {
		t.Error("a real style line was lost")
	}
}
