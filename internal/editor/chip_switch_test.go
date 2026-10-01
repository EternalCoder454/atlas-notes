package editor

import (
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// A task's due chip belongs to its note. Opening another note must take it off
// the view: a person saw a "Jul 1" from one note floating over the text of the
// next, which had no tasks at all. It needs a display.
func TestDueChipLeavesWithItsNote(t *testing.T) {
	// GTK works from one thread, and this test sleeps while the main loop runs,
	// which would otherwise let the scheduler move it.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		t.Skip("no display")
	}
	if !gtk.InitCheck() {
		t.Skip("no display")
	}
	e := New()
	win := gtk.NewWindow()
	win.SetDefaultSize(800, 600)
	win.SetChild(e.Widget())
	win.Present()
	defer win.Destroy()
	// Long enough for the window to be laid out and the idle passes that
	// place the chips to run.
	pump := func() {
		ctx := glib.MainContextDefault()
		end := time.Now().Add(700 * time.Millisecond)
		for time.Now().Before(end) {
			for ctx.Pending() {
				ctx.Iteration(false)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	e.SetContent("# Welcome\n\n- [ ] Ship it 📅 2026-07-01\n")
	e.Reparse()
	pump()
	e.placeBlocks()
	pump()
	if n := visibleChips(e); n != 1 {
		t.Fatalf("%d due chips on the note with one dated task, want 1", n)
	}

	e.SetContent("# Atlas Commander\n\nWhat is Atlas Commander?\n")
	e.Reparse()
	pump()
	e.placeBlocks()
	pump()
	if n := visibleChips(e); n != 0 {
		t.Errorf("%d due chips left on a note with no tasks, want 0", n)
	}
}

// visibleChips counts the due chips on the view that are showing. GTK keeps a
// text view's overlays inside a child of its own, so the whole tree is walked.
func visibleChips(e *Editor) int {
	n := 0
	var walk func(w *gtk.Widget, shown bool)
	walk = func(w *gtk.Widget, shown bool) {
		shown = shown && w.IsVisible()
		if w.HasCSSClass("due-chip") && shown && w.Root() != nil {
			n++
		}
		for c := w.FirstChild(); c != nil; c = gtk.BaseWidget(c).NextSibling() {
			walk(gtk.BaseWidget(c), shown)
		}
	}
	walk(gtk.BaseWidget(e.view), true)
	return n
}
