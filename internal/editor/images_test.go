package editor

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestImageLineSpan(t *testing.T) {
	cases := []struct {
		line   string
		target string // "" when the line is not an image line
	}{
		{"![a](b.png)", "b.png"},
		{"![](pics/b.png)", "pics/b.png"},
		{"  ![a](b.png)  ", "b.png"},
		{"\t![alt text](b.png)", "b.png"},
		{"![[x.png]]", "x.png"},
		{"  ![[Photos/x.JPG]] ", "Photos/x.JPG"},

		// Anything else on the line makes it text with a picture in it.
		{"text ![a](b)", ""},
		{"![a](b.png) text", ""},
		{"![a](b.png) ![c](d.png)", ""},
		{"- ![a](b.png)", ""},
		{"> ![a](b.png)", ""},
		{"# ![a](b.png)", ""},
		{"![a](b.png) <!-- note -->", ""},

		// Not a picture at all.
		{"", ""},
		{"   ", ""},
		{"![[Some note]]", ""}, // an embedded note, not an image file
		{"[a](b.png)", ""},     // a link, without the "!"
		{"![a]()", ""},         // nothing to load
		{"![a](b", ""},
		{"`![a](b.png)`", ""}, // inside inline code
		{"￼![a](b.png)", ""},
	}
	for _, c := range cases {
		sp, ok := imageLineSpan(c.line)
		if ok != (c.target != "") {
			t.Errorf("imageLineSpan(%q) ok = %v, want %v", c.line, ok, c.target != "")
			continue
		}
		if ok && sp.Target != c.target {
			t.Errorf("imageLineSpan(%q) target = %q, want %q", c.line, sp.Target, c.target)
		}
		if ok && c.line[sp.Start:sp.End] != strings.TrimSpace(c.line) {
			t.Errorf("imageLineSpan(%q) covers %q, want the whole content %q",
				c.line, c.line[sp.Start:sp.End], strings.TrimSpace(c.line))
		}
	}
}

func TestFitImage(t *testing.T) {
	cases := []struct {
		name              string
		natW, natH, avail int
		w, h              int
	}{
		{"small picture keeps its size", 200, 100, 800, 200, 100},
		{"width is capped at what the view gives", 1600, 800, 800, 800, 400},
		{"height is capped at 480", 400, 1200, 800, 160, 480},
		{"both caps, proportions kept", 4000, 3000, 1000, 640, 480},
		{"exactly the view width", 800, 400, 800, 800, 400},
		{"exactly the height cap", 300, 480, 800, 300, 480},
		{"square", 1000, 1000, 500, 480, 480},
		{"wide strip", 2000, 10, 500, 500, 3},
		{"tiny view still gives a pixel", 800, 400, 1, 1, 1},
		{"tiny view, tall picture", 100, 1000, 3, 3, 30},
		{"zero width means not ready", 800, 400, 0, 0, 0},
		{"negative width means not ready", 800, 400, -20, 0, 0},
		{"picture with no size", 0, 0, 800, 0, 0},
		{"picture with no height", 100, 0, 800, 0, 0},
	}
	for _, c := range cases {
		w, h := fitImage(c.natW, c.natH, c.avail)
		if w != c.w || h != c.h {
			t.Errorf("%s: fitImage(%d, %d, %d) = %d x %d, want %d x %d",
				c.name, c.natW, c.natH, c.avail, w, h, c.w, c.h)
		}
		if w > 0 && (w > c.avail || h > maxImageHeight || w > c.natW) {
			t.Errorf("%s: %d x %d breaks a cap", c.name, w, h)
		}
	}
}

func TestFitImageKeepsAspectRatio(t *testing.T) {
	for _, nat := range [][2]int{{1920, 1080}, {1080, 1920}, {333, 777}, {5000, 400}, {64, 64}} {
		for avail := 20; avail <= 2400; avail += 97 {
			w, h := fitImage(nat[0], nat[1], avail)
			want := float64(nat[0]) / float64(nat[1])
			got := float64(w) / float64(h)
			// Rounding to whole pixels moves the ratio by up to half a pixel on the
			// smaller side.
			slack := 1.0 / float64(min(w, h))
			if got < want*(1-slack) || got > want*(1+slack) {
				t.Errorf("fitImage(%d, %d, %d) = %d x %d: ratio %.3f, want %.3f",
					nat[0], nat[1], avail, w, h, got, want)
			}
		}
	}
}

func TestImagePad(t *testing.T) {
	cases := []struct {
		h    int
		name string
		px   int
	}{
		{1, "image-pad-16", 24},
		{16, "image-pad-16", 24},
		{17, "image-pad-32", 40},
		{100, "image-pad-112", 120},
		{480, "image-pad-480", 488},
	}
	for _, c := range cases {
		name, px := imagePad(c.h)
		if name != c.name || px != c.px {
			t.Errorf("imagePad(%d) = %q, %d, want %q, %d", c.h, name, px, c.name, c.px)
		}
	}
	// Every height up to the cap must share one of a few tags.
	names := map[string]bool{}
	for h := 1; h <= maxImageHeight; h++ {
		name, px := imagePad(h)
		names[name] = true
		if px < h+imageGap {
			t.Fatalf("imagePad(%d) leaves %d px, less than the picture and its gap", h, px)
		}
	}
	if len(names) != maxImageHeight/imagePadStep {
		t.Errorf("%d tags for heights up to %d, want %d", len(names), maxImageHeight, maxImageHeight/imagePadStep)
	}
}

func TestNewlinePadding(t *testing.T) {
	cases := []struct {
		name                       string
		atLineStart, nextIsNewline bool
		before, after              bool
	}{
		{"empty line", true, true, false, false},
		{"end of a text line", false, true, true, false},
		{"start of a text line", true, false, false, true},
		{"middle of a text line", false, false, true, true},
	}
	for _, c := range cases {
		before, after := newlinePadding(c.atLineStart, c.nextIsNewline)
		if before != c.before || after != c.after {
			t.Errorf("%s: newlinePadding(%v, %v) = %v, %v, want %v, %v",
				c.name, c.atLineStart, c.nextIsNewline, before, after, c.before, c.after)
		}
	}
}

func TestHasImageMIME(t *testing.T) {
	yes := [][]string{{"image/png"}, {"text/plain", "image/jpeg"}, {"image/x-something"}}
	no := [][]string{nil, {"text/plain"}, {"text/uri-list", "text/html"}, {"application/image/png"}}
	for _, m := range yes {
		if !hasImageMIME(m) {
			t.Errorf("hasImageMIME(%v) = false", m)
		}
	}
	for _, m := range no {
		if hasImageMIME(m) {
			t.Errorf("hasImageMIME(%v) = true", m)
		}
	}
}

func TestKeepImagePaths(t *testing.T) {
	got := keepImagePaths([]string{"/a/one.png", "", "/a/notes.txt", "/b/Two.JPG", "/c/three.svg", "/d/dir"})
	want := []string{"/a/one.png", "/b/Two.JPG", "/c/three.svg"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("keepImagePaths = %v, want %v", got, want)
	}
	if got := keepImagePaths(nil); got != nil {
		t.Errorf("keepImagePaths(nil) = %v, want nil", got)
	}
}

func TestLRUEvictsLeastRecentlyUsed(t *testing.T) {
	c := newLRU[int](3)
	c.put("a", 1)
	c.put("b", 2)
	c.put("c", 3)
	if got, want := c.keys(), []string{"c", "b", "a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}

	// Reading "a" makes it the newest, so "b" is now the oldest.
	if v, ok := c.get("a"); !ok || v != 1 {
		t.Fatalf("get(a) = %d, %v", v, ok)
	}
	c.put("d", 4)
	if _, ok := c.get("b"); ok {
		t.Error("b should have been evicted first")
	}
	if got, want := c.keys(), []string{"d", "a", "c"}; !reflect.DeepEqual(got, want) {
		t.Errorf("keys = %v, want %v", got, want)
	}

	// Putting an existing key replaces the value and refreshes it, without growing.
	c.put("c", 30)
	c.put("e", 5)
	if got, want := c.keys(), []string{"e", "c", "d"}; !reflect.DeepEqual(got, want) {
		t.Errorf("keys = %v, want %v", got, want)
	}
	if v, _ := c.get("c"); v != 30 {
		t.Errorf("c = %d, want 30", v)
	}
	if _, ok := c.get("a"); ok {
		t.Error("a should have been evicted")
	}
}

func TestLRUMissAndClear(t *testing.T) {
	c := newLRU[string](2)
	if v, ok := c.get("nope"); ok || v != "" {
		t.Errorf("get on empty = %q, %v", v, ok)
	}
	c.put("x", "1")
	c.put("y", "2")
	c.clear()
	if _, ok := c.get("x"); ok || len(c.keys()) != 0 {
		t.Error("clear left entries behind")
	}
	c.put("z", "3")
	if got := c.keys(); !reflect.DeepEqual(got, []string{"z"}) {
		t.Errorf("keys after reuse = %v", got)
	}
}

func TestLRUHoldsItsSize(t *testing.T) {
	c := newLRU[int](imageCacheSize)
	for i := 0; i < imageCacheSize*3; i++ {
		c.put(strconv.Itoa(i), i)
	}
	keys := c.keys()
	if len(keys) != imageCacheSize {
		t.Fatalf("%d entries, want %d", len(keys), imageCacheSize)
	}
	// The newest 32 survive, newest first.
	for i, k := range keys {
		if want := strconv.Itoa(imageCacheSize*3 - 1 - i); k != want {
			t.Fatalf("keys[%d] = %s, want %s", i, k, want)
		}
	}
}
