package editor

import (
	"reflect"
	"testing"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

func TestPlainLine(t *testing.T) {
	for l, want := range map[string]bool{
		"": true, "prose with **bold** and [[a link]]": true, "- item": true, "  # indented is prose": true,
		"# Heading": true, "---": false, "--- x": false, "--": true, "> quote": false, "```go": false, "text ``` more": false, "~~~": false, "a | b": false,
	} {
		if got := plainLine(l); got != want {
			t.Errorf("plainLine(%q) = %v, want %v", l, got, want)
		}
	}
}

// A typing session, replayed on an editor and then compared with a fresh editor
// given the same final text: skipping the note-wide scan for plain edits must
// leave every result of it as a full scan would. It needs a display, and is
// skipped without one.
func TestPlainEditsMatchAFullScan(t *testing.T) {
	if !gtk.InitCheck() {
		t.Skip("no display")
	}
	const doc = "# Title\n\nsome prose here\n\n```go\ncode\n```\n\n> quote\n> more\n\n## Second\ntext | with pipe\nend"
	type step struct {
		line, col int
		del       int // characters to delete at the position
		ins       string
	}
	steps := []step{
		{2, 4, 0, "x"}, {2, 4, 0, "y"}, {2, 5, 1, ""}, // typing in prose
		{5, 0, 0, "#"},                                // a "#" inside the code block
		{4, 2, 1, ""},                                 // breaking the fence's opening
		{4, 2, 0, "`"},                                // mending it
		{2, 0, 0, "## "},                              // making a heading
		{2, 0, 3, ""},                                 // and unmaking it
		{2, 0, 0, "```"},                              // opening a fence in prose
		{2, 0, 3, ""},                                 // closing it again
		{12, 0, 0, "> "},                              // a quote
		{12, 0, 2, ""},                                //
		{0, 7, 0, "z"}, {0, 8, 0, "w"}, {0, 8, 1, ""}, // typing in a heading
		{0, 0, 0, "#"}, {0, 0, 1, ""}, {0, 1, 1, ""}, {0, 1, 0, " "}, // its level, and its "#" going and coming
		{11, 3, 0, "q"}, {11, 0, 3, ""}, {11, 0, 0, "### "}, // the second heading
		{0, 0, 0, "---"}, {0, 0, 3, ""}, // front matter's opening
		{2, 0, 0, "a|b"}, {2, 0, 3, ""}, // a pipe
	}
	e := New()
	e.SetContent(doc)
	e.Reparse()
	for i, st := range steps {
		it, ok := e.buffer.IterAtLineOffset(st.line, st.col)
		if !ok {
			t.Fatalf("step %d: no such place", i)
		}
		e.buffer.PlaceCursor(it)
		if st.del > 0 {
			end := it.Copy()
			end.ForwardChars(st.del)
			e.buffer.Delete(it, end)
		}
		if st.ins != "" {
			e.InsertAtCursor(st.ins)
		}
		e.Reparse()
		f := New()
		f.SetContent(e.rawText())
		f.Reparse()
		if !reflect.DeepEqual(e.fence, f.fence) || e.outline.heads == nil != (f.outline.heads == nil) ||
			!reflect.DeepEqual(e.outline.heads, f.outline.heads) || e.hasPipe != f.hasPipe && f.hasPipe {
			t.Fatalf("step %d: scan results differ from a full scan\nfence %v vs %v\nheads %v vs %v\npipe %v vs %v",
				i, e.fence, f.fence, e.outline.heads, f.outline.heads, e.hasPipe, f.hasPipe)
		}
		if !reflect.DeepEqual(e.rich.blocks, f.rich.blocks) {
			t.Fatalf("step %d: blocks differ: %v vs %v", i, e.rich.blocks, f.rich.blocks)
		}
	}
}
