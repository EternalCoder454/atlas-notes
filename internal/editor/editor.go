// Package editor implements the center WYSIWYG markdown editor: a GtkTextView
// whose buffer is re-tagged (headings, bold, italic, code) on a 50ms debounce.
// It is a live preview: the markdown syntax markers are hidden except around the
// exact construct the caret is in, so the text stays put as the caret moves.
// Checklist lines are converted to embedded checkbox widgets on the same pass,
// list markers are drawn as "•" away from the caret (see bullets.go), and a
// Markdown table is drawn as a grid of labels (see tables.go).
package editor

import (
	"log"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"atlas-notes/internal/markup"
)

const reparseDebounceMs = 50

// Editor is the center markdown editor component.
type Editor struct {
	view   *gtk.TextView
	buffer *gtk.TextBuffer
	scroll *gtk.ScrolledWindow
	tags   map[string]*gtk.TextTag

	reparseScheduled bool
	reparseDeferred  bool // a re-tag was due while text was selected
	loading          bool

	// Dirty range: the lines edited since the last render pass. Re-tagging the
	// whole document on every keystroke is what made typing in a long note
	// expensive, so a pass only touches these lines plus the caret's own line
	// (whose markers reveal) and the line it just left.
	dirtyFrom  int
	dirtyTo    int
	fullDirty  bool
	lastCursor int
	// fence says, per line, whether it is part of a fenced code block (the fences
	// included), as markup.InCodeFence reports it; nil when the note has no
	// fence at all. It is worked out again only when the text has changed, which
	// fenceStale records, and fenceLines is the number of lines it was worked out
	// for. See refreshFence.
	fence      []bool
	fenceLines int
	fenceStale bool
	// lastKey is what the last render pass left revealed around the caret, and
	// keyValid whether that pass got as far as knowing. A caret move that would
	// leave the key as it is changes nothing on screen and skips the pass (see
	// caretPassNeeded).
	lastKey  caretKey
	keyValid bool
	// freshLoad is set when a note has just been opened, so that the first pass to
	// draw its bullets leaves nothing in the undo history (see applyBulletEdits).
	freshLoad bool
	// histBefore is the text as a step of the undo history started, and histSwap
	// whether the step that has just run was only bullet swaps (see afterHistory).
	histBefore string
	histSwap   bool
	// snapping is set while the caret is being moved out of a task's hidden
	// metadata (see snapToMetadata), so that the move does not snap again.
	snapping   bool
	hasAnchors bool           // whether any checklist widget is embedded in the buffer
	items      []anchoredItem // embedded checklist rows, by the anchor holding them
	rowPool    []*itemRow     // rows kept for reuse instead of being rebuilt
	// revealCaret gates showing markdown markers on the caret's line. A freshly
	// opened note renders fully clean; the markers appear once the caret is
	// actually moved or something is typed.
	revealCaret bool
	// layoutStale is set by a render pass and cleared once GTK has laid the
	// text out again; hit-testing waits for it (see linkUnder).
	layoutStale   bool
	staleClearing bool
	staleGen      uint64
	heldPolling   bool // whenHandsFree is waiting for the button to come up
	pressing      bool // the primary button is down over the view (see New)
	// The pointer's last position over the text, for the link lookup once it
	// rests there; see hoverRestMs.
	hoverX, hoverY float64
	hoverCtrl      bool
	hoverIn        bool
	hoverGen       uint64
	hoverAt        time.Time // when the pointer last moved
	hoverArmed     bool      // the one hover timer is pending
	shown          int       // the line whose markers the last render pass left showing, or -1

	// press is what a click's button-down was over, kept for its release (see
	// installLinks); overLink is whether the pointer is currently a hand.
	press    linkPress
	overLink bool
	// sg is the suggestion popover for note names and tags (see complete.go).
	sg suggester
	// img is the state of the pictures shown under image lines (see images.go).
	img imageState
	// tbl is the state of the tables drawn in place of their Markdown (see
	// tables.go), and hasPipe whether the note has a pipe anywhere, which is what a
	// table needs; it is worked out with the fence (see refreshFence).
	tbl     tableState
	hasPipe bool

	// OnOpenNote, OnOpenTag and OnOpenURL are called when a link is clicked: a
	// note link with its target and heading (either may be a bare name or a
	// path), a tag without its "#", and a web address. A nil callback means
	// that kind of link does nothing.
	OnOpenNote func(target, heading string)
	OnOpenTag  func(tag string)
	OnOpenURL  func(url string)
	// NoteNames lists the notes that can be linked to, as vault-relative paths
	// without an extension ("Work/Todo"), and TagNames the tags in use without
	// their "#". They are asked for when a suggestion popover opens.
	NoteNames func() []string
	TagNames  func() []string

	// LoadImage returns the bytes of the picture a note's "![](path)" names, given
	// the path as written in the note. It runs on a goroutine, never on the main
	// thread, so it may read files. Without it images stay plain text. SaveImage
	// stores a pasted or dropped picture and returns the path to write into the
	// note; it runs on a goroutine too. OnImageError is told when adding one
	// failed, on the main thread. OnOpenImage is called with the path when a
	// picture is double-clicked.
	LoadImage    func(mdPath string) ([]byte, error)
	SaveImage    func(data []byte) (mdPath string, err error)
	OnImageError func(err error)
	OnOpenImage  func(mdPath string)

	// OnChanged fires after a user edit (suppressed during SetContent).
	OnChanged func()
	// OnReparsed fires once per debounced reparse (and after SetContent), letting
	// the app refresh derived UI — word count, AI button state — off the
	// per-keystroke path. Reconstructing the document on every keypress was the
	// main typing-latency cost.
	OnReparsed func()
}

// New builds the editor component.
func New() *Editor {
	e := &Editor{tags: map[string]*gtk.TextTag{}, dirtyFrom: -1, dirtyTo: -1, shown: -1, fenceStale: true}

	e.view = gtk.NewTextView()
	e.view.SetWrapMode(gtk.WrapWordChar)
	e.view.SetLeftMargin(16)
	e.view.SetRightMargin(16)
	e.view.SetTopMargin(12)
	e.view.SetBottomMargin(12)
	e.view.SetPixelsBelowLines(2)
	e.view.AddCSSClass("atlas-editor")
	e.buffer = e.view.Buffer()

	e.createTags()

	e.scroll = gtk.NewScrolledWindow()
	e.scroll.SetChild(e.view)
	e.scroll.SetVExpand(true)
	e.scroll.SetHExpand(true)

	// Record which lines an edit touched, so the render pass can be incremental.
	// These fire before the change is applied, which is exactly when the old
	// line numbers are still valid.
	e.buffer.ConnectInsertText(func(location *gtk.TextIter, text string, _ int) {
		line := location.Line()
		e.markDirty(line, line+strings.Count(text, "\n"))
	})
	e.buffer.ConnectDeleteRange(func(start, end *gtk.TextIter) {
		e.markDirty(start.Line(), end.Line())
	})

	e.buffer.ConnectChanged(func() {
		// Before the loading check: a programmatic edit can open or close a fence
		// as well as a typed one can.
		e.fenceStale = true
		if e.loading {
			return
		}
		e.scheduleReparse()
		if e.OnChanged != nil {
			e.OnChanged()
		}
	})
	// Enter on a list line continues the list (see continueList). Otherwise, at
	// the end of a task line, it keeps the task's hidden metadata (its priority
	// and due date, in a trailing comment) on the task. The caret is kept in front
	// of the comment (snapToMetadata), so the newline would otherwise split it off
	// onto the new line, where it belongs to nothing. Shift+Enter is always a plain
	// newline.
	enter := gtk.NewEventControllerKey()
	enter.SetPropagationPhase(gtk.PhaseCapture)
	enter.ConnectKeyPressed(func(keyval, _ uint, state gdk.ModifierType) bool {
		if keyval != gdk.KEY_Return && keyval != gdk.KEY_KP_Enter {
			return false
		}
		if state&(gdk.ShiftMask|gdk.ControlMask|gdk.AltMask) != 0 || e.buffer.HasSelection() {
			return false
		}
		if e.continueList() {
			return true // the list item, and its newline, are in
		}
		e.enterPastMetadata()
		return false // the view inserts the newline, wherever the caret now is
	})
	e.view.AddController(enter)
	e.installBullets()

	e.installItemMenu()
	e.installLinks()
	e.installComplete()
	e.installImages()

	e.buffer.ConnectMarkSet(func(_ *gtk.TextIter, mark *gtk.TextMark) {
		if mark.Name() != "insert" {
			return
		}
		if !e.loading {
			e.revealCaret = true
		}
		// A caret that has landed in a task's hidden metadata is moved out of it,
		// and that move arrives here again for the place it lands.
		if e.snapToMetadata() {
			return
		}
		if e.loading || e.caretPassNeeded() {
			e.scheduleReparse()
		}
	})

	// When a selection goes away, run what was held back while it existed: the
	// render pass (see reparse), the pictures' and tables' spacing (see placeBlocks) and the
	// find highlights (see finder.schedule). All of them change tags.
	e.buffer.NotifyProperty("has-selection", e.runHeld)

	// The primary button's press, followed as a drag gesture of our own that
	// watches and never claims the sequence, so the view's own selection drag
	// is untouched. Its begin and end are what say a drag is in progress: the
	// pointer device's own button state is not reported for every kind of
	// input (it was not for synthetic input, which is how the crash this
	// guards against was reproduced). See reparse.
	press := gtk.NewGestureDrag()
	press.SetButton(gdk.BUTTON_PRIMARY)
	press.SetPropagationPhase(gtk.PhaseCapture)
	press.ConnectDragBegin(func(float64, float64) { e.pressing = true })
	release := func() {
		if e.pressing {
			e.pressing = false
			e.runHeld()
		}
	}
	press.ConnectDragEnd(func(float64, float64) { release() })
	press.ConnectCancel(func(*gdk.EventSequence) { release() })
	e.view.AddController(press)

	return e
}

// Widget returns the scrollable editor widget.
func (e *Editor) Widget() gtk.Widgetter { return e.scroll }

// createTags defines the rendered look of each markdown construct. Spacing is
// part of the styling: headings get air above them and list items hang their
// text off a left margin, so a note reads like a document rather than a wall of
// monospaced source.
func (e *Editor) createTags() {
	e.newTag("heading1", map[string]any{
		"scale": 1.7, "weight": int(pango.WeightBold),
		"pixels-above-lines": 18, "pixels-below-lines": 8,
	})
	e.newTag("heading2", map[string]any{
		"scale": 1.32, "weight": int(pango.WeightBold),
		"pixels-above-lines": 16, "pixels-below-lines": 6,
	})
	e.newTag("heading3", map[string]any{
		"scale": 1.14, "weight": int(pango.WeightBold),
		"pixels-above-lines": 12, "pixels-below-lines": 4,
	})
	e.newTag("heading4", map[string]any{
		"scale": 1.0, "weight": int(pango.WeightBold),
		"pixels-above-lines": 10, "pixels-below-lines": 2,
	})
	e.newTag("bold", map[string]any{"weight": int(pango.WeightBold)})
	e.newTag("italic", map[string]any{"style": pango.StyleItalic})
	e.newTag("strike", map[string]any{"strikethrough": true})
	e.newTag("code", map[string]any{"family": "monospace", "scale": 0.94})
	// A line of a fenced code block. The background is a neutral gray with alpha,
	// so it lifts the block a little from a light page and from a dark one alike.
	e.newTag("codeblock", map[string]any{
		"family": "monospace", "scale": 0.94,
		"paragraph-background": "rgba(128,128,128,0.14)",
	})
	// Hidden markers are shrunk to nothing and drawn transparent rather than
	// made invisible. GTK's invisible text is removed from the line's layout,
	// and every hit-test then maps layout positions back past it; when a
	// line's hidden runs change between a layout and a hit-test (the caret
	// reaching a line reveals its markers, a drag hit-tests continuously) GTK
	// maps past the end of the line and aborts the process. Shrunk text stays
	// in the layout, so there is nothing to map around.
	e.newTag("invisible", map[string]any{"scale": 0.001, "foreground": "rgba(0,0,0,0)"})
	// Recessive markers (list bullets, quote bars) stay visible but quiet; a
	// mid-gray reads correctly against both the light and the dark theme.
	e.newTag("marker", map[string]any{"foreground": "#9a9a9a"})
	e.newTag("listitem", map[string]any{"indent": -14, "left-margin": 30, "pixels-below-lines": 3})
	e.newTag("quote", map[string]any{
		"left-margin": 26, "style": pango.StyleItalic, "foreground": "#9a9a9a",
	})
	e.newTag("divider", map[string]any{"foreground": "#9a9a9a", "scale": 0.8})
	e.createBulletTags()
	// A finished task reads as done: struck through and receded.
	e.newTag("done", map[string]any{"strikethrough": true, "foreground": "#9a9a9a"})
	// Links go last of the formatting tags, so their colour wins over the quote
	// and done grays. Nothing else creates tags after this, apart from the find
	// highlights, which are made on first use and so rank above all of these.
	e.createLinkTags()
}

func (e *Editor) newTag(name string, props map[string]any) {
	tag := gtk.NewTextTag(name)
	for k, v := range props {
		tag.SetObjectProperty(k, v)
	}
	e.buffer.TagTable().Add(tag)
	e.tags[name] = tag
}

// SetContent replaces the text without firing OnChanged, then re-renders. The
// caret is placed at the top, so opening a note shows its beginning.
func (e *Editor) SetContent(s string) {
	e.clearItems()  // the old note's checkboxes go with its text
	e.clearImages() // and so do its pictures
	e.clearTables() // and its tables
	e.closeSuggest()
	e.sg.dismissed = -1 // an Escape in the last note says nothing about this one

	// The text is replaced with the view detached from the buffer.
	//
	// Moving the cursor emits "mark-set", and the view answers that by
	// updating the input method's spot location, which asks for the cursor's
	// location, which lays the line out and keeps the result in GTK's
	// line-display cache. Opening a note therefore cached a fresh layout that
	// nothing ever released, and the cost grows with the size of the note: on
	// a vault of long notes, 1,200 opens grew memory by 271 MB (231 KB an
	// open) and by 81 MB (69 KB) with the view looking away for the
	// replacement. On ordinary notes it is a few KB either way — this is
	// insurance for the person with a very long note, not a general win.
	//
	// Nothing is anchored in the buffer at this point (clearItems above took
	// the checkboxes out), and the view is re-attached before the pass below
	// puts new ones in.
	e.view.SetBuffer(nil)
	e.withLoading(func() { e.buffer.SetText(s) })
	start, _ := e.buffer.Bounds()
	e.withLoading(func() { e.buffer.PlaceCursor(start) })
	e.view.SetBuffer(e.buffer)

	e.revealCaret = false
	e.freshLoad = true
	e.lastCursor = 0
	// One full pass: reparse renders every "- [ ] " line as a checkbox and
	// applies the tags. (This used to run the checklist pass twice.)
	e.markAllDirty()
	e.reparse()
	// Scroll back to the top once the new text has been laid out: a note should
	// open at its beginning, not wherever the last one was scrolled to.
	coreglib.IdleAdd(func() bool {
		e.scroll.VAdjustment().SetValue(0)
		return false
	})
}

// markDirty widens the range of lines the next render pass must re-tag.
func (e *Editor) markDirty(from, to int) {
	if from > to {
		from, to = to, from
	}
	if e.fullDirty {
		return
	}
	if e.dirtyFrom < 0 {
		e.dirtyFrom, e.dirtyTo = from, to
		return
	}
	if from < e.dirtyFrom {
		e.dirtyFrom = from
	}
	if to > e.dirtyTo {
		e.dirtyTo = to
	}
}

// markAllDirty forces the next pass to re-tag the whole document.
func (e *Editor) markAllDirty() { e.fullDirty = true }

// clearDirty resets the range after a render pass.
func (e *Editor) clearDirty() {
	e.fullDirty = false
	e.dirtyFrom, e.dirtyTo = -1, -1
}

// Content returns the full markdown text, reconstructing "- [ ]/[x] " prefixes
// from the embedded checkboxes, stripping the anchor characters and turning each
// "•" that stands for a list marker back into it (see sourceText).
func (e *Editor) Content() string {
	raw := e.sourceText()
	if !e.hasAnchors && !strings.Contains(raw, anchorChar) {
		return raw // no embedded checkboxes: the buffer text is the document
	}
	lines := strings.Split(raw, "\n")
	for i, line := range lines {
		r := []rune(line)
		if len(r) == 0 || r[0] != '￼' {
			continue
		}
		box := "[ ]"
		if e.anchorChecked(i) {
			box = "[x]"
		}
		lines[i] = "- " + box + " " + string(r[1:])
	}
	return strings.Join(lines, "\n")
}

// rawText returns the buffer text verbatim, including anchor characters and
// hidden metadata. It uses Slice (not Text) so that the returned string's
// character offsets line up with TextIter offsets — Text omits the 0xFFFC
// anchor characters, which would skew every offset on a checklist line.
func (e *Editor) rawText() string {
	start, end := e.buffer.Bounds()
	return e.buffer.Slice(start, end, true)
}

func (e *Editor) scheduleReparse() {
	if e.reparseScheduled {
		return
	}
	e.reparseScheduled = true
	coreglib.TimeoutAdd(reparseDebounceMs, func() bool {
		e.reparseScheduled = false
		e.reparse()
		return false // one-shot
	})
}

// reparse re-renders the document. It only touches the lines edited since the
// last pass, plus the caret's line and the line it left — those two change
// appearance because markdown markers are revealed on the line being edited.
// A pass after a single keystroke therefore costs the same in a 200-line note
// as in a 5-line one.
func (e *Editor) reparse() {
	// Never re-tag while text is selected.
	//
	// Re-tagging changes which characters are invisible, and GTK converts a
	// layout byte offset back into a buffer position whenever it hit-tests a
	// line — which is what selecting with the mouse does, continuously. Doing
	// that against a line whose invisible runs have just moved underneath it
	// aborts the process: "byte index off the end of the line", which is an
	// error-level GLib message and so calls abort().
	//
	// Dragging a selection moves the insert mark with every pixel, and each
	// move scheduled a re-tag 50 ms later, so a slow drag re-tagged the
	// document repeatedly while GTK was hit-testing it. The markers stay as
	// they are until the selection collapses, and then the pass that was owed
	// runs.
	//
	// Nor while the mouse button is down. A click or the start of a drag can
	// collapse a selection (find's "next" leaves one), and the pass that was
	// held for it then runs 50 ms later, in the middle of the drag, before a
	// new selection exists to hold it back. The button being down is what a
	// drag is, so that is what is checked.
	if e.handsBusy() {
		e.reparseDeferred = true
		e.whenHandsFree()
		return
	}

	cursorLine, caretCol := -1, -1
	if ins := e.buffer.IterAtMark(e.buffer.GetInsert()); ins != nil {
		cursorLine, caretCol = ins.Line(), ins.LineOffset()
	}

	revealLine := -1
	if e.revealCaret {
		revealLine = cursorLine
	}
	e.shown = revealLine // what the person sees from now on; a click asks (see linkPress)

	lastLine := e.buffer.LineCount() - 1
	from, to := e.dirtyFrom, e.dirtyTo
	if e.fullDirty || from < 0 {
		from, to = 0, lastLine
	} else {
		from = minInt(from, minInt(cursorLine, e.lastCursor))
		to = maxInt(to, maxInt(cursorLine, e.lastCursor))
	}
	from = clamp(from, lastLine)
	to = clamp(to, lastLine)
	if e.fenceStale {
		from, to = e.refreshFence(from, to, lastLine)
	}
	// A table is drawn or shown whole, so a pass that touches one covers it.
	from, to = e.widenForTables(from, to, lastLine)
	e.lastCursor = cursorLine
	e.clearDirty()

	breadcrumb("reparse lines %d-%d of %d, caret %d", from, to, lastLine+1, cursorLine)
	e.renderChecklists(from, to, revealLine)
	e.reapItems()
	e.renderBullets(from, to, revealLine)
	e.tagRange(from, to, revealLine, caretCol)
	e.syncImages(from, to)
	e.syncTables(from, to)

	if e.OnReparsed != nil {
		e.OnReparsed()
	}
	// The pass stripped the find highlights along with every other tag on the
	// lines it touched, so they are put back. This is called from here, not hung
	// on OnReparsed, which belongs to the app and may be assigned again.
	if f := finders[e]; f != nil {
		f.restore()
	}
}

// refreshFence works out again which lines are in a fenced code block, for a
// pass over lines from to to (of a document whose last line is lastLine). It
// returns the range widened to what must be re-tagged: opening or closing a
// fence changes every line after it, so when any line past the range is in a
// different state than it was in before the edit, the range runs to the end of
// the document.
func (e *Editor) refreshFence(from, to, lastLine int) (int, int) {
	old, oldLines := e.fence, e.fenceLines
	raw := e.rawText()
	var cur []bool
	// Nearly every note has no fence, and finding that out is much cheaper than
	// splitting the note into lines.
	if strings.Contains(raw, "```") || strings.Contains(raw, "~~~") {
		cur = markup.InCodeFence(raw)
	}
	e.fence, e.fenceLines, e.fenceStale = cur, lastLine+1, false
	// The same text is at hand for finding out whether a table is possible.
	e.hasPipe = strings.IndexByte(raw, '|') >= 0
	if fenceChanged(old, cur, oldLines, lastLine+1, to) {
		to = lastLine
	}
	return from, to
}

// fenceChanged reports whether any line after line to is in a different fenced
// state than before an edit. Lines after the edit moved with the text, so a line
// is compared with the one that was in its place then, which is the line the
// same number of lines from the end. A nil slice is a note with no fence.
func fenceChanged(old, cur []bool, oldLines, curLines, to int) bool {
	if old == nil && cur == nil {
		return false
	}
	shift := curLines - oldLines
	for j := to + 1; j < curLines; j++ {
		k := j - shift
		if k < 0 || k >= oldLines || inFence(cur, j) != inFence(old, k) {
			return true
		}
	}
	return false
}

// inFence reports whether line n is in a fenced code block. Lines past the end
// of fence (or all of them, when it is nil) are not.
func inFence(fence []bool, n int) bool {
	return n >= 0 && n < len(fence) && fence[n]
}

// fenceRole is what part a line plays in a fenced code block.
type fenceRole uint8

const (
	fenceNone fenceRole = iota // an ordinary line
	fenceEdge                  // a line that opens or closes a block
	fenceBody                  // a line between the fences
)

// roleOf says what line n, whose text is line, is in a note whose fenced lines
// are fence. The first line of a run is the opening fence. The last line of a run
// is the closing fence when it starts like one (the run may also just end with
// the note, when the fence was never closed).
func roleOf(fence []bool, n int, line string) fenceRole {
	if !inFence(fence, n) {
		return fenceNone
	}
	if !inFence(fence, n-1) {
		return fenceEdge
	}
	if !inFence(fence, n+1) {
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			return fenceEdge
		}
	}
	return fenceBody
}

// fenceSpans is what a line of a code block gets in place of everything else:
// nothing is parsed inside a block, not headings, not emphasis, not links. The
// fences are dimmed and the lines between them set in code.
func fenceSpans(line string, role fenceRole) []span {
	n := utf8.RuneCountInString(line)
	if role == fenceEdge {
		return []span{{"marker", 0, n}}
	}
	return []span{{"codeblock", 0, n}}
}

// tagRange re-applies every formatting tag for lines [from, to]. caret is the
// caret's character offset in cursorLine, which is -1 when no line is showing
// its markers.
func (e *Editor) tagRange(from, to, cursorLine, caret int) {
	// Tags are about to change under a layout the pointer may be hit-tested
	// against (see linkUnder). Every place that changes tags says so itself; the
	// buffer's tag signals are not listened to, as they fire for each tag of each
	// range and a render pass makes thousands of those.
	e.markLayoutStale()
	e.keyValid = false
	start, ok1 := e.buffer.IterAtLine(from)
	end, ok2 := e.buffer.IterAtLine(to)
	if !ok1 || !ok2 {
		return
	}
	if !end.EndsLine() {
		end.ForwardToLineEnd()
	}
	text := e.buffer.Slice(start, end, true) // Slice keeps offsets aligned with TextIter
	// A "•" is a list marker as far as parsing goes, and its tag is what says so,
	// so the bullets are found before the tags come off and given theirs back.
	bullets := e.swappedBullets(from, text)
	e.buffer.RemoveAllTags(start, end)

	lines := strings.Split(text, "\n")
	// Tables are looked for only where there is a pipe, and a line without one
	// costs nothing more than the search for it.
	var tables []tableAt
	if e.hasPipe && strings.IndexByte(text, '|') >= 0 {
		tables = e.findTablesIn(lines, from, bullets)
	}
	nextTable := 0

	key := caretKey{line: -1}
	lineNum := from
	for _, line := range lines {
		// The caret is only looked for on its own line, and the key says what it
		// revealed there.
		at, keyed := -1, (*caretKey)(nil)
		if lineNum == cursorLine {
			at, key = caret, caretKey{line: lineNum}
			keyed = &key
		}
		// A completed task reads as done (struck through, receded). Only a
		// line that starts with an anchor can be one, and only then is the
		// line's length in characters needed.
		if strings.HasPrefix(line, anchorChar) && e.anchorChecked(lineNum) {
			e.applyTag("done", lineNum, 1, utf8.RuneCountInString(line))
		}
		parse := line
		marker := bullets[lineNum] // 0 when the line is not drawn as a bullet
		if marker != 0 {
			parse = restoreMarker(line, marker)
		}
		for nextTable < len(tables) && lineNum >= tables[nextTable].line+tables[nextTable].lines {
			nextTable++
		}
		if role := roleOf(e.fence, lineNum, parse); role != fenceNone {
			e.tagFenceLine(lineNum, parse, role)
		} else if nextTable < len(tables) && lineNum >= tables[nextTable].line {
			tb := &tables[nextTable]
			e.tagTableLine(lineNum, parse, tb, cursorLine >= tb.line && cursorLine < tb.line+tb.lines)
			if keyed != nil {
				// What caretPassNeeded will ask about this line, so that a caret
				// moving in a table's Markdown does not draw it again for nothing.
				parseLine(parse, at, keyed)
				linkSpansKey(parse, at, keyed)
			}
		} else {
			e.tagLine(lineNum, parse, at, keyed)
		}
		if marker != 0 {
			if it, ok := e.buffer.IterAtLineOffset(lineNum, glyphCol(line)); ok {
				e.setBulletTag(it.Offset(), marker)
			}
		}
		if breadcrumbsOn {
			breadcrumb("tagged line %d: %d chars, %d bytes", lineNum,
				utf8.RuneCountInString(line), len(line))
		}
		lineNum++
	}
	e.lastKey, e.keyValid = key, true
}

// tagLine applies the tags of an ordinary line: the caret is at character offset
// at in it (-1 when it is elsewhere), and key, when not nil, collects what the
// caret reveals.
func (e *Editor) tagLine(lineNum int, line string, at int, key *caretKey) {
	// Spans come back within the line's bounds (enforced by the fuzz test
	// over parseLineSpans), and an offset past the end simply fails to
	// resolve to an iterator, so no clamping is done here: measuring the
	// line in characters for every render pass was costing more than the
	// parse itself.
	for _, sp := range parseLine(line, at, key) {
		e.applyTag(sp.tag, lineNum, sp.start, sp.end)
	}
	// Note links, tags and web addresses go on top. A line with none of them
	// costs a few substring searches and allocates nothing.
	for _, sp := range linkSpansKey(line, at, key) {
		e.applyTag(sp.tag, lineNum, sp.start, sp.end)
	}
	// A line that is a picture hides its Markdown, away from the caret, and
	// makes room below itself for the picture. Only lines with "![" pay. A
	// picture is one thing, so the caret anywhere on its line shows it all.
	if e.imagesOn() && strings.Contains(line, "![") {
		e.tagImageLine(lineNum, line, at >= 0)
	}
}

// tagFenceLine tags a line that is part of a fenced code block.
func (e *Editor) tagFenceLine(lineNum int, line string, role fenceRole) {
	for _, sp := range fenceSpans(line, role) {
		if sp.end > sp.start {
			e.applyTag(sp.tag, lineNum, sp.start, sp.end)
			continue
		}
		// An empty line has no character to carry the tag, and the block's
		// background is taken from the tags at the start of the line. The
		// newline that ends it carries the tag instead.
		e.tagNewline(sp.tag, lineNum)
	}
}

// tagNewline applies a tag to the line break that ends a line. The last line has
// none.
func (e *Editor) tagNewline(name string, line int) {
	tag := e.tags[name]
	if tag == nil {
		return
	}
	si, ok := e.buffer.IterAtLine(line)
	if !ok || si == nil || !si.EndsLine() {
		return
	}
	ei := si.Copy()
	ei.ForwardChar()
	if ei.Offset() == si.Offset() {
		return // the end of the buffer
	}
	e.buffer.ApplyTag(tag, si, ei)
}

// snapToMetadata moves the caret out of a line's hidden metadata comment, when
// that is where it has just landed (see snapCaret), and reports whether it did.
// It leaves a selection alone, and so a drag that sweeps over the comment,
// and it does nothing while a note is loading.
func (e *Editor) snapToMetadata() bool {
	if e.loading || e.snapping || e.buffer.HasSelection() {
		return false
	}
	ins := e.buffer.IterAtMark(e.buffer.GetInsert())
	if ins == nil {
		return false
	}
	line, col := ins.Line(), ins.LineOffset()
	text, ok := e.lineText(line)
	if !ok || !strings.Contains(text, "<!--") {
		return false
	}
	to := snapCaret(text, col)
	if to == col {
		return false
	}
	at, ok := e.buffer.IterAtLineOffset(line, to)
	if !ok {
		return false
	}
	e.snapping = true
	e.buffer.PlaceCursor(at)
	e.snapping = false
	return true
}

// caretPassNeeded reports whether the caret's move has changed what the pass
// draws. A move within a line changes it only when the construct the caret is in
// changes, or it enters or leaves a heading's or quote's prefix; moving from
// character to character inside one bold word changes nothing, and a render pass
// is many calls into GTK. Edited text always needs a pass.
func (e *Editor) caretPassNeeded() bool {
	if e.fullDirty || e.dirtyFrom >= 0 || !e.keyValid {
		return true
	}
	ins := e.buffer.IterAtMark(e.buffer.GetInsert())
	if ins == nil {
		return true
	}
	key := caretKey{line: ins.Line()}
	if !inFence(e.fence, key.line) {
		text, ok := e.lineText(key.line)
		if !ok {
			return true
		}
		col := ins.LineOffset()
		parseLine(text, col, &key)
		linkSpansKey(text, col, &key)
	}
	return key != e.lastKey
}

// breadcrumb records what the editor is about to do, for the case where GTK
// then aborts the process.
//
// A GLib message at error level calls abort(), so there is no recovering and
// no Go stack worth reading: the crash lands in cgo with GTK's own message the
// last thing in the journal. A line from us immediately before it is what
// turns "it keeps closing" into a note, a line number and a length. It is off
// unless ATLAS_DEBUG_EDITOR is set, because this runs on every keystroke.
var breadcrumbsOn = os.Getenv("ATLAS_DEBUG_EDITOR") != ""

func breadcrumb(format string, args ...any) {
	if breadcrumbsOn {
		log.Printf("atlas-notes: editor: "+format, args...)
	}
}

// applyTag applies a named tag over a character range within one line.
//
// The range is clamped against what the buffer says the line is, not against
// what the caller measured. An offset past the end of a line does not fail
// quietly: GTK logs "byte index off the end of the line" at error level, and
// an error-level GLib log aborts the process. This code used to check the
// boolean the iterator call returns, which is too late — the abort has already
// happened by the time it comes back.
//
// Every caller derives offsets from a string it read out of the buffer, so
// they agree with the buffer as long as the two never disagree about where a
// line ends. That is a lot of things to keep true at once, and one of them not
// being true crashed the app rather than misplacing a tag.
func (e *Editor) applyTag(name string, line, start, end int) {
	if end <= start {
		return
	}
	tag := e.tags[name]
	if tag == nil {
		return
	}
	limit, ok := e.lineCharLen(line)
	if !ok {
		return
	}
	start, end = clamp(start, limit), clamp(end, limit)
	if end <= start {
		return
	}
	si, ok1 := e.buffer.IterAtLineOffset(line, start)
	ei, ok2 := e.buffer.IterAtLineOffset(line, end)
	if !ok1 || !ok2 {
		return
	}
	e.buffer.ApplyTag(tag, si, ei)
}

// lineCharLen is how many characters a line holds, not counting the newline
// that ends it. It is the only offset the buffer will accept as a line's end.
// A line number past the end of the buffer reports not ok.
func (e *Editor) lineCharLen(line int) (int, bool) {
	if line < 0 || line >= e.buffer.LineCount() {
		return 0, false
	}
	it, ok := e.buffer.IterAtLine(line)
	if !ok || it == nil {
		return 0, false
	}
	n := it.CharsInLine()
	if !it.EndsLine() && n > 0 {
		n-- // drop the trailing newline; the last line has none
	}
	if n < 0 {
		n = 0
	}
	return n, true
}

func minInt(a, b int) int {
	if b < a {
		return b
	}
	return a
}

func maxInt(a, b int) int {
	if b > a {
		return b
	}
	return a
}

func clamp(v, max int) int {
	if v < 0 {
		return 0
	}
	if v > max {
		return max
	}
	return v
}

// withLoading runs fn with change notifications suppressed, restoring the
// previous state afterwards. Setting the flag directly does not nest: a helper
// that cleared it in the middle of a render pass used to let the rest of that
// pass look like user edits, which marked a freshly opened note dirty and
// scheduled a save for a note nobody had touched.
func (e *Editor) withLoading(fn func()) {
	prev := e.loading
	e.loading = true
	defer func() { e.loading = prev }()
	fn()
}

// InsertAtCursor inserts text at the caret (replacing the selection, if any),
// exactly as typing it would. Used by the formatting toolbar and the bench mode.
func (e *Editor) InsertAtCursor(text string) {
	e.buffer.DeleteSelection(true, true)
	e.buffer.InsertAtCursor(text)
}

// Reparse re-renders the document now, skipping the debounce. Call it after a
// programmatic edit that must be reflected immediately.
func (e *Editor) Reparse() { e.reparse() }

// handsBusy reports whether the person is in the middle of selecting: text is
// selected, or the primary button is down over the view. Nothing that changes
// tags may run then; see reparse.
func (e *Editor) handsBusy() bool {
	return e.buffer.HasSelection() || e.buttonDown()
}

// buttonDown asks the pointer itself whether its primary button is held, so
// the answer is right however the press began or ended: a drag that became a
// drag-and-drop, a press on a picture, a release outside the window.
func (e *Editor) buttonDown() bool {
	if e.pressing {
		return true
	}
	display := e.view.Display()
	if display == nil {
		return false
	}
	seat := display.DefaultSeat()
	if seat == nil {
		return false
	}
	pointer := gdk.BaseSeat(seat).Pointer()
	if pointer == nil {
		return false
	}
	return gdk.BaseDevice(pointer).ModifierState()&gdk.Button1Mask != 0
}

// whenHandsFree runs what was held back once the button is up and nothing is
// selected. A selection going away is announced (has-selection), a button
// coming up is not, so while the button is down this looks again every 50 ms.
func (e *Editor) whenHandsFree() {
	if e.heldPolling {
		return
	}
	e.heldPolling = true
	coreglib.TimeoutAdd(50, func() bool {
		if e.buttonDown() {
			return true // still dragging; look again
		}
		e.heldPolling = false
		e.runHeld()
		return false
	})
}

// runHeld runs what was put off while the person was selecting: the render
// pass, the pictures' spacing and the find highlights, all of which change
// tags. If they are still selecting, it waits.
func (e *Editor) runHeld() {
	if e.handsBusy() {
		if e.buttonDown() {
			e.whenHandsFree()
		}
		return
	}
	if e.reparseDeferred {
		e.reparseDeferred = false
		e.scheduleReparse()
	}
	if e.img.padDeferred {
		e.img.padDeferred = false
		e.queuePlace()
	}
	if f := finders[e]; f != nil {
		f.resume()
	}
}

// enterPastMetadata moves the caret past a task line's trailing metadata when
// it sits at the end of the task's text, so a newline typed there goes after
// the comment. It moves under the snapping guard, or snapToMetadata would put
// the caret straight back in front of the comment.
func (e *Editor) enterPastMetadata() {
	ins := e.buffer.IterAtMark(e.buffer.GetInsert())
	if ins == nil {
		return
	}
	line, col := ins.Line(), ins.LineOffset()
	text, ok := e.lineText(line)
	if !ok || !strings.Contains(text, "<!--") {
		return
	}
	stop := snapCaret(text, utf8.RuneCountInString(text))
	if stop == utf8.RuneCountInString(text) || col != stop {
		return // no trailing metadata, or the caret is mid-text
	}
	end, ok := e.lineCharLen(line)
	if !ok {
		return
	}
	at, ok := e.buffer.IterAtLineOffset(line, end)
	if !ok {
		return
	}
	e.snapping = true
	e.buffer.PlaceCursor(at)
	e.snapping = false
}
