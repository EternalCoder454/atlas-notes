package app

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
)

// The command box (Ctrl+P): one field that finds notes, runs commands, jumps to
// a setting and opens a tag. Everything is ranked together, so the person does
// not have to know which of them they are looking for; a first character
// narrows it when they do.
//
// What to show for a query is decided by buildPalette, which knows nothing
// about widgets and is tested on its own. The rest is the dialog.

type palKind int

const (
	palNote palKind = iota
	palAction
	palSetting
	palTag
	palAsk
)

// palEntry is one row of the results. Target is what running it needs: a note's
// path, an action's name, a settings section, a tag, or the question.
type palEntry struct {
	Kind   palKind
	Title  string
	Sub    string
	Target string
}

// paletteAction is a command the box can run. Keys are extra words that find
// it, and NeedsNote says it does nothing without a note open, so it is left
// out until there is one.
type paletteAction struct {
	Name      string // the GAction's name, without "app."
	Label     string
	Keys      string
	NeedsNote bool
}

// paletteActions labels every action registered in actions.go. A test keeps
// the two lists in step, so a new shortcut cannot be left out of the box.
var paletteActions = []paletteAction{
	{"new-note", "New note", "create", false},
	{"new-checklist", "New checklist", "create tasks todo", false},
	{"new-folder", "New folder", "create", false},
	{"new-from-template", "New note from a template", "create", false},
	{"today", "Open today's note", "daily journal", false},
	{"save", "Save now", "", true},
	{"rename", "Rename the open note", "title", true},
	{"export", "Export the open note", "pdf html save as", true},
	{"history", "Version history", "restore earlier", true},
	{"insert-image", "Insert an image", "picture photo", true},
	{"search", "Search the vault", "find notes sidebar", false},
	{"find", "Find in this note", "search", true},
	{"find-replace", "Find and replace", "search", true},
	{"find-next", "Next match", "find", true},
	{"find-previous", "Previous match", "find", true},
	{"home", "Home screen", "welcome start", false},
	{"toggle-vault", "Show or hide the vault", "sidebar panel notes", false},
	{"toggle-assistant", "Show or hide the assistant", "sidebar panel ai", false},
	{"lock-now", "Lock protected notes now", "password secure", false},
	{"settings", "Open Settings", "preferences options", false},
	{"shortcuts", "Keyboard shortcuts", "keys help", false},
	{"about", "About Atlas Notes", "version", false},
	{"focus-assistant", "Ask the assistant", "ai question", false},
	{"bold", "Bold", "format", true},
	{"italic", "Italic", "format", true},
	{"code", "Inline code", "format", true},
	{"task", "Turn the line into a task", "checkbox todo", true},
	{"heading1", "Heading", "title format", true},
	{"heading2", "Subheading", "format", true},
	{"body-text", "Plain text", "paragraph format", true},
}

// paletteSkip are actions the box does not list: opening it from itself.
var paletteSkip = map[string]bool{"command-box": true}

// paletteSection is a Settings section the box can jump to.
type paletteSection struct{ ID, Label string }

var paletteSections = []paletteSection{
	{sectionAppearance, "Appearance"},
	{sectionNotes, "Notes"},
	{sectionAssistant, "Assistant"},
	{sectionUpdates, "Updates"},
	{sectionAbout, "About"},
}

// paletteTag is a tag and how many notes carry it.
type paletteTag struct {
	Name  string
	Notes int
}

// paletteInput is what the box searches. ContentHits finds notes by their text
// (nil when there is no store); it is only asked for what looks like a word.
type paletteInput struct {
	Notes       []string // every note's path
	Recent      []string // newest first, for the empty box
	Tags        []paletteTag
	Actions     []paletteAction // already without those that need a note when none is open
	Sections    []paletteSection
	ContentHits func(query string) []string
}

const (
	paletteMixedLimit = 12 // results when everything is searched together
	paletteListLimit  = 30 // results when a prefix narrows it to one kind
	paletteRecent     = 4  // recent notes at the top of the empty box
)

// paletteScore says how well query matches text, lower being better, or -1 for
// no match: the whole text, then its start, then the start of a word in it,
// then anywhere in it, then every word of the query somewhere in it.
func paletteScore(query, text string) int {
	q := strings.ToLower(strings.TrimSpace(query))
	t := strings.ToLower(text)
	switch {
	case q == "":
		return 0
	case t == q:
		return 0
	case strings.HasPrefix(t, q):
		return 1
	}
	for _, w := range strings.FieldsFunc(t, func(r rune) bool { return r == ' ' || r == '/' || r == '-' || r == '_' }) {
		if strings.HasPrefix(w, q) {
			return 2
		}
	}
	if strings.Contains(t, q) {
		return 3
	}
	for _, w := range strings.Fields(q) {
		if !strings.Contains(t, w) {
			return -1
		}
	}
	return 4
}

// palCandidate is something the box could show, with the texts a query is
// matched against. A text's penalty makes a match on it count for less: a note
// found by its folder ranks under one found by its name.
type palCandidate struct {
	entry palEntry
	texts []palText
}

type palText struct {
	text    string
	penalty int
}

func noteCandidate(p string) palCandidate {
	return palCandidate{noteEntry(p, ""), []palText{{path.Base(p), 0}, {p, 2}}}
}

func noteEntry(p, why string) palEntry {
	sub := path.Dir(p)
	if sub == "." {
		sub = ""
	}
	if why != "" {
		if sub != "" {
			sub = why + " in " + sub
		} else {
			sub = why
		}
	}
	return palEntry{Kind: palNote, Title: path.Base(p), Sub: sub, Target: p}
}

func actionCandidates(actions []paletteAction) []palCandidate {
	out := make([]palCandidate, len(actions))
	for i, a := range actions {
		out[i] = palCandidate{
			palEntry{Kind: palAction, Title: a.Label, Sub: "Command", Target: a.Name},
			[]palText{{a.Label, 0}, {a.Keys, 2}},
		}
	}
	return out
}

func tagCandidates(tags []paletteTag) []palCandidate {
	out := make([]palCandidate, len(tags))
	for i, t := range tags {
		sub := fmt.Sprintf("Tag, %d notes", t.Notes)
		if t.Notes == 1 {
			sub = "Tag, 1 note"
		}
		out[i] = palCandidate{
			palEntry{Kind: palTag, Title: "#" + t.Name, Sub: sub, Target: t.Name},
			[]palText{{t.Name, 0}},
		}
	}
	return out
}

func settingCandidates(sections []paletteSection) []palCandidate {
	out := make([]palCandidate, len(sections))
	for i, s := range sections {
		out[i] = palCandidate{
			palEntry{Kind: palSetting, Title: s.Label, Sub: "Settings", Target: s.ID},
			[]palText{{s.Label, 0}, {"settings " + s.Label, 3}},
		}
	}
	return out
}

// rankPalette keeps the candidates the query matches, best first, and at most
// limit of them. Equal scores go by kind (notes, commands, settings, tags) and
// then by name (commands and settings keep their given order), so the order never depends on the order they were given in. An
// empty query keeps them all in the order given.
func rankPalette(q string, limit int, pool []palCandidate) []palEntry {
	type hit struct {
		e     palEntry
		score int
	}
	var hits []hit
	for _, c := range pool {
		best := -1
		for _, t := range c.texts {
			if s := paletteScore(q, t.text); s >= 0 && (best < 0 || s+t.penalty < best) {
				best = s + t.penalty
			}
		}
		if best >= 0 && (q != "" || len(c.texts) > 0) {
			hits = append(hits, hit{c.entry, best})
		}
	}
	if q != "" {
		sort.SliceStable(hits, func(i, j int) bool {
			a, b := hits[i], hits[j]
			switch {
			case a.score != b.score:
				return a.score < b.score
			case a.e.Kind != b.e.Kind:
				return a.e.Kind < b.e.Kind
			case a.e.Kind == palAction || a.e.Kind == palSetting:
				return false // the order they were registered in is the order of importance
			}
			return strings.ToLower(a.e.Title) < strings.ToLower(b.e.Title)
		})
	}
	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]palEntry, len(hits))
	for i, h := range hits {
		out[i] = h.e
	}
	return out
}

// buildPalette is the results for what is typed. A leading ">" lists commands
// only, "#" tags only, and "?" turns the rest into a question for the
// assistant.
func buildPalette(input string, in paletteInput) []palEntry {
	input = strings.TrimLeft(input, " ")
	mode, q := "", strings.TrimSpace(input)
	if input != "" && strings.ContainsRune(">#?", rune(input[0])) {
		mode, q = input[:1], strings.TrimSpace(input[1:])
	}

	switch mode {
	case "?":
		if q == "" {
			return []palEntry{{Kind: palAsk, Title: "Type a question for the assistant", Sub: "Enter sends it"}}
		}
		return []palEntry{{Kind: palAsk, Title: "Ask the assistant: " + q, Sub: "Enter sends it", Target: q}}
	case ">":
		return rankPalette(q, paletteListLimit, actionCandidates(in.Actions))
	case "#":
		q = strings.TrimPrefix(q, "#")
		return rankPalette(q, paletteListLimit, tagCandidates(in.Tags))
	}

	if q == "" {
		var out []palEntry
		for i, p := range in.Recent {
			if i == paletteRecent {
				break
			}
			out = append(out, noteEntry(p, "Recent"))
		}
		return append(out, rankPalette("", paletteMixedLimit-len(out), actionCandidates(in.Actions))...)
	}

	var pool []palCandidate
	for _, p := range in.Notes {
		pool = append(pool, noteCandidate(p))
	}
	pool = append(pool, actionCandidates(in.Actions)...)
	pool = append(pool, settingCandidates(in.Sections)...)
	pool = append(pool, tagCandidates(in.Tags)...)
	out := rankPalette(q, paletteMixedLimit, pool)

	// Notes whose text has the words but whose name does not come after
	// everything that matched by name.
	if in.ContentHits != nil && len(out) < paletteMixedLimit && len([]rune(q)) >= 3 {
		seen := map[string]bool{}
		for _, e := range out {
			if e.Kind == palNote {
				seen[e.Target] = true
			}
		}
		for _, p := range in.ContentHits(q) {
			if len(out) == paletteMixedLimit {
				break
			}
			if !seen[p] {
				out = append(out, noteEntry(p, "Mentioned"))
			}
		}
	}
	return out
}

func palIcon(k palKind) string {
	switch k {
	case palNote:
		return "atlasnotes-note-symbolic"
	case palAction:
		return "atlasnotes-command-symbolic"
	case palSetting:
		return "atlasnotes-settings-symbolic"
	case palTag:
		return "atlasnotes-tag-symbolic"
	}
	return "atlasnotes-sparkle-symbolic"
}

// paletteView is the open command box; there is at most one per window.
type paletteView struct {
	a       *App
	dialog  *adw.Dialog
	entry   *gtk.SearchEntry
	list    *gtk.ListBox
	scroll  *gtk.ScrolledWindow
	empty   *gtk.Label
	input   paletteInput
	results []palEntry
	sel     int
}

var openPalette = map[*App]*paletteView{}

// showPalette opens the command box, or closes it when it is already open, so
// Ctrl+P works as a toggle.
func (a *App) showPalette() {
	if v := openPalette[a]; v != nil {
		v.dialog.Close()
		return
	}
	v := &paletteView{a: a, input: a.paletteInput()}
	openPalette[a] = v

	v.dialog = adw.NewDialog()
	v.dialog.SetContentWidth(620)
	v.dialog.SetContentHeight(430)
	v.dialog.AddCSSClass("atlas-palette")

	v.entry = gtk.NewSearchEntry()
	v.entry.SetPlaceholderText("Find a note, run a command, or type ? to ask")
	v.entry.SetMarginTop(12)
	v.entry.SetMarginBottom(12)
	v.entry.SetMarginStart(12)
	v.entry.SetMarginEnd(12)
	v.entry.ConnectChanged(v.refresh)
	v.entry.ConnectActivate(v.activate)

	// Up, Down and Escape are taken before the field sees them: it would
	// otherwise clear itself on Escape, and does nothing useful with the arrows.
	keys := gtk.NewEventControllerKey()
	keys.SetPropagationPhase(gtk.PhaseCapture)
	keys.ConnectKeyPressed(func(keyval, _ uint, state gdk.ModifierType) bool {
		switch keyval {
		case gdk.KEY_Down:
			v.move(1)
		case gdk.KEY_Up:
			v.move(-1)
		case gdk.KEY_Escape:
			v.dialog.Close()
		default:
			return false
		}
		return true
	})
	v.entry.AddController(keys)

	v.list = gtk.NewListBox()
	v.list.SetSelectionMode(gtk.SelectionSingle)
	v.list.SetActivateOnSingleClick(true)
	v.list.AddCSSClass("atlas-palette-list")
	v.list.ConnectRowActivated(func(row *gtk.ListBoxRow) {
		v.sel = row.Index()
		v.activate()
	})

	v.empty = gtk.NewLabel("Nothing matches")
	v.empty.AddCSSClass("dim-label")
	v.empty.SetMarginTop(24)
	v.empty.SetVisible(false)

	inner := gtk.NewBox(gtk.OrientationVertical, 0)
	inner.Append(v.list)
	inner.Append(v.empty)
	v.scroll = gtk.NewScrolledWindow()
	v.scroll.SetChild(inner)
	v.scroll.SetVExpand(true)
	v.scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)

	body := gtk.NewBox(gtk.OrientationVertical, 0)
	body.Append(v.entry)
	body.Append(gtk.NewSeparator(gtk.OrientationHorizontal))
	body.Append(v.scroll)
	body.Append(gtk.NewSeparator(gtk.OrientationHorizontal))
	body.Append(paletteFooter())

	v.dialog.SetChild(body)
	v.dialog.SetFocus(v.entry)
	v.dialog.ConnectClosed(func() { delete(openPalette, a) })
	v.refresh()
	v.dialog.Present(a.win)
	v.entry.GrabFocus()
}

// paletteFooter is the line of key hints under the results.
func paletteFooter() *gtk.Box {
	foot := gtk.NewBox(gtk.OrientationHorizontal, 14)
	foot.AddCSSClass("atlas-palette-footer")
	foot.SetMarginTop(8)
	foot.SetMarginBottom(8)
	foot.SetMarginStart(14)
	foot.SetMarginEnd(14)
	for _, h := range [][2]string{{"Up Down", "move"}, {"Enter", "open"}, {"Esc", "close"}, {"> # ?", "commands, tags, ask"}} {
		item := gtk.NewBox(gtk.OrientationHorizontal, 6)
		for _, k := range strings.Fields(h[0]) {
			cap := gtk.NewLabel(k)
			cap.AddCSSClass("keycap")
			item.Append(cap)
		}
		what := gtk.NewLabel(h[1])
		what.AddCSSClass("dim-label")
		what.AddCSSClass("caption")
		item.Append(what)
		foot.Append(item)
	}
	return foot
}

// paletteInput gathers what the box searches, once, when it opens: the lists
// hardly change in the seconds it is open, and asking the store on every key
// would be wasted work. Only the notes' text is asked for as the query grows.
func (a *App) paletteInput() paletteInput {
	in := paletteInput{Sections: paletteSections}
	open := a.noteOpen()
	for _, act := range paletteActions {
		if !act.NeedsNote || open {
			in.Actions = append(in.Actions, act)
		}
	}
	if a.store == nil {
		return in
	}
	if notes, err := a.store.ListNotes(); err == nil {
		sort.Slice(notes, func(i, j int) bool { return notes[i].ModifiedAt.After(notes[j].ModifiedAt) })
		for _, n := range notes {
			in.Recent = append(in.Recent, n.Path)
		}
		in.Notes = append([]string(nil), in.Recent...)
	}
	if tags, err := a.store.Tags(); err == nil {
		for _, t := range tags {
			in.Tags = append(in.Tags, paletteTag{t.Tag, t.Notes})
		}
	}
	store := a.store
	in.ContentHits = func(q string) []string {
		hits, err := store.SearchContent(q, paletteMixedLimit)
		if err != nil {
			return nil
		}
		return hits
	}
	return in
}

// refresh redraws the rows for what is typed.
func (v *paletteView) refresh() {
	for c := v.list.FirstChild(); c != nil; c = v.list.FirstChild() {
		v.list.Remove(c)
	}
	v.results = buildPalette(v.entry.Text(), v.input)
	for _, e := range v.results {
		v.list.Append(v.row(e))
	}
	v.empty.SetVisible(len(v.results) == 0)
	v.sel = 0
	v.selectRow()
}

// row draws one result: its icon, its name, what it is or where it lives, and
// the shortcut of a command.
func (v *paletteView) row(e palEntry) *gtk.ListBoxRow {
	box := gtk.NewBox(gtk.OrientationHorizontal, 10)
	box.SetMarginTop(6)
	box.SetMarginBottom(6)
	box.SetMarginStart(12)
	box.SetMarginEnd(12)
	box.Append(gtk.NewImageFromIconName(palIcon(e.Kind)))

	title := gtk.NewLabel(e.Title)
	title.SetXAlign(0)
	title.SetEllipsize(pango.EllipsizeEnd)
	box.Append(title)

	sub := gtk.NewLabel(e.Sub)
	sub.SetXAlign(0)
	sub.SetHExpand(true)
	sub.SetEllipsize(pango.EllipsizeStart)
	sub.AddCSSClass("dim-label")
	sub.AddCSSClass("caption")
	box.Append(sub)

	if e.Kind == palAction {
		if label := v.a.actionShortcut(e.Target); label != "" {
			cap := gtk.NewLabel(label)
			cap.AddCSSClass("keycap")
			box.Append(cap)
		}
	}
	row := gtk.NewListBoxRow()
	row.SetChild(box)
	return row
}

// actionShortcut is how the first shortcut of an action is written on this
// keyboard, "Ctrl+N", or "" when it has none.
func (a *App) actionShortcut(name string) string {
	accels := a.adw.AccelsForAction("app." + name)
	if len(accels) == 0 {
		return ""
	}
	key, mods, ok := gtk.AcceleratorParse(accels[0])
	if !ok {
		return ""
	}
	return gtk.AcceleratorGetLabel(key, mods)
}

// move steps the selection, wrapping at both ends.
func (v *paletteView) move(delta int) {
	n := len(v.results)
	if n == 0 {
		return
	}
	v.sel = (v.sel + delta + n) % n
	v.selectRow()
}

// selectRow marks the chosen row and scrolls it into view. The row cannot take
// focus, as the field must keep it, so the scroller is moved by hand.
func (v *paletteView) selectRow() {
	row := v.list.RowAtIndex(v.sel)
	if row == nil {
		return
	}
	v.list.SelectRow(row)
	bounds, ok := row.ComputeBounds(v.list)
	if !ok {
		return
	}
	adj := v.scroll.VAdjustment()
	top, height := float64(bounds.Y()), float64(bounds.Height())
	switch {
	case top < adj.Value():
		adj.SetValue(top)
	case top+height > adj.Value()+adj.PageSize():
		adj.SetValue(top + height - adj.PageSize())
	}
}

// activate runs the chosen result. The box closes first, and the result runs
// once it has gone, so a dialog it opens (Settings, say) is not opened over
// this one, and a command that acts on the editor finds focus back there.
func (v *paletteView) activate() {
	if v.sel < 0 || v.sel >= len(v.results) {
		return
	}
	e := v.results[v.sel]
	if e.Target == "" {
		return
	}
	a := v.a
	v.dialog.Close()
	coreglib.IdleAdd(func() bool {
		a.runPalette(e)
		return false
	})
}

// runPalette does what a result stands for.
func (a *App) runPalette(e palEntry) {
	switch e.Kind {
	case palNote:
		a.openNote(e.Target)
	case palAction:
		a.adw.ActivateAction(e.Target, (*glib.Variant)(nil))
	case palSetting:
		a.showSettingsPage(e.Target)
	case palTag:
		a.showTagged(e.Target)
	case palAsk:
		a.askAssistant(e.Target)
	}
}

// askAssistant opens the assistant panel, if it was hidden, and sends it a
// question.
func (a *App) askAssistant(question string) {
	if a.sidebar == nil {
		a.toast("The assistant is not ready yet")
		return
	}
	if a.rightToggle != nil && !a.rightToggle.Active() {
		a.rightToggle.SetActive(true)
	}
	a.sidebar.Ask(question)
}
