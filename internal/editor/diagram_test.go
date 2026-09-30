package editor

import (
	"bytes"
	"fmt"
	"os"
	"strings"
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
	if n := len(e.dia.items); n != 2 {
		t.Fatalf("%d diagrams drawn, want 2 (the flowchart and the sequence diagram)", n)
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
	if _, _, _, err := RenderDiagramPNG("pie\n\"a\": 1"); err == nil {
		t.Error("a pie chart was drawn")
	}
}

func TestRenderGanttAndSequencePNG(t *testing.T) {
	for _, src := range []string{
		"gantt\ntitle Plan\nsection S\nBuild :active, b, 2026-01-05, 5d\nShip :milestone, after b, 0d",
		"sequenceDiagram\nparticipant A as Alice\nactor B\nA->>+B: hi\nalt x\nB-->>-A: yes\nelse y\nNote over A,B: n\nend",
	} {
		png, w, h, err := RenderDiagramPNG(src)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.HasPrefix(png, []byte("\x89PNG")) || w <= 0 || h <= 0 {
			t.Errorf("not a picture: %d bytes, %dx%d", len(png), w, h)
		}
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

// Done replaces the block it was opened on, found again if lines were added
// above it, and refuses when the block was edited meanwhile. Needs a display.
func TestDiagramDoneReplacesTheRightLines(t *testing.T) {
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		t.Skip("no display")
	}
	if !gtk.InitCheck() {
		t.Skip("no display")
	}
	const doc = "# T\n\n```mermaid\nflowchart TD\n  a[One] --> b[Two]\n```\n\nafter\n"
	const orig = "flowchart TD\n  a[One] --> b[Two]\n"
	const next = "flowchart TD\n  a[One] --> b[Two]\n  b --> c[Three]\n"
	open := func(text string) (*Editor, *diagramEditor) {
		e := New()
		e.SetContent(text)
		e.Reparse()
		return e, &diagramEditor{e: e, first: 2, last: 5, orig: orig, buf: e.buffer}
	}
	e, d := open(doc)
	if msg := e.replaceDiagramBlock(d, next); msg != "" {
		t.Fatal(msg)
	}
	if got, want := e.Content(), strings.Replace(doc, orig, next, 1); got != want {
		t.Errorf("content %q, want %q", got, want)
	}
	// Lines were added above the block: it is found where it moved to.
	e, d = open("new line\nanother\n" + doc)
	if msg := e.replaceDiagramBlock(d, next); msg != "" {
		t.Fatal(msg)
	}
	if got, want := e.Content(), "new line\nanother\n"+strings.Replace(doc, orig, next, 1); got != want {
		t.Errorf("moved: content %q, want %q", got, want)
	}
	// The block itself was edited: nothing is written.
	edited := strings.Replace(doc, "One", "Uno", 1)
	e, d = open(edited)
	if msg := e.replaceDiagramBlock(d, next); msg == "" || e.Content() != edited {
		t.Errorf("edited block was overwritten: %q", e.Content())
	}
	// The block is gone.
	e, d = open("# T\n\nnothing\n")
	if msg := e.replaceDiagramBlock(d, next); msg == "" || e.Content() != "# T\n\nnothing\n" {
		t.Error("a missing block was written")
	}
}
