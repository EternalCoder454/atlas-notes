// Package imagefit prepares an image the user pasted or dropped into a note for
// storage in the vault: capped in size, free of private metadata, the right way
// up and in a compact format. It has no GTK dependency, so the desktop app and
// the Android build share it.
package imagefit

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"

	xdraw "golang.org/x/image/draw"

	// Registered so image.DecodeConfig and image.Decode understand these
	// formats. GIF is never decoded here, but a header check still tells a
	// real GIF from a file that only starts with the right letters.
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
	_ "image/gif"
)

// ErrNotImage means the bytes are not an image format this package knows, or
// they claim to be one and cannot be read as one.
var ErrNotImage = errors.New("this is not an image that can be added to a note")

// ErrTooLarge means the image has more pixels than can be decoded safely.
var ErrTooLarge = errors.New("this image is too large (over 100 megapixels)")

const (
	// maxSide caps the longest side. A 4K screen is 3840 wide, so a larger
	// image cannot look any sharper in a note.
	maxSide = 3840

	// maxPixels is checked against the header before anything is decoded. A
	// tiny file can claim a huge canvas, and decoding it would exhaust memory.
	maxPixels = 100_000_000

	// jpegQuality is high enough that photos show no visible loss, and it is
	// where JPEG size stops falling quickly.
	jpegQuality = 88

	// A JPEG replaces the PNG only when it is under this percentage of the
	// PNG's size. A win that big means photographic content. Screenshots and
	// diagrams compress well as PNG, and JPEG would blur their text and edges
	// for little gain.
	jpegWinPercent = 60
)

// kind is the image format found from the leading bytes of the input.
type kind int

const (
	kindUnknown kind = iota
	kindPNG
	kindJPEG
	kindGIF
	kindWebP
	kindBMP
	kindTIFF
	kindAVIF
	kindHEIC
)

// Fit returns the bytes to store and the file extension to store them under
// (".png", ".jpg", ".gif", ".webp", ".avif", ".heic"). The returned slice may
// be data itself when nothing needed to change, so callers must not modify it.
//
// GIF, AVIF and HEIC come back as they are: GIF may be animated, and Go cannot
// decode the other two. A JPEG with no rotation and no resize needed keeps its
// compressed data and only loses its metadata, and so does a WebP that needs no
// resize, or a PNG that is already smaller than Go can make it. Everything else
// is decoded, resized to at most 3840 pixels on its longest side and stored as
// whichever of PNG and JPEG suits the content.
func Fit(data []byte) (out []byte, ext string, err error) {
	k := sniff(data)
	switch k {
	case kindUnknown:
		return nil, "", ErrNotImage
	case kindAVIF:
		return data, ".avif", nil
	case kindHEIC:
		return data, ".heic", nil
	}

	// The header alone gives the size, so a decompression bomb is refused
	// before any pixel memory is allocated.
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("%w: %w", ErrNotImage, err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, "", ErrNotImage
	}
	if int64(cfg.Width)*int64(cfg.Height) > maxPixels {
		return nil, "", ErrTooLarge
	}

	// An animated GIF would lose its frames if it were decoded and resized,
	// so a GIF is stored as it came, whatever its size.
	if k == kindGIF {
		return data, ".gif", nil
	}

	w, h, down := fitSize(cfg.Width, cfg.Height)

	switch k {
	case kindJPEG:
		return fitJPEG(data, w, h, down)
	case kindWebP:
		// A WebP that is small enough keeps its compressed data and loses its
		// EXIF and XMP chunks. When the chunks cannot be walked it is decoded
		// and encoded again, as a downscaled one is, which drops all metadata.
		if !down {
			if kept, err := stripWebP(data); err == nil {
				return kept, ".webp", nil
			}
		}
		img, err := decode(data)
		if err != nil {
			return nil, "", err
		}
		if down {
			img = downscale(img, w, h)
		}
		if isOpaque(img) {
			return encodeJPEG(img)
		}
		return encodePNG(img)
	}
	return fitLossless(data, k == kindPNG, w, h, down)
}

// fitJPEG handles JPEG input. A file that is upright and small enough keeps its
// compressed data and only has its metadata removed. Otherwise the pixels have
// to change, so the image is decoded, turned and resized, and encoded again.
// Go's encoder writes no EXIF, so nothing needs stripping on that path.
func fitJPEG(data []byte, w, h int, down bool) ([]byte, string, error) {
	orientation, stripped, err := parseJPEG(data)
	if err == nil && orientation == 1 && !down {
		return stripped, ".jpg", nil
	}
	// When the markers cannot be followed the metadata cannot be removed
	// safely, and a re-encode drops all of it, so that is the fallback.

	img, err := decode(data)
	if err != nil {
		return nil, "", err
	}
	// Shrinking first keeps the rotation, which touches every pixel, to at
	// most 3840 by 3840. A resize is the same before or after a rotation.
	if down {
		img = downscale(img, w, h)
	}
	if orientation != 1 {
		img = orient(toRGBA(img), orientation)
	}
	return encodeJPEG(img)
}

// fitLossless handles PNG, BMP and TIFF, which is what a clipboard paste
// arrives as. The image is lossless on the way in, so it is stored as PNG
// unless it is opaque and a JPEG is much smaller, which means it is a photo.
func fitLossless(data []byte, isPNG bool, w, h int, down bool) ([]byte, string, error) {
	img, err := decode(data)
	if err != nil {
		return nil, "", err
	}
	if down {
		img = downscale(img, w, h)
	}

	best, _, err := encodePNG(img)
	if err != nil {
		return nil, "", err
	}
	ext := ".png"
	if isOpaque(img) {
		jpg, _, err := encodeJPEG(img)
		if err != nil {
			return nil, "", err
		}
		if len(jpg)*100 < len(best)*jpegWinPercent {
			best, ext = jpg, ".jpg"
		}
	}

	// Another program may have compressed the PNG harder than Go can, and
	// re-encoding it would only make it bigger. Its bytes are as good as ours
	// when the pixels are not changing, once the text and metadata chunks are
	// out of them. The comparison is made on what would be stored, and a file
	// whose chunks cannot be walked gives way to the re-encode, which has no
	// metadata.
	if isPNG && !down {
		if kept, err := stripPNG(data); err == nil && len(kept) < len(best) {
			return kept, ".png", nil
		}
	}
	return best, ext, nil
}

// sniff finds the format from the leading bytes, never from a file name, since
// pasted data has none.
func sniff(b []byte) kind {
	switch {
	case bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")):
		return kindPNG
	case bytes.HasPrefix(b, []byte{0xFF, 0xD8, 0xFF}):
		return kindJPEG
	case bytes.HasPrefix(b, []byte("GIF87a")), bytes.HasPrefix(b, []byte("GIF89a")):
		return kindGIF
	case len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return kindWebP
	case bytes.HasPrefix(b, []byte("BM")):
		return kindBMP
	case bytes.HasPrefix(b, []byte("II*\x00")), bytes.HasPrefix(b, []byte("MM\x00*")):
		return kindTIFF
	}
	return sniffISOBMFF(b)
}

// sniffISOBMFF recognises AVIF and HEIC, which are both ISO base media files
// that begin with an "ftyp" box naming their brands. The first brand that is
// one of ours decides. The generic "mif1" brand is shared by both formats, so
// a file that names nothing more specific cannot be told apart and is left
// alone.
func sniffISOBMFF(b []byte) kind {
	if len(b) < 12 || string(b[4:8]) != "ftyp" {
		return kindUnknown
	}
	end := len(b)
	if size := int64(binary.BigEndian.Uint32(b)); size >= 16 && size < int64(end) {
		end = int(size)
	}
	// The major brand comes first, then a four-byte minor version that is
	// skipped, then the compatible brands.
	if k := brandKind(b[8:12]); k != kindUnknown {
		return k
	}
	for at := 16; at+4 <= end; at += 4 {
		if k := brandKind(b[at : at+4]); k != kindUnknown {
			return k
		}
	}
	return kindUnknown
}

func brandKind(brand []byte) kind {
	switch string(brand) {
	case "avif", "avis":
		return kindAVIF
	case "heic", "heix", "hevc", "hevx", "heim", "heis", "hevm", "hevs":
		return kindHEIC
	}
	return kindUnknown
}

// fitSize returns the size to store at, and whether that is a downscale. The
// aspect ratio is kept, and neither side goes below one pixel.
func fitSize(w, h int) (int, int, bool) {
	long := max(w, h)
	if long <= maxSide {
		return w, h, false
	}
	// Integer maths with rounding, in 64 bits so 32-bit builds cannot overflow.
	scale := func(side int) int {
		v := (int64(side)*maxSide + int64(long)/2) / int64(long)
		return max(int(v), 1)
	}
	if w >= h {
		return maxSide, scale(h), true
	}
	return scale(w), maxSide, true
}

func decode(data []byte) (image.Image, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotImage, err)
	}
	return img, nil
}

// downscale resizes with Catmull-Rom, which stays sharp on text and photos
// alike. An opaque image lands in an RGBA image, which the scaler and both
// encoders have fast paths for. One with transparency lands in NRGBA so the
// colour of nearly clear pixels is not rounded away.
func downscale(src image.Image, w, h int) image.Image {
	r := image.Rect(0, 0, w, h)
	if isOpaque(src) {
		dst := image.NewRGBA(r)
		xdraw.CatmullRom.Scale(dst, r, src, src.Bounds(), xdraw.Src, nil)
		return dst
	}
	dst := image.NewNRGBA(r)
	xdraw.CatmullRom.Scale(dst, r, src, src.Bounds(), xdraw.Src, nil)
	return dst
}

// isOpaque reports whether every pixel is fully opaque. The standard image
// types answer this themselves, and most do it without visiting pixels (a
// paletted image only looks at its palette). Anything else has its alpha
// channel scanned.
func isOpaque(img image.Image) bool {
	if o, ok := img.(interface{ Opaque() bool }); ok {
		return o.Opaque()
	}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if _, _, _, a := img.At(x, y).RGBA(); a != 0xFFFF {
				return false
			}
		}
	}
	return true
}

// toRGBA returns img as an RGBA image whose bounds start at the origin. Both
// the JPEG encoder and the rotation below are much faster on that layout than
// on the generic image.Image interface.
func toRGBA(img image.Image) *image.RGBA {
	if r, ok := img.(*image.RGBA); ok && r.Rect.Min == (image.Point{}) {
		return r
	}
	b := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	xdraw.Copy(dst, image.Point{}, img, b, xdraw.Src, nil)
	return dst
}

// orient applies an EXIF orientation (2 to 8) so the pixels come out upright.
// For each value src(x, y) is the pixel that lands at dst(x, y), with the
// source sw wide and sh tall:
//
//	2 mirror left to right    dst(x, y) = src(sw-1-x, y)
//	3 rotate 180              dst(x, y) = src(sw-1-x, sh-1-y)
//	4 mirror top to bottom    dst(x, y) = src(x, sh-1-y)
//	5 transpose               dst(x, y) = src(y, x)
//	6 rotate 90 clockwise     dst(x, y) = src(y, sh-1-x)
//	7 transverse              dst(x, y) = src(sw-1-y, sh-1-x)
//	8 rotate 90 anticlockwise dst(x, y) = src(sw-1-y, x)
//
// Values from 5 to 8 swap width and height. Anything else returns src.
func orient(src *image.RGBA, o int) *image.RGBA {
	if o < 2 || o > 8 {
		return src
	}
	sw, sh := src.Rect.Dx(), src.Rect.Dy()
	dw, dh := sw, sh
	if o >= 5 {
		dw, dh = sh, sw
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		row := dst.Pix[y*dst.Stride:]
		for x := 0; x < dw; x++ {
			var sx, sy int
			switch o {
			case 2:
				sx, sy = sw-1-x, y
			case 3:
				sx, sy = sw-1-x, sh-1-y
			case 4:
				sx, sy = x, sh-1-y
			case 5:
				sx, sy = y, x
			case 6:
				sx, sy = y, sh-1-x
			case 7:
				sx, sy = sw-1-y, sh-1-x
			case 8:
				sx, sy = sw-1-y, x
			}
			copy(row[x*4:x*4+4], src.Pix[sy*src.Stride+sx*4:])
		}
	}
	return dst
}

// encodeJPEG and encodePNG also return the extension of what they wrote, so a
// caller can return the result of either straight from Fit.
func encodeJPEG(img image.Image) ([]byte, string, error) {
	// The encoder reads *image.RGBA directly but goes through the slow
	// generic path for other types.
	if _, ok := img.(*image.RGBA); !ok {
		img = toRGBA(img)
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), ".jpg", nil
}

func encodePNG(img image.Image) ([]byte, string, error) {
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(&buf, img); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), ".png", nil
}
