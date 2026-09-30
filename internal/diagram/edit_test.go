package diagram

import (
	"regexp"
	"strings"
	"testing"
)

func reparse(t *testing.T, g *Graph) (string, *Graph) {
	t.Helper()
	s := g.Mermaid()
	g2, err := Parse(s)
	if err != nil {
		t.Fatalf("output does not parse: %v\n%s", err, s)
	}
	if !sameGraph(g, g2) {
		t.Fatalf("output reads back differently:\n%s", s)
	}
	return s, g2
}

func TestMermaidUnchangedIsIdentical(t *testing.T) {
	for _, src := range []string{example, "flowchart LR\n  a --> b\n  %% note\n  classDef hot fill:#f96\n  class a hot\n  linkStyle 0 stroke:red\n", "graph TD\n%% atlas:pos a 40 50\na[A] --> b[B]\n%% atlas:size 900 600\n"} {
		g := mustParse(t, src)
		if got := g.Mermaid(); got != src {
			t.Errorf("unchanged source rewritten:\n%s\n---\n%s", src, got)
		}
		if got := g.canonical(); mustParse(t, got) == nil || !sameGraph(g, mustParse(t, got)) {
			t.Errorf("canonical form reads back differently:\n%s", got)
		}
	}
}

func TestMermaidEditKeepsOtherLines(t *testing.T) {
	src := "flowchart TD\n  %% keep me\n  a[One] --> b[Two]\n  b --> c[Three]\n  classDef hot fill:#f96\n  linkStyle 0 stroke:red\n  click a href \"x\"\n"
	g := mustParse(t, src)
	g.SetText(g.Items["c"], "Third \"one\" [x] | y", "line two")
	d := g.AddBox("Fresh start", nil, 100, 200)
	g.AddEdge("c", d.ID)
	g.SetPos("a", 12, 34)
	s, g2 := reparse(t, g)
	for _, keep := range []string{"%% keep me", "a[One] --> b[Two]", "classDef hot fill:#f96", "linkStyle 0 stroke:red", `click a href "x"`} {
		if !strings.Contains(s, keep) {
			t.Errorf("lost %q:\n%s", keep, s)
		}
	}
	if !strings.Contains(s, "%% atlas:pos a 12 34") || !strings.Contains(s, "%% atlas:pos fresh_star 100 200") && !strings.Contains(s, "atlas:pos "+d.ID+" 100 200") {
		t.Errorf("positions missing:\n%s", s)
	}
	if l := g2.Items["c"].Lines; len(l) != 2 || l[0].Text != `Third "one" [x] | y` {
		t.Errorf("text = %+v", l)
	}
	if strings.Index(s, "a[One]") > strings.Index(s, "b --> c") && strings.Contains(s, "b --> c") {
		t.Errorf("order changed:\n%s", s)
	}
}

func TestMermaidGroups(t *testing.T) {
	g := mustParse(t, example)
	n := len(g.Items)
	gr := g.GroupItems([]*Item{g.Items["approve"], g.Items["record"]}, "Human steps")
	if gr == nil || g.Items["approve"].Parent != gr {
		t.Fatal("not grouped")
	}
	s, g2 := reparse(t, g)
	if g2.Items[gr.ID] == nil || g2.Items["record"].Parent.ID != gr.ID || len(g2.Items) != n+1 {
		t.Errorf("group lost:\n%s", s)
	}
	g2.Ungroup(g2.Items[gr.ID])
	s, g3 := reparse(t, g2)
	if g3.Items["approve"].Parent != g3.Root {
		t.Errorf("not ungrouped:\n%s", s)
	}
	g3.Reparent(g3.Items["yt"], g3.Items["prog"])
	reparse(t, g3)
	g3.Remove(g3.Items["qbox"])
	s, _ = reparse(t, g3)
	if strings.Contains(s, "qbox") {
		t.Errorf("removed box remains:\n%s", s)
	}
}

func TestMermaidAccentAndStyles(t *testing.T) {
	g := mustParse(t, example)
	g.SetAccent(g.Items["approve"], false)
	g.Items["send"].Accent = true
	_, g2 := reparse(t, g)
	if g2.Items["approve"].Accent || !g2.Items["send"].Accent {
		t.Error("accent not kept")
	}
	h := mustParse(t, "flowchart TD\n a:::hot --> b\n classDef hot fill:#f96\n")
	h.SetAccent(h.Items["a"], false)
	reparse(t, h)
	h.Items["b"].Accent = true
	h.SetText(h.Items["b"], "B text", "")
	reparse(t, h)
}

func TestMermaidQuoting(t *testing.T) {
	for _, title := range []string{`say "hi"`, "a [b] c", "x | y", "p(q)", "end", "a <b> c", "#quot; literal", "semi; colon: {x}", "`code`", "`", "émoji ü", "a --> b", "100%"} {
		g := mustParse(t, "flowchart TD\n a --> b\n")
		g.SetText(g.Items["a"], title, "sub "+title)
		e := g.Edges[0]
		e.Label = title
		g.SetText(g.AddGroup(t, "g"), title, "")
		_, g2 := reparse(t, g)
		if g2.Items["a"].Lines[0].Text != Clean(title) || g2.Edges[0].Label != title {
			t.Errorf("%q came back as %q / %q", title, g2.Items["a"].Lines[0].Text, g2.Edges[0].Label)
		}
	}
}

func (g *Graph) AddGroup(t *testing.T, title string) *Item {
	gr := g.GroupItems([]*Item{g.Items["b"]}, title)
	if gr == nil {
		t.Fatal("no group")
	}
	return gr
}

func TestEdgeStylesRoundTrip(t *testing.T) {
	g := mustParse(t, "flowchart LR\n a --> b\n")
	e := g.Edges[0]
	for _, st := range []Edge{{Head: true}, {Head: true, Dotted: true}, {Head: true, Thick: true}, {}, {Dotted: true}, {Thick: true}, {Head: true, Tail: true}, {Invisible: true}, {Head: true, Dotted: true, Label: "yes|no"}} {
		e.Head, e.Dotted, e.Thick, e.Tail, e.Invisible, e.Label = st.Head, st.Dotted, st.Thick, st.Tail, st.Invisible, st.Label
		reparse(t, g)
	}
}

func TestNewID(t *testing.T) {
	g := mustParse(t, "flowchart TD\n a --> b\n")
	ids := map[string]bool{}
	for _, title := range []string{"a", "a", "End", "", "2nd step", "Données", "a", "A very long title indeed"} {
		id := g.NewID(title)
		if ids[id] || reserved[id] || g.Items[id] != nil || id == "" || strings.ContainsAny(id, " -") || len(id) > 14 {
			t.Errorf("bad id %q for %q", id, title)
		}
		ids[id] = true
		g.AddBox(title, nil, 0, 0)
	}
}

func TestPositionsHonoured(t *testing.T) {
	g := mustParse(t, "flowchart TD\n a[A] --> b[B] --> c[C]\n subgraph s [G]\n  d[D]\n end\n c --> d\n")
	auto := Layout(g, nil)
	ay := g.Items["b"].Y
	g.SetPos("b", 400, 300)
	g.SetPos("d", 500, 50)
	sc := Layout(g, nil)
	if g.Items["b"].X != 400 || g.Items["b"].Y != 300 || g.Items["d"].X != 500 {
		t.Errorf("not placed: %v %v", g.Items["b"].X, g.Items["b"].Y)
	}
	if s := g.Items["s"]; s.X > 500-groupPad+1 || s.X+s.W < 500+g.Items["d"].W {
		t.Errorf("group does not hold its box: %+v", s)
	}
	if g.Items["a"].Y == ay && ay == g.Items["b"].Y {
		t.Error("auto box moved wrongly")
	}
	if sc.W < 500 || sc.H < 300 {
		t.Errorf("page %vx%v too small", sc.W, sc.H)
	}
	if len(sc.Routes) != 3 {
		t.Errorf("%d routes, want 3", len(sc.Routes))
	}
	_ = auto
	// Arrows follow the moved box.
	for _, r := range sc.Routes {
		if r.Edge.To == "b" {
			last := r.Pts[len(r.Pts)-1]
			if last.X < 400-1 || last.X > 400+g.Items["b"].W+1 || last.Y < 300-1 || last.Y > 300+g.Items["b"].H+1 {
				t.Errorf("arrow ends at %v, not at the box", last)
			}
		}
	}
}

func TestPositionComments(t *testing.T) {
	g := mustParse(t, "flowchart TD\n%% atlas:pos a 10.4 20\n%% atlas:pos zz 1 1\n%% atlas:pos a bad 3\n%% atlas:pos b -5 1e9\n%% atlas:size 800 600\na --> b\n")
	if g.Pos["a"] != (Pt{10.4, 20}) || g.Pos["b"].X != 0 || g.Pos["b"].Y != MaxSize || g.CanvasW != 800 {
		t.Errorf("pos = %v size %v", g.Pos, g.CanvasW)
	}
	s := g.Mermaid()
	if strings.Contains(s, "zz") || !strings.Contains(s, "atlas:pos a 10 20") {
		t.Errorf("stale or unrounded:\n%s", s)
	}
}

func FuzzMermaidRoundTrip(f *testing.F) {
	f.Add(example)
	f.Add("graph LR\n a[\"x [y]\"] -->|\"l|m\"| b(B)\n subgraph s\n  c{C} -.-> d((D))\n end\n b ==> s\n%% atlas:pos a 5 5\n")
	f.Add("flowchart TD\n a:::hot --> b; classDef hot fill:#f96\n")
	f.Fuzz(func(t *testing.T, src string) {
		g, err := Parse(src)
		if err != nil {
			return
		}
		for id := range g.Items {
			if reserved[strings.ToLower(id)] || strings.ContainsAny(id, `"[]`) || (g.Items[id].Group && !tidyGroupID.MatchString(id)) {
				return // written back only as far as it can be read
			}
		}
		out := g.Mermaid()
		g2, err := Parse(out)
		if err != nil {
			t.Fatalf("output does not parse: %v\n%s\nfrom\n%s", err, out, src)
		}
		if !sameGraph(g, g2) {
			t.Fatalf("round trip differs:\n%s\nfrom\n%s", out, src)
		}
		// And after an edit.
		if b := g.AddBox("New \"one\"", nil, 10, 10); b != nil && len(g.Order) > 1 {
			g.AddEdge(g.Order[0].ID, b.ID)
			g2, err = Parse(g.Mermaid())
			if err != nil || !sameGraph(g, g2) {
				t.Fatalf("edit round trip differs (%v):\n%s\nfrom\n%s", err, g.Mermaid(), src)
			}
		}
	})
}

var tidyGroupID = regexp.MustCompile(`^[\p{L}\p{N}_]([\p{L}\p{N}_ -]*[\p{L}\p{N}_])?$`)

func TestCarriedLines(t *testing.T) {
	src := "flowchart TD\n  a:::k --> b\n  b --> c\n  c --> d\n  classDef k fill:#eee\n  class c,d k\n  linkStyle 1,2 stroke:red\n  click c href \"x\"\n  click d href \"y\"\n"
	g := mustParse(t, src)
	g.RemoveEdge(g.Edges[0]) // a --> b goes
	g.Remove(g.Items["d"])
	g.SetText(g.Items["a"], "A2", "")
	s, g2 := reparse(t, g)
	if strings.Contains(s, "linkStyle 1,2") || !strings.Contains(s, "linkStyle 0 stroke:red") {
		t.Errorf("linkStyle not renumbered:\n%s", s)
	}
	if strings.Contains(s, "click d") || !strings.Contains(s, "click c") || strings.Contains(s, "c,d") {
		t.Errorf("lines for deleted boxes kept:\n%s", s)
	}
	if !hasStr(g2.Items["a"].Classes, "k") || !hasStr(g2.Items["c"].Classes, "k") {
		t.Errorf("classes lost:\n%s", s)
	}
	if id := g.NewID("d"); id == "d" {
		t.Error("new id reuses a name a carried line mentions")
	}
	// The canonical form keeps them too.
	c := g.canonical()
	g3 := mustParse(t, c)
	if !sameGraph(g, g3) || !hasStr(g3.Items["c"].Classes, "k") {
		t.Errorf("canonical lost classes:\n%s", c)
	}
}

func TestFreeEdgesAlwaysDrawn(t *testing.T) {
	g := mustParse(t, "flowchart TD\n a --> b\n")
	g.SetPos("a", 100, 100)
	g.SetPos("b", 110, 105) // on top of each other
	sc := Layout(g, nil)
	if len(sc.Routes) != 1 {
		t.Fatalf("%d routes", len(sc.Routes))
	}
	g.SetPos("b", 300, 120)
	r := Layout(g, nil).Routes[0]
	for i := 1; i < len(r.Pts); i++ {
		if r.Pts[i] == r.Pts[i-1] {
			t.Error("zero-length step")
		}
	}
}
