package editor

import (
	"strings"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"atlas-notes/internal/markup"
)

// This file draws note links, tags and web addresses, and opens them on a
// click.
//
// What counts as one is decided by internal/markup, the same code the index
// uses, so a tag the editor colours is a tag the tag list knows about. The
// editor only decides how they look and what a click does. The parts that do not
// need GTK (finding the spans, converting their offsets, hit-testing) are plain
// functions and are tested on their own.

const (
	tagWikiLink = "wikilink"
	tagHashtag  = "hashtag"
	tagURL      = "url"

	// linkColor reads on both the light and the dark theme, like the gray the
	// dimmed markers use.
	linkColor = "#3584e4"

	// dragSlop is how far, in pixels, the pointer may move between press and
	// release and still count as a click rather than the start of a selection.
	dragSlop = 6.0
)

// createLinkTags defines the look of links. It runs from createTags, ahead of
// anything else that makes tags, so the find highlights created later keep
// priority over these and a match inside a link still shows.
func (e *Editor) createLinkTags() {
	e.newTag(tagWikiLink, map[string]any{"foreground": linkColor, "weight": int(pango.WeightSemibold)})
	e.newTag(tagHashtag, map[string]any{"foreground": linkColor})
	e.newTag(tagURL, map[string]any{"foreground": linkColor, "underline": pango.UnderlineSingle})
}

// mayHaveLinks is the cheap test that lets the render pass skip a line: every
// construct internal/markup finds starts with "[", "#" or "http". It allocates
// nothing, and most lines of prose fail it.
func mayHaveLinks(line string) bool {
	return strings.IndexByte(line, '[') >= 0 || strings.IndexByte(line, '#') >= 0 ||
		strings.Contains(line, "http")
}

// markupSpans finds what internal/markup sees on a line, with byte offsets into
// the line as given. It returns nil, without allocating, for a line that cannot
// hold any.
//
// Two things about the editor's lines are not the note's text. A task line
// starts with the character standing in for its checkbox, which is not a space,
// so "#work" right after it would not look like the start of a tag; the anchor
// is left out of what markup reads and the offsets are shifted back. And a
// trailing "<!-- ... -->" is task metadata that the render pass hides, so
// nothing inside it is a link.
func markupSpans(line string) []markup.Span {
	if !mayHaveLinks(line) {
		return nil
	}
	base, end := 0, len(line)
	if strings.HasPrefix(line, anchorChar) {
		base = len(anchorChar)
	}
	if b := strings.Index(line, "<!--"); b >= base && strings.Contains(line[b:], "-->") {
		end = b
	}
	// The editor does not track fenced code blocks, so no line is known to be
	// inside one. Inline code is still left alone by markup itself.
	spans := markup.Line(line[base:end], false)
	if base > 0 {
		for i := range spans {
			spans[i].Start += base
			spans[i].End += base
		}
	}
	return spans
}

// visibleStart is where a span's own text starts. An embedded note,
// "![[Note]]", begins with a "!" that is not part of the link.
func visibleStart(line string, sp markup.Span) int {
	if sp.Kind == markup.KindWikiLink && line[sp.Start] == '!' {
		return sp.Start + 1
	}
	return sp.Start
}

// linkSpans returns the tag applications for the links on one line, in
// character offsets. Like parseLineSpans, it leaves the brackets and the
// address visible when reveal is set (the caret is on the line) and hides them
// otherwise, so the line reads as a link and can still be edited.
//
// Images are not handled here: an image line is picked up by tagImageLine
// (images.go), and any other image stays plain text.
func linkSpans(line string, reveal bool) []span {
	found := markupSpans(line)
	if len(found) == 0 {
		return nil
	}
	out := make([]span, 0, 2*len(found))
	hide := func(s, e int) {
		if reveal || e <= s {
			return
		}
		// The same choice parseLineSpans makes, for the same reason.
		if hideMarkers {
			out = append(out, span{"invisible", s, e})
			return
		}
		out = append(out, span{"marker", s, e})
	}
	for _, sp := range found {
		switch sp.Kind {
		case markup.KindWikiLink:
			open := visibleStart(line, sp)
			out = append(out, span{tagWikiLink, open, sp.End})
			hide(open, open+2)
			hide(sp.End-2, sp.End)
			// With an alias only the alias shows: the target and heading before
			// the "|" are hidden along with the brackets.
			if sp.Alias != "" {
				if bar := strings.IndexByte(line[open+2:sp.End-2], '|'); bar >= 0 {
					hide(open+2, open+2+bar+1)
				}
			}
		case markup.KindTag:
			out = append(out, span{tagHashtag, sp.Start, sp.End})
		case markup.KindURL:
			if line[sp.Start] == '[' && sp.Alias != "" {
				// "[text](url)": only the text shows. An empty text would leave
				// nothing to see or click, so that case is left as it is written.
				textEnd := sp.Start + 1 + len(sp.Alias)
				out = append(out, span{tagURL, sp.Start + 1, textEnd})
				hide(sp.Start, sp.Start+1)
				hide(textEnd, sp.End)
			} else {
				out = append(out, span{tagURL, sp.Start, sp.End})
			}
		}
	}
	return charSpans(line, out)
}

// byteOffset converts a character offset within line to a byte offset. An
// offset equal to the line's length in characters is the end of the line; one
// past that, or a negative one, is not ok.
func byteOffset(line string, chars int) (int, bool) {
	if chars < 0 {
		return 0, false
	}
	n := 0
	for i := range line {
		if n == chars {
			return i, true
		}
		n++
	}
	if n == chars {
		return len(line), true
	}
	return 0, false
}

// spanAt finds the link under a character offset in a line: the span a click
// there would open. A span owns its first character and not the one after its
// last, so two links side by side never both claim the same offset.
func spanAt(line string, chars int) (markup.Span, bool) {
	found := markupSpans(line)
	if len(found) == 0 {
		return markup.Span{}, false
	}
	b, ok := byteOffset(line, chars)
	if !ok {
		return markup.Span{}, false
	}
	for _, sp := range found {
		if sp.Kind == markup.KindImage {
			continue // images are handled elsewhere
		}
		if b >= visibleStart(line, sp) && b < sp.End {
			return sp, true
		}
	}
	return markup.Span{}, false
}

// linkPress is what the pointer was over when the button went down.
//
// The answer is taken at the press, not the release. The press moves the caret,
// and a moment later the line reveals its markers, which moves the text
// under a pointer that has not moved. Asking again at the release would look
// at a different layout than the one the person clicked on.
type linkPress struct {
	have     bool
	kind     markup.Kind
	target   string
	heading  string
	revealed bool // the line was showing its markers, so a plain click edits
	x, y     float64
}

// linkUnder finds the link at a point in the view, and the line it is on.
func (e *Editor) linkUnder(x, y float64) (sp markup.Span, line int, ok bool) {
	// Finding the character under the pointer turns a layout byte offset into
	// a buffer position, and GTK aborts the process when that line's hidden
	// runs have changed since it was laid out: "byte index off the end of the
	// line". A render pass changes them, and the pointer moves between the
	// pass and the next layout all the time. So nothing is looked up until
	// GTK has laid out again (see markLayoutStale).
	if e.layoutStale {
		return markup.Span{}, 0, false
	}
	bx, by := e.view.WindowToBufferCoords(gtk.TextWindowWidget, int(x), int(y))
	// The line first, which needs no byte offsets, and the character only on
	// a line that can hold a link and is not a picture's hidden line.
	lineIt, _ := e.view.LineAtY(by)
	if lineIt == nil {
		return markup.Span{}, 0, false
	}
	line = lineIt.Line()
	text, found := e.lineText(line)
	if !found || !mayHaveLinks(text) {
		return markup.Span{}, 0, false
	}
	if _, picture := imageLineSpan(text); picture {
		return markup.Span{}, 0, false
	}
	it, found := e.view.IterAtLocation(bx, by)
	if !found || it == nil || it.Line() != line {
		return markup.Span{}, 0, false
	}
	sp, ok = spanAt(text, it.LineOffset())
	return sp, line, ok
}

// layoutSettleMs is how long the text must go unchanged, in its characters and
// its tags, before the pointer is hit-tested against it again.
const layoutSettleMs = 100

// markLayoutStale records that the text or its tags have just changed, which
// is anything from a keystroke to a render pass to the find bar's highlights
// or a picture's spacing, and clears the mark once they have stayed as they
// are for layoutSettleMs. The clearing runs at low priority, after GTK's own
// layout and redraw work, which runs at higher ones.
func (e *Editor) markLayoutStale() {
	e.layoutStale = true
	e.staleGen++
	if e.staleClearing {
		return
	}
	e.staleClearing = true
	e.scheduleStaleClear(e.staleGen)
}

func (e *Editor) scheduleStaleClear(gen uint64) {
	coreglib.TimeoutAddPriority(layoutSettleMs, coreglib.PriorityLow, func() bool {
		if gen != e.staleGen {
			// Something changed meanwhile: wait for that to settle too.
			e.scheduleStaleClear(e.staleGen)
			return false
		}
		e.staleClearing = false
		e.layoutStale = false
		return false
	})
}

// hoverRestMs is how long the pointer rests before the link under it is
// looked up. Looking up on every motion event hit-tests the text hundreds of
// times a second; a pointer that has come to rest needs it once.
const hoverRestMs = 120

// canOpen reports whether the app is listening for this kind of link.
func (e *Editor) canOpen(k markup.Kind) bool {
	switch k {
	case markup.KindWikiLink:
		return e.OnOpenNote != nil
	case markup.KindTag:
		return e.OnOpenTag != nil
	case markup.KindURL:
		return e.OnOpenURL != nil
	}
	return false
}

// installLinks makes links clickable and gives them a pointer cursor.
func (e *Editor) installLinks() {
	click := gtk.NewGestureClick()
	click.SetButton(1) // primary
	// The capture phase runs before the text view's own handling, so the press
	// is looked at before the view moves the caret. The gesture never claims
	// the sequence, so the view still places the caret and starts selections.
	click.SetPropagationPhase(gtk.PhaseCapture)
	click.ConnectPressed(func(_ int, x, y float64) {
		e.press = linkPress{}
		sp, line, ok := e.linkUnder(x, y)
		if !ok {
			return
		}
		e.press = linkPress{
			have: true, kind: sp.Kind, target: sp.Target, heading: sp.Heading,
			revealed: line == e.shown, x: x, y: y,
		}
	})
	click.ConnectReleased(func(nPress int, x, y float64) {
		p := e.press
		e.press = linkPress{}
		if !p.have || nPress != 1 || e.buffer.HasSelection() {
			return
		}
		dx, dy := x-p.x, y-p.y
		if dx*dx+dy*dy > dragSlop*dragSlop {
			return
		}
		// On a line that shows its markers a plain click is for editing; Ctrl
		// opens the link anyway.
		if p.revealed && click.CurrentEventState()&gdk.ControlMask == 0 {
			return
		}
		e.openLink(p)
	})
	e.view.AddController(click)

	motion := gtk.NewEventControllerMotion()
	motion.ConnectMotion(func(x, y float64) {
		ctrl := motion.CurrentEventState()&gdk.ControlMask != 0
		e.hoverX, e.hoverY, e.hoverCtrl = x, y, ctrl
		e.hoverGen++
		gen := e.hoverGen
		coreglib.TimeoutAdd(hoverRestMs, func() bool {
			if gen != e.hoverGen || !e.hoverIn {
				return false // moved on, or left
			}
			sp, line, ok := e.linkUnder(e.hoverX, e.hoverY)
			e.setLinkCursor(ok && e.canOpen(sp.Kind) && (e.hoverCtrl || line != e.shown))
			return false
		})
	})
	motion.ConnectEnter(func(float64, float64) { e.hoverIn = true })
	motion.ConnectLeave(func() {
		e.hoverIn = false
		e.hoverGen++
		e.setLinkCursor(false)
	})
	e.view.AddController(motion)

	// Every change to the text or its tags leaves the layout behind until GTK
	// lays it out again; see markLayoutStale.
	e.buffer.ConnectChanged(e.markLayoutStale)
	e.buffer.ConnectApplyTag(func(*gtk.TextTag, *gtk.TextIter, *gtk.TextIter) { e.markLayoutStale() })
	e.buffer.ConnectRemoveTag(func(*gtk.TextTag, *gtk.TextIter, *gtk.TextIter) { e.markLayoutStale() })
}

// setLinkCursor switches the pointer between a hand over a link and the text
// cursor, and only touches the widget when the answer changes.
func (e *Editor) setLinkCursor(over bool) {
	if over == e.overLink {
		return
	}
	e.overLink = over
	if over {
		e.view.SetCursorFromName("pointer")
		return
	}
	e.view.SetCursorFromName("text")
}

// openLink hands a clicked link to the app. It runs from an idle callback: the
// app answers by loading another note, and replacing the buffer from inside the
// click that is still being delivered to the view is asking for trouble.
func (e *Editor) openLink(p linkPress) {
	var run func()
	switch p.kind {
	case markup.KindWikiLink:
		if cb := e.OnOpenNote; cb != nil {
			run = func() { cb(p.target, p.heading) }
		}
	case markup.KindTag:
		if cb := e.OnOpenTag; cb != nil {
			run = func() { cb(p.target) }
		}
	case markup.KindURL:
		if cb := e.OnOpenURL; cb != nil {
			run = func() { cb(p.target) }
		}
	}
	if run == nil {
		return
	}
	coreglib.IdleAdd(func() bool {
		run()
		return false
	})
}
