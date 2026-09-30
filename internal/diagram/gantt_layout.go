package diagram

import (
	"fmt"
	"math"
	"strings"
	"time"
)

const (
	ganttPad      = 10.0
	ganttRow      = 28.0
	ganttBar      = 18.0
	ganttAxis     = 26.0
	ganttLabelMax = 150.0
	ganttNatural  = 820.0 // the page width when none is given
)

// strftime writes t by a strftime-style format: %Y %y %m %d %e %b %B %a %A %H
// %M %S and %%, with a - after the % to drop the padding.
func strftime(f string, t time.Time) string {
	var b strings.Builder
	for i := 0; i < len(f); i++ {
		if f[i] != '%' || i+1 >= len(f) {
			b.WriteByte(f[i])
			continue
		}
		i++
		trim := false
		if f[i] == '-' && i+1 < len(f) {
			trim = true
			i++
		}
		num := func(v, w int) {
			if trim {
				fmt.Fprintf(&b, "%d", v)
			} else {
				fmt.Fprintf(&b, "%0*d", w, v)
			}
		}
		switch f[i] {
		case 'Y':
			fmt.Fprintf(&b, "%d", t.Year())
		case 'y':
			num(t.Year()%100, 2)
		case 'm':
			num(int(t.Month()), 2)
		case 'd':
			num(t.Day(), 2)
		case 'e':
			fmt.Fprintf(&b, "%d", t.Day())
		case 'b':
			b.WriteString(t.Month().String()[:3])
		case 'B':
			b.WriteString(t.Month().String())
		case 'a':
			b.WriteString(t.Weekday().String()[:3])
		case 'A':
			b.WriteString(t.Weekday().String())
		case 'H':
			num(t.Hour(), 2)
		case 'M':
			num(t.Minute(), 2)
		case 'S':
			num(t.Second(), 2)
		case '%':
			b.WriteByte('%')
		default:
			b.WriteByte('%')
			b.WriteByte(f[i])
		}
	}
	return b.String()
}

// tickStep is a spacing for the axis: n units of hour, day, week, month or year.
type tickStep struct {
	unit byte
	n    int
}

var tickSteps = []tickStep{
	{'h', 1}, {'h', 3}, {'h', 6}, {'h', 12}, {'d', 1}, {'d', 2}, {'w', 1}, {'w', 2},
	{'M', 1}, {'M', 2}, {'M', 3}, {'M', 6}, {'y', 1}, {'y', 2}, {'y', 5}, {'y', 10}, {'y', 50}, {'y', 100},
}

func (s tickStep) format() string {
	switch s.unit {
	case 'h':
		return "%b %d %H:%M"
	case 'M':
		return "%b %Y"
	case 'y':
		return "%Y"
	}
	return "%b %d"
}

// ticks lists the tick times from lo to hi, aligned to the hour, the day, the
// Monday, the month or the year.
func (s tickStep) ticks(lo, hi time.Time) []time.Time {
	var t time.Time
	y, mo, d := lo.Date()
	switch s.unit {
	case 'h':
		t = time.Date(y, mo, d, lo.Hour()/s.n*s.n, 0, 0, 0, time.UTC)
	case 'd':
		t = time.Date(y, mo, d, 0, 0, 0, 0, time.UTC)
	case 'w':
		t = time.Date(y, mo, d, 0, 0, 0, 0, time.UTC)
		t = t.AddDate(0, 0, -((int(t.Weekday()) + 6) % 7))
	case 'M':
		t = time.Date(y, mo, 1, 0, 0, 0, 0, time.UTC)
	default:
		t = time.Date(y/s.n*s.n, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	var out []time.Time
	for ; !t.After(hi) && len(out) < 600; t = s.next(t) {
		if !t.Before(lo) {
			out = append(out, t)
		}
	}
	return out
}

func (s tickStep) next(t time.Time) time.Time {
	switch s.unit {
	case 'h':
		return t.Add(time.Duration(s.n) * time.Hour)
	case 'd':
		return t.AddDate(0, 0, s.n)
	case 'w':
		return t.AddDate(0, 0, 7*s.n)
	case 'M':
		return t.AddDate(0, s.n, 0)
	}
	return t.AddDate(s.n, 0, 0)
}

// Scene lays the chart out for a page avail wide (0 for a natural width). Past a
// least width per day the chart stops narrowing and is wider than the page.
func (g *Gantt) Scene(m Measure, avail float64) *Scene {
	if m == nil {
		m = ApproxMeasure
	}
	if avail <= 0 {
		avail = ganttNatural
	}
	sc := &Scene{Label: "Gantt chart"}
	lo, hi := g.Tasks[0].Start, g.Tasks[0].End
	for _, t := range g.Tasks {
		if t.Start.Before(lo) {
			lo = t.Start
		}
		if t.End.After(hi) {
			hi = t.End
		}
	}
	if !hi.After(lo) {
		hi = lo.Add(24 * time.Hour)
	}
	span := hi.Sub(lo).Hours() / 24
	// Room on the left for the section names.
	var labelW float64
	for _, s := range g.Sections {
		if s.Name != "" {
			w, _ := m(s.Name, headSize, true)
			labelW = math.Max(labelW, math.Min(w, ganttLabelMax))
		}
	}
	x0 := ganttPad
	if labelW > 0 {
		x0 += labelW + 14
	}
	chartW := math.Max(avail-x0-ganttPad, 200)
	minPx := 12.0
	if span > 400 {
		minPx = 2.5
	} else if span > 120 {
		minPx = 6
	}
	chartW = math.Max(chartW, span*minPx)
	pxDay := chartW / span
	xOf := func(t time.Time) float64 { return x0 + t.Sub(lo).Hours()/24*pxDay }
	x1 := x0 + chartW
	W := x1 + ganttPad

	y := ganttPad
	if g.Title != "" {
		_, th := m(g.Title, titleSize, true)
		sc.Prims = append(sc.Prims, Prim{Kind: PrimText, X: ganttPad, Y: y, Text: g.Title, Size: titleSize, Bold: true, Left: true, Fill: RoleFg})
		y += th + 8
	}
	axisY := y
	rowsY := axisY + ganttAxis
	rows := len(g.Tasks)
	H := rowsY + float64(rows)*ganttRow + ganttPad

	// Section bands and their names.
	var bands, shade, grid, axis, marks, bars, texts []Prim
	ry := rowsY
	for i, s := range g.Sections {
		if len(s.Tasks) == 0 {
			continue
		}
		h := float64(len(s.Tasks)) * ganttRow
		if i%2 == 0 {
			bands = append(bands, Prim{Kind: PrimBox, X: ganttPad, Y: ry, W: W - 2*ganttPad, H: h, Fill: RoleGroup})
		}
		if s.Name != "" {
			name := fitWidth(s.Name, headSize, true, m, labelW)
			_, th := m(name, headSize, true)
			texts = append(texts, Prim{Kind: PrimText, X: ganttPad + 4, Y: ry + (h-th)/2, Text: name, Size: headSize, Bold: true, Left: true, Fill: RoleDim})
		}
		ry += h
	}

	// Excluded days, shaded, when a day is wide enough to see.
	if g.anyExcluded() && pxDay >= 5 {
		day := time.Date(lo.Year(), lo.Month(), lo.Day(), 0, 0, 0, 0, time.UTC)
		for n := 0; day.Before(hi) && n < 4000; n++ {
			if g.excluded(day) {
				a, b := math.Max(xOf(day), x0), math.Min(xOf(day.AddDate(0, 0, 1)), x1)
				if b > a {
					shade = append(shade, Prim{Kind: PrimBox, X: a, Y: rowsY, W: b - a, H: H - ganttPad - rowsY, Fill: RoleBox})
				}
			}
			day = day.AddDate(0, 0, 1)
		}
	}

	// The axis: the finest step whose labels fit side by side.
	step, found := tickSteps[len(tickSteps)-1], false
	for _, s := range tickSteps {
		ts := s.ticks(lo, hi)
		if len(ts) > 400 {
			continue
		}
		f := g.AxisFormat
		if f == "" {
			f = s.format()
		}
		var wide float64
		for _, t := range ts {
			w, _ := m(strftime(f, t), labelSize, false)
			wide = math.Max(wide, w)
		}
		if len(ts) == 0 {
			continue
		}
		step, found = s, true
		// Pixels between two ticks of this step.
		var px float64
		if len(ts) > 1 {
			px = xOf(ts[1]) - xOf(ts[0])
		} else {
			px = chartW
		}
		if px >= wide+16 {
			break
		}
	}
	if !found { // a span too short for any step has no tick: the start is one
		step = tickSteps[0]
	}
	f := g.AxisFormat
	if f == "" {
		f = step.format()
	}
	ticks := step.ticks(lo, hi)
	if len(ticks) == 0 {
		ticks = []time.Time{lo}
	}
	_, lh := m("0", labelSize, false)
	for _, t := range ticks {
		x := xOf(t)
		grid = append(grid, Prim{Kind: PrimPath, Pts: []Pt{{x, axisY + ganttAxis - 4}, {x, H - ganttPad}}, Stroke: RoleGroupStroke, StrokeW: 1})
		label := strftime(f, t)
		if w, _ := m(label, labelSize, false); x+4+w <= W-ganttPad/2 {
			axis = append(axis, Prim{Kind: PrimText, X: x + 4, Y: axisY + (ganttAxis-4-lh)/2, Text: label, Size: labelSize, Left: true, Fill: RoleDim})
		}
	}
	axis = append(axis, Prim{Kind: PrimPath, Pts: []Pt{{x0, axisY + ganttAxis - 4}, {x1, axisY + ganttAxis - 4}}, Stroke: RoleBoxStroke, StrokeW: 1})

	// The tasks.
	for i, t := range g.Tasks {
		cy := rowsY + float64(i)*ganttRow + ganttRow/2
		a, b := xOf(t.Start), xOf(t.End)
		_, th := m(t.Name, labelSize, false)
		fg := RoleFg
		if t.Done {
			fg = RoleDim
		}
		var left, right float64 // the bar's extent, for the name to go beside
		if t.Milestone {
			c := (a + b) / 2
			const r = 9.0
			fill, stroke := RoleAccentStrong, RoleAccent
			if t.Crit {
				fill, stroke = RoleErrorTint, RoleError
			}
			if t.Done {
				fill, stroke = RoleBox, RoleBoxStroke
			}
			bars = append(bars, Prim{Kind: PrimBox, X: c - r, Y: cy - r, W: 2 * r, H: 2 * r, Shape: ShapeDiamond, Fill: fill, Stroke: stroke, StrokeW: 1.5})
			left, right = c-r, c+r
		} else {
			w := math.Max(b-a, 3)
			fill, stroke, sw := RoleAccentTint, RoleAccent, 1.0
			switch {
			case t.Done && t.Crit:
				fill, stroke, sw = RoleBox, RoleError, 1
			case t.Done:
				fill, stroke = RoleBox, RoleBoxStroke
			case t.Crit && t.Active:
				fill, stroke, sw = RoleErrorTint, RoleError, 2
			case t.Crit:
				fill, stroke = RoleErrorTint, RoleError
			case t.Active:
				fill, sw = RoleAccentStrong, 2
			}
			bars = append(bars, Prim{Kind: PrimBox, X: a, Y: cy - ganttBar/2, W: w, H: ganttBar, Radius: 4, Fill: fill, Stroke: stroke, StrokeW: sw})
			left, right = a, a+w
		}
		name := t.Name
		tw, _ := m(name, labelSize, false)
		ty := cy - th/2
		switch {
		case !t.Milestone && tw+12 <= right-left:
			texts = append(texts, Prim{Kind: PrimText, X: left + 6, Y: ty, Text: name, Size: labelSize, Left: true, Fill: fg})
		case right+6+tw <= W-ganttPad:
			texts = append(texts, Prim{Kind: PrimText, X: right + 6, Y: ty, Text: name, Size: labelSize, Left: true, Fill: fg})
		case left-6-tw >= x0:
			texts = append(texts, Prim{Kind: PrimText, X: left - 6 - tw, Y: ty, Text: name, Size: labelSize, Left: true, Fill: fg})
		default:
			room := math.Max(W-ganttPad-(right+6), left-6-x0)
			name = fitWidth(name, labelSize, false, m, room)
			tw, _ = m(name, labelSize, false)
			tx := right + 6
			if W-ganttPad-(right+6) < left-6-x0 {
				tx = left - 6 - tw
			}
			texts = append(texts, Prim{Kind: PrimText, X: tx, Y: ty, Text: name, Size: labelSize, Left: true, Fill: fg})
		}
	}

	// Today.
	if !g.NoToday {
		n := Now()
		today := time.Date(n.Year(), n.Month(), n.Day(), n.Hour(), n.Minute(), 0, 0, time.UTC)
		if !today.Before(lo) && !today.After(hi) {
			x := xOf(today)
			marks = append(marks, Prim{Kind: PrimPath, Pts: []Pt{{x, axisY + ganttAxis - 4}, {x, H - ganttPad}}, Stroke: RoleAccent, StrokeW: 2})
		}
	}

	sc.W, sc.H = W, H
	for _, p := range [][]Prim{bands, shade, grid, axis, bars, marks, texts} {
		sc.Prims = append(sc.Prims, p...)
	}
	// A screen reader hears the chart's name and its tasks.
	var names []string
	for _, t := range g.Tasks {
		names = append(names, t.Name)
	}
	d := fmt.Sprintf("%d tasks from %s to %s: %s", len(g.Tasks), lo.Format("2 Jan 2006"), hi.Format("2 Jan 2006"), strings.Join(names, ", "))
	if g.Title != "" {
		d = g.Title + ", " + d
	}
	sc.Desc = truncate(d, 300)
	return sc
}
