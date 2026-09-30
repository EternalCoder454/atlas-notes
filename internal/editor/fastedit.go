package editor

import (
	"sort"
	"strings"
)

// A note-wide scan (refreshFence: the code fences, the headings, the quotes and
// callouts, the front matter, the pipes) read the whole document on every
// keystroke, and in a long note that was nearly half of what typing cost. Almost
// every keystroke changes nothing any of them look at: it adds a letter to a
// line of prose. This file finds those edits so the scan can be skipped.
//
// The edit signals fire before the text changes, so they say which line an edit
// is in and what that line was. A change is a "plain edit" when it stays inside
// one line, that line is neither the first nor a line the scans care about
// (plainLine) before or after the edit, and it is not next to a quote, a code
// block or a folded heading. Every scan looks at a line only through the
// features plainLine rules out, so a run of plain edits on one line leaves all
// of their results as they were.

// editTrack is what the edit signals have seen since the last note-wide scan.
type editTrack struct {
	// bad says some edit since the scan was not plain, or was not seen at all
	// (a note being loaded, a fold), so the scan must run.
	bad bool
	// line is the one line the plain edits since the scan were in, or -1.
	line int
	// plain is the line the last pass found plain after an edit, so that the
	// next edit in it does not have to read it, or -1.
	plain int
	// frontOpen says the note starts with "---": whether that is front matter
	// depends on lines further down, so edits there are never plain.
	frontOpen bool
}

// plainLine reports whether the note-wide scans have nothing to find in a line
// but, perhaps, a heading: no fence, no quote, no pipe, and no "---" that could
// open front matter. A heading is the one thing a plain edit may change, and
// patchOutline makes up for it. Front matter is otherwise judged apart, by line
// number.
func plainLine(l string) bool {
	if l != "" && (l[0] == '>' || strings.HasPrefix(l, "---")) {
		return false
	}
	return !strings.Contains(l, "```") && !strings.Contains(l, "~~~") && strings.IndexByte(l, '|') < 0
}

// noteEdit is called by the edit signals before an edit inside lines from to to
// that adds text ("added") or removes it. It keeps track of whether the edit is
// plain.
func (e *Editor) noteEdit(from, to int, added string) {
	t := &e.track
	if t.bad {
		return
	}
	if from != to || t.frontOpen || strings.IndexByte(added, '\n') >= 0 ||
		(t.line >= 0 && t.line != from) {
		t.bad = true
		return
	}
	if t.line < 0 && t.plain != from {
		// The first edit of the pass, in a line not known to be plain: read what
		// it is before the edit.
		if l, ok := e.lineText(from); !ok || !plainLine(l) {
			t.bad = true
			return
		}
	}
	t.line = from
}

// plainEditPass reports whether the edits since the last scan were plain, and so
// changed nothing the scan would find. lastLine is the document's last line.
func (e *Editor) plainEditPass(lastLine int) bool {
	t := &e.track
	if t.bad || t.line < 0 || lastLine+1 != e.fenceLines {
		return false
	}
	ln := t.line
	if d := e.props.doc; d != nil && ln <= d.End {
		return false
	}
	s := &e.rich
	for _, b := range s.blocks {
		if b.last >= ln-1 && b.first <= ln+1 {
			return false
		}
	}
	for _, h := range s.hides {
		if h[1] >= ln-1 && h[0] <= ln+2 {
			return false
		}
	}
	if len(s.headMarks) > 0 || len(s.flipMarks) > 0 {
		return false
	}
	l, ok := e.lineText(ln)
	if !ok || !plainLine(l) {
		return false
	}
	if l[:min(1, len(l))] == "#" || e.hasHeadingAt(ln) {
		e.patchOutline(ln, l)
	}
	t.line, t.plain = -1, ln
	return true
}

// scanned notes that the note-wide scan has just run.
func (e *Editor) scanned(raw string) {
	e.track = editTrack{line: -1, plain: -1, frontOpen: strings.HasPrefix(raw, "---")}
}

// hasHeadingAt reports whether the outline has a heading on line ln.
func (e *Editor) hasHeadingAt(ln int) bool {
	h := e.outline.heads
	i := sort.Search(len(h), func(i int) bool { return h[i].line >= ln })
	return i < len(h) && h[i].line == ln
}
