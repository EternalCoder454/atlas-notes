package editor

import (
	"errors"
	"math"

	"github.com/diamondburned/gotk4/pkg/cairo"
	"github.com/diamondburned/gotk4/pkg/pango"
	"github.com/diamondburned/gotk4/pkg/pangocairo"

	"atlas-notes/internal/diagram"
)

// Drawing a diagram.Scene with Cairo and Pango. The scene says what to draw in
// colour roles; the widget fills the roles from the theme each time it draws, so
// a diagram follows light, dark and the app's themes without being laid out
// again.

// diagramFont is the text of a diagram: the family it is set in, and the layout
// that measures and draws it.
type diagramFont struct {
	layout *pango.Layout
	desc   *pango.FontDescription
}

func newDiagramFont(layout *pango.Layout, family string) *diagramFont {
	desc := pango.NewFontDescription()
	if family != "" {
		desc.SetFamily(family)
	}
	return &diagramFont{layout: layout, desc: desc}
}

func (f *diagramFont) set(text string, size float64, bold bool) {
	f.desc.SetAbsoluteSize(size * float64(pango.SCALE))
	if bold {
		f.desc.SetWeight(pango.WeightBold)
	} else {
		f.desc.SetWeight(pango.WeightNormal)
	}
	f.layout.SetFontDescription(f.desc)
	f.layout.SetText(text)
}

// measure is the diagram package's Measure, over Pango.
func (f *diagramFont) measure(text string, size float64, bold bool) (float64, float64) {
	f.set(text, size, bold)
	w, h := f.layout.PixelSize()
	return float64(w), float64(h)
}

func setRole(cr *cairo.Context, pal *diagram.Palette, r diagram.Role) {
	c := pal[r]
	cr.SetSourceRGBA(c.R, c.G, c.B, c.A)
}

func roundedRect(cr *cairo.Context, x, y, w, h, r float64) {
	r = math.Min(r, math.Min(w, h)/2)
	cr.NewSubPath()
	cr.Arc(x+w-r, y+r, r, -math.Pi/2, 0)
	cr.Arc(x+w-r, y+h-r, r, 0, math.Pi/2)
	cr.Arc(x+r, y+h-r, r, math.Pi/2, math.Pi)
	cr.Arc(x+r, y+r, r, math.Pi, 3*math.Pi/2)
	cr.ClosePath()
}

// paintScene draws a scene at its own size; the caller scales and translates.
func paintScene(cr *cairo.Context, sc *diagram.Scene, pal *diagram.Palette, f *diagramFont) {
	cr.SetLineJoin(cairo.LineJoinRound)
	for _, p := range sc.Prims {
		switch p.Kind {
		case diagram.PrimBox:
			switch p.Shape {
			case diagram.ShapeDiamond:
				cr.NewSubPath()
				cr.MoveTo(p.X+p.W/2, p.Y)
				cr.LineTo(p.X+p.W, p.Y+p.H/2)
				cr.LineTo(p.X+p.W/2, p.Y+p.H)
				cr.LineTo(p.X, p.Y+p.H/2)
				cr.ClosePath()
			case diagram.ShapeCircle:
				cr.Save()
				cr.Translate(p.X+p.W/2, p.Y+p.H/2)
				cr.Scale(p.W/2, p.H/2)
				cr.NewSubPath()
				cr.Arc(0, 0, 1, 0, 2*math.Pi)
				cr.Restore()
			default:
				roundedRect(cr, p.X, p.Y, p.W, p.H, p.Radius)
			}
			if p.Fill != diagram.RoleNone {
				setRole(cr, pal, p.Fill)
				cr.FillPreserve()
			}
			if p.Stroke != diagram.RoleNone {
				setRole(cr, pal, p.Stroke)
				cr.SetLineWidth(p.StrokeW)
				cr.Stroke()
			} else {
				cr.NewPath()
			}
		case diagram.PrimText:
			f.set(p.Text, p.Size, p.Bold)
			w, _ := f.layout.PixelSize()
			x := p.X - float64(w)/2
			if p.Left {
				x = p.X
			}
			setRole(cr, pal, p.Fill)
			cr.MoveTo(x, p.Y)
			pangocairo.ShowLayout(cr, f.layout)
		case diagram.PrimPath:
			for _, s := range diagram.PathSegs(p.Pts, p.Corner) {
				switch s.Op {
				case 'M':
					cr.MoveTo(s.P[0].X, s.P[0].Y)
				case 'L':
					cr.LineTo(s.P[0].X, s.P[0].Y)
				case 'C':
					cr.CurveTo(s.P[0].X, s.P[0].Y, s.P[1].X, s.P[1].Y, s.P[2].X, s.P[2].Y)
				}
			}
			setRole(cr, pal, p.Stroke)
			cr.SetLineWidth(p.StrokeW)
			if p.Dash {
				cr.SetDash([]float64{5, 4}, 0)
			}
			cr.Stroke()
			cr.SetDash(nil, 0)
		case diagram.PrimArrow:
			cr.MoveTo(p.Pts[0].X, p.Pts[0].Y)
			cr.LineTo(p.Pts[1].X, p.Pts[1].Y)
			cr.LineTo(p.Pts[2].X, p.Pts[2].Y)
			cr.ClosePath()
			setRole(cr, pal, p.Fill)
			cr.Fill()
		}
	}
}

// maxPNGPixels bounds the picture an export draws.
const maxPNGPixels = 16e6

// RenderDiagramPNG draws a diagram for a document: on white, at twice its size
// so it stays sharp on paper. It returns the PNG and the size in diagram pixels.
func RenderDiagramPNG(src string) (png []byte, w, h int, err error) {
	g, err := diagram.ParseDrawable(src)
	if err != nil {
		return nil, 0, 0, err
	}
	surf := cairo.CreateImageSurface(cairo.FormatARGB32, 1, 1)
	cr := cairo.Create(surf)
	f := newDiagramFont(pangocairo.CreateLayout(cr), "Sans")
	sc := diagram.Layout(g, f.measure)
	if sc.TooLarge() {
		return nil, 0, 0, errors.New("the diagram is too large")
	}
	w, h = int(math.Ceil(sc.W)), int(math.Ceil(sc.H))
	// Twice the size, unless that would be more than about 16 million pixels:
	// the export runs on the main thread and must not allocate gigabytes.
	k := math.Min(2, math.Sqrt(maxPNGPixels/float64(w*h)))
	if k < 0.25 {
		return nil, 0, 0, errors.New("the diagram is too large")
	}
	pw, ph := int(math.Ceil(float64(w)*k)), int(math.Ceil(float64(h)*k))
	surf = cairo.CreateImageSurface(cairo.FormatARGB32, pw, ph)
	cr = cairo.Create(surf)
	pal := diagram.LightPalette
	setRole(cr, &pal, diagram.RoleBg)
	cr.Paint()
	cr.Scale(k, k)
	f = newDiagramFont(pangocairo.CreateLayout(cr), "Sans")
	paintScene(cr, sc, &pal, f)
	var buf bytesBuffer
	if err := surf.WriteToPNGWriter(&buf); err != nil {
		return nil, 0, 0, err
	}
	return buf.b, w, h, nil
}

type bytesBuffer struct{ b []byte }

func (b *bytesBuffer) Write(p []byte) (int, error) {
	b.b = append(b.b, p...)
	return len(p), nil
}
