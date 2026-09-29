package editor

import (
	"path"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
)

// This file suggests note names after "[[" and tag names after "#".
//
// Deciding what is being typed, ranking the candidates and working out what to
// insert are plain functions and are tested on their own. The rest is one
// popover that is built the first time it is needed and reused after that.

// suggestLimit is how many suggestions are shown at once, and how many rows the
// popover has.
const suggestLimit = 8

type suggestKind int

const (
	suggestNone suggestKind = iota
	suggestNotes
	suggestTags
)

// rankBy keeps the items bucket accepts (it returns a negative number for the
// rest), orders them by bucket, then shorter first, then alphabetically without
// regard to case, and returns the first limit of them. The last tie-break, the
// text as written, only exists so the order never depends on the input order.
func rankBy(items []string, limit int, bucket func(lower string) int) []string {
	if limit <= 0 {
		return nil
	}
	type hit struct {
		name  string
		lower string
		rank  int
		size  int
	}
	var hits []hit
	for _, it := range items {
		low := strings.ToLower(it)
		if r := bucket(low); r >= 0 {
			hits = append(hits, hit{it, low, r, utf8.RuneCountInString(it)})
		}
	}
	if len(hits) == 0 {
		return nil
	}
	sort.Slice(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		switch {
		case a.rank != b.rank:
			return a.rank < b.rank
		case a.size != b.size:
			return a.size < b.size
		case a.lower != b.lower:
			return a.lower < b.lower
		}
		return a.name < b.name
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.name
	}
	return out
}

// rankNotes picks the notes to offer for what has been typed after "[[". Notes
// are vault-relative paths without an extension. A name that starts with the
// query comes first, then one that contains it, then one whose folder does, so
// typing "todo" offers "Todo" before "Work/Todo list" before "Todo/Ideas".
func rankNotes(query string, notes []string, limit int) []string {
	q := strings.ToLower(query)
	return rankBy(notes, limit, func(low string) int {
		base := low[strings.LastIndexByte(low, '/')+1:]
		switch {
		case strings.HasPrefix(base, q):
			return 0
		case strings.Contains(base, q):
			return 1
		case strings.Contains(low, q):
			return 2
		}
		return -1
	})
}

// rankTags picks the tags to offer for what has been typed after "#": those
// that start with it, then those that contain it.
func rankTags(query string, tags []string, limit int) []string {
	q := strings.ToLower(query)
	return rankBy(tags, limit, func(low string) int {
		switch {
		case strings.HasPrefix(low, q):
			return 0
		case strings.Contains(low, q):
			return 1
		}
		return -1
	})
}

// noteInsert is what goes between "[[" and "]]" for a chosen note. People write
// links by name, so it is the note's name, unless another note has the same
// name in another folder. A bare name would then mean whichever of the two
// the resolver prefers, so the path is written instead. Names are compared
// without regard to case, as links are resolved.
func noteInsert(choice string, notes []string) string {
	base := path.Base(choice)
	for _, n := range notes {
		if n != choice && strings.EqualFold(path.Base(n), base) {
			return choice
		}
	}
	return base
}

// splitNote separates a note path into the name shown first and the folder
// shown dimmed after it. A note in the vault root has no folder.
func splitNote(p string) (name, dir string) {
	name = path.Base(p)
	if d := path.Dir(p); d != "." {
		dir = d
	}
	return name, dir
}

// isTagRune is what a tag may be made of, the same characters internal/markup
// reads.
func isTagRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '/'
}

// wikiQuery reports whether the caret, at the end of before, is in the name
// part of a "[[" link that is still open, and returns what has been typed of
// it. Once a "|" or "#" has been typed the name is done and the rest is an
// alias or a heading, which there is nothing to suggest for.
func wikiQuery(before string) (string, bool) {
	i := strings.LastIndex(before, "[[")
	if i < 0 {
		return "", false
	}
	q := before[i+2:]
	if strings.ContainsAny(q, "[]|#") {
		return "", false
	}
	return q, true
}

// tagQuery reports whether the caret, at the end of before, is right after a
// "#" that starts a word and at least one character of a tag, and returns
// those characters. The "#" must open the line or follow a space or an opening
// bracket, as markup requires, so "C#" and "page#section" never ask, and a
// heading's "# " does not either because a space is not a tag character.
func tagQuery(before string) (string, bool) {
	i := len(before)
	for i > 0 {
		r, size := utf8.DecodeLastRuneInString(before[:i])
		if !isTagRune(r) {
			break
		}
		i -= size
	}
	if i == len(before) || i == 0 || before[i-1] != '#' {
		return "", false
	}
	if h := i - 1; h > 0 {
		prev, _ := utf8.DecodeLastRuneInString(before[:h])
		if !unicode.IsSpace(prev) && prev != '(' && prev != '[' {
			return "", false
		}
	}
	return before[i:], true
}

// tagTail is how many bytes at the start of after continue a tag word.
func tagTail(after string) int {
	i := 0
	for i < len(after) {
		r, size := utf8.DecodeRuneInString(after[i:])
		if !isTagRune(r) {
			break
		}
		i += size
	}
	return i
}

// suggestContext works out what, if anything, the text before the caret is
// asking to complete, and the part of it typed so far. Inside `inline code`
// nothing is, since markup does not read links there either.
func suggestContext(before string) (suggestKind, string) {
	if strings.Count(before, "`")%2 == 1 {
		return suggestNone, ""
	}
	if q, ok := wikiQuery(before); ok {
		return suggestNotes, q
	}
	// Inside a link that has not been closed yet, a "#" starts a heading.
	if strings.LastIndex(before, "[[") > strings.LastIndex(before, "]]") {
		return suggestNone, ""
	}
	if q, ok := tagQuery(before); ok {
		return suggestTags, q
	}
	return suggestNone, ""
}

// completion is an edit around the caret, counted in characters: replace this
// many before it and this many after it with text, then put the caret at the
// end of text moved on by skip. skip steps over text that was already there
// and should stay, such as a "]]" that closes the link.
type completion struct {
	before int
	after  int
	text   string
	skip   int
}

// noteTail works out what follows the caret in a link being completed: how many
// bytes of after are the rest of the name that is being replaced, whether the
// link is closed with "]]" right after them, and whether something ends the
// name at all ("]]", or the "|" or "#" of an alias or a heading).
//
// The name runs up to the next "]", "[", "|" or "#", none of which a name can
// hold. Only when one of the ones that end a name follows is what lies before it
// taken as part of the name. With none of them there the link is still open and
// the text after the caret is not known to belong to it, as in
// "see [[Pl| for details", so it is left alone.
func noteTail(after string) (n int, closed, ended bool) {
	i := strings.IndexAny(after, "[]|#")
	if i < 0 {
		return 0, false, false
	}
	switch after[i] {
	case '|', '#':
		return i, false, true
	case ']':
		if strings.HasPrefix(after[i:], "]]") {
			return i, true, true
		}
	}
	return 0, false, false
}

// completeNote works out the edit for choosing a note. It replaces what was
// typed since "[[" with the note's name (or path, see noteInsert), and the rest
// of the name after the caret when the caret is inside an existing link (see
// noteTail). The link is closed unless the text after the caret already does
// or goes on to an alias or a heading.
func completeNote(before, after, choice string, notes []string) (completion, bool) {
	kind, q := suggestContext(before)
	if kind != suggestNotes {
		return completion{}, false
	}
	c := completion{before: utf8.RuneCountInString(q), text: noteInsert(choice, notes)}
	n, closed, ended := noteTail(after)
	c.after = utf8.RuneCountInString(after[:n])
	switch {
	case closed:
		c.skip = 2
	case !ended:
		c.text += "]]"
	}
	return c, true
}

// completeTag works out the edit for choosing a tag. It finishes the word, so
// the rest of a tag the caret is inside goes too, and follows it with a space
// unless one is already there.
func completeTag(before, after, tag string) (completion, bool) {
	kind, q := suggestContext(before)
	if kind != suggestTags {
		return completion{}, false
	}
	n := tagTail(after)
	c := completion{
		before: utf8.RuneCountInString(q),
		after:  utf8.RuneCountInString(after[:n]),
		text:   tag,
	}
	if rest := after[n:]; rest != "" && (rest[0] == ' ' || rest[0] == '\t') {
		c.skip = 1
	} else {
		c.text += " "
	}
	return c, true
}

// suggestRow is one row of the popover, kept for reuse: building widgets for
// every keystroke would grow memory for as long as the app ran.
type suggestRow struct {
	row  *gtk.ListBoxRow
	name *gtk.Label
	dir  *gtk.Label
}

// suggester is the state of the suggestion popover.
type suggester struct {
	pop  *gtk.Popover
	list *gtk.ListBox
	rows [suggestLimit]suggestRow

	kind      suggestKind
	trigger   int      // buffer offset of the "[[" or "#" the suggestions belong to
	dismissed int      // the trigger the person closed with Escape; typing on must not reopen it
	pool      []string // everything that can be offered, fetched when the popover opened
	items     []string // what the visible rows stand for
	sel       int
	busy      bool // an insertion of ours is in progress
}

// installComplete hooks suggestions up to the buffer and the view.
func (e *Editor) installComplete() {
	e.sg.dismissed = -1

	// An edit can open the popover or update it; only a caret move that ends the
	// context closes it. Clicking into an old, unfinished "[[" should not pop
	// anything up.
	e.buffer.ConnectChanged(func() {
		if e.loading || e.sg.busy {
			return
		}
		e.suggest(true)
	})
	e.buffer.ConnectMarkSet(func(_ *gtk.TextIter, mark *gtk.TextMark) {
		if e.sg.kind == suggestNone || e.loading || e.sg.busy || mark.Name() != "insert" {
			return
		}
		e.suggest(false)
	})

	// The keys are taken in the capture phase, before the text view sees them,
	// because Enter and Tab would otherwise insert a newline or a tab.
	keys := gtk.NewEventControllerKey()
	keys.SetPropagationPhase(gtk.PhaseCapture)
	keys.ConnectKeyPressed(func(keyval, _ uint, state gdk.ModifierType) bool {
		return e.suggestKey(keyval, state)
	})
	e.view.AddController(keys)

	// Nothing else closes the popover when focus goes elsewhere, since it does
	// not autohide.
	focus := gtk.NewEventControllerFocus()
	focus.ConnectLeave(func() { e.closeSuggest() })
	e.view.AddController(focus)

	// The popover is parented to the view, and GTK complains of a child left
	// behind when a view is finalized with one, so it is let go of first.
	e.view.ConnectDestroy(func() {
		s := &e.sg
		s.kind = suggestNone
		if s.pop != nil {
			s.pop.Unparent()
			s.pop, s.list = nil, nil
		}
	})
}

// lineBefore is the text of the caret's line up to the caret. The checkbox
// anchor is left off: it is not text, and it would stop a "#" right after it
// from looking like the start of a word.
func (e *Editor) lineBefore(caret *gtk.TextIter) string {
	start, ok := e.buffer.IterAtLine(caret.Line())
	if !ok {
		return ""
	}
	return strings.TrimPrefix(e.buffer.Slice(start, caret, true), anchorChar)
}

// lineAfter is the text of the caret's line from the caret on.
func (e *Editor) lineAfter(caret *gtk.TextIter) string {
	end := caret.Copy()
	if !end.EndsLine() {
		end.ForwardToLineEnd()
	}
	return e.buffer.Slice(caret, end, true)
}

// suggest opens, updates or closes the popover for the text at the caret. typed
// says an edit caused the call, which is the only thing allowed to open it.
func (e *Editor) suggest(typed bool) {
	s := &e.sg
	// A note being loaded, or text put in by the app while the view is not the
	// thing being typed into, is not someone asking for suggestions.
	if e.loading || !e.view.HasFocus() {
		e.closeSuggest()
		return
	}
	if !typed && s.kind == suggestNone {
		return
	}
	caret := e.buffer.IterAtMark(e.buffer.GetInsert())
	if caret == nil {
		e.closeSuggest()
		return
	}
	kind, query := suggestContext(e.lineBefore(caret))
	if kind == suggestNone {
		s.dismissed = -1
		e.closeSuggest()
		return
	}
	prefix := 1 // the "#"
	if kind == suggestNotes {
		prefix = 2 // the "[["
	}
	qlen := utf8.RuneCountInString(query)
	trigger := caret.Offset() - qlen - prefix
	if trigger == s.dismissed {
		e.closeSuggest()
		return
	}
	same := kind == s.kind && trigger == s.trigger
	if !typed && !same {
		e.closeSuggest()
		return
	}

	// The candidates are asked for once per "[[" or "#", not once per key.
	pool := s.pool
	if !same {
		pool = nil
		switch {
		case kind == suggestNotes && e.NoteNames != nil:
			pool = e.NoteNames()
		case kind == suggestTags && e.TagNames != nil:
			pool = e.TagNames()
		}
	}
	var items []string
	if kind == suggestNotes {
		items = rankNotes(query, pool, suggestLimit)
	} else {
		items = rankTags(query, pool, suggestLimit)
		// Nothing to complete when the only candidate is what is already there.
		if len(items) == 1 && strings.EqualFold(items[0], query) {
			items = nil
		}
	}
	if len(items) == 0 {
		e.closeSuggest()
		return
	}

	s.kind, s.trigger, s.pool, s.items, s.sel = kind, trigger, pool, items, 0
	s.build(e)
	s.fill()

	// Anchor at the start of what is being typed, so the popover holds still
	// while the query grows.
	if at, ok := e.buffer.IterAtLineOffset(caret.Line(), caret.LineOffset()-qlen); ok {
		r := e.view.IterLocation(at)
		x, y := e.view.BufferToWindowCoords(gtk.TextWindowWidget, r.X(), r.Y())
		rect := gdk.NewRectangle(x, y, 1, r.Height())
		s.pop.SetPointingTo(&rect)
	}
	if !s.pop.Visible() {
		s.pop.Popup()
	}
}

// build makes the popover the first time it is needed.
//
// It must not take focus from the text view: the person is still typing. It
// does not autohide (that would grab input), it cannot be focused, and its
// rows cannot either, so a click on a row activates it without moving focus.
func (s *suggester) build(e *Editor) {
	if s.pop != nil {
		return
	}
	pop := gtk.NewPopover()
	pop.SetAutohide(false)
	pop.SetCanFocus(false)
	pop.SetFocusable(false)
	pop.SetHasArrow(false)
	pop.SetPosition(gtk.PosBottom)

	list := gtk.NewListBox()
	list.SetSelectionMode(gtk.SelectionSingle)
	list.SetActivateOnSingleClick(true)
	list.SetCanFocus(false)
	list.SetFocusable(false)
	list.SetFocusOnClick(false)
	list.SetSizeRequest(260, -1)

	for i := range s.rows {
		row := gtk.NewListBoxRow()
		row.SetCanFocus(false)
		row.SetFocusable(false)
		row.SetFocusOnClick(false)

		box := gtk.NewBox(gtk.OrientationHorizontal, 10)
		box.SetMarginTop(3)
		box.SetMarginBottom(3)
		box.SetMarginStart(6)
		box.SetMarginEnd(6)

		name := gtk.NewLabel("")
		name.SetXAlign(0)
		name.SetEllipsize(pango.EllipsizeEnd)
		name.SetMaxWidthChars(32)

		dir := gtk.NewLabel("")
		dir.SetXAlign(0)
		dir.SetHExpand(true)
		dir.SetEllipsize(pango.EllipsizeStart)
		dir.SetMaxWidthChars(28)
		dir.AddCSSClass("dim-label")

		box.Append(name)
		box.Append(dir)
		row.SetChild(box)
		list.Append(row)
		s.rows[i] = suggestRow{row: row, name: name, dir: dir}
	}
	// The handler holds the editor, not the widgets it is attached to.
	list.ConnectRowActivated(func(row *gtk.ListBoxRow) { e.acceptSuggestion(row.Index()) })

	pop.SetChild(list)
	pop.SetParent(e.view)
	s.pop, s.list = pop, list
}

// fill dresses the rows for the current suggestions and selects the first.
func (s *suggester) fill() {
	for i := range s.rows {
		r := &s.rows[i]
		if i >= len(s.items) {
			r.row.SetVisible(false)
			continue
		}
		r.row.SetVisible(true)
		if s.kind == suggestTags {
			r.name.SetText("#" + s.items[i])
			r.dir.SetText("")
			continue
		}
		name, dir := splitNote(s.items[i])
		r.name.SetText(name)
		r.dir.SetText(dir)
	}
	s.list.SelectRow(s.rows[s.sel].row)
}

// move steps the selection, wrapping at both ends.
func (s *suggester) move(delta int) {
	n := len(s.items)
	if n == 0 {
		return
	}
	s.sel = (s.sel + delta + n) % n
	s.list.SelectRow(s.rows[s.sel].row)
}

// suggestKey handles a key while the popover is open and reports whether it
// used it. Keys with Ctrl or Alt held are left alone, so shortcuts still work.
func (e *Editor) suggestKey(keyval uint, state gdk.ModifierType) bool {
	s := &e.sg
	if s.kind == suggestNone || state&(gdk.ControlMask|gdk.AltMask) != 0 {
		return false
	}
	switch keyval {
	case gdk.KEY_Down:
		s.move(1)
	case gdk.KEY_Up:
		s.move(-1)
	case gdk.KEY_Return, gdk.KEY_KP_Enter, gdk.KEY_ISO_Enter, gdk.KEY_Tab:
		e.acceptSuggestion(s.sel)
	case gdk.KEY_Escape:
		s.dismissed = s.trigger
		e.closeSuggest()
	default:
		return false
	}
	return true
}

// closeSuggest hides the popover and forgets what it was offering.
func (e *Editor) closeSuggest() {
	s := &e.sg
	if s.kind == suggestNone {
		return
	}
	s.kind = suggestNone
	s.pool, s.items = nil, nil
	if s.pop != nil {
		s.pop.Popdown()
	}
}

// acceptSuggestion inserts the suggestion in row idx.
func (e *Editor) acceptSuggestion(idx int) {
	s := &e.sg
	if s.kind == suggestNone || idx < 0 || idx >= len(s.items) {
		return
	}
	kind, item, pool := s.kind, s.items[idx], s.pool
	e.closeSuggest()

	// The edit is worked out from the buffer as it is now rather than from what
	// was recorded when the popover last updated.
	caret := e.buffer.IterAtMark(e.buffer.GetInsert())
	if caret == nil {
		return
	}
	before, after := e.lineBefore(caret), e.lineAfter(caret)
	var c completion
	var ok bool
	if kind == suggestNotes {
		c, ok = completeNote(before, after, item, pool)
	} else {
		c, ok = completeTag(before, after, item)
	}
	if !ok {
		return
	}

	line, col := caret.Line(), caret.LineOffset()
	from, ok1 := e.buffer.IterAtLineOffset(line, col-c.before)
	to, ok2 := e.buffer.IterAtLineOffset(line, col+c.after)
	if !ok1 || !ok2 {
		return
	}
	base := from.Offset()

	// One user action, so undo takes the whole completion back at once. The
	// flag keeps our own edit from being read as more typing.
	s.busy = true
	defer func() { s.busy = false }()
	e.buffer.BeginUserAction()
	e.buffer.Delete(from, to)
	e.buffer.Insert(from, c.text)
	e.buffer.EndUserAction()
	e.buffer.PlaceCursor(e.buffer.IterAtOffset(base + utf8.RuneCountInString(c.text) + c.skip))
}
