// Package editor implements the center WYSIWYG markdown editor: a GtkTextView
// whose buffer is re-tagged (headings, bold, italic, code) on a 50ms debounce,
// hiding the markdown syntax markers except on the line holding the cursor.
// Checklist lines are converted to embedded checkbox widgets on the same pass.
package editor

import (
	"log"
	"os"
	"strings"
	"unicode/utf8"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
)

const reparseDebounceMs = 50

// Editor is the center markdown editor component.
type Editor struct {
	view   *gtk.TextView
	buffer *gtk.TextBuffer
	scroll *gtk.ScrolledWindow
	tags   map[string]*gtk.TextTag

	reparseScheduled bool
	loading          bool

	// Dirty range: the lines edited since the last render pass. Re-tagging the
	// whole document on every keystroke is what made typing in a long note
	// expensive, so a pass only touches these lines plus the caret's own line
	// (whose markers reveal) and the line it just left.
	dirtyFrom  int
	dirtyTo    int
	fullDirty  bool
	lastCursor int
	hasAnchors bool           // whether any checklist widget is embedded in the buffer
	items      []anchoredItem // embedded checklist rows, by the anchor holding them
	rowPool    []*itemRow     // rows kept for reuse instead of being rebuilt
	// revealCaret gates showing markdown markers on the caret's line. A freshly
	// opened note renders fully clean; the markers appear once the caret is
	// actually moved or something is typed.
	revealCaret bool

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
	e := &Editor{tags: map[string]*gtk.TextTag{}, dirtyFrom: -1, dirtyTo: -1}

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
		if e.loading {
			return
		}
		e.scheduleReparse()
		if e.OnChanged != nil {
			e.OnChanged()
		}
	})
	e.installItemMenu()

	e.buffer.ConnectMarkSet(func(_ *gtk.TextIter, mark *gtk.TextMark) {
		if mark.Name() != "insert" {
			return
		}
		if !e.loading {
			e.revealCaret = true
		}
		e.scheduleReparse()
	})

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
	e.newTag("invisible", map[string]any{"invisible": true})
	// Recessive markers (list bullets, quote bars) stay visible but quiet; a
	// mid-gray reads correctly against both the light and the dark theme.
	e.newTag("marker", map[string]any{"foreground": "#9a9a9a"})
	e.newTag("listitem", map[string]any{"indent": -14, "left-margin": 30, "pixels-below-lines": 3})
	e.newTag("quote", map[string]any{
		"left-margin": 26, "style": pango.StyleItalic, "foreground": "#9a9a9a",
	})
	e.newTag("divider", map[string]any{"foreground": "#9a9a9a", "scale": 0.8})
	// A finished task reads as done: struck through and receded.
	e.newTag("done", map[string]any{"strikethrough": true, "foreground": "#9a9a9a"})
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
	e.clearItems() // the old note's checkboxes go with its text

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
// from the embedded checkboxes and stripping the anchor characters.
func (e *Editor) Content() string {
	raw := e.rawText()
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
	cursorLine := -1
	if ins := e.buffer.IterAtMark(e.buffer.GetInsert()); ins != nil {
		cursorLine = ins.Line()
	}

	revealLine := -1
	if e.revealCaret {
		revealLine = cursorLine
	}

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
	e.lastCursor = cursorLine
	e.clearDirty()

	breadcrumb("reparse lines %d-%d of %d, caret %d", from, to, lastLine+1, cursorLine)
	e.renderChecklists(from, to, revealLine)
	e.reapItems()
	e.tagRange(from, to, revealLine)

	if e.OnReparsed != nil {
		e.OnReparsed()
	}
}

// tagRange re-applies every formatting tag for lines [from, to].
func (e *Editor) tagRange(from, to, cursorLine int) {
	start, ok1 := e.buffer.IterAtLine(from)
	end, ok2 := e.buffer.IterAtLine(to)
	if !ok1 || !ok2 {
		return
	}
	if !end.EndsLine() {
		end.ForwardToLineEnd()
	}
	e.buffer.RemoveAllTags(start, end)
	text := e.buffer.Slice(start, end, true) // Slice keeps offsets aligned with TextIter

	lineNum := from
	for _, line := range strings.Split(text, "\n") {
		// A completed task reads as done (struck through, receded). Only a
		// line that starts with an anchor can be one, and only then is the
		// line's length in characters needed.
		if strings.HasPrefix(line, anchorChar) && e.anchorChecked(lineNum) {
			e.applyTag("done", lineNum, 1, utf8.RuneCountInString(line))
		}
		// Spans come back within the line's bounds (enforced by the fuzz test
		// over parseLineSpans), and an offset past the end simply fails to
		// resolve to an iterator, so no clamping is done here: measuring the
		// line in characters for every render pass was costing more than the
		// parse itself.
		for _, sp := range parseLineSpans(line, lineNum == cursorLine) {
			e.applyTag(sp.tag, lineNum, sp.start, sp.end)
		}
		if breadcrumbsOn {
			breadcrumb("tagged line %d: %d chars, %d bytes", lineNum,
				utf8.RuneCountInString(line), len(line))
		}
		lineNum++
	}
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
