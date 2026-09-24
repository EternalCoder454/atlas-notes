package editor

import (
	"strings"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// This file implements the editing commands behind the formatting toolbar and
// its keyboard shortcuts. They all work the way a word processor's do: with a
// selection they wrap or prefix it, without one they insert the markers and
// leave the caret where the text goes.

// ToggleBold wraps the selection (or caret) in **…**.
func (e *Editor) ToggleBold() { e.toggleWrap("**") }

// ToggleItalic wraps the selection (or caret) in *…*.
func (e *Editor) ToggleItalic() { e.toggleWrap("*") }

// ToggleCode wraps the selection (or caret) in `…`.
func (e *Editor) ToggleCode() { e.toggleWrap("`") }

// ToggleStrike wraps the selection (or caret) in ~~…~~.
func (e *Editor) ToggleStrike() { e.toggleWrap("~~") }

// toggleWrap adds marker around the selection, or removes it when the selection
// is already wrapped in it.
func (e *Editor) toggleWrap(marker string) {
	start, end, hasSel := e.buffer.SelectionBounds()
	if !hasSel {
		ins := e.buffer.IterAtMark(e.buffer.GetInsert())
		e.buffer.Insert(ins, marker+marker)
		// Put the caret between the two markers so typing lands inside.
		e.buffer.PlaceCursor(e.buffer.IterAtOffset(ins.Offset() - len([]rune(marker))))
		e.afterEdit()
		return
	}
	text := e.buffer.Slice(start, end, true)
	if strings.HasPrefix(text, marker) && strings.HasSuffix(text, marker) && len(text) > 2*len(marker) {
		text = strings.TrimSuffix(strings.TrimPrefix(text, marker), marker)
	} else {
		text = marker + text + marker
	}
	offset := start.Offset()
	e.buffer.Delete(start, end)
	at := e.buffer.IterAtOffset(offset)
	e.buffer.Insert(at, text)
	e.afterEdit()
}

// SetHeading sets (or clears, with level 0) the heading level of every line the
// selection touches.
func (e *Editor) SetHeading(level int) {
	e.eachSelectedLine(func(line string) string {
		body := strings.TrimLeft(strings.TrimLeft(line, "#"), " ")
		if level <= 0 {
			return body
		}
		return strings.Repeat("#", level) + " " + body
	})
}

// ToggleBullet turns the touched lines into "- " list items, or back.
func (e *Editor) ToggleBullet() {
	e.eachSelectedLine(func(line string) string {
		trimmed := strings.TrimLeft(line, " \t")
		indent := line[:len(line)-len(trimmed)]
		if strings.HasPrefix(trimmed, "- ") && !strings.HasPrefix(trimmed, "- [") {
			return indent + strings.TrimPrefix(trimmed, "- ")
		}
		return indent + "- " + trimmed
	})
}

// ToggleQuote turns the touched lines into "> " quotes, or back.
func (e *Editor) ToggleQuote() {
	e.eachSelectedLine(func(line string) string {
		trimmed := strings.TrimLeft(line, " \t")
		indent := line[:len(line)-len(trimmed)]
		if strings.HasPrefix(trimmed, "> ") {
			return indent + strings.TrimPrefix(trimmed, "> ")
		}
		return indent + "> " + trimmed
	})
}

// ToggleTask turns the touched lines into "- [ ] " tasks, or back into plain
// text. Lines already rendered as a checkbox widget are converted back to text.
func (e *Editor) ToggleTask() {
	e.eachSelectedLine(func(line string) string {
		if strings.HasPrefix(line, anchorChar) { // rendered checkbox: unwrap it
			return strings.TrimPrefix(line, anchorChar)
		}
		trimmed := strings.TrimLeft(line, " \t")
		indent := line[:len(line)-len(trimmed)]
		if strings.HasPrefix(trimmed, "- [") {
			if i := strings.Index(trimmed, "] "); i > 0 {
				return indent + trimmed[i+2:]
			}
		}
		trimmed = strings.TrimPrefix(trimmed, "- ")
		return indent + "- [ ] " + trimmed
	})
}

// InsertDivider inserts a horizontal rule on its own line.
func (e *Editor) InsertDivider() {
	ins := e.buffer.IterAtMark(e.buffer.GetInsert())
	prefix := "\n"
	if ins.StartsLine() {
		prefix = ""
	}
	e.buffer.Insert(ins, prefix+"---\n")
	e.afterEdit()
}

// eachSelectedLine rewrites every line touched by the selection (or the caret's
// line) through fn, preserving the caret's line.
func (e *Editor) eachSelectedLine(fn func(string) string) {
	start, end, hasSel := e.buffer.SelectionBounds()
	if !hasSel {
		ins := e.buffer.IterAtMark(e.buffer.GetInsert())
		start, end = ins, ins
	}
	first, last := start.Line(), end.Line()
	lines := strings.Split(e.rawText(), "\n")
	for ln := first; ln <= last && ln < len(lines); ln++ {
		if strings.HasPrefix(lines[ln], anchorChar) && !strings.HasPrefix(lines[ln], anchorChar+"\x00") {
			// A rendered checkbox line: operate on the text after the anchor.
			body := fn(lines[ln])
			if body == lines[ln] {
				continue
			}
			e.replaceLine(ln, strings.TrimPrefix(body, anchorChar), true)
			continue
		}
		next := fn(lines[ln])
		if next == lines[ln] {
			continue
		}
		e.replaceLine(ln, next, false)
	}
	e.afterEdit()
}

// replaceLine swaps a whole buffer line for text. dropAnchor removes a leading
// checkbox widget with it.
func (e *Editor) replaceLine(ln int, text string, dropAnchor bool) {
	start, ok := e.buffer.IterAtLine(ln)
	if !ok {
		return
	}
	end, ok := e.buffer.IterAtLine(ln)
	if !ok {
		return
	}
	if !end.EndsLine() {
		end.ForwardToLineEnd()
	}
	e.withLoading(func() {
		e.buffer.Delete(start, end)
		if at, ok := e.buffer.IterAtLine(ln); ok {
			e.buffer.Insert(at, text)
		}
	})
	e.markDirty(ln, ln)
	_ = dropAnchor // deleting the line range removes the anchor with it
}

// afterEdit re-renders and notifies the app that the document changed.
func (e *Editor) afterEdit() {
	e.reparse()
	if e.OnChanged != nil {
		e.OnChanged()
	}
}

// Focus puts the caret in the text view.
func (e *Editor) Focus() { e.view.GrabFocus() }

// View exposes the underlying text view (used to attach shortcut controllers).
func (e *Editor) View() *gtk.TextView { return e.view }
