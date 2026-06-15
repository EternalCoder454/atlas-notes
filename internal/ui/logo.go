package ui

import (
	"math"
	"time"

	"github.com/diamondburned/gotk4/pkg/cairo"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// logoOrb is the assistant's animated mark: a glowing nucleus that drifts and
// morphs gently when idle, then swells and brightens with faster ripples while
// the model is generating. It is drawn with Cairo on the widget frame clock, so
// it costs nothing while the panel is hidden — AddTickCallback only fires while
// the widget is mapped.
type logoOrb struct {
	*gtk.DrawingArea
	start  time.Time
	last   time.Time
	active float64 // eased 0 (idle) .. 1 (generating)
	target float64
}

func newLogoOrb() *logoOrb {
	o := &logoOrb{DrawingArea: gtk.NewDrawingArea(), start: time.Now(), last: time.Now()}
	o.SetContentWidth(110)
	o.SetContentHeight(110)
	o.SetHAlign(gtk.AlignCenter)
	o.SetDrawFunc(o.draw)
	o.AddTickCallback(o.tick)
	return o
}

// setActive aims the orb toward idle (false) or generating (true); the
// transition is eased in tick so the glow rises and falls smoothly.
func (o *logoOrb) setActive(busy bool) {
	if busy {
		o.target = 1
	} else {
		o.target = 0
	}
}

func (o *logoOrb) tick(_ gtk.Widgetter, _ gdk.FrameClocker) bool {
	now := time.Now()
	// ~22 fps while idle (the drift is gentle), ~33 fps while generating or
	// easing between states, where smoothness matters more.
	interval := 45 * time.Millisecond
	if o.active > 0.02 || o.target > 0.02 {
		interval = 28 * time.Millisecond
	}
	if now.Sub(o.last) < interval {
		return true
	}
	dt := now.Sub(o.last).Seconds()
	o.last = now
	o.active += (o.target - o.active) * math.Min(1, dt*6) // ease ~0.2s
	o.QueueDraw()
	return true
}

func (o *logoOrb) draw(_ *gtk.DrawingArea, cr *cairo.Context, w, h int) {
	s := math.Min(float64(w), float64(h))
	if s <= 0 {
		return
	}
	cx, cy := float64(w)/2, float64(h)/2
	t := time.Since(o.start).Seconds()
	a := o.active // 0 idle .. 1 generating

	R := s * 0.34     // outer ring radius
	coreR := s * 0.16 // base nucleus radius

	pr, pg, pb := 0.62, 0.34, 0.90 // orb purple
	lr, lg, lb := 0.86, 0.74, 1.0  // light core

	// Halo: concentric translucent fills fading outward (the ambient glow).
	const haloN = 9
	for i := haloN; i >= 1; i-- {
		f := float64(i) / haloN
		cr.SetSourceRGBA(pr, pg, pb, (0.05+0.06*a)*(1-f*0.85))
		cr.Arc(cx, cy, coreR+(R*1.15-coreR)*f, 0, 2*math.Pi)
		cr.Fill()
	}

	// Ripples: rings expanding out and fading (faster/brighter when generating).
	const nRip = 3
	speed := 0.16 + 0.30*a
	cr.SetLineWidth(s * 0.012)
	for k := 0; k < nRip; k++ {
		frac := math.Mod(t*speed+float64(k)/nRip, 1)
		cr.SetSourceRGBA(pr, pg, pb, (1-frac)*(0.16+0.20*a))
		cr.Arc(cx, cy, R*(0.85+0.45*frac), 0, 2*math.Pi)
		cr.Stroke()
	}

	// Outer ring, breathing slightly.
	cr.SetSourceRGBA(pr, pg, pb, 0.50+0.32*a)
	cr.SetLineWidth(s * 0.022)
	cr.Arc(cx, cy, R*(1+0.015*math.Sin(t*1.1)), 0, 2*math.Pi)
	cr.Stroke()

	// Nucleus: a wobbling blob — subtle when idle, lively while generating.
	amp := 0.10 + 0.22*a
	pulse := 1 + (0.04+0.10*a)*math.Sin(t*(1.6+1.0*a))
	const pts = 40
	for i := 0; i <= pts; i++ {
		ang := 2 * math.Pi * float64(i) / pts
		wob := 0.55*math.Sin(3*ang+t*1.3) + 0.30*math.Sin(5*ang-t*0.9) + 0.15*math.Sin(2*ang+t*1.9)
		rr := coreR * pulse * (1 + amp*wob*0.5)
		x, y := cx+rr*math.Cos(ang), cy+rr*math.Sin(ang)
		if i == 0 {
			cr.MoveTo(x, y)
		} else {
			cr.LineTo(x, y)
		}
	}
	cr.ClosePath()
	cr.SetSourceRGBA(lr, lg, lb, 0.45+0.20*a)
	cr.Fill()

	// Bright inner glow that drifts a touch (the "thing in the middle" moving).
	hx := cx + coreR*0.12*math.Cos(t*0.8)
	hy := cy + coreR*0.12*math.Sin(t*1.1)
	for i := 3; i >= 1; i-- {
		f := float64(i) / 3
		cr.SetSourceRGBA(1, 1, 1, (0.12+0.18*a)*(1-0.55*f))
		cr.Arc(hx, hy, coreR*(0.35+0.5*f), 0, 2*math.Pi)
		cr.Fill()
	}
	cr.SetSourceRGBA(1, 1, 1, 0.92)
	cr.Arc(hx, hy, coreR*0.32, 0, 2*math.Pi)
	cr.Fill()
}
