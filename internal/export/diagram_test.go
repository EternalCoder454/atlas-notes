package export

import (
	"archive/zip"
	"bytes"
	"errors"
	"image"
	"image/png"
	"strings"
	"testing"
)

const flow = "# T\n\n```mermaid\nflowchart TD\n  a[One<br>First] --> b[Two]\n```\n\n```mermaid\nsequenceDiagram\n  A->>B: hi\n```\n"

func tinyPNG() []byte {
	var b bytes.Buffer
	png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 40, 20)))
	return b.Bytes()
}

func TestHTMLDiagramIsSVG(t *testing.T) {
	out, err := RenderWith("html", "t", flow, Options{})
	if err != nil {
		t.Fatal(err)
	}
	page := string(out)
	if !strings.Contains(page, `<figure class="diagram"><svg`) || !strings.Contains(page, "First") {
		t.Error("the flowchart is not an SVG")
	}
	if !strings.Contains(page, "language-mermaid") || !strings.Contains(page, "sequenceDiagram") {
		t.Error("an undrawable diagram should stay a code block")
	}
}

func TestDocumentsHoldDiagramPicture(t *testing.T) {
	opt := Options{Diagram: func(src string) ([]byte, int, int, error) {
		if !strings.Contains(src, "flowchart") {
			return nil, 0, 0, errors.New("not a flowchart")
		}
		return tinyPNG(), 40, 20, nil
	}}
	for _, id := range []string{"docx", "odt"} {
		out, err := RenderWith(id, "t", flow, opt)
		if err != nil {
			t.Fatal(err)
		}
		zr, err := zip.NewReader(bytes.NewReader(out), int64(len(out)))
		if err != nil {
			t.Fatal(err)
		}
		pics := 0
		for _, f := range zr.File {
			if strings.Contains(f.Name, "media/") || strings.Contains(f.Name, "Pictures/") {
				pics++
			}
		}
		if pics != 1 {
			t.Errorf("%s holds %d pictures, want 1", id, pics)
		}
		// Without a way to draw, both stay code blocks.
		if _, err := RenderWith(id, "t", flow, Options{Diagram: func(string) ([]byte, int, int, error) { return nil, 0, 0, errors.New("no") }}); err != nil {
			t.Error(err)
		}
	}
}
