package editor

import (
	"reflect"
	"testing"
	"unicode/utf8"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// bulletEditor is a bareEditor that also has the bullet tags and watches its
// undo history, which is all the bullet code needs from a display.
func bulletEditor(text string) *Editor {
	e := bareEditor(text)
	e.tags = map[string]*gtk.TextTag{}
	e.createBulletTags()
	e.installHistory()
	return e
}

// typeText inserts text the way the view does for typing: as one user action.
func (e *Editor) typeText(at int, text string) {
	e.buffer.BeginUserAction()
	e.buffer.Insert(e.buffer.IterAtOffset(at), text)
	e.buffer.EndUserAction()
}

func TestContentMapsTaggedBulletsBack(t *testing.T) {
	e := bulletEditor("• a\n• b\n  • c\n• typed")
	e.setBulletTag(0, '-')
	e.setBulletTag(4, '*')
	e.setBulletTag(10, '+')
	// The last line's "•" was typed, so it has no tag and stays.
	if got, want := e.Content(), "- a\n* b\n  + c\n• typed"; got != want {
		t.Errorf("Content() = %q, want %q", got, want)
	}
	if got, want := e.textNow(), "• a\n• b\n  • c\n• typed"; got != want {
		t.Errorf("the buffer changed to %q, want %q", got, want)
	}
}

func TestContentWithNoBullets(t *testing.T) {
	e := bulletEditor("- a\nplain • text")
	if got, want := e.Content(), "- a\nplain • text"; got != want {
		t.Errorf("Content() = %q, want %q", got, want)
	}
}

func TestContentMapsABulletAtTheStartOfTheBuffer(t *testing.T) {
	// A tagged range that begins at offset 0 is not a toggle after the start.
	e := bulletEditor("• only")
	e.setBulletTag(0, '-')
	if got, want := e.Content(), "- only"; got != want {
		t.Errorf("Content() = %q, want %q", got, want)
	}
}

func TestSourceTextKeepsBufferOffsets(t *testing.T) {
	e := bulletEditor("• alpha\nbeta\n• alpha")
	e.setBulletTag(0, '-')
	e.setBulletTag(13, '-')
	src := e.sourceText()
	if got, want := utf8.RuneCountInString(src), int(e.buffer.CharCount()); got != want {
		t.Fatalf("source text has %d characters, the buffer %d", got, want)
	}
	// Find sees the file's "-", at the offsets the buffer has.
	got := findMatches(src, "- alpha", true)
	if want := [][2]int{{0, 7}, {13, 20}}; !reflect.DeepEqual(got, want) {
		t.Errorf("matches for \"- alpha\" = %v, want %v", got, want)
	}
	if got := findMatches(src, "•", true); got != nil {
		t.Errorf("a drawn bullet was found as a \"•\": %v", got)
	}
}

func TestSourceTextFindsATypedBullet(t *testing.T) {
	e := bulletEditor("• typed\n• drawn")
	e.setBulletTag(8, '*')
	if got, want := findMatches(e.sourceText(), "•", true), [][2]int{{0, 1}}; !reflect.DeepEqual(got, want) {
		t.Errorf("matches for \"•\" = %v, want %v", got, want)
	}
}

func TestReplaceCharKeepsTheCaretOffset(t *testing.T) {
	for caret := 0; caret <= 5; caret++ {
		e := bulletEditor("- foo")
		e.buffer.PlaceCursor(e.buffer.IterAtOffset(caret))
		e.replaceChar(bulletEdit{0, 0, bulletGlyph, '-'})
		if got := e.buffer.IterAtMark(e.buffer.GetInsert()).Offset(); got != caret {
			t.Errorf("caret at %d moved to %d by drawing the bullet", caret, got)
		}
		if got := e.textNow(); got != "• foo" {
			t.Fatalf("text is %q after drawing the bullet", got)
		}
		if got := e.bulletMarkerAt(0, 0); got != '-' {
			t.Errorf("the bullet is tagged as %q, want '-'", got)
		}
		e.replaceChar(bulletEdit{0, 0, "-", 0})
		if got := e.buffer.IterAtMark(e.buffer.GetInsert()).Offset(); got != caret {
			t.Errorf("caret at %d moved to %d by putting the marker back", caret, got)
		}
		if got := e.textNow(); got != "- foo" {
			t.Errorf("text is %q after putting the marker back", got)
		}
		if got := e.bulletMarkerAt(0, 0); got != 0 {
			t.Errorf("the marker still has a bullet tag (%q)", got)
		}
	}
}

func TestPlanBullets(t *testing.T) {
	e := bulletEditor("- a\n* b\n  + c\n1. n\n> q\n- [ ] t\n• typed")
	type plan = []bulletEdit
	cases := []struct {
		name   string
		reveal int
		fence  []bool
		want   plan
	}{
		{"no caret line", -1, nil, plan{
			{0, 0, bulletGlyph, '-'}, {1, 0, bulletGlyph, '*'}, {2, 2, bulletGlyph, '+'}}},
		{"the caret's own line is left", 1, nil, plan{
			{0, 0, bulletGlyph, '-'}, {2, 2, bulletGlyph, '+'}}},
		{"code is left", -1, []bool{true, true, false}, plan{{2, 2, bulletGlyph, '+'}}},
	}
	for _, c := range cases {
		e.fence = c.fence
		got := e.planBullets(e.textNow(), 0, c.reveal)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestPlanBulletsPutsMarkersBack(t *testing.T) {
	e := bulletEditor("• a\n• b\n  • c\nx • d")
	e.setBulletTag(0, '-')
	e.setBulletTag(4, '*')
	e.setBulletTag(10, '+')
	e.setBulletTag(16, '-') // a tagged "•" that is no longer at the start of its line
	got := e.planBullets(e.textNow(), 0, 1)
	want := []bulletEdit{{1, 0, "*", 0}, {3, 2, "-", 0}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("caret on line 1: got %v, want %v", got, want)
	}
	e.fence = []bool{false, false, true}
	got = e.planBullets(e.textNow(), 0, -1)
	want = []bulletEdit{{2, 2, "+", 0}, {3, 2, "-", 0}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("code on line 2: got %v, want %v", got, want)
	}
}

func TestBulletSwapDiff(t *testing.T) {
	cases := []struct {
		name, before, after string
		want                []bulletMark
		ok                  bool
	}{
		{"drawn", "- a", "• a", []bulletMark{{0, '-'}}, true},
		{"put back", "• a", "* a", []bulletMark{{0, 0}}, true},
		{"indented, two lines", "x\n  + a\n- b", "x\n  • a\n• b", []bulletMark{{4, '+'}, {8, '-'}}, true},
		{"no change", "- a", "- a", nil, false},
		{"text changed", "- a", "• b", nil, false},
		{"length changed", "- a", "• ab", nil, false},
		{"not at a marker column", "a - b", "a • b", nil, false},
		{"no space after", "-a", "•a", nil, false},
		{"another character", "> a", "• a", nil, false},
	}
	for _, c := range cases {
		got, ok := bulletSwapDiff(c.before, c.after)
		if ok != c.ok || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v, %v, want %v, %v", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestGlyphColAndRestoreMarker(t *testing.T) {
	cases := []struct {
		line string
		col  int
		back string
	}{
		{"• a", 0, "- a"},
		{"  • a", 2, "  - a"},
		{"\t• a", 1, "\t- a"},
		{"•a", -1, "•a"},
		{"x • a", -1, "x • a"},
		{"- a", -1, "- a"},
	}
	for _, c := range cases {
		if got := glyphCol(c.line); got != c.col {
			t.Errorf("glyphCol(%q) = %d, want %d", c.line, got, c.col)
		}
		if got := restoreMarker(c.line, '-'); got != c.back {
			t.Errorf("restoreMarker(%q) = %q, want %q", c.line, got, c.back)
		}
	}
}

// What matters about undo is that a Ctrl+Z after typing takes the typing back, and
// not the bullet swap that a render pass made after it. GTK's own undo would take
// the swap first, and there is no way to keep the swap out of its history without
// clearing the history (gtk_text_buffer_begin_irreversible_action does that).
func TestUndoAfterASwapUndoesTheTyping(t *testing.T) {
	e := bulletEditor("")
	e.typeText(0, "- a\nb")
	e.buffer.PlaceCursor(e.buffer.IterAtOffset(5))
	// The caret has left line 0, so the pass draws its bullet.
	e.applyBulletEdits(e.planBullets(e.textNow(), 0, 1), false)
	if got := e.textNow(); got != "• a\nb" {
		t.Fatalf("the pass left %q", got)
	}

	e.undoStep()
	if got := e.textNow(); got != "" {
		t.Errorf("after undo the text is %q, want the typing gone", got)
	}
	if !e.buffer.CanRedo() {
		t.Error("the undo left nothing to redo")
	}

	e.redoStep()
	if got := e.textNow(); got != "• a\nb" {
		t.Errorf("after redo the text is %q, want the typing back with its bullet drawn", got)
	}
	if got, want := e.Content(), "- a\nb"; got != want {
		t.Errorf("Content() = %q after redo, want %q: the redone bullet lost its tag", got, want)
	}
	if e.buffer.CanRedo() {
		t.Error("the swap was left waiting to be redone")
	}
}

func TestUndoStepsOverSeveralPasses(t *testing.T) {
	e := bulletEditor("")
	e.typeText(0, "- a")
	e.applyBulletEdits(e.planBullets(e.textNow(), 0, -1), false) // drawn
	e.applyBulletEdits(e.planBullets(e.textNow(), 0, 0), false)  // the caret came back: put back
	e.applyBulletEdits(e.planBullets(e.textNow(), 0, -1), false) // drawn again
	e.undoStep()
	if got := e.textNow(); got != "" {
		t.Errorf("after undo the text is %q, want the typing gone", got)
	}
}

func TestUndoWithOnlySwapsLeftChangesNothing(t *testing.T) {
	e := bulletEditor("- a\nb")
	e.applyBulletEdits(e.planBullets(e.textNow(), 0, 1), false)
	e.undoStep()
	if got := e.textNow(); got != "• a\nb" {
		t.Errorf("text is %q, want the bullet still drawn", got)
	}
	if got, want := e.Content(), "- a\nb"; got != want {
		t.Errorf("Content() = %q, want %q", got, want)
	}
}

// The history is also driven without going through undoStep (the view's own
// Ctrl+Z on a layout that is not Latin, the context menu). The bullet must come
// back tagged all the same, or it would be saved as a "•".
func TestNativeRedoTagsTheBulletAgain(t *testing.T) {
	e := bulletEditor("- a\nb")
	e.applyBulletEdits(e.planBullets(e.textNow(), 0, 1), false)
	e.buffer.Undo()
	if got := e.textNow(); got != "- a\nb" {
		t.Fatalf("undo left %q", got)
	}
	e.buffer.Redo()
	if got := e.textNow(); got != "• a\nb" {
		t.Fatalf("redo left %q", got)
	}
	if got, want := e.Content(), "- a\nb"; got != want {
		t.Errorf("Content() = %q, want %q", got, want)
	}
}

func TestNoSwapWhileARedoIsWaiting(t *testing.T) {
	e := bulletEditor("- a\nb")
	e.typeText(5, "!")
	e.buffer.Undo()
	if !e.buffer.CanRedo() {
		t.Fatal("no redo to protect")
	}
	// The pass returns at once when a redo is waiting, before it looks at the view
	// (this editor has none), because a swap would be recorded and end the redo.
	e.renderBullets(0, 1, 1)
	if got := e.textNow(); got != "- a\nb" {
		t.Errorf("a pass edited the text to %q with a redo waiting", got)
	}
}
