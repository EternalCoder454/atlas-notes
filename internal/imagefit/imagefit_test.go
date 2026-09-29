package imagefit

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math"
	"math/rand/v2"
	"testing"
	"time"

	"golang.org/x/image/bmp"
	"golang.org/x/image/tiff"
)

// photoLike is smooth colour with sensor-style noise on top. PNG cannot
// squeeze the noise, and JPEG throws it away, which is what a camera photo
// looks like to the size comparison.
func photoLike(w, h int) *image.RGBA {
	rng := rand.New(rand.NewPCG(1, 2))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			fx, fy := float64(x), float64(y)
			r := 128 + 90*math.Sin(fx/70+fy/210)
			g := 128 + 90*math.Sin(fx/130-fy/90)
			b := 128 + 90*math.Cos(fx/50+fy/170)
			n := func(v float64) uint8 {
				return uint8(max(0, min(255, int(v)+rng.IntN(17)-8)))
			}
			img.SetRGBA(x, y, color.RGBA{n(r), n(g), n(b), 255})
		}
	}
	return img
}

// screenshotLike is a white page with a few flat blocks and thin dark lines.
func screenshotLike(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	fill := func(r image.Rectangle, c color.RGBA) {
		for y := r.Min.Y; y < r.Max.Y; y++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				img.SetRGBA(x, y, c)
			}
		}
	}
	fill(img.Bounds(), color.RGBA{255, 255, 255, 255})
	fill(image.Rect(0, 0, w, 40), color.RGBA{40, 90, 200, 255})
	fill(image.Rect(20, 80, w/2, h/2), color.RGBA{230, 230, 235, 255})
	for y := 100; y < h-20; y += 24 {
		fill(image.Rect(w/2+20, y, w-30, y+3), color.RGBA{30, 30, 30, 255})
	}
	return img
}

// gradient is cheap for every encoder, so the large-image tests stay fast.
func gradient(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y*img.Stride + x*4
			img.Pix[i] = uint8(x * 255 / w)
			img.Pix[i+1] = uint8(y * 255 / h)
			img.Pix[i+2] = uint8((x + y) & 0xFF)
			img.Pix[i+3] = 255
		}
	}
	return img
}

func mustPNG(t *testing.T, img image.Image, level png.CompressionLevel) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: level}).Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func mustJPEG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func mustFit(t *testing.T, data []byte) ([]byte, string) {
	t.Helper()
	out, ext, err := Fit(data)
	if err != nil {
		t.Fatalf("Fit: %v", err)
	}
	return out, ext
}

func config(t *testing.T, data []byte) (image.Config, string) {
	t.Helper()
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("output is not a readable image: %v", err)
	}
	return cfg, format
}

// segment builds a JPEG marker segment: 0xFF, the marker, then a two-byte
// length that counts itself.
func segment(marker byte, payload []byte) []byte {
	out := []byte{0xFF, marker}
	out = binary.BigEndian.AppendUint16(out, uint16(len(payload)+2))
	return append(out, payload...)
}

// exifSegment builds an APP1 segment holding the smallest useful Exif: a TIFF
// header and an IFD0 with an ImageDescription, which stands in for private
// text such as GPS data, and the orientation tag.
func exifSegment(bo binary.AppendByteOrder, orientation uint16, description string) []byte {
	desc := append([]byte(description), 0)

	tf := []byte("II")
	if bo == binary.BigEndian {
		tf = []byte("MM")
	}
	tf = bo.AppendUint16(tf, 42)
	tf = bo.AppendUint32(tf, 8) // IFD0 follows the eight-byte header.

	// Two 12-byte entries, so the description text lands after the count (2),
	// the entries (24) and the next-IFD offset (4).
	descOffset := uint32(8 + 2 + 24 + 4)
	tf = bo.AppendUint16(tf, 2)
	// ImageDescription, type ASCII, stored out of line.
	tf = bo.AppendUint16(tf, 0x010E)
	tf = bo.AppendUint16(tf, 2)
	tf = bo.AppendUint32(tf, uint32(len(desc)))
	tf = bo.AppendUint32(tf, descOffset)
	// Orientation, type SHORT, one value stored inline.
	tf = bo.AppendUint16(tf, 0x0112)
	tf = bo.AppendUint16(tf, 3)
	tf = bo.AppendUint32(tf, 1)
	tf = bo.AppendUint16(tf, orientation)
	tf = bo.AppendUint16(tf, 0)
	tf = bo.AppendUint32(tf, 0) // no next IFD
	tf = append(tf, desc...)

	return segment(markerAPP1, append([]byte("Exif\x00\x00"), tf...))
}

// jfifSegment is the APP0 header most JPEG files start with.
func jfifSegment() []byte {
	return segment(0xE0, []byte("JFIF\x00\x01\x01\x00\x00\x01\x00\x01\x00\x00"))
}

// insertAfterSOI puts segments straight after the start-of-image marker.
func insertAfterSOI(jpg []byte, segs ...[]byte) []byte {
	out := append([]byte{}, jpg[:2]...)
	for _, s := range segs {
		out = append(out, s...)
	}
	return append(out, jpg[2:]...)
}

func TestPhotoBecomesSmallerJPEG(t *testing.T) {
	in := mustPNG(t, photoLike(1200, 800), png.DefaultCompression)
	out, ext := mustFit(t, in)
	if ext != ".jpg" {
		t.Fatalf("ext = %q, want .jpg", ext)
	}
	if len(out) >= len(in) {
		t.Errorf("output is %d bytes, input was %d", len(out), len(in))
	}
	cfg, format := config(t, out)
	if format != "jpeg" || cfg.Width != 1200 || cfg.Height != 800 {
		t.Errorf("got %s %dx%d, want jpeg 1200x800", format, cfg.Width, cfg.Height)
	}
}

func TestScreenshotStaysPNG(t *testing.T) {
	// Stored uncompressed, so the output has to beat it by re-encoding.
	in := mustPNG(t, screenshotLike(1200, 800), png.NoCompression)
	out, ext := mustFit(t, in)
	if ext != ".png" {
		t.Fatalf("ext = %q, want .png", ext)
	}
	if len(out) >= len(in) {
		t.Errorf("output is %d bytes, input was %d", len(out), len(in))
	}
	if _, format := config(t, out); format != "png" {
		t.Errorf("format = %s, want png", format)
	}
}

func TestTransparentPNGStaysPNG(t *testing.T) {
	// A photo would normally turn into a JPEG, so one see-through pixel is
	// what keeps this a PNG.
	img := photoLike(600, 400)
	img.SetRGBA(10, 10, color.RGBA{})
	in := mustPNG(t, img, png.DefaultCompression)
	out, ext := mustFit(t, in)
	if ext != ".png" {
		t.Fatalf("ext = %q, want .png", ext)
	}
	got, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, a := got.At(10, 10).RGBA(); a != 0 {
		t.Errorf("transparent pixel came back with alpha %d", a)
	}
	if _, _, _, a := got.At(11, 10).RGBA(); a != 0xFFFF {
		t.Errorf("neighbouring pixel lost its opacity: alpha %d", a)
	}
}

// grayAlphaPNG is a PNG that Fit prefers to keep. Gray with alpha decodes to
// NRGBA, so re-encoding writes twice the channels and comes out larger than
// this file, which stores two.
func grayAlphaPNG(t *testing.T) []byte {
	t.Helper()
	const w, h = 64, 64
	rng := rand.New(rand.NewPCG(3, 4))
	var raw bytes.Buffer
	for y := 0; y < h; y++ {
		raw.WriteByte(0) // filter type: none
		for x := 0; x < w; x++ {
			raw.WriteByte(byte(rng.IntN(256)))
			raw.WriteByte(200)
		}
	}
	return rawPNG(t, w, h, 8, 4, raw.Bytes())
}

func TestKeepsInputPNGWhenItIsSmaller(t *testing.T) {
	in := grayAlphaPNG(t)

	out, ext := mustFit(t, in)
	if ext != ".png" || !bytes.Equal(out, in) {
		t.Errorf("ext = %q, kept input = %v; want the input PNG back unchanged", ext, bytes.Equal(out, in))
	}
}

func TestLargeImageIsCappedAt3840(t *testing.T) {
	cases := []struct {
		name         string
		w, h         int
		wantW, wantH int
	}{
		{"landscape", 5000, 3000, 3840, 2304},
		{"portrait", 3000, 5000, 2304, 3840},
		{"at the cap", 3840, 100, 3840, 100},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := mustPNG(t, gradient(c.w, c.h), png.BestSpeed)
			out, _ := mustFit(t, in)
			cfg, _ := config(t, out)
			if cfg.Width != c.wantW || cfg.Height != c.wantH {
				t.Errorf("got %dx%d, want %dx%d", cfg.Width, cfg.Height, c.wantW, c.wantH)
			}
		})
	}
}

func TestFitSize(t *testing.T) {
	cases := []struct {
		w, h         int
		wantW, wantH int
		down         bool
	}{
		{100, 100, 100, 100, false},
		{3840, 3840, 3840, 3840, false},
		{7680, 4320, 3840, 2160, true},
		{4320, 7680, 2160, 3840, true},
		{20000, 1, 3840, 1, true}, // a thin strip keeps one pixel
	}
	for _, c := range cases {
		w, h, down := fitSize(c.w, c.h)
		if w != c.wantW || h != c.wantH || down != c.down {
			t.Errorf("fitSize(%d, %d) = %d, %d, %v; want %d, %d, %v", c.w, c.h, w, h, down, c.wantW, c.wantH, c.down)
		}
	}
}

func TestJPEGMetadataIsStrippedAndDataKept(t *testing.T) {
	base := mustJPEG(t, photoLike(160, 120))

	jfif := jfifSegment()
	icc := segment(0xE2, append([]byte("ICC_PROFILE\x00\x01\x01"), bytes.Repeat([]byte{7}, 40)...))
	adobe := segment(0xEE, []byte("Adobe\x00\x64\x00\x00\x00\x00\x01"))
	exif := exifSegment(binary.LittleEndian, 1, "GPS 52.5200N 13.4050E")
	xmp := segment(markerAPP1, []byte("http://ns.adobe.com/xap/1.0/\x00<x:xmpmeta>GPS</x:xmpmeta>"))
	iptc := segment(markerAPP13, []byte("Photoshop 3.0\x008BIMGPS"))
	comment := segment(markerCOM, []byte("taken at home, GPS on"))

	in := insertAfterSOI(base, jfif, exif, xmp, icc, iptc, comment, adobe)
	if !bytes.Contains(in, []byte("GPS")) {
		t.Fatal("test input carries no GPS bytes")
	}
	want := insertAfterSOI(base, jfif, icc, adobe)

	out, ext := mustFit(t, in)
	if ext != ".jpg" {
		t.Fatalf("ext = %q, want .jpg", ext)
	}
	if bytes.Contains(out, []byte("GPS")) || bytes.Contains(out, []byte("Exif")) {
		t.Error("metadata is still in the output")
	}
	if !bytes.Equal(out, want) {
		t.Errorf("output differs from the input minus APP1, APP13 and COM (got %d bytes, want %d)", len(out), len(want))
	}
	if _, err := jpeg.Decode(bytes.NewReader(out)); err != nil {
		t.Errorf("output does not decode: %v", err)
	}
}

func TestPlainJPEGComesBackUnchanged(t *testing.T) {
	in := mustJPEG(t, photoLike(160, 120))
	out, ext := mustFit(t, in)
	if ext != ".jpg" || !bytes.Equal(out, in) {
		t.Error("a JPEG with no metadata and no resize should be returned as it is")
	}
}

func TestJPEGTrailingDataIsDropped(t *testing.T) {
	// Camera files append previews and video after the image, and those carry
	// their own metadata.
	base := mustJPEG(t, photoLike(64, 48))
	in := append(append([]byte{}, base...), []byte("GPS trailing preview")...)
	out, _ := mustFit(t, in)
	if !bytes.Equal(out, base) {
		t.Error("data after EOI should be dropped")
	}
}

// quadrants is red top left, green top right, blue bottom left and yellow
// bottom right, so a rotation shows up as colours changing corners.
func quadrants(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.RGBA{255, 0, 0, 255}
			switch {
			case x >= w/2 && y < h/2:
				c = color.RGBA{0, 255, 0, 255}
			case x < w/2 && y >= h/2:
				c = color.RGBA{0, 0, 255, 255}
			case x >= w/2 && y >= h/2:
				c = color.RGBA{255, 255, 0, 255}
			}
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

// nearColor reports whether the pixel at the middle of a quadrant is close to
// want, allowing for JPEG loss.
func nearColor(img image.Image, x, y int, want color.RGBA) bool {
	r, g, b, _ := img.At(x, y).RGBA()
	d := func(a uint32, w uint8) bool {
		return math.Abs(float64(a>>8)-float64(w)) < 40
	}
	return d(r, want.R) && d(g, want.G) && d(b, want.B)
}

func TestJPEGOrientationTurnsThePicture(t *testing.T) {
	red := color.RGBA{255, 0, 0, 255}
	green := color.RGBA{0, 255, 0, 255}
	blue := color.RGBA{0, 0, 255, 255}
	yellow := color.RGBA{255, 255, 0, 255}

	cases := []struct {
		name        string
		bo          binary.AppendByteOrder
		orientation uint16
		// Expected colours at top left, top right, bottom left, bottom right.
		want [4]color.RGBA
	}{
		{"6 little endian", binary.LittleEndian, 6, [4]color.RGBA{blue, red, yellow, green}},
		{"8 big endian", binary.BigEndian, 8, [4]color.RGBA{green, yellow, red, blue}},
		{"3", binary.LittleEndian, 3, [4]color.RGBA{yellow, blue, green, red}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			base := mustJPEG(t, quadrants(200, 100))
			in := insertAfterSOI(base, exifSegment(c.bo, c.orientation, "GPS 1,2"))

			out, ext := mustFit(t, in)
			if ext != ".jpg" {
				t.Fatalf("ext = %q, want .jpg", ext)
			}
			wantW, wantH := 200, 100
			if c.orientation >= 5 {
				wantW, wantH = 100, 200
			}
			img, err := jpeg.Decode(bytes.NewReader(out))
			if err != nil {
				t.Fatal(err)
			}
			if b := img.Bounds(); b.Dx() != wantW || b.Dy() != wantH {
				t.Fatalf("got %dx%d, want %dx%d", b.Dx(), b.Dy(), wantW, wantH)
			}
			if bytes.Contains(out, []byte("Exif")) || bytes.Contains(out, []byte("GPS")) {
				t.Error("re-encoded output still has metadata")
			}
			pts := [4]image.Point{{wantW / 4, wantH / 4}, {3 * wantW / 4, wantH / 4}, {wantW / 4, 3 * wantH / 4}, {3 * wantW / 4, 3 * wantH / 4}}
			for i, p := range pts {
				if !nearColor(img, p.X, p.Y, c.want[i]) {
					t.Errorf("corner %d (%v) is %v, want near %v", i, p, img.At(p.X, p.Y), c.want[i])
				}
			}
		})
	}
}

func TestJPEGOrientationAndDownscaleTogether(t *testing.T) {
	in := insertAfterSOI(mustJPEG(t, gradient(5000, 3000)), exifSegment(binary.LittleEndian, 6, ""))
	out, ext := mustFit(t, in)
	cfg, format := config(t, out)
	if ext != ".jpg" || format != "jpeg" || cfg.Width != 2304 || cfg.Height != 3840 {
		t.Errorf("got %s %s %dx%d, want .jpg jpeg 2304x3840", ext, format, cfg.Width, cfg.Height)
	}
}

func TestOrientTransforms(t *testing.T) {
	// The source is three wide and two tall:
	//   A B C
	//   D E F
	src := image.NewRGBA(image.Rect(0, 0, 3, 2))
	for i := 0; i < 6; i++ {
		src.SetRGBA(i%3, i/3, color.RGBA{uint8('A' + i), 0, 0, 255})
	}
	cases := []struct {
		o    int
		w, h int
		want string
	}{
		{1, 3, 2, "ABCDEF"},
		{2, 3, 2, "CBAFED"},
		{3, 3, 2, "FEDCBA"},
		{4, 3, 2, "DEFABC"},
		{5, 2, 3, "ADBECF"},
		{6, 2, 3, "DAEBFC"},
		{7, 2, 3, "FCEBDA"},
		{8, 2, 3, "CFBEAD"},
	}
	for _, c := range cases {
		got := orient(src, c.o)
		if b := got.Bounds(); b.Dx() != c.w || b.Dy() != c.h {
			t.Errorf("orientation %d: size %dx%d, want %dx%d", c.o, b.Dx(), b.Dy(), c.w, c.h)
			continue
		}
		var s []byte
		for y := 0; y < c.h; y++ {
			for x := 0; x < c.w; x++ {
				s = append(s, got.RGBAAt(x, y).R)
			}
		}
		if string(s) != c.want {
			t.Errorf("orientation %d: got %s, want %s", c.o, s, c.want)
		}
	}
}

func TestExifOrientation(t *testing.T) {
	tiffOf := func(bo binary.AppendByteOrder, o uint16) []byte {
		seg := exifSegment(bo, o, "x")
		return seg[4+6:] // skip marker, length and the "Exif" header
	}
	for _, bo := range []binary.AppendByteOrder{binary.LittleEndian, binary.BigEndian} {
		for o := uint16(1); o <= 8; o++ {
			if got := exifOrientation(tiffOf(bo, o)); got != int(o) {
				t.Errorf("orientation %d read as %d", o, got)
			}
		}
		for _, bad := range []uint16{0, 9, 300} {
			if got := exifOrientation(tiffOf(bo, bad)); got != 1 {
				t.Errorf("out of range value %d read as %d, want 1", bad, got)
			}
		}
	}
	// Truncated at every length, and pure noise, must not panic.
	full := tiffOf(binary.LittleEndian, 6)
	for n := 0; n < len(full); n++ {
		if got := exifOrientation(full[:n]); got != 1 && n < 8+2+12*2 {
			t.Errorf("truncated to %d bytes read as %d, want 1", n, got)
		}
	}
	rng := rand.New(rand.NewPCG(5, 6))
	for i := 0; i < 2000; i++ {
		junk := make([]byte, rng.IntN(64))
		for j := range junk {
			junk[j] = byte(rng.IntN(256))
		}
		copy(junk, "II*\x00")
		exifOrientation(junk)
	}
}

func TestParseJPEGSurvivesTruncation(t *testing.T) {
	base := mustJPEG(t, photoLike(32, 24))
	in := insertAfterSOI(base, exifSegment(binary.LittleEndian, 6, "GPS"), segment(markerCOM, []byte("hi")))
	for n := 0; n < len(in); n++ {
		if _, out, err := parseJPEG(in[:n]); err == nil {
			t.Errorf("a JPEG cut to %d of %d bytes was accepted (%d bytes out)", n, len(in), len(out))
		}
	}
	if _, _, err := parseJPEG(in); err != nil {
		t.Errorf("the whole file was rejected: %v", err)
	}
}

func TestTruncatedJPEGIsRefused(t *testing.T) {
	in := mustJPEG(t, photoLike(160, 120))
	_, _, err := Fit(in[:len(in)/2])
	if !errors.Is(err, ErrNotImage) {
		t.Errorf("err = %v, want ErrNotImage", err)
	}
}

func TestJPEGTheMarkerWalkCannotFollowIsReencoded(t *testing.T) {
	// Go's decoder skips a stray byte between segments, but the marker walk
	// cannot vouch for such a file. It must not be stored as it is, because
	// the metadata could not be removed with confidence, so the pixels are
	// re-encoded, which leaves no metadata behind.
	base := mustJPEG(t, photoLike(64, 48))
	in := insertAfterSOI(base, jfifSegment(), []byte{0x00}, exifSegment(binary.LittleEndian, 1, "GPS 1,2"))
	if _, _, err := parseJPEG(in); err == nil {
		t.Fatal("test input should defeat the marker walk")
	}

	out, ext := mustFit(t, in)
	if ext != ".jpg" {
		t.Fatalf("ext = %q, want .jpg", ext)
	}
	if bytes.Contains(out, []byte("GPS")) || bytes.Contains(out, []byte("Exif")) {
		t.Error("metadata survived")
	}
	if cfg, _ := config(t, out); cfg.Width != 64 || cfg.Height != 48 {
		t.Errorf("got %dx%d, want 64x48", cfg.Width, cfg.Height)
	}
}

// pngChunk builds one PNG chunk with its checksum.
func pngChunk(typ string, data []byte) []byte {
	out := binary.BigEndian.AppendUint32(nil, uint32(len(data)))
	out = append(out, typ...)
	out = append(out, data...)
	return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(out[4:]))
}

func pngHeader(w, h uint32, bitDepth, colorType byte) []byte {
	ihdr := binary.BigEndian.AppendUint32(nil, w)
	ihdr = binary.BigEndian.AppendUint32(ihdr, h)
	ihdr = append(ihdr, bitDepth, colorType, 0, 0, 0)
	return append([]byte("\x89PNG\r\n\x1a\n"), pngChunk("IHDR", ihdr)...)
}

// rawPNG writes a PNG from already filtered scanlines, for shapes Go's own
// encoder does not produce.
func rawPNG(t *testing.T, w, h uint32, bitDepth, colorType byte, scanlines []byte) []byte {
	t.Helper()
	var z bytes.Buffer
	zw, err := zlib.NewWriterLevel(&z, zlib.BestCompression)
	if err != nil {
		t.Fatal(err)
	}
	zw.Write(scanlines)
	zw.Close()
	out := pngHeader(w, h, bitDepth, colorType)
	out = append(out, pngChunk("IDAT", z.Bytes())...)
	return append(out, pngChunk("IEND", nil)...)
}

func TestHugeHeaderIsRefusedQuickly(t *testing.T) {
	// A PNG with only a header, claiming 20000 by 20000 (400 megapixels).
	hugePNG := pngHeader(20000, 20000, 8, 2)

	// A JPEG with just SOI, a JFIF header and a baseline frame header, claiming
	// the same. Go stops reading at the frame header only for JFIF files.
	sof := []byte{8}
	sof = binary.BigEndian.AppendUint16(sof, 20000)
	sof = binary.BigEndian.AppendUint16(sof, 20000)
	sof = append(sof, 3, 1, 0x22, 0, 2, 0x11, 1, 3, 0x11, 1)
	hugeJPEG := append([]byte{0xFF, 0xD8}, jfifSegment()...)
	hugeJPEG = append(hugeJPEG, segment(0xC0, sof)...)

	for name, in := range map[string][]byte{"png": hugePNG, "jpeg": hugeJPEG} {
		start := time.Now()
		_, _, err := Fit(in)
		if !errors.Is(err, ErrTooLarge) {
			t.Errorf("%s: err = %v, want ErrTooLarge", name, err)
		}
		if d := time.Since(start); d > 200*time.Millisecond {
			t.Errorf("%s: took %v, a header check should be instant", name, d)
		}
	}
}

func TestJustUnderTheLimitIsAccepted(t *testing.T) {
	// 10000 by 10000 is exactly 100 megapixels. Only the header is checked
	// here, since the body would need hundreds of megabytes.
	_, _, err := Fit(pngHeader(10000, 10000, 8, 2))
	if errors.Is(err, ErrTooLarge) {
		t.Error("exactly 100 megapixels should not be refused as too large")
	}
}

func TestNotImage(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 8))
	noise := make([]byte, 4096)
	for i := range noise {
		noise[i] = byte(rng.IntN(256))
	}
	cases := map[string][]byte{
		"random bytes":  noise,
		"nil":           nil,
		"empty":         {},
		"one byte":      {0xFF},
		"text":          []byte("hello, this is a note and not a picture"),
		"png signature": []byte("\x89PNG\r\n\x1a\n"),
		"bmp letters":   []byte("BM but nothing else that makes it a bitmap"),
		"riff not webp": []byte("RIFF\x00\x00\x00\x00WAVEfmt "),
	}
	for name, in := range cases {
		out, ext, err := Fit(in)
		if !errors.Is(err, ErrNotImage) {
			t.Errorf("%s: err = %v, want ErrNotImage", name, err)
		}
		if out != nil || ext != "" {
			t.Errorf("%s: got %d bytes and %q alongside the error", name, len(out), ext)
		}
	}
}

// ftyp builds the start of an ISO base media file with the given brands.
func ftyp(major string, compatible ...string) []byte {
	body := append([]byte(major), 0, 0, 0, 0)
	for _, b := range compatible {
		body = append(body, b...)
	}
	out := binary.BigEndian.AppendUint32(nil, uint32(8+len(body)))
	out = append(out, "ftyp"...)
	out = append(out, body...)
	// Some payload after the box, so a copy that stops early would show.
	return append(out, []byte("mdat and other boxes")...)
}

func TestAVIFAndHEICPassThrough(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		ext  string
	}{
		{"avif", ftyp("avif", "mif1", "miaf"), ".avif"},
		{"avif sequence", ftyp("avis", "msf1"), ".avif"},
		{"avif named only as compatible", ftyp("mif1", "miaf", "avif"), ".avif"},
		{"heic", ftyp("heic", "mif1", "heic"), ".heic"},
		{"heix", ftyp("heix", "mif1"), ".heic"},
		{"heic named only as compatible", ftyp("mif1", "heic"), ".heic"},
		{"avif with just the major brand", []byte("\x00\x00\x00\x0cftypavif"), ".avif"},
	}
	for _, c := range cases {
		out, ext, err := Fit(c.data)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if ext != c.ext || !bytes.Equal(out, c.data) {
			t.Errorf("%s: ext = %q, unchanged = %v; want %q and unchanged", c.name, ext, bytes.Equal(out, c.data), c.ext)
		}
	}

	// The generic brand alone does not say which of the two it is.
	for name, in := range map[string][]byte{
		"generic brand only": ftyp("mif1", "miaf"),
		"mp4":                ftyp("isom", "mp42"),
	} {
		if _, _, err := Fit(in); !errors.Is(err, ErrNotImage) {
			t.Errorf("%s: err = %v, want ErrNotImage", name, err)
		}
	}
}

func TestGIFIsKeptAsIs(t *testing.T) {
	// Wider than the cap on purpose: a GIF may be animated, so it is never
	// resized.
	frame := image.NewPaletted(image.Rect(0, 0, 4000, 10), color.Palette{color.Black, color.White})
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, &gif.GIF{
		Image: []*image.Paletted{frame, frame},
		Delay: []int{5, 5},
	}); err != nil {
		t.Fatal(err)
	}
	out, ext := mustFit(t, buf.Bytes())
	if ext != ".gif" || !bytes.Equal(out, buf.Bytes()) {
		t.Errorf("ext = %q, unchanged = %v; want .gif and unchanged", ext, bytes.Equal(out, buf.Bytes()))
	}
}

// vp8lWebP writes a lossless WebP of one flat colour. Every prefix code has a
// single symbol, so the pixel data takes no bits and any size is a few dozen
// bytes.
func vp8lWebP(w, h int, c color.NRGBA) []byte {
	var bits []byte
	var n uint
	put := func(v uint32, count uint) {
		for i := uint(0); i < count; i++ {
			if n%8 == 0 {
				bits = append(bits, 0)
			}
			if v>>i&1 == 1 {
				bits[len(bits)-1] |= 1 << (n % 8)
			}
			n++
		}
	}
	put(0x2F, 8) // VP8L signature
	put(uint32(w-1), 14)
	put(uint32(h-1), 14)
	if c.A != 255 {
		put(1, 1) // alpha is used
	} else {
		put(0, 1)
	}
	put(0, 3) // version
	put(0, 1) // no transform
	put(0, 1) // no colour cache
	put(0, 1) // no meta prefix codes
	// Green, red, blue, alpha and distance codes, each a "simple" code with
	// one symbol given in eight bits.
	for _, sym := range []uint8{c.G, c.R, c.B, c.A, 0} {
		put(1, 1)
		put(0, 1)
		put(1, 1)
		put(uint32(sym), 8)
	}

	chunk := []byte("VP8L")
	chunk = binary.LittleEndian.AppendUint32(chunk, uint32(len(bits)))
	chunk = append(chunk, bits...)
	if len(bits)%2 == 1 {
		chunk = append(chunk, 0)
	}
	out := []byte("RIFF")
	out = binary.LittleEndian.AppendUint32(out, uint32(4+len(chunk)))
	out = append(out, "WEBP"...)
	return append(out, chunk...)
}

func TestWebP(t *testing.T) {
	opaque := color.NRGBA{200, 60, 30, 255}
	clear := color.NRGBA{200, 60, 30, 128}

	t.Run("kept as is", func(t *testing.T) {
		in := vp8lWebP(300, 200, opaque)
		out, ext := mustFit(t, in)
		if ext != ".webp" || !bytes.Equal(out, in) {
			t.Errorf("ext = %q, unchanged = %v; want .webp and unchanged", ext, bytes.Equal(out, in))
		}
	})
	t.Run("large and opaque becomes JPEG", func(t *testing.T) {
		out, ext := mustFit(t, vp8lWebP(5000, 100, opaque))
		cfg, format := config(t, out)
		if ext != ".jpg" || format != "jpeg" || cfg.Width != 3840 || cfg.Height != 77 {
			t.Errorf("got %s %s %dx%d, want .jpg jpeg 3840x77", ext, format, cfg.Width, cfg.Height)
		}
	})
	t.Run("large with transparency becomes PNG", func(t *testing.T) {
		out, ext := mustFit(t, vp8lWebP(5000, 100, clear))
		cfg, format := config(t, out)
		if ext != ".png" || format != "png" || cfg.Width != 3840 || cfg.Height != 77 {
			t.Errorf("got %s %s %dx%d, want .png png 3840x77", ext, format, cfg.Width, cfg.Height)
		}
	})
}

func TestBMPAndTIFFBecomePNG(t *testing.T) {
	img := screenshotLike(300, 200)
	var b, tf bytes.Buffer
	if err := bmp.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	if err := tiff.Encode(&tf, img, nil); err != nil {
		t.Fatal(err)
	}
	for name, in := range map[string][]byte{"bmp": b.Bytes(), "tiff": tf.Bytes()} {
		out, ext := mustFit(t, in)
		cfg, format := config(t, out)
		if ext != ".png" || format != "png" || cfg.Width != 300 || cfg.Height != 200 {
			t.Errorf("%s: got %s %s %dx%d, want .png png 300x200", name, ext, format, cfg.Width, cfg.Height)
		}
		if len(out) >= len(in) {
			t.Errorf("%s: output is %d bytes, input was %d", name, len(out), len(in))
		}
	}
}

func TestIsOpaque(t *testing.T) {
	rgba := image.NewRGBA(image.Rect(0, 0, 4, 4))
	if isOpaque(rgba) {
		t.Error("a blank RGBA image is transparent")
	}
	for i := 3; i < len(rgba.Pix); i += 4 {
		rgba.Pix[i] = 255
	}
	if !isOpaque(rgba) {
		t.Error("an RGBA image with full alpha is opaque")
	}
	rgba.Pix[4*5+3] = 254
	if isOpaque(rgba) {
		t.Error("one see-through pixel makes the image not opaque")
	}

	// A type with no Opaque method takes the scan.
	if !isOpaque(plain{image.NewGray(image.Rect(0, 0, 3, 3))}) {
		t.Error("gray without an Opaque method should scan as opaque")
	}
	if isOpaque(plain{image.NewNRGBA(image.Rect(0, 0, 3, 3))}) {
		t.Error("blank NRGBA without an Opaque method should scan as transparent")
	}
}

// insertPNGChunks puts chunks straight after the IHDR chunk, which is 8 bytes
// of signature and 25 of header chunk.
func insertPNGChunks(data []byte, chunks ...[]byte) []byte {
	out := append([]byte{}, data[:33]...)
	for _, c := range chunks {
		out = append(out, c...)
	}
	return append(out, data[33:]...)
}

func TestKeptPNGLosesItsTextAndMetadataChunks(t *testing.T) {
	base := grayAlphaPNG(t)

	phys := pngChunk("pHYs", []byte{0, 0, 0x0B, 0x13, 0, 0, 0x0B, 0x13, 1})
	private := [][]byte{
		pngChunk("tEXt", []byte("Comment\x00GPS 52.5200N 13.4050E")),
		pngChunk("eXIf", exifSegment(binary.BigEndian, 1, "GPS 52.5200N")[10:]),
		pngChunk("zTXt", []byte("Note\x00\x00\x78\x9c\x03\x00\x00\x00\x00\x01")),
		pngChunk("iTXt", []byte("Title\x00\x00\x00\x00\x00GPS private")),
		pngChunk("tIME", []byte{0x07, 0xEA, 9, 29, 12, 0, 0}),
	}
	in := insertPNGChunks(base, append([][]byte{phys}, private...)...)
	in = append(in, []byte("GPS after IEND")...)
	want := insertPNGChunks(base, phys)

	inImg, err := png.Decode(bytes.NewReader(in))
	if err != nil {
		t.Fatalf("test input does not decode: %v", err)
	}

	out, ext := mustFit(t, in)
	if ext != ".png" {
		t.Fatalf("ext = %q, want .png", ext)
	}
	if bytes.Contains(out, []byte("GPS")) {
		t.Error("private text is still in the output")
	}
	for _, typ := range []string{"tEXt", "eXIf", "zTXt", "iTXt", "tIME"} {
		if bytes.Contains(out, []byte(typ)) {
			t.Errorf("%s chunk is still in the output", typ)
		}
	}
	if !bytes.Equal(out, want) {
		t.Errorf("output is not the input minus the private chunks (got %d bytes, want %d)", len(out), len(want))
	}
	outImg, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("output does not decode: %v", err)
	}
	if b := outImg.Bounds(); b != inImg.Bounds() {
		t.Fatalf("bounds changed from %v to %v", inImg.Bounds(), b)
	}
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			if outImg.At(x, y) != inImg.At(x, y) {
				t.Fatalf("pixel %d,%d changed", x, y)
			}
		}
	}
}

func TestStripPNGRefusesWhatItCannotFollow(t *testing.T) {
	in := insertPNGChunks(grayAlphaPNG(t), pngChunk("tEXt", []byte("a\x00b")))
	if _, err := stripPNG(in); err != nil {
		t.Fatalf("the whole file was rejected: %v", err)
	}
	// Every cut short of the whole file loses IEND or part of a chunk.
	for n := 0; n < len(in); n++ {
		if _, err := stripPNG(in[:n]); err == nil {
			t.Errorf("a PNG cut to %d of %d bytes was accepted", n, len(in))
		}
	}
	// A length that runs past the end of the data.
	bad := append([]byte{}, in...)
	binary.BigEndian.PutUint32(bad[33:], 0xFFFFFFF0)
	if _, err := stripPNG(bad); err == nil {
		t.Error("a chunk longer than the file was accepted")
	}
}

// webpChunk builds one RIFF chunk, padded to an even length.
func webpChunk(fourcc string, data []byte) []byte {
	out := []byte(fourcc)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(data)))
	out = append(out, data...)
	if len(data)%2 == 1 {
		out = append(out, 0)
	}
	return out
}

// riffWebP wraps chunks in a RIFF header with the right size.
func riffWebP(chunks ...[]byte) []byte {
	out := []byte("RIFF\x00\x00\x00\x00WEBP")
	for _, c := range chunks {
		out = append(out, c...)
	}
	binary.LittleEndian.PutUint32(out[4:], uint32(len(out)-8))
	return out
}

// vp8x is the extended header chunk for a canvas of w by h.
func vp8x(flags byte, w, h int) []byte {
	d := []byte{flags, 0, 0, 0}
	d = append(d, byte(w-1), byte((w-1)>>8), byte((w-1)>>16))
	d = append(d, byte(h-1), byte((h-1)>>8), byte((h-1)>>16))
	return webpChunk("VP8X", d)
}

func TestKeptWebPLosesItsEXIFAndXMP(t *testing.T) {
	const (
		iccFlag  = 0x20
		xmpFlag  = 0x04
		exifFlag = 0x08
	)
	// The image chunk comes from a real lossless bitstream, so the whole file
	// can be decoded before and after.
	pixels := vp8lWebP(300, 200, color.NRGBA{200, 60, 30, 255})[12:]
	icc := webpChunk("ICCP", []byte("a profile of odd length"))
	// An odd length, so the pad byte has to go with the chunk.
	exif := webpChunk("EXIF", []byte("II*\x00GPS 52.5200N"))
	xmp := webpChunk("XMP ", []byte("<x:xmpmeta>GPS</x:xmpmeta>"))

	in := riffWebP(vp8x(iccFlag|exifFlag|xmpFlag, 300, 200), icc, pixels, exif, xmp)
	want := riffWebP(vp8x(iccFlag, 300, 200), icc, pixels)
	if !bytes.Contains(in, []byte("GPS")) {
		t.Fatal("test input carries no GPS bytes")
	}
	if _, _, err := image.Decode(bytes.NewReader(in)); err != nil {
		t.Fatalf("test input is not a readable WebP: %v", err)
	}

	out, ext := mustFit(t, in)
	if ext != ".webp" {
		t.Fatalf("ext = %q, want .webp", ext)
	}
	if bytes.Contains(out, []byte("EXIF")) || bytes.Contains(out, []byte("XMP ")) || bytes.Contains(out, []byte("GPS")) {
		t.Error("metadata is still in the output")
	}
	if flags := out[12+8]; flags != iccFlag {
		t.Errorf("VP8X flags = %#x, want %#x", flags, iccFlag)
	}
	if got := binary.LittleEndian.Uint32(out[4:]); int(got) != len(out)-8 {
		t.Errorf("RIFF size = %d, want %d", got, len(out)-8)
	}
	if !bytes.Equal(out, want) {
		t.Errorf("output is not the input minus the metadata (got %d bytes, want %d)", len(out), len(want))
	}
	img, _, err := image.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("output does not decode: %v", err)
	}
	if b := img.Bounds(); b.Dx() != 300 || b.Dy() != 200 {
		t.Errorf("output decodes to %v, want 300x200", b)
	}
}

func TestPlainWebPIsUnchangedByStripping(t *testing.T) {
	in := vp8lWebP(40, 30, color.NRGBA{1, 2, 3, 255})
	out, err := stripWebP(in)
	if err != nil || !bytes.Equal(out, in) {
		t.Errorf("a WebP with no VP8X header should come back as it is (err = %v)", err)
	}
}

func TestStripWebPRefusesWhatItCannotFollow(t *testing.T) {
	good := riffWebP(vp8x(0x08, 300, 200), vp8lWebP(300, 200, color.NRGBA{1, 2, 3, 255})[12:], webpChunk("EXIF", []byte("abc")))
	if _, err := stripWebP(good); err != nil {
		t.Fatalf("the whole file was rejected: %v", err)
	}
	// Any cut leaves the RIFF size claiming more than there is.
	for _, n := range []int{0, 4, 11, 12, 15, 20, len(good) - 1} {
		if _, err := stripWebP(good[:n]); err == nil {
			t.Errorf("a WebP cut to %d of %d bytes was accepted", n, len(good))
		}
	}
	// A chunk that claims to be longer than the RIFF payload.
	bad := append([]byte{}, good...)
	binary.LittleEndian.PutUint32(bad[16:], 0x7FFFFFF0)
	if _, err := stripWebP(bad); err == nil {
		t.Error("a chunk longer than the file was accepted")
	}
	// A VP8X chunk that is not ten bytes long.
	if _, err := stripWebP(riffWebP(webpChunk("VP8X", []byte{8, 0, 0}))); err == nil {
		t.Error("a short VP8X chunk was accepted")
	}
}

func TestWebPWithBadStructureIsReencoded(t *testing.T) {
	// Three stray bytes after the EXIF chunk, counted in the RIFF size. The
	// decoder stops at the image chunk and never sees them, but the chunk walk
	// cannot vouch for the file, so the EXIF chunk must not be stored as it is.
	in := riffWebP(
		vp8x(0x08, 300, 200),
		vp8lWebP(300, 200, color.NRGBA{200, 60, 30, 255})[12:],
		webpChunk("EXIF", []byte("GPS 52.5200N")),
		[]byte{1, 2, 3},
	)
	if _, err := stripWebP(in); err == nil {
		t.Fatal("test input should defeat the chunk walk")
	}
	out, ext := mustFit(t, in)
	if ext != ".jpg" {
		t.Fatalf("ext = %q, want .jpg", ext)
	}
	if bytes.Contains(out, []byte("GPS")) {
		t.Error("metadata survived")
	}
	if cfg, _ := config(t, out); cfg.Width != 300 || cfg.Height != 200 {
		t.Errorf("got %dx%d, want 300x200", cfg.Width, cfg.Height)
	}
}

// plain hides the Opaque method of whatever it wraps.
type plain struct{ m image.Image }

func (p plain) ColorModel() color.Model { return p.m.ColorModel() }
func (p plain) Bounds() image.Rectangle { return p.m.Bounds() }
func (p plain) At(x, y int) color.Color { return p.m.At(x, y) }
