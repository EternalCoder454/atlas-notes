package editor

import (
	"testing"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// bareEditor is an Editor with only a buffer, which is all inserting a picture
// touches. A buffer needs no display.
func bareEditor(text string) *Editor {
	e := &Editor{buffer: gtk.NewTextBuffer(nil)}
	e.buffer.SetText(text)
	return e
}

func (e *Editor) textNow() string {
	start, end := e.buffer.Bounds()
	return e.buffer.Slice(start, end, true)
}

func TestInsertImageOnATaskLineGoesAfterIt(t *testing.T) {
	e := bareEditor(anchorChar + "Task one\nnext")
	e.buffer.PlaceCursor(e.buffer.IterAtOffset(4)) // inside "Task"
	e.InsertImage("a.png")
	if got, want := e.textNow(), anchorChar+"Task one\n![](a.png)\nnext"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestInsertImageOnAPlainLineSplitsIt(t *testing.T) {
	e := bareEditor("hello world")
	e.buffer.PlaceCursor(e.buffer.IterAtOffset(6))
	e.InsertImage("a.png")
	if got, want := e.textNow(), "hello \n![](a.png)\nworld"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestInsertImagesAtAMarkOnATaskLine(t *testing.T) {
	e := bareEditor(anchorChar + "Task one\nnext")
	mark := e.buffer.CreateMark("", e.buffer.IterAtOffset(3), false)
	e.insertImages([]string{"a.png", "b.png"}, mark)
	if got, want := e.textNow(), anchorChar+"Task one\n![](a.png)\n![](b.png)\nnext"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if !mark.Deleted() {
		t.Error("the drop mark was left in the buffer")
	}
}

// When every save failed there is nothing to insert, and the mark that held the
// drop point must not be left behind.
func TestInsertImagesDeletesTheMarkWhenNothingSaved(t *testing.T) {
	e := bareEditor("text")
	mark := e.buffer.CreateMark("", e.buffer.IterAtOffset(2), false)
	e.insertImages(nil, mark)
	if !mark.Deleted() {
		t.Error("the drop mark was left in the buffer")
	}
	if got := e.textNow(); got != "text" {
		t.Errorf("text changed to %q", got)
	}
}

func TestPadPixels(t *testing.T) {
	if got := padPixels(""); got != 0 {
		t.Errorf("padPixels(\"\") = %d, want 0", got)
	}
	for _, h := range []int{1, 16, 100, 480} {
		name, want := imagePad(h)
		if got := padPixels(name); got != want {
			t.Errorf("padPixels(%q) = %d, want %d", name, got, want)
		}
	}
}
