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

const flow = "# T\n\n```mermaid\nflowchart TD\n  a[One<br>First] --> b[Two]\n```\n\n```mermaid\npie\n  \"a\": 1\n```\n"

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
	if !strings.Contains(page, "language-mermaid") || !strings.Contains(page, "pie") {
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

func TestDiagramPictureUsesGivenSize(t *testing.T) {
	opt := Options{Diagram: func(string) ([]byte, int, int, error) { return tinyPNG(), 400, 200, nil }}
	out, err := RenderWith("docx", "t", "```mermaid\nflowchart TD\na-->b\n```\n", opt)
	if err != nil {
		t.Fatal(err)
	}
	zr, _ := zip.NewReader(bytes.NewReader(out), int64(len(out)))
	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			rc, _ := f.Open()
			var b bytes.Buffer
			b.ReadFrom(rc)
			if !strings.Contains(b.String(), `cx="3810000"`) {
				t.Error("the picture is not shown at the size the drawer gave")
			}
			return
		}
	}
	t.Error("no document.xml")
}

func TestHTMLGanttAndSequenceAreSVG(t *testing.T) {
	src := "```mermaid\ngantt\n  title Plan\n  section S\n  Build :b, 2026-01-05, 5d\n```\n\n```mermaid\nsequenceDiagram\n  A->>B: hi <there>\n```\n"
	out, err := RenderWith("html", "t", src, Options{})
	if err != nil {
		t.Fatal(err)
	}
	page := string(out)
	if strings.Count(page, `<figure class="diagram"><svg`) != 2 || !strings.Contains(page, "Gantt chart") || !strings.Contains(page, "Sequence diagram") {
		t.Error("the Gantt chart and the sequence diagram are not both SVGs")
	}
	if strings.Contains(page, "hi <there>") {
		t.Error("diagram text is not escaped")
	}
	if strings.Contains(page, "language-mermaid") {
		t.Error("a drawn diagram is also a code block")
	}
}

func TestDocumentsHoldGanttPicture(t *testing.T) {
	var got []string
	opt := Options{Diagram: func(src string) ([]byte, int, int, error) {
		got = append(got, strings.Fields(src)[0])
		return tinyPNG(), 40, 20, nil
	}}
	src := "```mermaid\ngantt\nA :2026-01-01, 1d\n```\n\n```mermaid\nsequenceDiagram\nA->>B: x\n```\n"
	if _, err := RenderWith("docx", "t", src, opt); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "gantt" || got[1] != "sequenceDiagram" {
		t.Errorf("the hook was asked for %v", got)
	}
}
