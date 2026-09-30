package editor

import (
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"atlas-notes/internal/markup"
)

// This file suggests note names after "[[", tag names after "#", and the
// blocks of the slash menu after "/".
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
	suggestSlash
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
	if q, ok := slashQuery(before); ok {
		return suggestSlash, q
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
	icon *gtk.Image // only the slash menu shows one
	name *gtk.Label
	dir  *gtk.Label
}

// suggester is the state of the suggestion popover.
type suggester struct {
	pop  *gtk.Popover
	list *gtk.ListBox
	rows [suggestLimit]suggestRow

	kind      suggestKind
	trigger   int         // buffer offset of the "[[" or "#" the suggestions belong to
	dismissed int         // the trigger the person closed with Escape; typing on must not reopen it
	pool      []string    // everything that can be offered, fetched when the popover opened
	items     []string    // what is on offer; the rows show suggestLimit of them from top
	slash     []slashItem // for the slash menu, what each of items stands for
	sel       int         // index into items
	top       int         // index of the first item the rows show
	busy      bool        // an insertion of ours is in progress
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
	if kind == suggestSlash && e.inCodeFence(caret.Line()) {
		s.dismissed = -1
		e.closeSuggest()
		return
	}
	prefix := 1 // the "#" or "/"
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
	var slash []slashItem
	switch {
	case kind == suggestSlash:
		slash = slashMatches(query, e.OnAssistant != nil, e.OnInsertImage != nil)
		for _, it := range slash {
			items = append(items, it.label)
		}
	case kind == suggestNotes:
		items = rankNotes(query, pool, suggestLimit)
	default:
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

	s.kind, s.trigger, s.pool, s.items, s.slash, s.sel, s.top = kind, trigger, pool, items, slash, 0, 0
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

		icon := gtk.NewImage()
		icon.SetVisible(false)

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

		box.Append(icon)
		box.Append(name)
		box.Append(dir)
		row.SetChild(box)
		list.Append(row)
		s.rows[i] = suggestRow{row: row, icon: icon, name: name, dir: dir}
	}
	// The handler holds the editor, not the widgets it is attached to.
	list.ConnectRowActivated(func(row *gtk.ListBoxRow) { e.acceptSuggestion(s.top + row.Index()) })

	pop.SetChild(list)
	pop.SetParent(e.view)
	s.pop, s.list = pop, list
}

// fill dresses the rows for the current suggestions and selects the chosen
// one. The rows are a window on items: the slash menu has more entries than
// rows, and the window follows the selection.
func (s *suggester) fill() {
	if s.sel < s.top {
		s.top = s.sel
	} else if s.sel >= s.top+len(s.rows) {
		s.top = s.sel - len(s.rows) + 1
	}
	for i := range s.rows {
		r := &s.rows[i]
		n := s.top + i
		if n >= len(s.items) {
			r.row.SetVisible(false)
			continue
		}
		r.row.SetVisible(true)
		r.icon.SetVisible(s.kind == suggestSlash)
		switch s.kind {
		case suggestTags:
			r.name.SetText("#" + s.items[n])
			r.dir.SetText("")
		case suggestSlash:
			r.icon.SetFromIconName(s.slash[n].icon)
			r.name.SetText(s.slash[n].label)
			r.dir.SetText(s.slash[n].hint)
		default:
			name, dir := splitNote(s.items[n])
			r.name.SetText(name)
			r.dir.SetText(dir)
		}
	}
	s.list.SelectRow(s.rows[s.sel-s.top].row)
}

// move steps the selection, wrapping at both ends.
func (s *suggester) move(delta int) {
	n := len(s.items)
	if n == 0 {
		return
	}
	s.sel = (s.sel + delta + n) % n
	s.fill()
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
	s.pool, s.items, s.slash = nil, nil, nil
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
	var chosen slashItem
	if kind == suggestSlash {
		chosen = s.slash[idx]
	}
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
	switch kind {
	case suggestSlash:
		c, ok = completeSlash(before, chosen)
	case suggestNotes:
		c, ok = completeNote(before, after, item, pool)
	default:
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

	// What the block asks of the app happens once the text is settled, so an
	// assistant answer or a file chooser never sees a half-made edit.
	if kind != suggestSlash {
		return
	}
	s.busy = false
	switch {
	case chosen.action != "" && e.OnAssistant != nil:
		e.OnAssistant(chosen.action)
	case chosen.image && e.OnInsertImage != nil:
		e.OnInsertImage()
	case chosen.text == "[[":
		e.suggest(true) // the link's own suggestions take over
	}
}

// slashItem is one entry of the slash menu. text is what replaces the "/" and
// what was typed after it; a "\x00" in it marks where the caret goes, and
// without one the caret ends up after the text. block says the text is a whole
// line's worth of markup, so it starts a new line when the "/" was typed after
// other words. An entry with an action or image asks the app for something
// instead of, or besides, inserting text.
type slashItem struct {
	label, hint, icon string
	keys              string // extra words that find it
	text              string
	block             bool
	action            string // an assistant action, for OnAssistant
	image             bool   // asks the app to insert a picture
}

// Assistant actions the slash menu hands to Editor.OnAssistant.
const (
	AssistantSummarise = "summarise"
	AssistantContinue  = "continue"
	AssistantChecklist = "checklist"
)

var slashItems = []slashItem{
	{label: "Heading 1", hint: "#", icon: "atlasnotes-heading1-symbolic", keys: "title h1", text: "# ", block: true},
	{label: "Heading 2", hint: "##", icon: "atlasnotes-heading2-symbolic", keys: "subtitle h2", text: "## ", block: true},
	{label: "Bullet list", hint: "-", icon: "atlasnotes-bullet-list-symbolic", keys: "unordered points", text: "- ", block: true},
	{label: "Task", hint: "- [ ]", icon: "atlasnotes-task-symbolic", keys: "todo checkbox", text: "- [ ] ", block: true},
	{label: "Numbered list", hint: "1.", icon: "atlasnotes-outline-symbolic", keys: "ordered steps", text: "1. ", block: true},
	{label: "Quote", hint: ">", icon: "atlasnotes-quote-symbolic", keys: "blockquote", text: "> ", block: true},
	{label: "Callout", hint: "> [!note]", icon: "atlasnotes-callout-note-symbolic", keys: "note admonition", text: "> [!note] ", block: true},
	{label: "Table", hint: "2 by 2", icon: "atlasnotes-table-symbolic", keys: "grid", text: "| Name | Value |\n| --- | --- |\n| \x00 |  |\n|  |  |", block: true},
	{label: "Diagram", hint: "flowchart", icon: "atlasnotes-code-symbolic", keys: "mermaid flowchart graph chart", text: "```mermaid\nflowchart TD\n  a[\x00First step<br>What it does] --> b[Second step<br>What it does]\n  subgraph g [A group]\n    direction LR\n    c[One] --> d[Two]\n  end\n  b --> g\n```", block: true},
	{label: "Code block", hint: "```", icon: "atlasnotes-code-symbolic", keys: "fence snippet", text: "```\n\x00\n```", block: true},
	{label: "Divider", hint: "---", icon: "atlasnotes-divider-symbolic", keys: "rule line separator", text: "---\n", block: true},
	{label: "Image", hint: "from a file", icon: "atlasnotes-image-symbolic", keys: "picture photo", image: true},
	{label: "Link", hint: "[[", icon: "atlasnotes-link-symbolic", keys: "note wiki", text: "[["},
	{label: "Summarise", hint: "assistant", icon: "atlasnotes-sparkle-symbolic", keys: "summary ai", action: AssistantSummarise},
	{label: "Continue writing", hint: "assistant", icon: "atlasnotes-sparkle-symbolic", keys: "ai carry on", action: AssistantContinue},
	{label: "Make a checklist", hint: "assistant", icon: "atlasnotes-sparkle-symbolic", keys: "ai tasks todo", action: AssistantChecklist},
}

// slashMaxQuery is how long the text after "/" may get before the menu gives
// up: nothing in it is that long, and a slash that far back is prose.
const slashMaxQuery = 24

// slashQuery reports whether the caret, at the end of before, is in a "/" that
// opens the slash menu, and returns what has been typed after it. The "/" must
// open the line or follow a space or tab, so "a/b", "and/or" and "https://x"
// never ask. The query starts with a letter, so "a / b" and "1 /2" do not
// either, and it holds no more than a couple of words.
func slashQuery(before string) (string, bool) {
	i := strings.LastIndexByte(before, '/')
	if i < 0 {
		return "", false
	}
	if i > 0 {
		prev, _ := utf8.DecodeLastRuneInString(before[:i])
		if prev != ' ' && prev != '\t' {
			return "", false
		}
	}
	q := before[i+1:]
	if len(q) > slashMaxQuery || strings.ContainsAny(q, "/\t") || strings.Contains(q, "  ") {
		return "", false
	}
	if q != "" {
		if r, _ := utf8.DecodeRuneInString(q); !unicode.IsLetter(r) {
			return "", false
		}
	}
	return q, true
}

// slashMatches is the menu for what has been typed: every entry whose name or
// search words start with each word of query, the ones whose name does first.
// The assistant entries are left out when the app has no assistant to hand
// them to, and Image when it cannot choose a file.
func slashMatches(query string, assistant, image bool) []slashItem {
	words := strings.Fields(strings.ToLower(query))
	var first, rest []slashItem
	for _, it := range slashItems {
		if (it.action != "" && !assistant) || (it.image && !image) {
			continue
		}
		label := strings.ToLower(it.label)
		hay := strings.Fields(label + " " + it.keys)
		ok, byName := true, true
		for _, w := range words {
			if !anyPrefix(hay, w) {
				ok = false
				break
			}
			if !anyPrefix(strings.Fields(label), w) {
				byName = false
			}
		}
		switch {
		case !ok:
		case byName:
			first = append(first, it)
		default:
			rest = append(rest, it)
		}
	}
	return append(first, rest...)
}

func anyPrefix(words []string, p string) bool {
	for _, w := range words {
		if strings.HasPrefix(w, p) {
			return true
		}
	}
	return false
}

// completeSlash works out the edit for choosing an entry: the "/" and what was
// typed after it are replaced by its text. A block chosen after other words on
// the line starts a line of its own.
func completeSlash(before string, it slashItem) (completion, bool) {
	kind, q := suggestContext(before)
	if kind != suggestSlash {
		return completion{}, false
	}
	text := it.text
	if it.block && !blankBeforeSlash(before[:len(before)-len(q)-1]) {
		text = "\n" + text
	}
	c := completion{before: utf8.RuneCountInString(q) + 1}
	if i := strings.IndexByte(text, 0); i >= 0 {
		c.text = text[:i] + text[i+1:]
		c.skip = -utf8.RuneCountInString(text[i+1:])
	} else {
		c.text = text
	}
	return c, true
}

// lineMarker is what may stand before a block on a line without the line
// counting as having words on it: indentation, quote marks, a list marker, a
// task box.
var lineMarker = regexp.MustCompile(`^\s*(?:>\s*)*(?:(?:[-*+]|\d+[.)])\s+)?(?:\[[ xX]\]\s+)?$`)

// blankBeforeSlash says nothing but a marker stands before the "/", so a block
// chosen there needs no line of its own: starting one would leave the marker's
// item empty above it.
func blankBeforeSlash(prefix string) bool { return lineMarker.MatchString(prefix) }

// inCodeFence reports whether a line of the note is inside a fenced code
// block, where "/" is code and nothing is offered.
func (e *Editor) inCodeFence(line int) bool {
	fence := markup.InCodeFence(e.sourceText())
	return line >= 0 && line < len(fence) && fence[line]
}
