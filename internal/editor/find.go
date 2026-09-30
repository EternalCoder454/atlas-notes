package editor

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// This file implements find and replace inside the open note.
//
// The search is literal and runs on the buffer's text with the bullets drawn as
// "•" mapped back to the markers they stand for (see sourceText), so that a note
// is searched as it is written, and so that the string is the one the checklist
// code measures its offsets against: a match's character offsets are also buffer
// offsets and can go straight into IterAtOffset. Matching itself has no GTK in it (findMatches) and is tested on
// its own.

const (
	// findDebounceMs is how long the highlights may lag behind an edit.
	findDebounceMs = 100

	tagSearchMatch   = "search-match"
	tagSearchCurrent = "search-current"

	// anchorRune is what the buffer text holds where a checklist checkbox is
	// embedded. It is a widget, not text, so a match never covers one.
	anchorRune = '￼'
)

// finder is one editor's find state.
//
// It lives beside the Editor rather than inside it, and the buffer hooks it
// needs are attached the first time a search runs. An editor that is never
// searched pays nothing for any of this.
type finder struct {
	e      *Editor
	hooked bool

	query     string
	matchCase bool
	active    bool     // a non-empty query is in force, so highlights must be kept true
	matches   [][2]int // character offsets [start, end), in document order
	current   int      // index into matches, or -1
	stale     bool     // the text changed since matches was computed
	pending   bool     // a debounced refresh is scheduled
	held      bool     // a refresh came due while text was selected and waits for it to collapse

	match, cur *gtk.TextTag
	listener   func(current, count int)
}

// finders holds the state of every editor that has been searched. Widgets are
// touched on the main thread only, so it needs no lock.
var finders = map[*Editor]*finder{}

func (e *Editor) finder() *finder {
	f := finders[e]
	if f == nil {
		f = &finder{e: e, current: -1}
		finders[e] = f
	}
	return f
}

// hook creates the highlight tags and attaches the buffer hooks, once.
func (f *finder) hook() {
	if f.hooked {
		return
	}
	f.hooked = true
	e := f.e

	// Both tags set a dark foreground as well as a background. The theme picks
	// the text colour, and light text on a pale yellow is unreadable in the dark
	// theme. They are created after the formatting tags, so they take priority
	// over them, and search-current after search-match, so it wins where the
	// two overlap.
	e.newTag(tagSearchMatch, map[string]any{"background": "#f8e9a1", "foreground": "#1e1e1e"})
	e.newTag(tagSearchCurrent, map[string]any{"background": "#f5a623", "foreground": "#1e1e1e"})
	f.match, f.cur = e.tags[tagSearchMatch], e.tags[tagSearchCurrent]

	// Any change to the text, the user's, a note being opened, or the checklist
	// pass swapping "- [ ] " for a widget, moves offsets. It is not filtered
	// by e.loading for that reason.
	e.buffer.ConnectChanged(func() {
		if !f.active {
			return
		}
		f.stale = true
		f.schedule()
	})

	// The render pass strips every tag from the lines it re-tags (RemoveAllTags),
	// and that includes ours: the caret's line gets it on every caret move. So
	// once a pass is done, whatever it stripped is put back; reparse calls
	// restore itself when a finder exists, rather than through a hook here that
	// the app could overwrite.
}

// schedule arms one refresh for the edits of the next findDebounceMs.
func (f *finder) schedule() {
	if f.pending {
		return
	}
	f.pending = true
	coreglib.TimeoutAdd(findDebounceMs, func() bool {
		f.pending = false
		if !f.active || !f.stale {
			return false
		}
		// Repainting changes tags, and tags must not change while text is selected,
		// when a drag may have GTK hit-testing the lines (see reparse). The refresh
		// waits, and resume runs it once the selection has collapsed.
		if f.e.handsBusy() {
			f.e.whenHandsFree()
			f.held = true
			return false
		}
		f.refresh(false)
		return false // one-shot
	})
}

// resume schedules the refresh that was put off while text was selected.
func (f *finder) resume() {
	if !f.held {
		return
	}
	f.held = false
	if f.active && f.stale {
		f.schedule()
	}
}

// refresh recomputes the matches from the text and repaints them. The current
// match becomes the first one at or after the selection (or the caret), so
// typing more of a query keeps you where you are. reveal selects it and scrolls
// it into view; an edit elsewhere in the note must not move the view.
func (f *finder) refresh(reveal bool) {
	f.stale = false
	f.matches = findMatches(f.e.sourceText(), f.query, f.matchCase)
	f.current = -1
	if len(f.matches) > 0 {
		from, _ := f.e.selectionSpan()
		f.current = f.firstFrom(from)
	}
	f.paint()
	if reveal {
		f.reveal()
	}
	f.notify()
}

// paint puts the tags on every match and the current one, from scratch.
func (f *finder) paint() {
	f.e.markLayoutStale()
	b := f.e.buffer
	start, end := b.Bounds()
	b.RemoveTag(f.match, start, end)
	b.RemoveTag(f.cur, start, end)
	f.apply()
}

// apply tags the matches that are not tagged yet. A match sits on one line (a
// query is one line), and a render pass strips whole lines, so checking where
// a match starts is enough to tell whether it lost its tag. This runs after
// every render pass, and in a long note with many matches, tagging them all
// again each time the caret moved would be work for nothing.
func (f *finder) apply() {
	f.e.markLayoutStale() // tags change below; see linkUnder
	b := f.e.buffer
	total := b.CharCount()
	for i, m := range f.matches {
		if m[1] > total {
			return // the text is shorter than these offsets; a refresh is due
		}
		s := b.IterAtOffset(m[0])
		if !s.HasTag(f.match) {
			b.ApplyTag(f.match, s, b.IterAtOffset(m[1]))
		}
		if i == f.current && !s.HasTag(f.cur) {
			b.ApplyTag(f.cur, s, b.IterAtOffset(m[1]))
		}
	}
}

// restore re-applies the tags after a render pass, unless the text has moved
// on and a refresh is about to redo them anyway.
func (f *finder) restore() {
	if f.active && !f.stale && len(f.matches) > 0 {
		f.apply()
	}
}

// reveal selects the current match and scrolls it into view.
func (f *finder) reveal() {
	if f.current < 0 {
		return
	}
	e := f.e
	m := f.matches[f.current]
	// The caret goes to the end of the match, as if it had been selected by
	// dragging forwards, and the view follows the caret.
	e.buffer.SelectRange(e.buffer.IterAtOffset(m[1]), e.buffer.IterAtOffset(m[0]))
	e.view.ScrollToMark(e.buffer.GetInsert(), 0.1, false, 0, 0)
}

// notify tells the app where the search stands.
func (f *finder) notify() {
	if f.listener != nil {
		f.listener(f.number(), len(f.matches))
	}
}

// number is the current match counted from one, or 0 when there is none.
func (f *finder) number() int {
	if f.current < 0 || f.current >= len(f.matches) {
		return 0
	}
	return f.current + 1
}

// firstFrom is the first match starting at or after pos, wrapping to the first
// match when there is none.
func (f *finder) firstFrom(pos int) int {
	i := sort.Search(len(f.matches), func(i int) bool { return f.matches[i][0] >= pos })
	if i == len(f.matches) {
		return 0
	}
	return i
}

// lastBefore is the last match ending at or before pos, wrapping to the last
// match when there is none.
func (f *finder) lastBefore(pos int) int {
	i := sort.Search(len(f.matches), func(i int) bool { return f.matches[i][1] > pos })
	if i == 0 {
		return len(f.matches) - 1
	}
	return i - 1
}

// step moves to the next (dir > 0) or previous match, measured from the
// selection rather than from the last match the bar visited: after the caret
// has been clicked somewhere else, "next" means the next one from there.
func (f *finder) step(dir int) {
	if !f.active {
		return
	}
	if f.stale {
		f.refresh(false)
	}
	if len(f.matches) == 0 {
		return
	}
	from, to := f.e.selectionSpan()
	if dir > 0 {
		f.current = f.firstFrom(to)
	} else {
		f.current = f.lastBefore(from)
	}
	// Only the strong tag moves; the soft ones stay where they are.
	f.e.markLayoutStale()
	b := f.e.buffer
	start, end := b.Bounds()
	b.RemoveTag(f.cur, start, end)
	f.apply()
	f.reveal()
	f.notify()
}

// replaceRange swaps the characters [m[0], m[1]) for text with ordinary buffer
// edits. The insert-text and delete-range handlers in New see them exactly as
// they see typing, so the lines are marked dirty for the next render pass, and
// the changed handler goes on to fire OnChanged and the autosave.
func (f *finder) replaceRange(m [2]int, with string) {
	b := f.e.buffer
	b.Delete(b.IterAtOffset(m[0]), b.IterAtOffset(m[1]))
	if with != "" {
		b.Insert(b.IterAtOffset(m[0]), with)
	}
}

// selectionSpan is the selection as character offsets. With nothing selected
// both are the caret.
func (e *Editor) selectionSpan() (from, to int) {
	if start, end, ok := e.buffer.SelectionBounds(); ok {
		return start.Offset(), end.Offset()
	}
	at := e.buffer.IterAtMark(e.buffer.GetInsert()).Offset()
	return at, at
}

// SetFindListener registers fn to hear where the search stands (the current
// match counted from one, 0 for none, and how many there are) after every
// change: a new query, a step, an edit to the note, a different note.
func (e *Editor) SetFindListener(fn func(current, count int)) {
	e.finder().listener = fn
}

// SetFindQuery starts or updates the search: every literal match of q is
// highlighted, the first one from the caret is selected and scrolled into
// view, and the number of matches comes back. An empty query clears the
// highlights. Matching ignores case unless matchCase is set.
func (e *Editor) SetFindQuery(q string, matchCase bool) (count int) {
	f := e.finder()
	f.hook()
	f.query, f.matchCase = q, matchCase
	f.active = q != ""
	f.refresh(true)
	return len(f.matches)
}

// FindNext selects the next match, wrapping past the end of the note.
func (e *Editor) FindNext() { e.finder().step(1) }

// FindPrev selects the previous match, wrapping past the start of the note.
func (e *Editor) FindPrev() { e.finder().step(-1) }

// CurrentMatch is the position of the current match among the matches,
// counted from one, or 0 when there is none.
func (e *Editor) CurrentMatch() int { return e.finder().number() }

// FindCount is how many matches the current query has.
func (e *Editor) FindCount() int { return len(e.finder().matches) }

// FindSeed is the selected text when it is worth searching for: something
// selected, all on one line, and no checkbox inside it. Otherwise it is empty.
func (e *Editor) FindSeed() string {
	start, end, ok := e.buffer.SelectionBounds()
	if !ok || start.Line() != end.Line() {
		return ""
	}
	text := e.sourceSlice(start, end)
	if strings.ContainsRune(text, anchorRune) {
		return ""
	}
	return text
}

// ReplaceCurrent replaces the current match with text and moves on to the next
// one. It is one user action, so one undo puts it back.
func (e *Editor) ReplaceCurrent(with string) {
	f := e.finder()
	if !f.active {
		return
	}
	if f.stale {
		f.refresh(false) // the offsets are old; replacing at them would eat the wrong text
	}
	if f.current < 0 {
		return
	}
	with = cleanReplacement(with)
	m := f.matches[f.current]
	e.buffer.BeginUserAction()
	f.replaceRange(m, with)
	e.buffer.EndUserAction()
	// The caret goes after the new text, so the search resumes past it: a
	// replacement that itself contains the query is not found again.
	e.buffer.PlaceCursor(e.buffer.IterAtOffset(m[0] + utf8.RuneCountInString(with)))
	f.refresh(true)
}

// ReplaceAll replaces every match with text and returns how many there were.
// It is one user action, so one undo puts them all back. It works from the end
// of the note to the start, so an edit never moves a match still to be done.
func (e *Editor) ReplaceAll(with string) int {
	f := e.finder()
	if !f.active {
		return 0
	}
	// Recomputed rather than trusted: this is the one call that rewrites the
	// whole note, and it must not run on offsets from before the last edit.
	matches := findMatches(e.sourceText(), f.query, f.matchCase)
	if len(matches) == 0 {
		return 0
	}
	with = cleanReplacement(with)
	// The edits run as loading, so the buffer's changed handler does not fire
	// OnChanged, and with it the autosave, once per match. markEdited does that
	// once for the lot. Nothing else is lost by it: the handlers that track dirty
	// lines and find's own hook (which is what marks the matches stale) do not
	// look at the flag.
	e.withLoading(func() {
		e.buffer.BeginUserAction()
		for i := len(matches) - 1; i >= 0; i-- {
			f.replaceRange(matches[i], with)
		}
		e.buffer.EndUserAction()
	})
	e.markEdited()
	f.refresh(false)
	return len(matches)
}

// cleanReplacement drops the object-replacement character from text about to
// be inserted. Left in, it would sit in the buffer as a stray character, and a
// line that begins with one is read back as a checkbox (see Content).
func cleanReplacement(s string) string {
	return strings.ReplaceAll(s, anchorChar, "")
}

// ClearFind ends the search: both tags come off the whole note and the state
// is forgotten. The selection is left alone.
func (e *Editor) ClearFind() {
	f := finders[e]
	if f == nil {
		return
	}
	f.active, f.stale, f.held = false, false, false
	f.query, f.matches, f.current = "", nil, -1
	if f.hooked {
		e.markLayoutStale()
		start, end := e.buffer.Bounds()
		e.buffer.RemoveTag(f.match, start, end)
		e.buffer.RemoveTag(f.cur, start, end)
	}
}

// findMatches returns the literal, non-overlapping matches of query in text as
// [start, end) character offsets, in order. It has no GTK in it.
//
// Offsets count runes, not bytes, because that is what a text buffer counts, so
// they can be handed to it as they are. Case is folded per rune with Unicode
// simple folding, which is right for accented letters and Greek and does not
// try to treat "ß" as "ss".
//
// U+FFFC stands for an embedded checkbox in the buffer's text. A match never
// includes one, so a query cannot match across or on a widget.
//
// The "<!-- ... -->" comment a task carries its priority and due date in is
// hidden by the editor, so it is not text the person can see and a match in it
// would highlight nothing. Those runes are treated like a widget: never part of
// a match, and counted in the offsets all the same.
func findMatches(text, query string, matchCase bool) [][2]int {
	if query == "" {
		return nil
	}
	q := []rune(query)
	if !matchCase {
		for i, r := range q {
			q[i] = foldRune(r)
		}
	}
	t := []rune(text)
	maskMetadata(text, t)

	var out [][2]int
	for i := 0; i+len(q) <= len(t); {
		if matchesAt(t, i, q, matchCase) {
			out = append(out, [2]int{i, i + len(q)})
			i += len(q) // a match is never reported inside the last one
			continue
		}
		i++
	}
	return out
}

// maskMetadata overwrites, in t (the runes of text), each line's metadata
// comment with the widget character, which no match includes. The comment is the
// one parseLineSpans hides: from the line's first "<!--" to the first "-->" after
// it, and a "<!--" with no "-->" is ordinary text.
func maskMetadata(text string, t []rune) {
	if !strings.Contains(text, "<!--") {
		return // nearly every note
	}
	lineStart := 0 // rune offset of the line's first character
	for rest := text; ; {
		line, next, more := strings.Cut(rest, "\n")
		if b := strings.Index(line, "<!--"); b >= 0 {
			if rel := strings.Index(line[b:], "-->"); rel >= 0 {
				from := lineStart + utf8.RuneCountInString(line[:b])
				to := from + utf8.RuneCountInString(line[b:b+rel+len("-->")])
				for i := from; i < to; i++ {
					t[i] = anchorRune
				}
			}
		}
		if !more {
			return
		}
		lineStart += utf8.RuneCountInString(line) + 1
		rest = next
	}
}

// matchesAt reports whether q (already folded when case is ignored) sits in t
// at position i.
func matchesAt(t []rune, i int, q []rune, matchCase bool) bool {
	for j, want := range q {
		got := t[i+j]
		if got == anchorRune {
			return false
		}
		if !matchCase {
			got = foldRune(got)
		}
		if got != want {
			return false
		}
	}
	return true
}

// foldRune maps every rune of a case-folding orbit (K, k and the Kelvin sign,
// or s, S and the long s) to the one rune that stands for it, the smallest.
// Comparing the results is what strings.EqualFold does, without allocating.
func foldRune(r rune) rune {
	if r < utf8.RuneSelf {
		// Fast path, and consistent with the general one: an ASCII capital is
		// the smallest rune in its orbit.
		if 'a' <= r && r <= 'z' {
			return r - 'a' + 'A'
		}
		return r
	}
	least := r
	for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
		if f < least {
			least = f
		}
	}
	return least
}
