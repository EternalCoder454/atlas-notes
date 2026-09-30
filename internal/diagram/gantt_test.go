package diagram

import (
	"math"
	"strings"
	"testing"
	"time"
)

const ganttExample = "gantt\n  title Launch plan\n  dateFormat YYYY-MM-DD\n  excludes weekends\n  section Plan\n  Research :done, res, 2026-03-02, 5d\n  Write brief :done, brief, after res, 3d\n  section Build\n  Script episodes :active, scr, after brief, 2w\n  Record :crit, rec, after scr, 4d\n  Edit and ship :ed, after rec, 1w\n  Launch :milestone, after ed, 0d\n"

func mustGantt(t *testing.T, s string) *Gantt {
	t.Helper()
	g, err := ParseGantt(s)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return g
}

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func TestGanttParse(t *testing.T) {
	g := mustGantt(t, ganttExample)
	if g.Title != "Launch plan" || len(g.Sections) != 2 || len(g.Tasks) != 6 {
		t.Fatalf("got %q %d sections %d tasks", g.Title, len(g.Sections), len(g.Tasks))
	}
	by := map[string]*Task{}
	for _, tk := range g.Tasks {
		by[tk.ID] = tk
	}
	// 2026-03-02 is a Monday: five working days end on the next Monday.
	if !by["res"].End.Equal(day(2026, 3, 7)) {
		// Mon-Fri are 5 days; the end is Saturday 00:00, after Friday.
		t.Errorf("res ends %v", by["res"].End)
	}
	if !by["brief"].Start.Equal(by["res"].End) {
		t.Errorf("after: %v vs %v", by["brief"].Start, by["res"].End)
	}
	// Three working days from a Saturday start skip the weekend.
	if by["brief"].End.Weekday() == time.Sunday || !by["brief"].End.After(by["brief"].Start.AddDate(0, 0, 3)) {
		t.Errorf("weekend not skipped: %v to %v", by["brief"].Start, by["brief"].End)
	}
	if !by["res"].Done || !by["scr"].Active || !by["rec"].Crit {
		t.Error("tags lost")
	}
	last := g.Tasks[5]
	if !last.Milestone || !last.Start.Equal(last.End) || !last.Start.Equal(by["ed"].End) {
		t.Errorf("milestone %+v", last)
	}
}

func TestGanttForms(t *testing.T) {
	g := mustGantt(t, "gantt\ndateFormat DD/MM/YYYY\nA :a1, 01/02/2026, 2d\nB :02/02/2026, 03/02/2026\nC :3h\nD :d1, after a1 b1, 12h\nE :b1, 10/02/2026, 1w\n")
	if !g.Tasks[0].End.Equal(day(2026, 2, 3)) {
		t.Errorf("a1 ends %v", g.Tasks[0].End)
	}
	if !g.Tasks[2].Start.Equal(g.Tasks[1].End) || g.Tasks[2].End.Sub(g.Tasks[2].Start) != 3*time.Hour {
		t.Errorf("C = %v %v", g.Tasks[2].Start, g.Tasks[2].End)
	}
	// D follows both a1 and b1; b1 is later in the file and ends last.
	if !g.Tasks[3].Start.Equal(g.Tasks[4].End) {
		t.Errorf("D starts %v, want %v", g.Tasks[3].Start, g.Tasks[4].End)
	}
}

func TestGanttErrors(t *testing.T) {
	cases := map[string]string{
		"cycle":         "gantt\nA :a, after b, 1d\nB :b, after a, 1d\n",
		"unknown id":    "gantt\nA :a, after zz, 1d\n",
		"bad date":      "gantt\nA :2026-13-40, 1d\n",
		"end before":    "gantt\nA :2026-02-02, 2026-02-01\n",
		"no start":      "gantt\nA :3d\n",
		"dup id":        "gantt\nA :a, 2026-01-01, 1d\nB :a, 2026-01-01, 1d\n",
		"bad format":    "gantt\ndateFormat Do MMM\nA :2026-01-01, 1d\n",
		"no tasks":      "gantt\ntitle x\n",
		"excl all":      "gantt\nexcludes weekends, monday, tuesday, wednesday, thursday, friday\nA :2026-01-01, 3d\n",
		"huge duration": "gantt\nA :2026-01-01, 99999999y\n",
	}
	for name, src := range cases {
		if _, err := ParseGantt(src); err == nil {
			t.Errorf("%s: accepted", name)
		} else if _, ok := err.(*UnsupportedError); !ok {
			t.Errorf("%s: error type %T", name, err)
		}
	}
}

func TestGanttLayoutInvariants(t *testing.T) {
	g := mustGantt(t, ganttExample)
	Now = func() time.Time { return time.Date(2026, 3, 12, 9, 0, 0, 0, time.UTC) }
	defer func() { Now = time.Now }()
	for _, avail := range []float64{0, 500, 900, 1600} {
		sc := g.Scene(nil, avail)
		if avail > 0 && sc.W > avail+0.5 && sc.W > 12*90 {
			t.Errorf("avail %.0f: width %.0f", avail, sc.W)
		}
		lo := g.Tasks[0].Start
		for _, tk := range g.Tasks {
			if tk.Start.Before(lo) {
				lo = tk.Start
			}
		}
		// Every bar sits inside the chart, in row order, within its dates.
		var bars []Prim
		for _, p := range sc.Prims {
			if p.Kind == PrimBox && p.Radius == 4 && p.H == ganttBar || p.Shape == ShapeDiamond {
				bars = append(bars, p)
			}
		}
		if len(bars) != len(g.Tasks) {
			t.Fatalf("%d bars for %d tasks", len(bars), len(g.Tasks))
		}
		// x0 is the left of the earliest bar; pixels per day follow from any bar.
		minX := math.Inf(1)
		for _, b := range bars {
			minX = math.Min(minX, b.X+b.W/2*map[bool]float64{true: 1, false: 0}[b.Shape == ShapeDiamond])
		}
		var pxDay float64
		for i, b := range bars {
			if b.X < 0 || b.X+b.W > sc.W || b.Y < 0 || b.Y+b.H > sc.H {
				t.Errorf("bar %d outside the page: %+v", i, b)
			}
			if i > 0 && b.Y <= bars[i-1].Y {
				t.Errorf("bar %d not below the one before", i)
			}
			if d := g.Tasks[i].End.Sub(g.Tasks[i].Start).Hours() / 24; d >= 3 && b.Shape != ShapeDiamond {
				pxDay = b.W / d
			}
		}
		for i, tk := range g.Tasks {
			b := bars[i]
			if tk.Milestone {
				continue
			}
			wantX := minX + tk.Start.Sub(lo).Hours()/24*pxDay
			if math.Abs(b.X-wantX) > 0.51 {
				t.Errorf("avail %.0f: %s starts at %.1f, want %.1f", avail, tk.Name, b.X, wantX)
			}
		}
		// Text is on the page.
		for _, p := range sc.Prims {
			if p.Kind == PrimText && p.Left {
				w, _ := ApproxMeasure(p.Text, p.Size, p.Bold)
				if p.X < 0 || p.X+w > sc.W+0.5 {
					t.Errorf("avail %.0f: text %q at %.0f..%.0f of %.0f", avail, p.Text, p.X, p.X+w, sc.W)
				}
			}
		}
	}
}

func TestGanttTodayLine(t *testing.T) {
	g := mustGantt(t, ganttExample)
	Now = func() time.Time { return time.Date(2026, 3, 12, 9, 0, 0, 0, time.UTC) }
	defer func() { Now = time.Now }()
	count := func(g *Gantt) int {
		n := 0
		for _, p := range g.Scene(nil, 800).Prims {
			if p.Kind == PrimPath && p.Stroke == RoleAccent {
				n++
			}
		}
		return n
	}
	if count(g) != 1 {
		t.Error("no today line")
	}
	g.NoToday = true
	if count(g) != 0 {
		t.Error("today line with todayMarker off")
	}
	Now = func() time.Time { return time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC) }
	g.NoToday = false
	if count(g) != 0 {
		t.Error("today line outside the chart")
	}
}

func TestGanttDeterministicAndSVG(t *testing.T) {
	Now = func() time.Time { return time.Date(2026, 3, 12, 9, 0, 0, 0, time.UTC) }
	defer func() { Now = time.Now }()
	a := mustGantt(t, ganttExample).Scene(nil, 800).SVG(LightPalette)
	b := mustGantt(t, ganttExample).Scene(nil, 800).SVG(LightPalette)
	if a != b {
		t.Error("output differs between runs")
	}
	for _, want := range []string{"<svg", "Gantt chart", "Launch plan", "Record", "<polygon", "</svg>"} {
		if !strings.Contains(a, want) {
			t.Errorf("svg lacks %q", want)
		}
	}
	g := mustGantt(t, "gantt\ntitle A <b> & \"c\"\nX<y> :2026-01-01, 1d\n")
	if s := g.Scene(nil, 0).SVG(LightPalette); strings.Contains(s, "<y>") || strings.Contains(s, "<b>") {
		t.Error("unescaped text in svg")
	}
}

func TestGanttAxisFormat(t *testing.T) {
	if got := strftime("%Y-%m-%d %a %b %-d %H:%M %%", time.Date(2026, 3, 5, 7, 4, 0, 0, time.UTC)); got != "2026-03-05 Thu Mar 5 07:04 %" {
		t.Errorf("strftime = %q", got)
	}
	g := mustGantt(t, "gantt\naxisFormat %d/%m\nA :2026-03-01, 30d\n")
	s := g.Scene(nil, 900).SVG(LightPalette)
	if !strings.Contains(s, ">01/03<") && !strings.Contains(s, ">08/03<") {
		t.Errorf("axisFormat ignored: %s", s[:200])
	}
}

func TestGanttTooLarge(t *testing.T) {
	if _, err := ParseDocDrawable("gantt\nA :2000-01-01, 2100-01-01\n"); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Errorf("a century was accepted: %v", err)
	}
	if _, err := ParseDocDrawable(ganttExample); err != nil {
		t.Error(err)
	}
}

func TestPathologicalGantt(t *testing.T) {
	cases := map[string]string{
		"many tasks": "gantt\n" + strings.Repeat("A :2026-01-01, 1d\n", 5000),
		"chain":      "gantt\nA :a0, 2026-01-01, 1d\n" + strings.Repeat("B :after a0, 1d\n", 190),
		"long name":  "gantt\n" + strings.Repeat("x", 60000) + " :2026-01-01, 1d\n",
		"weekends":   "gantt\nexcludes weekends\nA :2026-01-01, 100000d\n",
		"after many": "gantt\nA :a, 2026-01-01, 1d\nB :after " + strings.Repeat("a ", 5000) + ", 1d\n",
		"sections":   "gantt\n" + strings.Repeat("section S\n", 5000),
		"hours":      "gantt\nA :2026-01-01, 2026-01-02\nB :2026-01-01, 1h\nC :2026-06-01, 1h\n",
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

func FuzzParseGantt(f *testing.F) {
	f.Add(ganttExample)
	f.Add("gantt\ndateFormat HH:mm\nA :a, 10:00, 2h\n")
	f.Add("gantt\nexcludes 2026-01-02, sunday\nA :2026-01-01, 3d\nB :milestone, after A, 0d\n")
	f.Fuzz(func(t *testing.T, s string) {
		if d, err := ParseDocDrawable(s); err == nil {
			d.Scene(nil, 600).SVG(LightPalette)
		}
	})
}
