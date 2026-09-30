package diagram

import (
	"math"
	"strings"
	"testing"
	"time"
)

const seqExample = "sequenceDiagram\n  title Weekly flow\n  autonumber\n  participant P as Program\n  actor H as Host\n  participant Pr as Producer\n  participant C as Clipper\n  P->>+H: Emails the drafts and sources\n  H-->>-P: Replies with approvals\n  Note over P,H: Sunday night\n  alt Approved\n    H->>Pr: Sends the recording\n    Pr->>Pr: Edits the video\n    Pr-)C: Hands over the final cut\n  else Needs changes\n    H-xP: Asks again\n  end\n  loop Every week\n    opt Sponsors\n      rect rgb(200,200,255)\n        C->>C: Cuts clips\n      end\n    end\n  end\n  Note left of P: Runs on a timer\n  Note right of C: Posts to ads\n"

func mustSeq(t *testing.T, s string) *Sequence {
	t.Helper()
	q, err := ParseSequence(s)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return q
}

func TestSeqParse(t *testing.T) {
	q := mustSeq(t, seqExample)
	if len(q.Parts) != 4 || !q.Parts[1].Actor || q.Parts[0].Label != "Program" || q.Title != "Weekly flow" || !q.AutoNumber {
		t.Fatalf("parts %+v", q.Parts)
	}
	m := q.Events[0]
	if m.Kind != evMsg || m.A != 0 || m.B != 1 || m.Head != '>' || m.Dashed || !m.Activate {
		t.Errorf("first message %+v", m)
	}
	r := q.Events[1]
	if !r.Dashed || !r.Deactivate || r.A != 1 {
		t.Errorf("reply %+v", r)
	}
	alt := q.Events[3].Block
	if alt.Kind != "alt" || len(alt.Parts) != 2 || alt.Parts[0].Label != "Approved" || alt.Parts[1].Label != "Needs changes" {
		t.Errorf("alt %+v", alt)
	}
}

func TestSeqArrows(t *testing.T) {
	q := mustSeq(t, "sequenceDiagram\nA->B: a\nA-->B: b\nA->>B: c\nA-->>B: d\nA-xB: e\nA--xB: f\nA-)B: g\nA--)B: h\nA<<->>B: i\n")
	want := []struct {
		dashed bool
		head   byte
		both   bool
	}{{false, 0, false}, {true, 0, false}, {false, '>', false}, {true, '>', false}, {false, 'x', false}, {true, 'x', false}, {false, ')', false}, {true, ')', false}, {false, '>', true}}
	for i, w := range want {
		e := q.Events[i]
		if e.Dashed != w.dashed || e.Head != w.head || e.Both != w.both || e.Text != string(rune('a'+i)) {
			t.Errorf("%d: %+v", i, e)
		}
	}
}

func TestSeqErrors(t *testing.T) {
	cases := map[string]string{
		"unclosed":      "sequenceDiagram\nA->>B: x\nloop x\nA->>B: y\n",
		"stray end":     "sequenceDiagram\nA->>B: x\nend\n",
		"stray else":    "sequenceDiagram\nA->>B: x\nelse y\n",
		"deactivate":    "sequenceDiagram\nA->>B: x\ndeactivate A\n",
		"bad line":      "sequenceDiagram\nA->>B: x\nwhat is this\n",
		"box":           "sequenceDiagram\nbox Blue\nparticipant A\nend\n",
		"create":        "sequenceDiagram\ncreate participant A\n",
		"empty":         "sequenceDiagram\n",
		"note two left": "sequenceDiagram\nNote left of A,B: x\n",
		"deep":          "sequenceDiagram\n" + strings.Repeat("loop x\n", 10),
	}
	for name, src := range cases {
		if _, err := ParseSequence(src); err == nil {
			t.Errorf("%s: accepted", name)
		} else if _, ok := err.(*UnsupportedError); !ok {
			t.Errorf("%s: %T", name, err)
		}
	}
}

// boxes gives the prims that are frames (an outline and no fill), in order.
func frameBoxes(sc *Scene) []Prim {
	var out []Prim
	for _, p := range sc.Prims {
		if p.Kind == PrimBox && p.Fill == RoleNone && p.Stroke == RoleLine {
			out = append(out, p)
		}
	}
	return out
}

func TestSeqLayoutInvariants(t *testing.T) {
	q := mustSeq(t, seqExample)
	for _, avail := range []float64{0, 500} {
		sc := q.Scene(nil, avail)
		if sc.Label != "Sequence diagram" {
			t.Error("label")
		}
		// Everything is on the page.
		for _, p := range sc.Prims {
			if p.Kind == PrimBox && (p.X < -0.01 || p.X+p.W > sc.W+0.01 || p.Y < -0.01 || p.Y+p.H > sc.H+0.01) {
				t.Errorf("box off the page: %+v of %.0f x %.0f", p, sc.W, sc.H)
			}
			for _, pt := range p.Pts {
				if pt.X < -0.01 || pt.X > sc.W+0.01 || pt.Y < -0.01 || pt.Y > sc.H+0.01 {
					t.Errorf("point off the page: %+v", pt)
				}
			}
		}
		// Message lines run top to bottom in the order written: the horizontal
		// two-point paths with a dash or not, between lifelines.
		var ys []float64
		for _, p := range sc.Prims {
			if p.Kind == PrimPath && len(p.Pts) == 2 && p.Pts[0].Y == p.Pts[1].Y && p.Stroke == RoleLine && p.StrokeW == 1.5 && p.Pts[0].X != p.Pts[1].X && p.Pts[0].Y > 80 && p.Pts[0].Y < sc.H-80 {
				ys = append(ys, p.Pts[0].Y)
			}
		}
		if len(ys) < 5 {
			t.Fatalf("only %d message lines", len(ys))
		}
		for i := 1; i < len(ys); i++ {
			if ys[i] <= ys[i-1] {
				t.Errorf("message %d at %.1f is not below %.1f", i, ys[i], ys[i-1])
			}
		}
		// Each frame encloses the lines drawn within its vertical range, and
		// frames inside others are inside in x.
		fr := frameBoxes(sc)
		if len(fr) != 4 { // alt, loop, opt, and the outline of the tab? see below
			// alt, loop, opt and three tabs (tabs have a fill); rect has no outline.
			t.Logf("frames: %d", len(fr))
		}
		for _, f := range fr {
			for _, p := range sc.Prims {
				if p.Kind == PrimPath && len(p.Pts) == 2 && p.Stroke == RoleLine && p.StrokeW == 1.5 && p.Pts[0].Y > f.Y && p.Pts[0].Y < f.Y+f.H && p.Pts[0].Y == p.Pts[1].Y {
					lo, hi := math.Min(p.Pts[0].X, p.Pts[1].X), math.Max(p.Pts[0].X, p.Pts[1].X)
					if lo < f.X || hi > f.X+f.W {
						t.Errorf("a message at y=%.0f (%.0f..%.0f) leaves its frame %.0f..%.0f", p.Pts[0].Y, lo, hi, f.X, f.X+f.W)
					}
				}
			}
		}
		for i, a := range fr {
			for j, b := range fr {
				if i != j && b.Y >= a.Y && b.Y+b.H <= a.Y+a.H && (b.X < a.X || b.X+b.W > a.X+a.W) {
					t.Errorf("a nested frame sticks out: %+v in %+v", b, a)
				}
			}
		}
		// Boxes of participants do not overlap one another.
		var heads []Prim
		for _, p := range sc.Prims {
			if p.Kind == PrimBox && p.Fill == RoleBox && p.H == seqBoxH {
				heads = append(heads, p)
			}
		}
		for i := range heads {
			for j := i + 1; j < len(heads); j++ {
				a, b := heads[i], heads[j]
				if a.Y == b.Y && a.X < b.X+b.W && b.X < a.X+a.W {
					t.Errorf("participant boxes overlap: %+v %+v", a, b)
				}
			}
		}
	}
}

func TestSeqNotesAvoidLifelines(t *testing.T) {
	q := mustSeq(t, "sequenceDiagram\nparticipant A\nparticipant B\nparticipant C\nNote left of B: a fairly long note that wraps onto several lines of text here\nNote right of B: another fairly long note that also has to wrap around\nNote over B: centre\n")
	sc := q.Scene(nil, 0)
	var lifeX []float64
	for _, p := range sc.Prims {
		if p.Kind == PrimPath && p.Dash && len(p.Pts) == 2 && p.Pts[0].X == p.Pts[1].X {
			lifeX = append(lifeX, p.Pts[0].X)
		}
	}
	if len(lifeX) != 3 {
		t.Fatalf("lifelines = %d", len(lifeX))
	}
	for _, p := range sc.Prims {
		if p.Kind == PrimBox && p.Fill == RoleAccentTint && p.Stroke == RoleAccent {
			for _, x := range []float64{lifeX[0], lifeX[2]} {
				if x > p.X && x < p.X+p.W {
					t.Errorf("a note %+v covers the lifeline at %.0f", p, x)
				}
			}
		}
	}
}

func TestSeqActivationBars(t *testing.T) {
	q := mustSeq(t, "sequenceDiagram\nA->>+B: hi\nB->>+B: again\nB-->>-B: inner\nB-->>-A: bye\n")
	sc := q.Scene(nil, 0)
	var bars []Prim
	for _, p := range sc.Prims {
		if p.Kind == PrimBox && p.W == seqBarW {
			bars = append(bars, p)
		}
	}
	if len(bars) != 2 {
		t.Fatalf("bars = %d", len(bars))
	}
	outer, inner := bars[1], bars[0]
	if inner.Y < outer.Y || inner.Y+inner.H > outer.Y+outer.H+0.01 || inner.X <= outer.X {
		t.Errorf("inner bar %+v not inside outer %+v", inner, outer)
	}
}

func TestSeqDeterministicAndSVG(t *testing.T) {
	a := mustSeq(t, seqExample).Scene(nil, 700).SVG(LightPalette)
	b := mustSeq(t, seqExample).Scene(nil, 700).SVG(LightPalette)
	if a != b {
		t.Error("output differs between runs")
	}
	for _, want := range []string{"<svg", "Sequence diagram", "Emails the drafts", "stroke-dasharray", "</svg>"} {
		if !strings.Contains(a, want) {
			t.Errorf("svg lacks %q", want)
		}
	}
	s := mustSeq(t, "sequenceDiagram\nA->>B: <script>alert(\"x\")</script> & more\n").Scene(nil, 0).SVG(LightPalette)
	if strings.Contains(s, "<script>") {
		t.Error("unescaped text in svg")
	}
}

func TestSeqTooLarge(t *testing.T) {
	var b strings.Builder
	b.WriteString("sequenceDiagram\n")
	for i := 0; i < 245; i++ {
		b.WriteString("A->>B: a message that wraps because it is long enough to need two lines of text here for sure yes\nNote over A: n\n")
	}
	if _, err := ParseDocDrawable(b.String()); err == nil {
		t.Error("a huge diagram was accepted")
	}
	if _, err := ParseDocDrawable(seqExample); err != nil {
		t.Error(err)
	}
}

func TestPathologicalSeq(t *testing.T) {
	cases := map[string]string{
		"many messages": "sequenceDiagram\n" + strings.Repeat("A->>B: x\n", 50000),
		"many parts": "sequenceDiagram\n" + strings.Repeat("participant P\nA->>B: x\n", 20) + func() string {
			var b strings.Builder
			for i := 0; i < 5000; i++ {
				b.WriteString("p" + strings.Repeat("x", i%20) + string(rune('a'+i%26)) + "->>q: x\n")
			}
			return b.String()
		}(),
		"long text": "sequenceDiagram\nA->>B: " + strings.Repeat("word ", 20000) + "\n",
		"long word": "sequenceDiagram\nA->>B: " + strings.Repeat("x", 60000) + "\n",
		"deep":      "sequenceDiagram\n" + strings.Repeat("loop x\n", 1000),
		"arrows":    "sequenceDiagram\nA" + strings.Repeat("->>", 20000) + "B: x\n",
		"activate":  "sequenceDiagram\n" + strings.Repeat("activate A\n", 240),
	}
	for name, src := range cases {
		done := make(chan struct{})
		go func() {
			defer close(done)
			if d, err := ParseDocDrawable(src); err == nil {
				d.Scene(nil, 700).SVG(LightPalette)
			}
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: took more than 5 seconds", name)
		}
	}
}

func TestKind(t *testing.T) {
	for src, want := range map[string]string{"gantt\n": "gantt", "\n%% c\nsequenceDiagram": "sequence", "graph TD": "flowchart", "flowchart LR;": "flowchart", "pie\n": "", "": ""} {
		if Kind(src) != want {
			t.Errorf("Kind(%q) = %q", src, Kind(src))
		}
	}
	if _, err := ParseDoc("pie\n\"a\": 1"); err == nil || !strings.Contains(err.Error(), "pie") {
		t.Errorf("pie: %v", err)
	}
}

func FuzzParseSequence(f *testing.F) {
	f.Add(seqExample)
	f.Add("sequenceDiagram\nA->>+B: x\nB-->>-A: y\nloop z\nNote over A,B: n\nend\n")
	f.Add("sequenceDiagram\nalt a\nA-xB: q\nelse b\nrect rgb(1,2,3)\nA--)A: s\nend\nend\n")
	f.Fuzz(func(t *testing.T, s string) {
		if d, err := ParseDocDrawable(s); err == nil {
			d.Scene(nil, 600).SVG(LightPalette)
		}
	})
}

func TestSeqFixes(t *testing.T) {
	if _, err := ParseSequence("sequenceDiagram\n" + strings.Repeat("activate A\n", 9)); err == nil || !strings.Contains(err.Error(), "activations") {
		t.Errorf("deep activation: %v", err)
	}
	if _, err := ParseSequence("sequenceDiagram\n" + strings.Repeat("A->>+B: x\n", 9)); err == nil {
		t.Error("deep activation by message accepted")
	}
	// Separators count as steps.
	src := "sequenceDiagram\nalt a\n" + strings.Repeat("else b\n", maxSeqEvents) + "end\n"
	if _, err := ParseSequence(src); err == nil || !strings.Contains(err.Error(), "too many") {
		t.Errorf("separators uncounted: %v", err)
	}
	// An error is a nil Doc, not a nil pointer inside one.
	for _, s := range []string{"gantt\n", "sequenceDiagram\n"} {
		if d, err := ParseDoc(s); err == nil || d != nil {
			t.Errorf("%q: doc %v err %v", s, d, err)
		}
	}
	// Eight bars deep still keep arrows between their lifelines.
	q := mustSeq(t, "sequenceDiagram\n"+strings.Repeat("A->>+B: x\n", 8)+"B->>A: y\n")
	sc := q.Scene(nil, 0)
	var ax, bx float64
	for _, p := range sc.Prims {
		if p.Kind == PrimPath && p.Dash && len(p.Pts) == 2 && p.Pts[0].X == p.Pts[1].X {
			if ax == 0 {
				ax = p.Pts[0].X
			} else {
				bx = p.Pts[0].X
			}
		}
	}
	for _, p := range sc.Prims {
		if p.Kind == PrimPath && len(p.Pts) == 2 && p.StrokeW == 1.5 && p.Pts[0].Y == p.Pts[1].Y && p.Pts[0].Y > 80 {
			if math.Min(p.Pts[0].X, p.Pts[1].X) < ax-1 || math.Max(p.Pts[0].X, p.Pts[1].X) > bx+seqBarW*3 {
				t.Errorf("arrow %v leaves its lifelines %.0f..%.0f", p.Pts, ax, bx)
			}
		}
	}
}
