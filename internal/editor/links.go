package editor

import (
	"strings"
	"time"

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

	// linkColor is where links start, libadwaita's blue, which reads on both
	// the light and the dark theme. The app replaces it with the theme's accent
	// (SetLinkColor).
	linkColor = "#3584e4"

	// dragSlop is how far, in pixels, the pointer may move between press and
	// release and still count as a click rather than the start of a selection.
	dragSlop = 6.0
)

// SetLinkColor draws links, tags and web addresses in color, a CSS colour
// such as "#88c0d0": a theme's accent. Text tags take a colour, not a CSS
// name, so a theme reaches them through here rather than the stylesheet.
func (e *Editor) SetLinkColor(color string) {
	for _, name := range []string{tagWikiLink, tagHashtag, tagURL} {
		if tag := e.tags[name]; tag != nil {
			tag.SetObjectProperty("foreground", color)
		}
	}
}

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
	// Lines inside a fenced code block never get here: the render pass tags them
	// as code without looking for links (see tagRange). Inline code is left alone
	// by markup itself.
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
// character offsets. caret is the caret's character offset in the line, or -1
// when it is elsewhere. Like parseLineSpans, it leaves a link's brackets and
// address visible while the caret is in that link (its brackets included) and
// hides them otherwise, so the line reads as a link and can still be edited.
// Each link is judged on its own: the caret in one leaves the next hidden.
//
// Images are not handled here: an image line is picked up by tagImageLine
// (images.go), which shows its Markdown while the caret is anywhere on the
// line, and any other image stays plain text.
func linkSpans(line string, caret int) []span {
	return linkSpansKey(line, caret, nil)
}

// linkSpansKey is linkSpans, and also fills in key (when it is not nil) with the
// link the caret is in.
func linkSpansKey(line string, caret int, key *caretKey) []span {
	found := markupSpans(line)
	if len(found) == 0 {
		return nil
	}
	cb := caretByte(line, caret)
	out := make([]span, 0, 2*len(found))
	shown := false // the caret is in the link being read
	// inside says whether the caret is in the link spanning bytes s to e, or
	// right against either end of it.
	inside := func(s, e int) bool {
		if cb < s || cb > e {
			return false
		}
		if key != nil {
			key.link = [2]int{s, e}
		}
		return true
	}
	hide := func(s, e int) {
		if shown || e <= s {
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
			shown = inside(open, sp.End)
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
				shown = inside(sp.Start, sp.End)
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
	revealed bool // the link was showing its markers, so a plain click edits
	// pending is a press that came while the layout was stale, so nothing could
	// be looked up under it; the release does that, if the layout has settled.
	pending bool
	x, y    float64
}

// pressOn is the press for a link found at a point.
func (e *Editor) pressOn(sp markup.Span, line int, x, y float64) linkPress {
	return linkPress{
		have: true, kind: sp.Kind, target: sp.Target, heading: sp.Heading,
		revealed: e.showsMarkers(sp, line), x: x, y: y,
	}
}

// showsMarkers reports whether the last render pass left the link sp, found on
// line, with its brackets and address showing, which is when the caret is in it.
// A plain click there is for editing the link, and Ctrl opens it. A link that
// hides nothing (a tag or a web address) has no such state, so for those it is
// the caret's line that counts, as it was before markers followed the caret.
func (e *Editor) showsMarkers(sp markup.Span, line int) bool {
	if line != e.shown {
		return false
	}
	text, ok := e.lineText(line)
	if !ok || !e.keyValid {
		return true
	}
	r, hides := linkRange(text, sp)
	return !hides || e.lastKey.link == r
}

// linkRange is the byte range linkSpans reveals together for a link, and
// whether the link has markers to hide at all.
func linkRange(line string, sp markup.Span) ([2]int, bool) {
	switch {
	case sp.Kind == markup.KindWikiLink:
		return [2]int{visibleStart(line, sp), sp.End}, true
	case sp.Kind == markup.KindURL && line[sp.Start] == '[' && sp.Alias != "":
		return [2]int{sp.Start, sp.End}, true
	}
	return [2]int{}, false
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
	// Nor is a line of a table drawn as a table: what is there is the grid.
	if e.inTable(line) {
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
//
// The text is caught by the buffer's changed signal. Tags are not: the buffer
// signals every tag applied or removed, a render pass does thousands of those,
// and a Go callback across cgo for each was a real cost. So whatever changes
// tags calls this itself (tagRange, applyPad, placeBlocks, renderChecklists and
// the find highlights), and a new place that does must too.
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
		// Nothing can be looked up while the layout is stale (see linkUnder), and
		// dropping the click would make a link pressed just after an edit dead.
		// The position is kept and the release tries again.
		if e.layoutStale {
			e.press = linkPress{pending: true, x: x, y: y}
			return
		}
		sp, line, ok := e.linkUnder(x, y)
		if !ok {
			return
		}
		e.press = e.pressOn(sp, line, x, y)
	})
	click.ConnectReleased(func(nPress int, x, y float64) {
		p := e.press
		e.press = linkPress{}
		if !(p.have || p.pending) || nPress != 1 || e.buffer.HasSelection() {
			return
		}
		dx, dy := x-p.x, y-p.y
		if dx*dx+dy*dy > dragSlop*dragSlop {
			return
		}
		if p.pending {
			// Still stale, or not on a link: linkUnder says no, and the click is
			// left to be a plain one.
			sp, line, ok := e.linkUnder(p.x, p.y)
			if !ok {
				return
			}
			p = e.pressOn(sp, line, p.x, p.y)
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
		e.hoverAt = time.Now()
		e.hoverGen++
		e.armHover(hoverRestMs)
	})
	motion.ConnectEnter(func(float64, float64) { e.hoverIn = true })
	motion.ConnectLeave(func() {
		e.hoverIn = false
		e.hoverGen++
		e.setLinkCursor(false)
	})
	e.view.AddController(motion)

	// Every change to the text leaves the layout behind until GTK lays it out
	// again; see markLayoutStale, which also says who marks tag changes.
	e.buffer.ConnectChanged(e.markLayoutStale)
}

// armHover makes sure the hover check runs in ms milliseconds. There is one
// timer, not one per motion event: a pointer sweeping across the text sends
// hundreds of events a second, and a timer for each was hundreds of callbacks
// that all found they had been overtaken. The timer remembers the generation
// it was armed for, and when the pointer has moved since, waits out what is
// left of the rest instead of looking up a link the pointer has already left.
func (e *Editor) armHover(ms int) {
	if e.hoverArmed {
		return
	}
	e.hoverArmed = true
	gen := e.hoverGen
	coreglib.TimeoutAdd(uint(ms), func() bool {
		e.hoverArmed = false
		if !e.hoverIn {
			return false // the pointer left
		}
		if gen != e.hoverGen {
			e.armHover(max(hoverRestMs-int(time.Since(e.hoverAt)/time.Millisecond), 1))
			return false
		}
		e.hoverLookup()
		return false
	})
}

// hoverLookup sets the pointer for the link under where it has come to rest.
func (e *Editor) hoverLookup() {
	// With the layout stale linkUnder has no answer. Guessing would flip the
	// cursor to the wrong thing until the next move, so it stays as it is and the
	// check is tried again once the layout has had time to settle.
	if e.layoutStale {
		e.armHover(layoutSettleMs)
		return
	}
	sp, line, ok := e.linkUnder(e.hoverX, e.hoverY)
	e.setLinkCursor(ok && e.canOpen(sp.Kind) && (e.hoverCtrl || !e.showsMarkers(sp, line)))
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
