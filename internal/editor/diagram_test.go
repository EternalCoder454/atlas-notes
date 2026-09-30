package editor

import (
	"bytes"
	"fmt"
	"os"
	"testing"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

const diagramDoc = "# T\n\n```mermaid\nflowchart TD\n  a[One<br>Sub] --> b[Two]\n```\n\nafter\n\n```mermaid\nsequenceDiagram\n  A->>B: hi\n```\n"

// A flowchart is drawn in place of its block, and only while the caret is away
// from it; a diagram that cannot be drawn stays a code block. The buffer's text
// is never changed. It needs a display, and is skipped without one.
func TestDiagramBlockDrawnAndRevealed(t *testing.T) {
	// GTK answers a second initialisation with yes after a first one failed, and
	// the tests after this one would then crash, so a missing display is noticed
	// first.
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		t.Skip("no display")
	}
	if !gtk.InitCheck() {
		t.Skip("no display")
	}
	e := New()
	e.SetContent(diagramDoc)
	e.Reparse()
	if n := len(e.dia.items); n != 1 {
		t.Fatalf("%d diagrams drawn, want 1 (the sequence diagram is not one)", n)
	}
	if got := e.Content(); got != diagramDoc {
		t.Error("drawing the diagram changed the text")
	}
	if iter, ok := e.buffer.IterAtLine(3); ok {
		e.buffer.PlaceCursor(iter)
	}
	e.Reparse()
	if n := len(e.dia.items); n != 0 {
		t.Errorf("%d diagrams drawn with the caret in the block, want 0", n)
	}
}

// The PNG a document gets is drawn without a window, so it is checked anywhere.
func TestRenderDiagramPNG(t *testing.T) {
	png, w, h, err := RenderDiagramPNG("flowchart LR\n  a[One<br>Sub] --> b[Two]")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(png, []byte("\x89PNG")) || w <= 0 || h <= 0 {
		t.Errorf("not a picture: %d bytes, %dx%d", len(png), w, h)
	}
	if _, _, _, err := RenderDiagramPNG("sequenceDiagram\nA->>B: x"); err == nil {
		t.Error("a sequence diagram was drawn")
	}
}

func TestRenderDiagramPNGRefusesHugeDiagrams(t *testing.T) {
	src := "flowchart TD\n"
	for i := 0; i < 190; i++ {
		src += fmt.Sprintf("n%d --> n%d\n", i, i+1)
	}
	if _, _, _, err := RenderDiagramPNG(src); err == nil {
		t.Error("a diagram too large to draw was drawn")
	}
}
