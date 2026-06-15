// Package editor implements the center WYSIWYG markdown editor: a GtkTextView
// whose buffer is re-tagged (headings, bold, italic, code) on a 50ms debounce,
// hiding the markdown syntax markers except on the line holding the cursor.
// Checklist lines are converted to embedded checkbox widgets on the same pass.
package editor

import (
	"strings"

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
	e := &Editor{tags: map[string]*gtk.TextTag{}}

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

	e.buffer.ConnectChanged(func() {
		if e.loading {
			return
		}
		e.scheduleReparse()
		if e.OnChanged != nil {
			e.OnChanged()
		}
	})
	e.buffer.ConnectMarkSet(func(_ *gtk.TextIter, mark *gtk.TextMark) {
		if mark.Name() == "insert" {
			e.scheduleReparse()
		}
	})

	return e
}

// Widget returns the scrollable editor widget.
func (e *Editor) Widget() gtk.Widgetter { return e.scroll }

// View exposes the underlying GtkTextView.
func (e *Editor) View() *gtk.TextView { return e.view }

func (e *Editor) createTags() {
	e.newTag("heading1", map[string]any{"scale": 1.6, "weight": int(pango.WeightBold)})
	e.newTag("heading2", map[string]any{"scale": 1.3, "weight": int(pango.WeightBold)})
	e.newTag("heading3", map[string]any{"scale": 1.15, "weight": int(pango.WeightBold)})
	e.newTag("bold", map[string]any{"weight": int(pango.WeightBold)})
	e.newTag("italic", map[string]any{"style": pango.StyleItalic})
	e.newTag("code", map[string]any{"family": "monospace"})
	e.newTag("invisible", map[string]any{"invisible": true})
}

func (e *Editor) newTag(name string, props map[string]any) {
	tag := gtk.NewTextTag(name)
	for k, v := range props {
		tag.SetObjectProperty(k, v)
	}
	e.buffer.TagTable().Add(tag)
	e.tags[name] = tag
}

// SetContent replaces the text without firing OnChanged, then re-renders.
func (e *Editor) SetContent(s string) {
	e.loading = true
	e.buffer.SetText(s)
	e.loading = false
	e.renderChecklists(-1) // convert every "- [ ] " line to a checkbox
	e.reparse()
}

// Content returns the full markdown text, reconstructing "- [ ]/[x] " prefixes
// from the embedded checkboxes and stripping the anchor characters.
func (e *Editor) Content() string {
	lines := strings.Split(e.rawText(), "\n")
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

// Stats returns word and character counts of the current content.
func (e *Editor) Stats() (words, chars int) {
	text := e.Content()
	return len(strings.Fields(text)), len([]rune(text))
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

// reparse converts any new checklist lines (except the one being edited) and
// re-applies all formatting tags. With the 50ms debounce it only runs after
// typing/caret movement pauses, so idle CPU stays low.
func (e *Editor) reparse() {
	cursorLine := -1
	if ins := e.buffer.IterAtMark(e.buffer.GetInsert()); ins != nil {
		cursorLine = ins.Line()
	}
	e.renderChecklists(cursorLine)

	start, end := e.buffer.Bounds()
	e.buffer.RemoveAllTags(start, end)
	text := e.buffer.Slice(start, end, true) // Slice keeps offsets aligned with TextIter

	for lineNum, line := range strings.Split(text, "\n") {
		runeLen := len([]rune(line))
		for _, sp := range parseLineSpans(line, lineNum == cursorLine) {
			s, en := clamp(sp.start, runeLen), clamp(sp.end, runeLen)
			if en <= s {
				continue
			}
			si, ok1 := e.buffer.IterAtLineOffset(lineNum, s)
			ei, ok2 := e.buffer.IterAtLineOffset(lineNum, en)
			if !ok1 || !ok2 {
				continue
			}
			if tag := e.tags[sp.tag]; tag != nil {
				e.buffer.ApplyTag(tag, si, ei)
			}
		}
	}

	if e.OnReparsed != nil {
		e.OnReparsed()
	}
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
