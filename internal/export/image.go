package export

import (
	"bytes"
	"image"
	_ "image/gif" // registered so image.DecodeConfig can read a GIF's size
	_ "image/jpeg"
	_ "image/png"
	"math"
	"net/http"
	"path"
)

// picture is an image a note refers to, read and checked, ready to be put in a
// document.
type picture struct {
	data []byte
	mime string // image/png, image/jpeg, image/gif or image/webp
	ext  string // the file extension for it, without the dot
	w, h int    // in pixels
}

// newPicture checks that data is a picture a document can hold: one of the
// four kinds every browser and office program shows, whose header says how big
// it is. It returns nil otherwise. The type comes from the bytes and not from
// the name the note used, because a file called photo.png can be anything, and
// an SVG is left out on purpose: it can carry script, and a web page that
// embeds one runs it in the reader's browser.
func newPicture(data []byte) *picture {
	p := &picture{data: data, mime: http.DetectContentType(data)}
	switch p.mime {
	case "image/png":
		p.ext = "png"
	case "image/jpeg":
		p.ext = "jpg"
	case "image/gif":
		p.ext = "gif"
	case "image/webp":
		p.ext = "webp"
	default:
		return nil
	}
	if p.ext == "webp" {
		p.w, p.h = webpSize(data)
	} else if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
		p.w, p.h = cfg.Width, cfg.Height
	}
	if p.w <= 0 || p.h <= 0 {
		return nil // damaged, or cut short: better the alt text than a broken image
	}
	return p
}

// webpSize reads a WebP's size from its header. The standard library has no
// WebP decoder, and the header is all that is needed to size the picture. It
// returns 0, 0 for anything it cannot read.
func webpSize(b []byte) (w, h int) {
	if len(b) < 30 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WEBP" {
		return 0, 0
	}
	switch string(b[12:16]) {
	case "VP8 ": // lossy: after the frame tag and start code, two 14-bit sizes
		if b[23] != 0x9d || b[24] != 0x01 || b[25] != 0x2a {
			return 0, 0
		}
		return int(b[26]) | int(b[27]&0x3f)<<8, int(b[28]) | int(b[29]&0x3f)<<8
	case "VP8L": // lossless: a signature byte, then two 14-bit sizes, less one
		if b[20] != 0x2f {
			return 0, 0
		}
		bits := uint32(b[21]) | uint32(b[22])<<8 | uint32(b[23])<<16 | uint32(b[24])<<24
		return int(bits&0x3fff) + 1, int(bits>>14&0x3fff) + 1
	case "VP8X": // extended: the canvas, as two 24-bit sizes, less one
		return (int(b[24]) | int(b[25])<<8 | int(b[26])<<16) + 1,
			(int(b[27]) | int(b[28])<<8 | int(b[29])<<16) + 1
	}
	return 0, 0
}

// A picture is drawn at the size it has on a screen of 96 dots to the inch,
// shrunk to fit the page if it is bigger. The page is the one a Word or
// OpenDocument file gets with no settings of its own, which has about 6.5
// inches across and 9 down inside its margins; 6 inches across leaves room. The
// height is capped too: Word will not open a picture taller than 56 inches, and
// a very thin image would otherwise make a file it refuses.
const (
	pixelsPerInch    = 96
	maxPictureWidth  = 6.0
	maxPictureHeight = 9.0
	emuPerInch       = 914400
)

// inches is the picture's size on the page, with its proportions kept.
func (p *picture) inches() (w, h float64) {
	w, h = float64(p.w)/pixelsPerInch, float64(p.h)/pixelsPerInch
	scale := 1.0
	if w > maxPictureWidth {
		scale = maxPictureWidth / w
	}
	if h*scale > maxPictureHeight {
		scale = maxPictureHeight / h
	}
	return w * scale, h * scale
}

// emu is a length in inches in English Metric Units, which Word measures in,
// never less than 1: a picture of no size is one Word will not draw.
func emu(inches float64) int64 {
	return max(1, int64(math.Round(inches*emuPerInch)))
}

// imageLabel is what stands for an image that cannot be shown: its alt text,
// or its file name when the note gave none.
func imageLabel(alt, src string) string {
	if alt != "" {
		return alt
	}
	return path.Base(src)
}

// withImages settles every image block. One whose picture can be had and
// checked keeps it; any other becomes a paragraph of its label, so a missing,
// damaged or unsupported image costs a note its picture and never its export.
// Each path is asked for once, however often the note shows it.
func withImages(blocks []block, opt Options) []block {
	out := make([]block, len(blocks))
	pics := map[string]*picture{}
	for i, bl := range blocks {
		if bl.kind == code && opt.Diagram != nil && isMermaid(bl) {
			if data, _, _, err := opt.Diagram(bl.code); err == nil {
				if pic := newPicture(data); pic != nil {
					out[i] = block{kind: figure, src: "diagram", alt: "Diagram", pic: pic}
					continue
				}
			}
		}
		if bl.kind == figure {
			pic, seen := pics[bl.src]
			if !seen && opt.Image != nil {
				if data, err := opt.Image(bl.src); err == nil {
					pic = newPicture(data)
				}
			}
			pics[bl.src] = pic
			if pic != nil {
				bl.pic = pic
			} else {
				bl = block{kind: paragraph, lines: [][]inline{{{text: imageLabel(bl.alt, bl.src)}}}}
			}
		}
		out[i] = bl
	}
	return out
}
