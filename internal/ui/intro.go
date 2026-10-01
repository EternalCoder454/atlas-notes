package ui

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/cairo"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/graphene"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
	"github.com/diamondburned/gotk4/pkg/pangocairo"
)

// The Atlas intro: the mark draws itself the way assets/atlas-mark-animated.svg
// does (left leg rising, right leg falling from the fold, the crossbar sliding
// in), then moves aside while the product's name slides out from under its
// right leg, and the whole thing fades into the window.
//
// It is drawn with Cairo rather than by loading the animated SVG: GTK's image
// loaders draw an SVG's first frame and never move it. The timings and curves
// below are the ones in that file, so the two stay the same animation.
//
// The file is shared with Atlas Monitor; a change to one belongs in the other.

// introDone is when the intro has finished fading, in seconds from its start.
const (
	introSlideAt = 1.10 // the mark starts moving aside for the name
	introSlide   = 0.75
	introFadeAt  = 2.70 // the intro starts fading into the window; Monitor's is 2.55, Notes holds the name a little longer
	introFade    = 0.40
	introSkip    = 0.22 // how long the fade takes when a click or key cuts it short
)

// introMarkHeight is the mark's height, in logical pixels, where the window
// has room for it.
var introMarkHeight = 148.0

// IntroEnabled reports whether the desktop allows animation at all. Someone
// who has turned animations off is not shown one at startup either.
func IntroEnabled() bool {
	st := gtk.SettingsGetDefault()
	if st == nil {
		return false
	}
	on, ok := st.ObjectProperty("gtk-enable-animations").(bool)
	return !ok || on
}

// Intro is the overlay that plays the intro over the window's content.
type Intro struct {
	overlay *gtk.Overlay
	child   gtk.Widgetter
	cover   *gtk.Box // the intro's background, over the whole window
	area    *gtk.DrawingArea
	keys    *gtk.EventControllerKey
	product string
	onDone  func()

	start   int64   // frame clock time of the first frame, µs
	t       float64 // seconds since then
	fadeAt  float64
	fadeDur float64
	tickID  uint
	done    bool
	root    *gtk.Root // the window, while its own background is off
	origin  *graphene.Point

	glow glowCache

	family string        // the desktop's font, for the product's name
	cap    float64       // its capitals' height at 1000 units
	name   *pango.Layout // the product's name, set once
}

// NewIntro wraps child in an overlay that plays the intro over it, with product
// ("Monitor", "Notes") as the name beside the mark. Use the returned widget in
// child's place. onDone, if set, runs once the intro has gone.
func NewIntro(child gtk.Widgetter, product string, onDone func()) *Intro {
	in := &Intro{
		overlay: gtk.NewOverlay(),
		child:   child,
		cover:   gtk.NewBox(gtk.OrientationVertical, 0),
		area:    gtk.NewDrawingArea(),
		product: product,
		onDone:  onDone,
		fadeAt:  introFadeAt,
		fadeDur: introFade,
		origin:  graphene.NewPointAlloc(),
	}
	in.overlay.SetChild(child)
	// The content is not drawn at all until the intro fades: at opacity 0 GTK
	// skips it, so the window's first frames cost only the mark.
	gtk.BaseWidget(child).SetOpacity(0)

	// The background covers the window; the mark is drawn only across the
	// band it can reach. A drawing area is a picture of its whole size on
	// every frame (an offscreen copy in the software renderer, a texture to
	// upload in the GPU ones), and the mark is a fifth of the window's height.
	in.cover.AddCSSClass("atlas-intro")
	in.area.SetHExpand(true)
	in.area.SetVExpand(true)
	in.area.SetVAlign(gtk.AlignCenter)
	in.area.SetDrawFunc(in.draw)
	in.cover.Append(in.area)
	in.overlay.AddOverlay(in.cover)

	// A click anywhere, or any key, cuts it short. The key still goes on to
	// whatever has focus: someone who starts typing straight away should not
	// lose the first letter.
	click := gtk.NewGestureClick()
	click.ConnectPressed(func(int, float64, float64) { in.Skip() })
	in.cover.AddController(click)
	in.keys = gtk.NewEventControllerKey()
	in.keys.SetPropagationPhase(gtk.PhaseCapture)
	in.keys.ConnectKeyPressed(func(uint, uint, gdk.ModifierType) bool {
		in.Skip()
		return false
	})
	in.overlay.AddController(in.keys)

	in.tickID = in.area.AddTickCallback(in.tick)
	return in
}

// Widget is what goes in the window in child's place.
func (in *Intro) Widget() gtk.Widgetter { return in.overlay }

// Skip starts the fade now, if it has not started already.
func (in *Intro) Skip() {
	if in.done || in.t >= in.fadeAt {
		return
	}
	in.fadeAt = in.t
	in.fadeDur = introSkip
}

func (in *Intro) tick(_ gtk.Widgetter, clock gdk.FrameClocker) bool {
	now := gdk.BaseFrameClock(clock).FrameTime()
	if in.start == 0 {
		in.start = now
	}
	in.t = float64(now-in.start) / 1e6

	if bh := bandHeight(in.cover.Height()); bh != in.area.ContentHeight() {
		in.area.SetContentHeight(bh)
	}
	if in.root == nil && in.t < in.fadeAt {
		// The intro's background covers the window's, which would otherwise
		// be painted under it on every frame. The stylesheet makes a window
		// with this class paint none until the intro starts to fade.
		if r := in.overlay.Root(); r != nil {
			r.AddCSSClass("atlas-intro-playing")
			in.root = r
		}
	}

	if in.t >= in.fadeAt {
		in.uncoverWindow()
		// Fading, it lets clicks through to the window it is uncovering.
		in.cover.SetCanTarget(false)
		f := tween(in.t, in.fadeAt, in.fadeDur, 0, 1, splineStandard)
		in.cover.SetOpacity(1 - f)
		gtk.BaseWidget(in.child).SetOpacity(f)
		if f >= 1 {
			in.finish()
			return false
		}
	}
	in.area.QueueDraw()
	return true
}

func (in *Intro) finish() {
	if in.done {
		return
	}
	in.done = true
	in.tickID = 0
	in.uncoverWindow()
	gtk.BaseWidget(in.child).SetOpacity(1)
	in.overlay.RemoveOverlay(in.cover)
	in.overlay.RemoveController(in.keys)
	in.glow = glowCache{}
	in.name = nil
	if in.onDone != nil {
		in.onDone()
	}
}

// uncoverWindow gives the window its own background back.
func (in *Intro) uncoverWindow() {
	if in.root != nil {
		in.root.RemoveCSSClass("atlas-intro-playing")
		in.root = nil
	}
}

// --- The mark ---------------------------------------------------------------

// The mark is drawn in the SVG's own units: a 1024 square, the mark itself
// inside it from x 136 to 888 and y 168 to 872.
const (
	markLeft, markRight = 136.0, 888.0
	markTop, markBottom = 168.0, 872.0
	markMidY            = 520.0
)

// The mark's paths, from assets' atlas-mark.svg.
var (
	pathLegLeft         = mustPath(legLeftData)
	pathLegRight        = mustPath(legRightData)
	pathCrossbar        = mustPath("M346.7 572L677.3 572L732.5 707L291.5 707Z")
	pathCrossbarShadow  = mustPath("M545.3 572L591.3 572L646.5 707L600.5 707Z")
	pathCrossbarHilite  = mustPath("M430.7 572.5L593.3 572.5L593.3 574.5L430.7 574.5Z")
	pathApexShadow      = mustPath("M592.6 186L607.4 186L630 241.3L542 456.4L527.1 420.1Q512 383.1 527.1 346.1L544.7 303.1Z")
	pathApexHilite      = mustPath("M456 168.5L586 168.5L586 170.5L456 170.5Z")
	pathWordmark        = mustPath(wordmarkData)
	pathCrossbarClipRef = mustPath("M-1400 0L460 0L878.9 1024L-1400 1024Z")
)

type stop struct {
	at         float64
	r, g, b, a float64
}

func hexStop(at float64, hex string, alpha float64) stop {
	v, _ := strconv.ParseUint(strings.TrimPrefix(hex, "#"), 16, 32)
	return stop{at, float64(v>>16&0xff) / 255, float64(v>>8&0xff) / 255, float64(v&0xff) / 255, alpha}
}

func linear(x1, y1, x2, y2 float64, stops ...stop) *cairo.Pattern {
	p, err := cairo.NewPatternLinear(x1, y1, x2, y2)
	if err != nil {
		return nil
	}
	for _, s := range stops {
		p.AddColorStopRGBA(s.at, s.r, s.g, s.b, s.a)
	}
	return p
}

// markPaints are the mark's gradients, made once.
type markPaints struct {
	legLeft, legRight, crossbar, apexShadow, crossbarShadow, highlight, shine *cairo.Pattern
}

var paints *markPaints

func markGradients() *markPaints {
	if paints != nil {
		return paints
	}
	paints = &markPaints{
		legLeft: linear(220, 168, 340, 872,
			hexStop(0, "#C3B8FF", 1), hexStop(1, "#9C8CF8", 1)),
		legRight: linear(600, 168, 660, 872,
			hexStop(0, "#251B72", 1), hexStop(.3, "#7262EA", 1), hexStop(1, "#6252DD", 1)),
		crossbar: linear(434.7, 572, 644.5, 707,
			hexStop(0, "#8A7AF4", 1), hexStop(1, "#7A69EE", 1)),
		apexShadow: linear(546, 300, 601.5, 322.7,
			hexStop(0, "#1A2A80", .45), hexStop(1, "#1A2A80", 0)),
		crossbarShadow: linear(576.2, 656.2, 616.9, 639.5,
			hexStop(0, "#1A2A80", 0), hexStop(1, "#1A2A80", .42)),
		highlight: linear(360, 0, 900, 0,
			hexStop(0, "#FFE6F7", .8), hexStop(1, "#FFE6F7", .35)),
		shine: shine(),
	}
	return paints
}

// shine is the light that sweeps across the mark once it is whole. It moves
// along its path by being set as the source under a translation (see
// fillShifted), so the one gradient serves every frame.
func shine() *cairo.Pattern {
	c := "#FFF3FB"
	return linear(0, 0, 1024, 614,
		hexStop(0, c, 0), hexStop(.40, c, 0),
		hexStop(.455, c, .22), hexStop(.485, c, .5), hexStop(.505, c, .22),
		hexStop(.53, c, 0), hexStop(.545, c, 0),
		hexStop(.555, c, .38), hexStop(.565, c, 0),
		hexStop(1, c, 0))
}

// The curves the SVG names in keySplines.
var (
	splineStandard = [4]float64{.4, 0, .2, 1}
	splineSettle   = [4]float64{.16, 1, .3, 1}
	splineRise     = [4]float64{.55, 0, .85, .55}
	splineFall     = [4]float64{.15, .45, .35, 1}
	splineSweep    = [4]float64{.2, .8, .2, 1}
	splineGlow     = [4]float64{.45, 0, .4, 1}
	splineShine    = [4]float64{.5, 0, .3, 1}
	splineSlide    = [4]float64{.65, 0, .25, 1}
)

// tween is SMIL's <animate> with calcMode="spline": from until begin, to after
// begin+dur, and the cubic Bézier sp in between.
func tween(t, begin, dur, from, to float64, sp [4]float64) float64 {
	switch {
	case t <= begin:
		return from
	case t >= begin+dur:
		return to
	}
	return from + (to-from)*bezier((t-begin)/dur, sp)
}

// bezier solves the CSS-style timing curve through (0,0), (x1,y1), (x2,y2),
// (1,1) for x and returns its y.
func bezier(x float64, sp [4]float64) float64 {
	x1, y1, x2, y2 := sp[0], sp[1], sp[2], sp[3]
	at := func(a, b, s float64) float64 {
		return 3*a*s*(1-s)*(1-s) + 3*b*s*s*(1-s) + s*s*s
	}
	lo, hi := 0.0, 1.0
	s := x
	for range 24 {
		if v := at(x1, x2, s); math.Abs(v-x) < 1e-5 {
			break
		} else if v < x {
			lo = s
		} else {
			hi = s
		}
		s = (lo + hi) / 2
	}
	return at(y1, y2, s)
}

// draw paints one frame: the background, the mark, and the name.
func (in *Intro) draw(_ *gtk.DrawingArea, cr *cairo.Context, w, bandH int) {
	if w <= 0 || bandH <= 0 {
		return
	}
	// The area is a band across the middle of the cover; the mark is placed
	// in the cover's own terms, as if the area were the whole of it.
	h := in.cover.Height()
	if h <= 0 {
		h = bandH
	}
	if p, ok := in.area.ComputePoint(in.cover, in.origin); ok {
		cr.Translate(0, -float64(p.Y()))
	}
	t := in.t
	dark := false
	if m := adw.StyleManagerGetDefault(); m != nil {
		dark = m.Dark()
	}

	name := in.nameLayout(cr)
	ink, _ := name.Extents()
	nameW := float64(ink.X()+ink.Width()) / pango.SCALE

	// The lockup, mark to the end of the name, in mark units: the mark, the
	// gap and "Atlas" are where atlas-lockup.svg puts them, then a word space
	// and the product.
	lockup := wordmarkEnd + nameSpace + nameW - markLeft

	// Big enough to be the point of the window, small enough to leave the
	// window around it.
	k := math.Min(introMarkHeight/(markBottom-markTop), 0.24*float64(h)/(markBottom-markTop))
	k = math.Min(k, 0.84*float64(w)/lockup)
	// The band is sized in the tick, from the last layout; on the frame a
	// window grows, the mark keeps the size that fits it.
	k = math.Min(k, float64(bandH-2)/960)

	// Where the mark's left edge is: centred alone, then moved so the whole
	// lockup is centred.
	slide := tween(t, introSlideAt, introSlide, 0, 1, splineSlide)
	alone := float64(w)/2 - (markRight-markLeft)*k/2
	together := float64(w)/2 - lockup*k/2
	left := alone + (together-alone)*slide

	cr.Save()
	cr.Translate(left-markLeft*k, float64(h)/2-markMidY*k)
	cr.Scale(k, k)
	in.drawMark(cr, t, k)
	if slide > 0 {
		in.drawName(cr, name, nameW, ink.X(), slide, dark)
	}
	cr.Restore()
}

// drawMark draws the mark at time t, in mark units. k is how many pixels a
// mark unit is, which the glow is rendered to fit.
func (in *Intro) drawMark(cr *cairo.Context, t, k float64) {
	p := markGradients()

	// The whole mark fades in over the first fifth of a second and settles
	// from 94% to full size, around (512, 520).
	alpha := tween(t, 0, .2, 0, 1, splineStandard)
	s := tween(t, 0, 1.25, .94, 1, splineSettle)
	cr.Save()
	cr.Translate(512, 520)
	cr.Scale(s, s)
	cr.Translate(-512, -520)

	// The shine: one sweep, 1.5 s, from above the top left to below the
	// bottom right.
	shineOn := t > 1.25
	f := tween(t, 1.25, 1.5, 0, 1, splineShine)
	shX, shY := -1100+2200*f, -660+1320*f

	// Crossbar, under both legs: revealed by a slanted edge sliding right.
	cr.Save()
	dx := tween(t, .64, .55, -500, 0, splineSweep)
	cr.Translate(dx, 0)
	pathCrossbarClipRef.trace(cr)
	cr.Translate(-dx, 0)
	cr.Clip()
	fill(cr, pathCrossbar, p.crossbar, alpha)
	if shineOn {
		fillShifted(cr, pathCrossbar, p.shine, shX, shY, alpha)
	}
	fill(cr, pathCrossbarShadow, p.crossbarShadow, alpha)
	fill(cr, pathCrossbarHilite, p.highlight, alpha*tween(t, 1.05, .4, 0, 1, splineStandard))
	cr.Restore()

	glowRight, glowLeft := glowLevels(t)

	// Right leg: falls from the fold at the top.
	cr.Save()
	cr.Rectangle(0, 80, 1024, tween(t, .43, .5, 70, 1040, splineFall))
	cr.Clip()
	in.paintGlow(cr, k, true, alpha*glowRight)
	fill(cr, pathLegRight, p.legRight, alpha)
	if shineOn {
		fillShifted(cr, pathLegRight, p.shine, shX, shY, alpha)
	}
	cr.Restore()

	// Left leg: rises from the bottom.
	cr.Save()
	cr.Rectangle(0, tween(t, 0, .47, 880, 90, splineRise), 1024, 1024)
	cr.Clip()
	in.paintGlow(cr, k, false, alpha*glowLeft)
	fill(cr, pathLegLeft, p.legLeft, alpha)
	if shineOn {
		fillShifted(cr, pathLegLeft, p.shine, shX, shY, alpha)
	}
	cr.Restore()

	// The fold's shadow and its edge of light, last, over the legs.
	apex := alpha * tween(t, .62, .35, 0, 1, splineStandard)
	fill(cr, pathApexShadow, p.apexShadow, apex)
	fill(cr, pathApexHilite, p.highlight, apex*tween(t, .95, .4, 0, 1, splineStandard))

	cr.Restore()
}

// glowLevels are the opacities of the soft light behind each leg at t: it
// comes up with the legs and then breathes, as in the animated SVG.
func glowLevels(t float64) (right, left float64) {
	right = tween(t, .15, 1.3, 0, .5, splineGlow)
	left = tween(t, .15, 1.3, 0, .55, splineGlow)
	if t > 1.45 {
		c := math.Mod(t-1.45, 5)
		if c < 1.75 {
			right = tween(c, 0, 1.75, .5, .75, splineStandard)
			left = tween(c, 0, 1.75, .55, .8, splineStandard)
		} else {
			right = tween(c, 1.75, 3.25, .75, .5, [4]float64{.4, 0, .6, 1})
			left = tween(c, 1.75, 3.25, .8, .55, [4]float64{.4, 0, .6, 1})
		}
	}
	return right, left
}

// fill paints path with pattern at alpha.
func fill(cr *cairo.Context, path svgPath, pat *cairo.Pattern, alpha float64) {
	fillShifted(cr, path, pat, 0, 0, alpha)
}

// fillShifted paints path with pattern moved by (dx, dy), at alpha. Opaque,
// it is a plain fill; only a part still fading in pays for a clip.
func fillShifted(cr *cairo.Context, path svgPath, pat *cairo.Pattern, dx, dy, alpha float64) {
	if pat == nil || alpha <= 0 {
		return
	}
	cr.Save()
	path.trace(cr)
	// A pattern keeps the user space in force when it is set, which is what
	// moves it; the path is already traced and stays where it is.
	cr.Translate(dx, dy)
	cr.SetSource(pat)
	if alpha >= 1 {
		cr.Fill()
	} else {
		cr.Clip()
		cr.PaintWithAlpha(alpha)
	}
	cr.Restore()
}

// bandHeight is the height of the strip across the middle of a cover h high
// that the mark, its glow and the name can reach: mark units 40 to 1000,
// centred on the mark's middle at 520, at the largest scale draw could pick.
func bandHeight(h int) int {
	k := math.Min(introMarkHeight, 0.24*float64(h)) / (markBottom - markTop)
	return max(1, int(math.Ceil(960*k))+2)
}

// --- The glow ---------------------------------------------------------------

// The glow behind each leg is the leg's own shape blurred, as the animated
// SVG's feGaussianBlur (stdDeviation 22) does it. Cairo has no blur, so it is
// made once per size: the leg is filled into an alpha-only image at the
// mark's size on screen, blurred here, and painted as a mask in the glow's
// colour at whatever opacity the moment wants. Only the leg's box and the
// blur's reach around it are kept.
type glowCache struct {
	px          int // pixels per 1024 mark units
	left, right glowMask
}

type glowMask struct {
	surf *cairo.Surface
	x, y float64 // the image's top left, in mark units
}

// glowSigma is the blur's standard deviation, in mark units.
const glowSigma = 22.0

func makeGlow(path svgPath, px int) glowMask {
	u := float64(px) / 1024 // pixels per mark unit
	x0, y0, x1, y1 := path.bounds()
	reach := 3 * glowSigma
	x0, y0, x1, y1 = x0-reach, y0-reach, x1+reach, y1+reach
	w := int(math.Ceil((x1 - x0) * u))
	h := int(math.Ceil((y1 - y0) * u))
	surf := cairo.CreateImageSurface(cairo.FormatA8, w, h)
	cr := cairo.Create(surf)
	cr.Scale(u, u)
	cr.Translate(-x0, -y0)
	path.trace(cr)
	cr.Fill()
	surf.Flush()
	gaussianBlurA8(surf.Data(), w, h, surf.Stride(), glowSigma*u)
	surf.MarkDirty()
	return glowMask{surf: surf, x: x0, y: y0}
}

func (in *Intro) paintGlow(cr *cairo.Context, k float64, right bool, alpha float64) {
	// Made on the first frame, before the glow shows, and in steps of 128
	// pixels so a window being resized does not make it again on every frame.
	px := int(math.Ceil(1024*k*float64(in.area.ScaleFactor())/128)) * 128
	if px <= 0 {
		return
	}
	if in.glow.px != px {
		in.glow = glowCache{
			px:    px,
			right: makeGlow(pathLegRight, px),
			left:  makeGlow(pathLegLeft, px),
		}
	}
	if alpha <= 0 {
		return
	}
	g, c := in.glow.left, glowLeftColour
	if right {
		g, c = in.glow.right, glowRightColour
	}
	cr.Save()
	cr.Translate(g.x, g.y)
	cr.Scale(1024/float64(px), 1024/float64(px))
	cr.SetSourceRGBA(c.r, c.g, c.b, alpha)
	cr.MaskSurface(g.surf, 0, 0)
	cr.Restore()
}

// The glows' colours: the right leg's middle purple, the left leg's lilac.
var (
	glowRightColour = hexStop(0, "#7262EA", 1)
	glowLeftColour  = hexStop(0, "#C3B8FF", 1)
)

// gaussianBlurA8 blurs an 8-bit alpha image in place the way SVG specifies
// feGaussianBlur for a deviation of 2 or more, and as librsvg draws it: three
// box blurs each way, of the width d the specification gives.
func gaussianBlurA8(pix []byte, w, h, stride int, sigma float64) {
	d := int(math.Floor(sigma*3*math.Sqrt(2*math.Pi)/4 + 0.5))
	if d < 2 || w <= 0 || h <= 0 {
		return
	}
	// The three boxes, as reach to the left and to the right of each pixel.
	// An odd d is centred; an even one is shifted left, then right, and the
	// third box is one wider and centred.
	boxes := [3][2]int{{d / 2, d / 2}, {d / 2, d / 2}, {d / 2, d / 2}}
	if d%2 == 0 {
		boxes = [3][2]int{{d / 2, d/2 - 1}, {d/2 - 1, d / 2}, {d / 2, d / 2}}
	}
	n := max(w, h)
	a := make([]byte, n)
	b := make([]byte, n)
	for y := 0; y < h; y++ {
		row := pix[y*stride : y*stride+w]
		copy(a, row)
		for _, bx := range boxes {
			boxBlur(a[:w], b[:w], bx[0], bx[1])
			a, b = b, a
		}
		copy(row, a[:w])
	}
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			a[y] = pix[y*stride+x]
		}
		for _, bx := range boxes {
			boxBlur(a[:h], b[:h], bx[0], bx[1])
			a, b = b, a
		}
		for y := 0; y < h; y++ {
			pix[y*stride+x] = a[y]
		}
	}
}

// boxBlur sets each dst[i] to the mean of src from i-left to i+right, with
// nothing beyond either end.
func boxBlur(src, dst []byte, left, right int) {
	size := left + right + 1
	n := len(src)
	sum := 0
	for i := 0; i < right && i < n; i++ {
		sum += int(src[i])
	}
	for i := 0; i < n; i++ {
		if j := i + right; j < n {
			sum += int(src[j])
		}
		if j := i - left - 1; j >= 0 {
			sum -= int(src[j])
		}
		dst[i] = byte((sum + size/2) / size)
	}
}

const (
	legLeftData  = "M411.9 197.6Q424 168 456 168L590 168Q600 168 596.2 177.3L324.1 842.4Q312 872 280 872L168 872Q136 872 148.1 842.4Z"
	legRightData = "M445.3 191.9Q424 168 456 168L590 168Q600 168 603.8 177.3L875.9 842.4Q888 872 856 872L744 872Q712 872 699.9 842.4L527.1 420.1Q512 383.1 527.1 346.1L544.7 303.1Z"
)

// --- The name ---------------------------------------------------------------

// The wordmark, "Atlas", is atlas-lockup.svg's outlines, in that file's units
// (half a mark unit), so it is the brand's own lettering whatever fonts the
// machine has. The product beside it is set in the desktop's font, lighter
// and in the mark's purple, with its capitals as tall as the wordmark's.
const (
	wordmarkScale = 2.0             // lockup units to mark units
	wordmarkEnd   = 1277.9 * 2      // where "Atlas" ends, in mark units
	wordmarkCap   = (369 - 151) * 2 // the wordmark's cap height, in mark units
	wordmarkBase  = 369 * 2         // its baseline
	nameSpace     = 190.0           // the word space before the product
)

// nameLayout sets the product's name at the wordmark's cap height, in mark
// units. It is set once; later frames only bring it up to date with cr, which
// shapes it again only if cr's font settings differ.
func (in *Intro) nameLayout(cr *cairo.Context) *pango.Layout {
	if in.name != nil {
		pangocairo.UpdateLayout(cr, in.name)
		return in.name
	}
	if in.family == "" {
		in.family = "Sans"
		if st := gtk.SettingsGetDefault(); st != nil {
			if f, ok := st.ObjectProperty("gtk-font-name").(string); ok && f != "" {
				if fam := pango.FontDescriptionFromString(f).Family(); fam != "" {
					in.family = fam
				}
			}
		}
		// Measured once: the height of a capital at 1000 units.
		h := nameText(cr, in.family, 1000, "H")
		ink, _ := h.Extents()
		in.cap = float64(ink.Height()) / pango.SCALE
	}
	size := 1000.0
	if in.cap > 0 {
		size = 1000 * wordmarkCap / in.cap
	}
	in.name = nameText(cr, in.family, size, in.product)
	return in.name
}

func nameText(cr *cairo.Context, family string, size float64, text string) *pango.Layout {
	l := pangocairo.CreateLayout(cr)
	desc := pango.NewFontDescription()
	desc.SetFamily(family)
	desc.SetWeight(pango.WeightMedium)
	desc.SetAbsoluteSize(size * pango.SCALE)
	l.SetFontDescription(desc)
	l.SetText(text)
	return l
}

// drawName draws "Atlas" and the product, sliding out from under the mark's
// right leg as slide goes from 0 to 1, in mark units.
func (in *Intro) drawName(cr *cairo.Context, name *pango.Layout, nameW float64, inkX int, slide float64, dark bool) {
	wordmarkStart := 566.2 * wordmarkScale
	travel := (wordmarkEnd + nameSpace + nameW - wordmarkStart) * 0.55
	offset := -(1 - slide) * travel

	cr.Save()
	// Everything right of the right leg's outer edge, a little out from it.
	edge := func(y float64) float64 { return 603.8 + 80 + (y-177.3)*(875.9-603.8)/(842.4-177.3) }
	cr.MoveTo(edge(0), 0)
	cr.LineTo(1e5, 0)
	cr.LineTo(1e5, 1024)
	cr.LineTo(edge(1024), 1024)
	cr.ClosePath()
	cr.Clip()

	alpha := tween(slide, 0, .45, 0, 1, splineStandard)
	cr.Translate(offset, 0)

	// "Atlas"
	cr.Save()
	cr.Scale(wordmarkScale, wordmarkScale)
	pathWordmark.trace(cr)
	cr.Restore()
	if dark {
		cr.SetSourceRGBA(1, 1, 1, alpha)
	} else {
		cr.SetSourceRGBA(0x1B/255.0, 0x17/255.0, 0x48/255.0, alpha)
	}
	cr.Fill()

	// The product, its capitals' baseline on the wordmark's.
	base := float64(name.Baseline()) / pango.SCALE
	cr.MoveTo(wordmarkEnd+nameSpace-float64(inkX)/pango.SCALE, wordmarkBase-base)
	if dark {
		cr.SetSourceRGBA(0xC3/255.0, 0xB8/255.0, 1, alpha)
	} else {
		cr.SetSourceRGBA(0x62/255.0, 0x52/255.0, 0xDD/255.0, alpha)
	}
	pangocairo.ShowLayout(cr, name)
	cr.Restore()
}

// --- Paths ------------------------------------------------------------------

// svgPath is an SVG path of absolute M, L, Q and Z commands, which is all the
// logo's files use.
type svgPath []pathOp

type pathOp struct {
	cmd byte
	pts [4]float64
}

func mustPath(d string) svgPath {
	p, err := parsePath(d)
	if err != nil {
		panic(err)
	}
	return p
}

func parsePath(d string) (svgPath, error) {
	var out svgPath
	fields := strings.FieldsFunc(d, func(r rune) bool { return r == ' ' || r == ',' })
	// Split commands glued to their first number ("M411.9").
	var toks []string
	for _, f := range fields {
		for len(f) > 0 {
			if strings.ContainsRune("MLQZ", rune(f[0])) {
				toks = append(toks, f[:1])
				f = f[1:]
				continue
			}
			i := strings.IndexAny(f, "MLQZ")
			if i < 0 {
				toks = append(toks, f)
				break
			}
			toks = append(toks, f[:i])
			f = f[i:]
		}
	}
	nums := func(i, n int) ([4]float64, error) {
		var v [4]float64
		if i+n > len(toks) {
			return v, fmt.Errorf("path ends early")
		}
		for j := 0; j < n; j++ {
			x, err := strconv.ParseFloat(toks[i+j], 64)
			if err != nil {
				return v, err
			}
			v[j] = x
		}
		return v, nil
	}
	for i := 0; i < len(toks); {
		c := toks[i]
		i++
		switch c {
		case "M", "L":
			v, err := nums(i, 2)
			if err != nil {
				return nil, err
			}
			out = append(out, pathOp{c[0], v})
			i += 2
		case "Q":
			v, err := nums(i, 4)
			if err != nil {
				return nil, err
			}
			out = append(out, pathOp{'Q', v})
			i += 4
		case "Z":
			out = append(out, pathOp{cmd: 'Z'})
		default:
			return nil, fmt.Errorf("path command %q not supported", c)
		}
	}
	return out, nil
}

// bounds is a box around the path: its points and its curves' control points.
func (p svgPath) bounds() (x0, y0, x1, y1 float64) {
	x0, y0, x1, y1 = math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, op := range p {
		n := 2
		switch op.cmd {
		case 'Z':
			continue
		case 'Q':
			n = 4
		}
		for i := 0; i < n; i += 2 {
			x0, x1 = math.Min(x0, op.pts[i]), math.Max(x1, op.pts[i])
			y0, y1 = math.Min(y0, op.pts[i+1]), math.Max(y1, op.pts[i+1])
		}
	}
	return x0, y0, x1, y1
}

// trace adds the path to cr's current path. Cairo has no quadratic curve, so
// each Q is raised to the cubic that draws the same curve.
func (p svgPath) trace(cr *cairo.Context) {
	var cx, cy, sx, sy float64
	for _, op := range p {
		switch op.cmd {
		case 'M':
			cx, cy = op.pts[0], op.pts[1]
			sx, sy = cx, cy
			cr.MoveTo(cx, cy)
		case 'L':
			cx, cy = op.pts[0], op.pts[1]
			cr.LineTo(cx, cy)
		case 'Q':
			qx, qy, x, y := op.pts[0], op.pts[1], op.pts[2], op.pts[3]
			cr.CurveTo(cx+2.0/3*(qx-cx), cy+2.0/3*(qy-cy), x+2.0/3*(qx-x), y+2.0/3*(qy-y), x, y)
			cx, cy = x, y
		case 'Z':
			cr.ClosePath()
			cx, cy = sx, sy
		}
	}
}

// wordmarkData is "Atlas" from atlas-lockup.svg.
const wordmarkData = "M610.2 369L566.2 369L654.0 151L688.4 151L775.8 369L730.9 369L715.7 328.4L625.7 328.4L610.2 369ZM639.4 293.0L702.0 293.0L671.0 210.5L639.4 293.0ZM850.6 369L809.9 369L809.9 254.9L774.9 254.9L774.9 219.2L809.9 219.2L809.9 156.9L850.6 156.9L850.6 219.2L885.6 219.2L885.6 254.9L850.6 254.9L850.6 369ZM949.2 369L908.6 369L908.6 144.8L949.2 144.8L949.2 369ZM1047.8 372.1L1047.8 372.1Q1027.3 372.1 1010.9 361.9Q994.4 351.6 985.1 334.0Q975.8 316.3 975.8 294.3L975.8 294.3Q975.8 271.9 985.1 254.3Q994.4 236.6 1010.9 226.4Q1027.3 216.1 1047.8 216.1L1047.8 216.1Q1063.9 216.1 1076.6 222.6L1076.6 222.6Q1085.0 227.0 1091.2 233.5L1091.2 233.5L1091.2 219.2L1131.5 219.2L1131.5 369L1091.2 369L1091.2 354.4Q1085.0 360.9 1076.6 365.3L1076.6 365.3Q1063.9 372.1 1047.8 372.1ZM1055.2 334.6L1055.2 334.6Q1072.3 334.6 1082.8 323.3Q1093.4 311.9 1093.4 294.0L1093.4 294.0Q1093.4 282.2 1088.6 273.0Q1083.8 263.9 1075.2 258.8Q1066.7 253.6 1055.2 253.6L1055.2 253.6Q1044.1 253.6 1035.5 258.8Q1027.0 263.9 1022.2 273.0Q1017.4 282.2 1017.4 294.0L1017.4 294.0Q1017.4 306.0 1022.2 315.2Q1027.0 324.3 1035.5 329.5Q1044.1 334.6 1055.2 334.6ZM1219.0 372.4L1219.0 372.4Q1206.2 372.4 1194.0 369Q1181.7 365.6 1171.5 359.5Q1161.3 353.5 1153.8 344.8L1153.8 344.8L1178.0 320.3Q1185.8 329.0 1196.0 333.3Q1206.2 337.7 1218.7 337.7L1218.7 337.7Q1228.6 337.7 1233.7 334.9Q1238.8 332.1 1238.8 326.5L1238.8 326.5Q1238.8 320.3 1233.4 316.9Q1228.0 313.5 1219.3 311.2Q1210.6 308.8 1201.1 305.9Q1191.7 302.9 1183.0 298.1Q1174.3 293.3 1168.9 284.8Q1163.5 276.3 1163.5 262.6L1163.5 262.6Q1163.5 248.4 1170.4 237.8Q1177.4 227.3 1190.4 221.4Q1203.5 215.5 1221.1 215.5L1221.1 215.5Q1239.7 215.5 1254.8 222.0Q1269.8 228.5 1279.7 241.5L1279.7 241.5L1255.2 266.0Q1248.4 257.7 1239.9 254.0Q1231.4 250.2 1221.4 250.2L1221.4 250.2Q1212.4 250.2 1207.6 253.0Q1202.8 255.8 1202.8 260.8L1202.8 260.8Q1202.8 266.4 1208.3 269.5Q1213.7 272.6 1222.4 274.9Q1231.1 277.2 1240.5 280.2Q1250.0 283.1 1258.5 288.4Q1267.0 293.6 1272.5 302.3Q1277.9 311.0 1277.9 324.7L1277.9 324.7Q1277.9 346.7 1262.1 359.5Q1246.3 372.4 1219.0 372.4Z"
