package editor

import (
	"strings"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// This file draws list markers as a real bullet. A "-", "*" or "+" that starts a
// list item is swapped in the buffer for "•" whenever the caret is not on that
// line, and swapped back when the caret arrives, so the Markdown shows while a
// line is being edited, as it does for every other marker.
//
// The swap is display only. Each "•" carries a tag saying which character it
// stands for (bullet-dash, bullet-star, bullet-plus), and everything that reads
// the note back out (Content, find, the formatting commands) maps a tagged "•"
// to that character. A "•" without a tag was typed by the person and stays.

const (
	bulletGlyph = "•"
	bulletRune  = '•'
)

// bulletKinds pairs each marker with the tag that records it.
var bulletKinds = [...]struct {
	tag    string
	marker byte
}{
	{"bullet-dash", '-'},
	{"bullet-star", '*'},
	{"bullet-plus", '+'},
}

// bulletTagFor is the name of the tag for a marker, or "" for a character that
// is not one.
func bulletTagFor(marker byte) string {
	for _, k := range bulletKinds {
		if k.marker == marker {
			return k.tag
		}
	}
	return ""
}

func isMarkerRune(r rune) bool { return r == '-' || r == '*' || r == '+' }

// createBulletTags defines the tags that mark a "•" as standing for a marker.
// They change nothing about how the text looks; they only carry the fact.
func (e *Editor) createBulletTags() {
	for _, k := range bulletKinds {
		e.newTag(k.tag, map[string]any{})
	}
}

// glyphCol is the character column of the "•" in a line drawn as a bullet (its
// indentation, then "•" and a space), or -1 when the line is not shaped like one.
// The indentation is ASCII, so the column is also a byte offset.
func glyphCol(line string) int {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	if strings.HasPrefix(line[i:], bulletGlyph+" ") {
		return i
	}
	return -1
}

// restoreMarker puts marker back in place of the "•" of a line drawn as a bullet,
// which is the line the Markdown parser has to be shown.
func restoreMarker(line string, marker byte) string {
	col := glyphCol(line)
	if col < 0 {
		return line
	}
	return line[:col] + string(rune(marker)) + line[col+len(bulletGlyph):]
}

// bulletMark says that the character at a buffer offset is a "•" standing for a
// marker.
type bulletMark struct {
	offset int
	marker byte
}

// unbullet returns text with each tagged "•" turned back into its marker. text
// starts at buffer offset base, and the marks are in buffer offsets. The result
// has as many characters as text, so offsets into it are still buffer offsets.
func unbullet(text string, base int, marks []bulletMark) string {
	if len(marks) == 0 || !strings.Contains(text, bulletGlyph) {
		return text
	}
	r := []rune(text)
	for _, m := range marks {
		if i := m.offset - base; i >= 0 && i < len(r) && r[i] == bulletRune {
			r[i] = rune(m.marker)
		}
	}
	return string(r)
}

// taggedBullets lists every "•" in the buffer that carries a bullet tag. It walks
// the tags' toggles, so its cost follows the number of bullets and not the length
// of the note.
func (e *Editor) taggedBullets() []bulletMark {
	var marks []bulletMark
	total := e.buffer.CharCount()
	for _, k := range bulletKinds {
		tag := e.tags[k.tag]
		if tag == nil {
			continue
		}
		it := e.buffer.StartIter()
		for {
			// A range that begins at the very start of the buffer is not a toggle
			// "after" the start, so it is asked about first.
			if !it.StartsTag(tag) && !it.ForwardToTagToggle(tag) {
				break
			}
			if !it.StartsTag(tag) {
				continue // the end of a range; look for the next one
			}
			from, to := it.Offset(), total
			if it.ForwardToTagToggle(tag) {
				to = it.Offset()
			}
			for o := from; o < to; o++ {
				marks = append(marks, bulletMark{o, k.marker})
			}
		}
	}
	return marks
}

// sourceText is the buffer's text with every tagged "•" turned back into the
// marker it stands for. Checkbox anchors are left in, so its offsets are the
// buffer's, which is what find measures against (see findMatches).
func (e *Editor) sourceText() string {
	raw := e.rawText()
	if !strings.Contains(raw, bulletGlyph) {
		return raw // nearly every note
	}
	return unbullet(raw, 0, e.taggedBullets())
}

// sourceSlice is sourceText for the stretch between two iterators.
func (e *Editor) sourceSlice(start, end *gtk.TextIter) string {
	text := e.buffer.Slice(start, end, true)
	if !strings.Contains(text, bulletGlyph) {
		return text
	}
	return unbullet(text, start.Offset(), e.taggedBullets())
}

// bulletMarkerAt is the marker the "•" at a line and column stands for, or 0 when
// the character there is not a tagged one.
func (e *Editor) bulletMarkerAt(line, col int) byte {
	it, ok := e.buffer.IterAtLineOffset(line, col)
	if !ok {
		return 0
	}
	for _, k := range bulletKinds {
		if t := e.tags[k.tag]; t != nil && it.HasTag(t) {
			return k.marker
		}
	}
	return 0
}

// sourceLine is lineText with a drawn bullet turned back into its marker.
func (e *Editor) sourceLine(ln int) (string, bool) {
	text, ok := e.lineText(ln)
	if !ok {
		return "", false
	}
	if c := glyphCol(text); c >= 0 {
		if m := e.bulletMarkerAt(ln, c); m != 0 {
			text = restoreMarker(text, m)
		}
	}
	return text, true
}

// setBulletTag makes the character at a buffer offset carry the tag for marker,
// and clears any other bullet tag from it. A marker of 0 only clears.
func (e *Editor) setBulletTag(offset int, marker byte) {
	from := e.buffer.IterAtOffset(offset)
	to := e.buffer.IterAtOffset(offset + 1)
	for _, k := range bulletKinds {
		if t := e.tags[k.tag]; t != nil && k.marker != marker {
			e.buffer.RemoveTag(t, from, to)
		}
	}
	if t := e.tags[bulletTagFor(marker)]; t != nil {
		e.buffer.ApplyTag(t, from, to)
	}
}

// bulletEdit is one character to swap.
type bulletEdit struct {
	line, col int
	with      string // the character to put there
	marker    byte   // what a "•" put there stands for, or 0 when putting a marker back
}

// planBullets works out the swaps for lines from on, whose text is text. A list
// marker on a line the caret is not on becomes "•"; a "•" that should not be one
// any more (the caret has come onto its line, the line is now inside a code
// block, or the text around it has changed so that it no longer starts a list
// item) goes back to its marker. Task lines are left alone: they are checkboxes.
func (e *Editor) planBullets(text string, from, revealLine int) []bulletEdit {
	var edits []bulletEdit
	lineNum := from
	for _, line := range strings.Split(text, "\n") {
		code := inFence(e.fence, lineNum) || e.inFront(lineNum) // front matter is not a list
		if strings.Contains(line, bulletGlyph) {
			start := glyphCol(line)
			col := 0
			for _, r := range line {
				if r == bulletRune {
					if m := e.bulletMarkerAt(lineNum, col); m != 0 &&
						(code || lineNum == revealLine || col != start) {
						edits = append(edits, bulletEdit{lineNum, col, string(rune(m)), 0})
					}
				}
				col++
			}
		}
		if !code && lineNum != revealLine {
			if m := bulletPrefix(line); m > 0 {
				edits = append(edits, bulletEdit{lineNum, m - 2, bulletGlyph, line[m-2]})
			}
		}
		lineNum++
	}
	return edits
}

// renderBullets swaps the list markers of lines from to to, which is a pass of
// the render loop and so not an edit: it runs with change notifications off, and
// the caret keeps its offset.
func (e *Editor) renderBullets(from, to, revealLine int) {
	// The same rule as for tags (see reparse): text does not change under a
	// selection or a drag. And while a redo is waiting, a swap would be recorded
	// and end the redo, so the markers stay as they are until the person edits or
	// redoes (see redoStep).
	if to < from || e.buffer.CanRedo() || e.handsBusy() {
		return
	}
	start, ok1 := e.buffer.IterAtLine(from)
	end, ok2 := e.buffer.IterAtLine(to)
	if !ok1 || !ok2 {
		return
	}
	if !end.EndsLine() {
		end.ForwardToLineEnd()
	}
	edits := e.planBullets(e.buffer.Slice(start, end, true), from, revealLine)
	fresh := e.freshLoad
	e.freshLoad = false
	if len(edits) == 0 && !fresh {
		return
	}
	e.applyBulletEdits(edits, fresh)
}

// applyBulletEdits makes the swaps. They are one user action, so that undo
// treats a pass as one step (see undoStep). On a note that has just been opened
// they are irreversible instead: there is no history to keep then, opening a note
// clears it, and the first Ctrl+Z must not undo the drawing.
func (e *Editor) applyBulletEdits(edits []bulletEdit, irreversible bool) {
	// A swap is neither an edit that needs another render pass nor one that can
	// change where a code fence is, so what the edit handlers noted is put back.
	from, to, full, stale := e.dirtyFrom, e.dirtyTo, e.fullDirty, e.fenceStale
	e.markLayoutStale()
	e.withLoading(func() {
		if irreversible {
			e.buffer.BeginIrreversibleAction()
			defer e.buffer.EndIrreversibleAction()
		} else {
			e.buffer.BeginUserAction()
			defer e.buffer.EndUserAction()
		}
		for _, ed := range edits {
			e.replaceChar(ed)
		}
	})
	e.dirtyFrom, e.dirtyTo, e.fullDirty, e.fenceStale = from, to, full, stale
}

// replaceChar swaps the one character at a line and column. The new character is
// put in after the old one and the old one then deleted, because a mark sitting
// at either side of it keeps its offset that way, where delete then insert
// would push a caret in front of the character to behind it.
func (e *Editor) replaceChar(ed bulletEdit) {
	after, ok := e.buffer.IterAtLineOffset(ed.line, ed.col+1)
	if !ok {
		return
	}
	e.buffer.Insert(after, ed.with)
	from, ok1 := e.buffer.IterAtLineOffset(ed.line, ed.col)
	to, ok2 := e.buffer.IterAtLineOffset(ed.line, ed.col+1)
	if !ok1 || !ok2 {
		return
	}
	offset := from.Offset()
	e.buffer.Delete(from, to)
	e.setBulletTag(offset, ed.marker)
}

// swappedBullets finds, among the lines from on whose text is text, the ones
// drawn as a bullet, and says which marker each stands for. A render pass strips
// every tag from the lines it re-tags, this one included, so it is asked before.
func (e *Editor) swappedBullets(from int, text string) map[int]byte {
	if !strings.Contains(text, bulletGlyph) {
		return nil
	}
	var out map[int]byte
	for i, line := range strings.Split(text, "\n") {
		c := glyphCol(line)
		if c < 0 {
			continue
		}
		if m := e.bulletMarkerAt(from+i, c); m != 0 {
			if out == nil {
				out = map[int]byte{}
			}
			out[from+i] = m
		}
	}
	return out
}

// bulletSwapDiff says whether after differs from before only by list markers
// swapped for "•" or back, and if so where. It is how a step of GTK's undo
// history is recognised as one of ours: the history records text and not tags,
// so it can put a "•" back without its tag, and it has to be told.
func bulletSwapDiff(before, after string) ([]bulletMark, bool) {
	a, b := []rune(before), []rune(after)
	if len(a) != len(b) {
		return nil, false
	}
	var marks []bulletMark
	for i := range a {
		if a[i] == b[i] {
			continue
		}
		var mark bulletMark
		switch {
		case b[i] == bulletRune && isMarkerRune(a[i]):
			mark = bulletMark{i, byte(a[i])}
		case a[i] == bulletRune && isMarkerRune(b[i]):
			mark = bulletMark{i, 0}
		default:
			return nil, false
		}
		if !atMarkerColumn(b, i) {
			return nil, false
		}
		marks = append(marks, mark)
	}
	return marks, len(marks) > 0
}

// atMarkerColumn reports whether r[i] is where a list marker sits: only
// indentation before it on its line, and a space after it.
func atMarkerColumn(r []rune, i int) bool {
	if i+1 >= len(r) || r[i+1] != ' ' {
		return false
	}
	for j := i - 1; j >= 0 && r[j] != '\n'; j-- {
		if r[j] != ' ' && r[j] != '\t' {
			return false
		}
	}
	return true
}

// installBullets hooks up what keeps the drawn bullets right through undo and
// redo: the buffer's history, and the keys that drive it.
func (e *Editor) installBullets() {
	e.installHistory()

	// Ctrl+Z and Ctrl+Shift+Z (or Ctrl+Y) step over the bullet swaps, which are in
	// the history as steps of their own but are not something the person did.
	keys := gtk.NewEventControllerKey()
	keys.SetPropagationPhase(gtk.PhaseCapture)
	keys.ConnectKeyPressed(func(keyval, _ uint, state gdk.ModifierType) bool {
		if state&gdk.ControlMask == 0 || state&(gdk.AltMask|gdk.SuperMask) != 0 || e.cardFocused() {
			return false // and Ctrl+Z in the properties card is the card entry's
		}
		var redo bool
		switch keyval {
		case gdk.KEY_z, gdk.KEY_Z:
			redo = state&gdk.ShiftMask != 0
		case gdk.KEY_y, gdk.KEY_Y:
			redo = true
		default:
			return false
		}
		if redo {
			e.redoStep()
		} else {
			e.undoStep()
		}
		e.view.ScrollMarkOnscreen(e.buffer.GetInsert())
		return true
	})
	e.view.AddController(keys)
}

// installHistory watches every undo and redo the buffer performs, however it was
// asked for. The history replays text and not tags, so replaying a swap can leave
// a "•" without its tag, which would then be saved as a "•". afterHistory tags it
// again.
func (e *Editor) installHistory() {
	before := func() { e.histBefore = e.rawText() }
	e.buffer.ConnectUndo(before)
	e.buffer.ConnectRedo(before)
	e.buffer.ConnectAfter("undo", e.afterHistory)
	e.buffer.ConnectAfter("redo", e.afterHistory)
}

// afterHistory runs after a step of the history has been applied. It records
// whether the step was only bullet swaps, and gives every "•" the step put back
// the tag for what it stands for.
func (e *Editor) afterHistory() {
	marks, ok := bulletSwapDiff(e.histBefore, e.rawText())
	e.histBefore, e.histSwap = "", ok
	if !ok {
		return
	}
	e.markLayoutStale()
	e.withLoading(func() {
		for _, m := range marks {
			e.setBulletTag(m.offset, m.marker)
		}
	})
}

// undoStep undoes what the person last did. The bullet swaps that came after it
// are undone on the way, silently: they are in the history because the buffer's
// text is what the history records, but to the person they are not a step, and
// making them press Ctrl+Z once more for each would undo nothing they can see.
func (e *Editor) undoStep() {
	swaps := 0
	for e.buffer.CanUndo() {
		e.withLoading(e.buffer.Undo)
		if e.histSwap {
			swaps++
			continue
		}
		e.markEdited()
		return
	}
	// Nothing but swaps was left to undo. They go back, so the note is not left
	// showing its markers with nothing that changed.
	for ; swaps > 0; swaps-- {
		e.withLoading(e.buffer.Redo)
	}
}

// redoStep redoes what the person last undid, and then the swaps that followed
// it, so the note comes back drawn as it was.
func (e *Editor) redoStep() {
	for e.buffer.CanRedo() {
		e.withLoading(e.buffer.Redo)
		if e.histSwap {
			continue
		}
		e.markEdited()
		e.redoSwaps()
		return
	}
}

// redoSwaps redoes the swaps waiting after a step, up to the next step of the
// person's. There is no way to look at the next step without taking it, so it is
// taken and, when it is not a swap, undone again. The caret is put back after,
// because the history moves it to wherever each step happened.
func (e *Editor) redoSwaps() {
	caret := e.buffer.IterAtMark(e.buffer.GetInsert()).Offset()
	for e.buffer.CanRedo() {
		e.withLoading(e.buffer.Redo)
		if !e.histSwap {
			e.withLoading(e.buffer.Undo)
			break
		}
	}
	e.withLoading(func() { e.buffer.PlaceCursor(e.buffer.IterAtOffset(caret)) })
}
