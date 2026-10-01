package ui

import "testing"

// The glow's blur spreads a point evenly both ways and keeps its weight, as
// the Gaussian it stands in for does.
func TestGaussianBlurA8(t *testing.T) {
	const w, h, stride = 61, 61, 64
	pix := make([]byte, h*stride)
	for y := 25; y < 36; y++ {
		for x := 25; x < 36; x++ {
			pix[y*stride+x] = 255
		}
	}
	before := 0
	for _, v := range pix {
		before += int(v)
	}

	for _, sigma := range []float64{3.5, 4} { // box widths 7 and 8
		p := append([]byte(nil), pix...)
		gaussianBlurA8(p, w, h, stride, sigma)
		after := 0
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				v := p[y*stride+x]
				after += int(v)
				if m := p[y*stride+(w-1-x)]; absDiff(v, m) > 1 {
					t.Fatalf("sigma %v: (%d,%d)=%d but its mirror is %d", sigma, x, y, v, m)
				}
			}
			for x := w; x < stride; x++ {
				if p[y*stride+x] != 0 {
					t.Fatalf("sigma %v: wrote into the row's padding at (%d,%d)", sigma, x, y)
				}
			}
		}
		if p[30*stride+30] >= 255 || p[30*stride+20] == 0 {
			t.Errorf("sigma %v: centre %d, 5 px out %d; the square was not spread", sigma, p[30*stride+30], p[30*stride+20])
		}
		if diff := after - before; diff > before/50 || -diff > before/50 {
			t.Errorf("sigma %v: weight went from %d to %d", sigma, before, after)
		}
	}
}

func absDiff(a, b byte) int {
	if a > b {
		return int(a - b)
	}
	return int(b - a)
}

// The band the mark is drawn in reaches the mark's glow above and below at
// every window height.
func TestBandHeightHoldsTheMark(t *testing.T) {
	for _, h := range []int{0, 1, 100, 400, 617, 900, 2000} {
		k := min(introMarkHeight, 0.24*float64(h)) / (markBottom - markTop)
		// The glow reaches 3 deviations past the legs, and the band is centred
		// on the mark's middle.
		reach := max(markMidY-(markTop-3*glowSigma), markBottom+3*glowSigma-markMidY)
		need := 2 * reach * k
		if got := float64(bandHeight(h)); got < need {
			t.Errorf("window %d high: band %v, the glow needs %v", h, got, need)
		}
	}
}
