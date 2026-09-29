package export

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// everything is a note that uses every piece of Markdown the exporter knows,
// and a few things it has to get right that are easy to get wrong.
const everything = `# Trip plan

Leaving on **Friday**, back *Sunday*. Bring ~~the tent~~ a *warm* coat.
A second line of the same paragraph, with ` + "`inline code`" + ` and a [link](https://example.org/a?b=1&c=2).

## Packing
- Boots
- Socks
  - Wool, not cotton
- [ ] Book the ferry <!-- priority:high due:2026-10-03 -->
- [x] Charge the camera <!-- priority:low -->

### Route
1. North on the coast road
2. Ferry at 10
3. Walk to the hut

> Take the ferry, not the bridge.
> The bridge is closed until spring.

---

` + "```go\nfunc main() {\n\tfmt.Println(\"a < b && c > d\")\n    indented\n}\n```" + `

Ends with a character XML cannot hold: ` + "\x01" + ` and an ampersand & a <tag>.
`

func TestParseUnderstandsTheNote(t *testing.T) {
	bl := parse(everything)
	var kinds []kind
	for _, b := range bl {
		kinds = append(kinds, b.kind)
	}
	want := []kind{heading, paragraph, heading, bullet, bullet, bullet, task, task,
		heading, numbered, numbered, numbered, quote, divider, code, paragraph}
	if len(kinds) != len(want) {
		t.Fatalf("got %d blocks %v, want %d", len(kinds), kinds, len(want))
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Errorf("block %d is %v, want %v", i, kinds[i], want[i])
		}
	}
	if bl[1].lines == nil || len(bl[1].lines) != 2 {
		t.Errorf("a paragraph's second line should stay a line of it: %+v", bl[1].lines)
	}
	if bl[5].level != 1 {
		t.Errorf("a nested bullet has depth %d", bl[5].level)
	}
	if bl[6].detail != "high priority, due 3 October 2026" || bl[6].checked {
		t.Errorf("task detail: %q, checked %v", bl[6].detail, bl[6].checked)
	}
	if !bl[7].checked || bl[7].detail != "low priority" {
		t.Errorf("done task: %+v", bl[7])
	}
	if len(bl[12].lines) != 2 {
		t.Errorf("a two-line quote became %d lines", len(bl[12].lines))
	}
	if !strings.Contains(bl[14].code, "\tfmt.Println") || bl[14].lang != "go" {
		t.Errorf("code block lost its indentation or language: %q %q", bl[14].code, bl[14].lang)
	}
}

func TestInlineFormatting(t *testing.T) {
	runs := parseInline("a **b *c* d** ~~e~~ `*f*` [g **h**](http://x) *")
	var got []string
	for _, r := range runs {
		tag := ""
		if r.bold {
			tag += "B"
		}
		if r.italic {
			tag += "I"
		}
		if r.strike {
			tag += "S"
		}
		if r.mono {
			tag += "M"
		}
		if r.link != "" {
			tag += "L"
		}
		got = append(got, tag+":"+r.text)
	}
	want := []string{":a ", "B:b ", "BI:c", "B: d", ": ", "S:e", ": ", "M:*f*", ": ", "L:g ", "BL:h", ": *"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("runs:\n got %v\nwant %v", got, want)
	}
}

// Every zip format's XML parts must be well-formed, whatever the note held.
func TestZipFormatsAreWellFormed(t *testing.T) {
	for _, id := range []string{"docx", "odt"} {
		for name, note := range map[string]string{"plain": everything, "with pictures": richNote} {
			data, err := RenderWith(id, `A "quoted" <title> & more`, note, testImages(t))
			if err != nil {
				t.Fatalf("%s, %s: %v", id, name, err)
			}
			readZip(t, id+", "+name, data)
		}
	}
}

// readZip opens a document as a zip, fails the test if any of its XML parts is
// not well-formed, and returns every part by name.
func readZip(t *testing.T, label string, data []byte) map[string][]byte {
	t.Helper()
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("%s is not a zip: %v", label, err)
	}
	parts := map[string][]byte{}
	for _, f := range z.File {
		r, err := f.Open()
		if err != nil {
			t.Fatalf("%s: cannot open %s: %v", label, f.Name, err)
		}
		body, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatalf("%s: cannot read %s: %v", label, f.Name, err)
		}
		parts[f.Name] = body
		if !strings.HasSuffix(f.Name, ".xml") && !strings.HasSuffix(f.Name, ".rels") {
			continue
		}
		dec := xml.NewDecoder(bytes.NewReader(body))
		for {
			if _, err := dec.Token(); err == io.EOF {
				break
			} else if err != nil {
				t.Errorf("%s: %s is not well-formed XML: %v", label, f.Name, err)
				break
			}
		}
	}
	return parts
}

// An OpenDocument file is recognised by its first bytes: the mimetype entry
// first in the zip, and stored, so its text sits at a fixed offset.
func TestODTStartsWithItsMimetype(t *testing.T) {
	data, err := Render("odt", "t", "hello")
	if err != nil {
		t.Fatal(err)
	}
	if string(data[30:38]) != "mimetype" || string(data[38:77]) != "application/vnd.oasis.opendocument.text" {
		t.Fatalf("the file does not start with a stored mimetype: %q", data[30:80])
	}
	// Its header has to say how big it is. Streamed, it says 0 and sets the
	// data-descriptor flag, and LibreOffice refuses to open the file at all.
	flags := uint16(data[6]) | uint16(data[7])<<8
	size := uint32(data[18]) | uint32(data[19])<<8 | uint32(data[20])<<16 | uint32(data[21])<<24
	if flags&0x8 != 0 || size != 39 {
		t.Fatalf("the mimetype entry is streamed (flags %#04x, size %d in its header)", flags, size)
	}
}

func TestODTKeepsWhitespace(t *testing.T) {
	got := odtText("    four\tand  two")
	want := `<text:s text:c="4"/>four<text:tab/>and <text:s text:c="1"/>two`
	if got != want {
		t.Errorf("odtText:\n got %s\nwant %s", got, want)
	}
}

func TestHTMLIsEscapedAndSafe(t *testing.T) {
	page, _ := Render("html", "<script>", "a <b> & [click](javascript:alert(1)) [ok](https://x.org)")
	s := string(page)
	for _, bad := range []string{"<script>", "<b>", "javascript:"} {
		if strings.Contains(s, bad) {
			t.Errorf("the page contains %q", bad)
		}
	}
	if !strings.Contains(s, `href="https://x.org"`) {
		t.Error("a safe link was dropped")
	}
}

func TestMarkdownIsTheNoteAsItIs(t *testing.T) {
	got, _ := Render("md", "t", everything)
	if string(got) != everything {
		t.Error("the Markdown export changed the note")
	}
}

func TestFileName(t *testing.T) {
	f, _ := Lookup("docx")
	for in, want := range map[string]string{
		"Trip plan":      "Trip plan.docx",
		`a/b\c:d*e?f"g`:  "a b c d e f g.docx",
		"   ":            "Note.docx",
		"Café \x01notes": "Café notes.docx",
	} {
		if got := FileName(in, f); got != want {
			t.Errorf("FileName(%q) = %q, want %q", in, got, want)
		}
	}
}

// The real test: LibreOffice opens each document, and the text a person would
// read is all there. ATLAS_SOFFICE picks which LibreOffice; otherwise the one
// on PATH. Skipped in -short, where there is none, and where the one there
// cannot open a text document at all: a LibreOffice installed without Writer
// says "source file could not be loaded" about everything, which is true of
// it and says nothing about these files.
func TestLibreOfficeOpensTheExports(t *testing.T) {
	if testing.Short() {
		t.Skip("-short")
	}
	soffice := os.Getenv("ATLAS_SOFFICE")
	if soffice == "" {
		var err error
		if soffice, err = exec.LookPath("soffice"); err != nil {
			t.Skip("LibreOffice is not installed")
		}
	}
	dir := t.TempDir()
	convert := func(in, outdir string) ([]byte, error) {
		// Its own profile, so this never touches, or waits on, a LibreOffice
		// the user has open; and no display, so it can never show a window.
		cmd := exec.Command(soffice, "-env:UserInstallation=file://"+filepath.Join(dir, "profile"),
			"--headless", "--convert-to", "txt:Text (encoded):UTF8", "--outdir", outdir, in)
		cmd.Env = append(os.Environ(), "DISPLAY=", "WAYLAND_DISPLAY=")
		return cmd.CombinedOutput()
	}

	probe := filepath.Join(dir, "probe.txt")
	os.WriteFile(probe, []byte("probe\n"), 0o644)
	if out, err := convert(probe, filepath.Join(dir, "probe-out")); err != nil {
		t.Skipf("this LibreOffice cannot open a text document (is Writer installed?): %v %s", err, out)
	}

	for _, id := range []string{"docx", "odt", "html"} {
		f, _ := Lookup(id)
		data, err := Render(id, "Trip plan", everything)
		if err != nil {
			t.Fatal(err)
		}
		in := filepath.Join(dir, "in-"+id+f.Ext)
		os.WriteFile(in, data, 0o644)
		out := filepath.Join(dir, "out-"+id)
		if b, err := convert(in, out); err != nil {
			t.Fatalf("%s: LibreOffice could not open it: %v\n%s", id, err, b)
		}
		text, err := os.ReadFile(filepath.Join(out, "in-"+id+".txt"))
		if err != nil {
			t.Fatalf("%s: LibreOffice produced nothing: %v", id, err)
		}
		for _, want := range []string{"Trip plan", "Friday", "Wool, not cotton", "Book the ferry",
			"high priority, due 3 October 2026", "Walk to the hut", "The bridge is closed",
			"a < b && c > d", "an ampersand & a <tag>", "link"} {
			if !strings.Contains(string(text), want) {
				t.Errorf("%s: LibreOffice's reading of it lacks %q", id, want)
			}
		}
	}
}

// ---- pictures, links to other notes, and tags --------------------------------

// testPNG is a w by h picture, made here so no fixture file is needed.
func testPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// testImages serves pic.png (2 by 1), photo.jpg (3 by 2) and anim.gif (4 by
// 4), and has nothing else: any other path is an error, as a missing file is.
func testImages(t *testing.T) Options {
	t.Helper()
	var jb, gb bytes.Buffer
	if err := jpeg.Encode(&jb, image.NewRGBA(image.Rect(0, 0, 3, 2)), nil); err != nil {
		t.Fatal(err)
	}
	pal := image.NewPaletted(image.Rect(0, 0, 4, 4), color.Palette{color.Black, color.White})
	if err := gif.Encode(&gb, pal, nil); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{"pic.png": testPNG(t, 2, 1), "photo.jpg": jb.Bytes(), "anim.gif": gb.Bytes()}
	return Options{Image: func(p string) ([]byte, error) {
		if b, ok := files[p]; ok {
			return b, nil
		}
		return nil, os.ErrNotExist
	}}
}

// richNote has pictures of every kind and every way of writing one, with
// special characters where the XML could break.
const richNote = everything + `
![Alt & <b> "text"](pic.png)

![[photo.jpg]]

![](anim.gif)

![again](pic.png)

See [[Some/Note#Head|the note]] and ![inline](pic.png) in a #tag line.
`

const onePicture = "Before\n\n![A tiny picture](pic.png)\n\nAfter\n"

func TestImagesAreCarriedInEveryFormat(t *testing.T) {
	pngBytes := testPNG(t, 2, 1)
	opt := testImages(t)

	page, err := RenderWith("html", "t", onePicture, opt)
	if err != nil {
		t.Fatal(err)
	}
	want := `<figure><img src="data:image/png;base64,` + base64.StdEncoding.EncodeToString(pngBytes) + `" alt="A tiny picture"></figure>`
	if !strings.Contains(string(page), want) {
		t.Errorf("html lacks the picture as a data URI:\n%s", page)
	}
	if !strings.Contains(string(page), "img{max-width:100%") {
		t.Error("html does not keep a wide picture inside the page")
	}

	// Word: the file, the relationship that reaches it, its type, and the
	// drawing that shows it.
	data, err := RenderWith("docx", "t", onePicture, opt)
	if err != nil {
		t.Fatal(err)
	}
	parts := readZip(t, "docx", data)
	if !bytes.Equal(parts["word/media/image1.png"], pngBytes) {
		t.Fatalf("docx has no word/media/image1.png holding the picture")
	}
	rel := regexp.MustCompile(`Id="([^"]+)" Type="[^"]*/relationships/image" Target="media/image1.png"`).FindSubmatch(parts["word/_rels/document.xml.rels"])
	if rel == nil {
		t.Fatalf("docx has no image relationship:\n%s", parts["word/_rels/document.xml.rels"])
	}
	if !strings.Contains(string(parts["[Content_Types].xml"]), `<Default Extension="png" ContentType="image/png"/>`) {
		t.Error("docx does not say what a .png part is")
	}
	doc := string(parts["word/document.xml"])
	for _, want := range []string{
		`<w:drawing><wp:inline `, `<a:graphic>`, `<pic:pic>`, `<a:blip r:embed="` + string(rel[1]) + `"/>`,
		// 2 by 1 pixels at 96 to the inch: 9525 EMU a pixel.
		`<wp:extent cx="19050" cy="9525"/>`, `<a:ext cx="19050" cy="9525"/>`,
		`<wp:docPr id="1" name="Picture 1" descr="A tiny picture"/>`,
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("docx document lacks %s", want)
		}
	}

	// OpenDocument: the file, its manifest entry, and the frame.
	data, err = RenderWith("odt", "t", onePicture, opt)
	if err != nil {
		t.Fatal(err)
	}
	parts = readZip(t, "odt", data)
	if !bytes.Equal(parts["Pictures/image1.png"], pngBytes) {
		t.Fatalf("odt has no Pictures/image1.png holding the picture")
	}
	if !strings.Contains(string(parts["META-INF/manifest.xml"]), `<manifest:file-entry manifest:full-path="Pictures/image1.png" manifest:media-type="image/png"/>`) {
		t.Errorf("odt manifest does not list the picture:\n%s", parts["META-INF/manifest.xml"])
	}
	content := string(parts["content.xml"])
	for _, want := range []string{
		`<draw:frame draw:name="Image1" text:anchor-type="as-char" svg:width="0.0208in" svg:height="0.0104in">`,
		`<draw:image xlink:href="Pictures/image1.png" xlink:type="simple" xlink:show="embed" xlink:actuate="onLoad"/>`,
		`<svg:desc>A tiny picture</svg:desc>`,
	} {
		if !strings.Contains(content, want) {
			t.Errorf("odt content lacks %s", want)
		}
	}
	// Adding files must not move the mimetype from the front of the zip, or
	// stop it being stored.
	z, _ := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if z.File[0].Name != "mimetype" || z.File[0].Method != zip.Store {
		t.Errorf("first odt entry is %s, method %d; want mimetype, stored", z.File[0].Name, z.File[0].Method)
	}
	if string(data[30:38]) != "mimetype" || string(data[38:77]) != "application/vnd.oasis.opendocument.text" {
		t.Error("odt with a picture no longer starts with its stored mimetype")
	}

	txt, _ := RenderWith("txt", "t", onePicture, opt)
	if string(txt) != "Before\n\n[Image: A tiny picture]\n\nAfter\n" {
		t.Errorf("text: %q", txt)
	}
	md, _ := RenderWith("md", "t", onePicture, opt)
	if string(md) != onePicture {
		t.Errorf("the Markdown export changed the note: %q", md)
	}
}

// An image that cannot be had leaves its alt text, or its file name when it
// has none, and never an error.
func TestMissingImageFallsBackToAltText(t *testing.T) {
	note := onePicture + "\n![[shot.png]]\n"
	failing := Options{Image: func(string) ([]byte, error) { return nil, errors.New("gone") }}
	for name, opt := range map[string]Options{"an error": failing, "no Image func": {}} {
		for _, f := range Formats {
			data, err := RenderWith(f.ID, "t", note, opt)
			if err != nil {
				t.Fatalf("%s, %s: %v", f.ID, name, err)
			}
			text := string(data)
			if f.ID == "docx" || f.ID == "odt" {
				parts := readZip(t, f.ID, data)
				for n := range parts {
					if strings.HasPrefix(n, "word/media/") || strings.HasPrefix(n, "Pictures/") {
						t.Errorf("%s, %s: has a picture part %s", f.ID, name, n)
					}
				}
				text = string(parts["word/document.xml"]) + string(parts["content.xml"])
			}
			for _, want := range []string{"A tiny picture", "shot.png"} {
				if !strings.Contains(text, want) {
					t.Errorf("%s, %s: lacks %q", f.ID, name, want)
				}
			}
			for _, bad := range []string{"data:image", "<img", "<w:drawing", "<draw:frame"} {
				if strings.Contains(text, bad) {
					t.Errorf("%s, %s: has %q with no picture to show", f.ID, name, bad)
				}
			}
		}
	}

	// The plain text still says a picture was there.
	txt, _ := RenderWith("txt", "t", note, failing)
	if !strings.Contains(string(txt), "[Image: A tiny picture]") || !strings.Contains(string(txt), "[Image: shot.png]") {
		t.Errorf("text: %q", txt)
	}
}

// Render is RenderWith with nothing to fetch, and has no surprises in it.
func TestRenderIsRenderWithNoOptions(t *testing.T) {
	for _, f := range Formats {
		a, err := Render(f.ID, "t", richNote)
		if err != nil {
			t.Fatal(err)
		}
		b, err := RenderWith(f.ID, "t", richNote, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if f.ID == "docx" || f.ID == "odt" {
			// The creation time is written into each, and may differ by a second.
			pa, pb := readZip(t, f.ID, a), readZip(t, f.ID, b)
			delete(pa, "docProps/core.xml")
			delete(pb, "docProps/core.xml")
			delete(pa, "meta.xml")
			delete(pb, "meta.xml")
			if fmt.Sprint(pa) != fmt.Sprint(pb) {
				t.Errorf("%s: Render and RenderWith differ", f.ID)
			}
			continue
		}
		if !bytes.Equal(a, b) {
			t.Errorf("%s: Render and RenderWith differ", f.ID)
		}
	}
}

// Only pictures a reader can show get in: by what the bytes are, not by what
// the note called the file, and never one that is damaged.
func TestUnsupportedImagesFallBackToAltText(t *testing.T) {
	cases := map[string][]byte{
		"svg":           []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><script>alert(1)</script></svg>`),
		"not an image":  []byte("just some text"),
		"empty":         nil,
		"cut-off png":   []byte("\x89PNG\r\n\x1a\n\x00\x00"),
		"damaged png":   append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0xff}, 40)...),
		"webp, no size": []byte("RIFF\x10\x00\x00\x00WEBPVP8X\x0a\x00\x00\x00\x00\x00\x00\x00"),
	}
	for name, data := range cases {
		opt := Options{Image: func(string) ([]byte, error) { return data, nil }}
		for _, id := range []string{"html", "docx", "odt", "txt"} {
			out, err := RenderWith(id, "t", "![Alt words](pic.png)", opt)
			if err != nil {
				t.Fatalf("%s, %s: %v", name, id, err)
			}
			text := string(out)
			if id == "docx" || id == "odt" {
				parts := readZip(t, id, out)
				for n := range parts {
					if strings.HasPrefix(n, "word/media/") || strings.HasPrefix(n, "Pictures/") {
						t.Errorf("%s, %s: kept a picture part %s", name, id, n)
					}
				}
				text = string(parts["word/document.xml"]) + string(parts["content.xml"])
			}
			if !strings.Contains(text, "Alt words") {
				t.Errorf("%s, %s: lost the alt text", name, id)
			}
			if strings.Contains(text, "<img") || strings.Contains(text, "<svg") || strings.Contains(text, "<w:drawing") || strings.Contains(text, "<draw:frame") {
				t.Errorf("%s, %s: embedded something it should not have", name, id)
			}
		}
	}
}

// webp has no decoder in the standard library, so its size is read from the
// header, in each of the three ways a file can write it.
func TestWebPIsIncludedAsIs(t *testing.T) {
	riff := func(chunk string, body []byte) []byte {
		b := append([]byte("RIFF\x00\x00\x00\x00WEBP"+chunk+"\x00\x00\x00\x00"), body...)
		return append(b, make([]byte, 30)...) // padding, so it is never too short
	}
	vp8x := riff("VP8X", []byte{0, 0, 0, 0, 39, 0, 0, 19, 0, 0}) // 40 by 20
	vp8l := riff("VP8L", []byte{0x2f, 0x27, 0xc0, 0x04, 0})      // 40 by 20: 39 | 19<<14 = 0x4c027
	vp8 := riff("VP8 ", []byte{0, 0, 0, 0x9d, 0x01, 0x2a, 40, 0, 20, 0})
	for name, data := range map[string][]byte{"VP8X": vp8x, "VP8L": vp8l, "VP8": vp8} {
		if w, h := webpSize(data); w != 40 || h != 20 {
			t.Errorf("%s: size %d by %d, want 40 by 20", name, w, h)
		}
	}

	opt := Options{Image: func(string) ([]byte, error) { return vp8x, nil }}
	page, _ := RenderWith("html", "t", "![w](a.webp)", opt)
	if !strings.Contains(string(page), `<img src="data:image/webp;base64,`) {
		t.Error("html left out the webp")
	}
	data, _ := RenderWith("docx", "t", "![w](a.webp)", opt)
	parts := readZip(t, "docx", data)
	if !bytes.Equal(parts["word/media/image1.webp"], vp8x) ||
		!strings.Contains(string(parts["[Content_Types].xml"]), `<Default Extension="webp" ContentType="image/webp"/>`) ||
		!strings.Contains(string(parts["word/document.xml"]), `<wp:extent cx="381000" cy="190500"/>`) {
		t.Error("docx does not carry the webp, or sizes it wrongly")
	}
	data, _ = RenderWith("odt", "t", "![w](a.webp)", opt)
	parts = readZip(t, "odt", data)
	if !bytes.Equal(parts["Pictures/image1.webp"], vp8x) ||
		!strings.Contains(string(parts["META-INF/manifest.xml"]), `manifest:full-path="Pictures/image1.webp" manifest:media-type="image/webp"`) {
		t.Error("odt does not carry the webp")
	}
}

// A picture is shown at the size it is on screen, shrunk to fit the page.
func TestPictureSize(t *testing.T) {
	for _, c := range []struct {
		w, h     int
		cx, cy   int64
		odtWidth string
	}{
		{2, 1, 19050, 9525, "0.0208in"},
		{576, 96, 5486400, 914400, "6.0000in"}, // exactly 6 by 1 inches
		{2000, 1000, 5486400, 2743200, "6.0000in"},
		{100, 20000, 41148, 8229600, "0.0450in"}, // capped by its height, not its width
	} {
		opt := Options{Image: func(string) ([]byte, error) { return testPNG(t, c.w, c.h), nil }}
		data, _ := RenderWith("docx", "t", "![x](a.png)", opt)
		doc := string(readZip(t, "docx", data)["word/document.xml"])
		want := fmt.Sprintf(`<wp:extent cx="%d" cy="%d"/>`, c.cx, c.cy)
		if !strings.Contains(doc, want) {
			t.Errorf("%dx%d px: docx lacks %s in\n%s", c.w, c.h, want, doc)
		}
		data, _ = RenderWith("odt", "t", "![x](a.png)", opt)
		if content := string(readZip(t, "odt", data)["content.xml"]); !strings.Contains(content, `svg:width="`+c.odtWidth+`"`) {
			t.Errorf("%dx%d px: odt lacks width %s", c.w, c.h, c.odtWidth)
		}
	}
}

// Each drawing in a Word file needs an id of its own, and a picture shown
// twice is stored once.
func TestPicturesAreNumberedAndShared(t *testing.T) {
	note := "![a](pic.png)\n\n![b](photo.jpg)\n\n![c](pic.png)\n\n![d](anim.gif)\n"
	opt := testImages(t)
	calls := map[string]int{}
	counting := Options{Image: func(p string) ([]byte, error) { calls[p]++; return opt.Image(p) }}

	data, _ := RenderWith("docx", "t", note, counting)
	parts := readZip(t, "docx", data)
	ids := regexp.MustCompile(`<wp:docPr id="(\d+)"`).FindAllStringSubmatch(string(parts["word/document.xml"]), -1)
	seen := map[string]bool{}
	for _, m := range ids {
		if seen[m[1]] {
			t.Errorf("docPr id %s is used twice", m[1])
		}
		seen[m[1]] = true
	}
	if len(ids) != 4 {
		t.Errorf("%d drawings, want 4", len(ids))
	}
	for _, n := range []string{"word/media/image1.png", "word/media/image2.jpg", "word/media/image3.gif"} {
		if parts[n] == nil {
			t.Errorf("docx lacks %s", n)
		}
	}
	if parts["word/media/image4.png"] != nil {
		t.Error("the same picture was stored twice")
	}
	types := string(parts["[Content_Types].xml"])
	for _, want := range []string{`Extension="png" ContentType="image/png"`, `Extension="jpg" ContentType="image/jpeg"`, `Extension="gif" ContentType="image/gif"`} {
		if strings.Count(types, want) != 1 {
			t.Errorf("content types should declare %s once:\n%s", want, types)
		}
	}
	// Every relationship a drawing uses exists.
	rels := string(parts["word/_rels/document.xml.rels"])
	for _, m := range regexp.MustCompile(`r:embed="([^"]+)"`).FindAllStringSubmatch(string(parts["word/document.xml"]), -1) {
		if !strings.Contains(rels, `Id="`+m[1]+`"`) {
			t.Errorf("no relationship %s", m[1])
		}
	}

	data, _ = RenderWith("odt", "t", note, opt)
	parts = readZip(t, "odt", data)
	content := string(parts["content.xml"])
	if strings.Count(content, "<draw:frame ") != 4 || strings.Count(content, `draw:name="Image4"`) != 1 {
		t.Errorf("odt frames:\n%s", content)
	}
	for n := range parts {
		if strings.HasPrefix(n, "Pictures/") && !strings.Contains(string(parts["META-INF/manifest.xml"]), `"`+n+`"`) {
			t.Errorf("%s is not in the manifest", n)
		}
	}
	if len(parts["Pictures/image3.gif"]) == 0 || parts["Pictures/image4.png"] != nil {
		t.Error("odt did not store three distinct pictures")
	}
	if calls["pic.png"] != 1 {
		t.Errorf("pic.png was fetched %d times, want once", calls["pic.png"])
	}
}

// Only an image on a line of its own is a picture; among words it is its alt
// text, and no picture is asked for.
func TestInlineImageIsItsAltText(t *testing.T) {
	asked := 0
	opt := Options{Image: func(string) ([]byte, error) { asked++; return testPNG(t, 1, 1), nil }}
	note := "See ![the diagram](d.png) above, and ![](e.png) too.\n"
	txt, _ := RenderWith("txt", "t", note, opt)
	if string(txt) != "See the diagram above, and e.png too.\n" {
		t.Errorf("text: %q", txt)
	}
	page, _ := RenderWith("html", "t", note, opt)
	if strings.Contains(string(page), "<img") || !strings.Contains(string(page), "See the diagram above") {
		t.Errorf("html: %s", page)
	}
	if asked != 0 {
		t.Errorf("asked for %d pictures that are not on a line of their own", asked)
	}

	// Two on a line, and a trailing comment, are still pictures.
	bl := parse("![a](1.png) ![[2.png|300]]  <!-- x -->\n- ![in a list](3.png)\n")
	if len(bl) != 3 || bl[0].kind != figure || bl[1].kind != figure || bl[2].kind != bullet {
		t.Fatalf("blocks: %+v", bl)
	}
	if bl[0].src != "1.png" || bl[0].alt != "a" || bl[1].src != "2.png" || bl[1].alt != "" {
		t.Errorf("pictures: %+v %+v", bl[0], bl[1])
	}
	if plainText(bl[2].lines[0]) != "in a list" {
		t.Errorf("an image in a list item is %q", plainText(bl[2].lines[0]))
	}
}

func TestWikiLinksReadAsText(t *testing.T) {
	for in, want := range map[string]string{
		"[[Target]]":                   "Target",
		"[[Target|Alias]]":             "Alias",
		"[[Target#Heading]]":           "Target › Heading",
		"[[Folder/Sub/Note]]":          "Note",
		"[[Folder/Note#Part]]":         "Note › Part",
		"[[Note#Part|Shown]]":          "Shown",
		"See [[A]] and [[B|b]], ok.":   "See A and b, ok.",
		"![[Some note]]":               "Some note",
		"[[Foo]] then [x](http://y.z)": "Foo then x",
		"`[[Code]]` stays":             "[[Code]] stays",
		"[[ ]] and [[]] are not links": "[[ ]] and [[]] are not links",
		"[[a]][[b]]":                   "ab",
		"[[Trailing/]]":                "Trailing",
		"[[x|*not italic*]] and *it*":  "*not italic* and it",
	} {
		if got := plainText(parseInline(in)); got != want {
			t.Errorf("%q reads as %q, want %q", in, got, want)
		}
	}

	// The text keeps the formatting around it.
	runs := parseInline("**[[T|Alias]]** and *see [[Note#H]]* and ~~[[Gone]]~~")
	var got []string
	for _, r := range runs {
		tag := ""
		if r.bold {
			tag += "B"
		}
		if r.italic {
			tag += "I"
		}
		if r.strike {
			tag += "S"
		}
		got = append(got, tag+":"+r.text)
	}
	want := []string{"B:Alias", ": and ", "I:see Note › H", ": and ", "S:Gone"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("runs:\n got %v\nwant %v", got, want)
	}

	note := "# Home\n\n**[[Target|Alias]]** and [[Folder/Note#Part]] and a [link](https://x.org)\n"
	page, _ := Render("html", "t", note)
	for _, want := range []string{"<strong>Alias</strong>", "Note › Part", `<a href="https://x.org">link</a>`} {
		if !strings.Contains(string(page), want) {
			t.Errorf("html lacks %s:\n%s", want, page)
		}
	}
	for _, id := range []string{"html", "txt", "docx", "odt"} {
		data, _ := Render(id, "t", note)
		text := string(data)
		if id == "docx" || id == "odt" {
			parts := readZip(t, id, data)
			text = string(parts["word/document.xml"]) + string(parts["content.xml"])
		}
		if strings.Contains(text, "[[") || strings.Contains(text, "Target") || !strings.Contains(text, "Note › Part") {
			t.Errorf("%s does not show links between notes as readable text:\n%s", id, text)
		}
	}
	md, _ := Render("md", "t", note)
	if string(md) != note {
		t.Errorf("the Markdown export changed the note: %q", md)
	}
}

func TestTagsSurviveExport(t *testing.T) {
	note := "# Plan #work\n\nRemember #project/atlas and #todo.\n\n- item #home\n> quoted #idea\n"
	for _, f := range Formats {
		data, err := Render(f.ID, "t", note)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		if f.ID == "docx" || f.ID == "odt" {
			parts := readZip(t, f.ID, data)
			text = string(parts["word/document.xml"]) + string(parts["content.xml"])
		}
		for _, tag := range []string{"#work", "#project/atlas", "#todo", "#home", "#idea"} {
			if !strings.Contains(text, tag) {
				t.Errorf("%s lost the tag %s", f.ID, tag)
			}
		}
	}
}
