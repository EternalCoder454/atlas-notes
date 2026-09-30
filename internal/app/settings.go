package app

import (
	"log"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-notes/internal/storage"
	"atlas-notes/internal/theme"
)

// Settings is one scrolling page of libadwaita preference groups, not a stack
// of pages. Everything is reachable by scrolling; the list of sections at the
// left only scrolls to one, and a search entry above narrows the rows. Every
// control takes effect the moment it changes. Nothing has a Save button, and
// nothing opens a second page to reach a setting.

// Section ids, in the order the page and the list show them.
const (
	sectionAppearance = "appearance"
	sectionNotes      = "notes"
	sectionAssistant  = "assistant"
	sectionUpdates    = "updates"
	sectionAbout      = "about"
)

// settingsSectionFor maps what the screenshot tooling passes (ATLAS_DEV_VIEW=
// settings=<name>) to a section id. The old page names still work, so scripts
// written for the three-page dialog keep reaching the same settings. An unknown
// or empty name is "", the top of the page.
func settingsSectionFor(name string) string {
	switch n := strings.ToLower(strings.TrimSpace(name)); n {
	case sectionAppearance, sectionNotes, sectionAssistant, sectionUpdates, sectionAbout:
		return n
	case "general":
		return sectionAppearance
	case "shortcuts", "prompts":
		return sectionAssistant
	case "app":
		return sectionUpdates
	}
	return ""
}

// matchesSearch reports whether every word of query is somewhere in fields,
// whatever the case. Words rather than the whole phrase, so "hover summary"
// finds "Show a 1-sentence AI summary when you hover a note". An empty query
// matches everything.
func matchesSearch(query string, fields ...string) bool {
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 {
		return true
	}
	hay := strings.ToLower(strings.Join(fields, "\n"))
	for _, w := range words {
		if !strings.Contains(hay, w) {
			return false
		}
	}
	return true
}

// sectionSlack is how far past a section's top the page may be scrolled while
// that section still counts as the one you are in. Without it the list's mark
// would only move once a heading had passed the very top edge, which reads as
// lagging.
const sectionSlack = 40

// settingsPageMargin is the page's margin. A section is scrolled to with this
// much above it, so the first one lands exactly where the page opens.
const settingsPageMargin = 12

// activeSection says which section the list should mark, given each section's
// top in page coordinates, which of them are showing (a search hides some),
// and where the scroller is. At the very bottom it is the last one showing:
// About is shorter than the viewport, so its top can never reach the top edge,
// and going by tops alone the list would never get to it. -1 means none are
// showing.
func activeSection(tops []float64, shown []bool, pos, page, upper float64) int {
	first, last, found := -1, -1, -1
	for i, top := range tops {
		if i >= len(shown) || !shown[i] {
			continue
		}
		if first < 0 {
			first = i
		}
		last = i
		if top <= pos+sectionSlack {
			found = i
		}
	}
	if last >= 0 && upper > page && pos+page >= upper-1 {
		return last
	}
	if found < 0 {
		return first
	}
	return found
}

// clampScroll keeps a scroll target inside what the scroller can reach.
func clampScroll(y, upper, page float64) float64 {
	if limit := upper - page; y > limit {
		y = limit
	}
	if y < 0 {
		y = 0
	}
	return y
}

// debouncer runs fn once, delay milliseconds after the last Trigger. It is how
// a control that changes many times in a row (typing, a slider) becomes one
// change to save. Flush runs a pending fn now, which is what closing the dialog
// needs: nothing may be lost to a timer that never gets to fire.
//
// The scheduler is a field so the logic can be tested without a main loop.
type debouncer struct {
	delay   uint
	fn      func()
	after   func(ms uint, f func()) (cancel func())
	cancel  func()
	pending bool
}

func newDebouncer(delay uint, fn func()) *debouncer {
	return &debouncer{delay: delay, fn: fn, after: glibAfter}
}

// glibAfter runs f once on the main loop after ms milliseconds.
func glibAfter(ms uint, f func()) func() {
	h := coreglib.TimeoutAdd(ms, func() bool {
		f()
		return false // once; GLib removes the source itself
	})
	return func() { coreglib.SourceRemove(h) }
}

// Trigger says something changed, and starts the wait again.
func (d *debouncer) Trigger() {
	if d.cancel != nil {
		d.cancel()
	}
	d.pending = true
	d.cancel = d.after(d.delay, d.fire)
}

// fire is the timer's end. Its source has finished, so it must not be
// cancelled afterwards; GLib complains about removing a source twice.
func (d *debouncer) fire() {
	d.cancel = nil
	if !d.pending {
		return
	}
	d.pending = false
	d.fn()
}

// Flush runs the pending change now, if there is one.
func (d *debouncer) Flush() {
	if !d.pending {
		return
	}
	if d.cancel != nil {
		d.cancel()
		d.cancel = nil
	}
	d.pending = false
	d.fn()
}

// noteFormats are the note-file combo's entries, in the order they appear.
var noteFormats = []storage.Compression{
	storage.CompressionNone, storage.CompressionZstd, storage.CompressionGzip, storage.CompressionXZ,
}

// noteFormatLabels are what noteFormats are called in the combo.
var noteFormatLabels = []string{
	"Plain Markdown (.md)",
	"Zstandard (.md.zst)",
	"Gzip (.md.gz)",
	"XZ (.md.xz)",
}

// noteFormatIndex is a format's position in the combo. A format this build does
// not list is shown as plain Markdown, the way the vault treats one it does
// not know.
func noteFormatIndex(c storage.Compression) int {
	for i, f := range noteFormats {
		if f == c {
			return i
		}
	}
	return 0
}

// fontRenderingModes are the combo entries, in the order they appear.
var fontRenderingModes = []string{
	storage.FontRenderingAuto, storage.FontRenderingCrisp, storage.FontRenderingSmooth,
}

// fontModeIndex maps a stored font-rendering mode to its combo position.
func fontModeIndex(mode string) int {
	for i, m := range fontRenderingModes {
		if m == mode {
			return i
		}
	}
	return 0
}

// transparencyLabels name storage.TransparencyLevels, in the same order.
var transparencyLabels = []string{"Off", "Subtle", "Medium", "Strong"}

// transparencyIndex is a level's position in the combo; an unknown level is
// off, as NormalizeTransparency has it.
func transparencyIndex(level string) int {
	level = storage.NormalizeTransparency(level)
	for i, l := range storage.TransparencyLevels {
		if l == level {
			return i
		}
	}
	return 0
}

// modeLabels are the prompt shortcut modes, in the order the combo lists them.
var modeLabels = []string{"Show result", "Replace note", "Sort checklist"}

func modeIndex(mode string) uint {
	switch mode {
	case storage.ActionModeReplace:
		return 1
	case storage.ActionModeSort:
		return 2
	default:
		return 0
	}
}

func modeFromIndex(i uint) string {
	switch i {
	case 1:
		return storage.ActionModeReplace
	case 2:
		return storage.ActionModeSort
	default:
		return storage.ActionModeShow
	}
}

// themeDescription is the line under the picker: which theme is chosen, or
// what following the desktop means.
func themeDescription(id string) string {
	if t, ok := theme.ByID(id); ok {
		return t.Name + ": " + t.Summary
	}
	return "Following the desktop's light and dark setting. Choose a theme to set it here instead."
}

// updateStatusLine is what the update status shows before anything has been
// tried: the version, and the channel it would update from.
func updateStatusLine(ver, channel string) string {
	name := "Release"
	if channel == storage.ChannelBeta {
		name = "Beta"
	}
	return "Version " + ver + " on the " + name + " channel"
}

// The waits before typing becomes a change, and before changes become a file.
// Typing gets longer than saving: a person mid-sentence should not have the
// assistant's prompt swapped under them, but the file only costs a write.
const (
	settingsTypingDelay = 600
	settingsSaveDelay   = 300
)

// openSettings holds each App's open Settings dialog, so opening it twice
// brings the first forward rather than stacking a second that edits the same
// config from stale widgets.
var openSettings = map[*App]*settingsView{}

// settingsItem is one thing on the page that a search can hide: its widget and
// the words it answers to.
type settingsItem struct {
	w    gtk.Widgetter
	text func() string
}

// settingsGroup is one AdwPreferencesGroup and what a search sees in it.
type settingsGroup struct {
	box   *adw.PreferencesGroup
	title string
	desc  string
	items []settingsItem
}

// settingsSection is one heading and its groups, and its entry in the list.
type settingsSection struct {
	id, title string
	box       *gtk.Box
	groups    []*settingsGroup
	navRow    *gtk.ListBoxRow
}

func (s *settingsSection) addGroup(title, desc string) *settingsGroup {
	g := &settingsGroup{box: adw.NewPreferencesGroup(), title: title, desc: desc}
	if title != "" {
		g.box.SetTitle(title)
	}
	if desc != "" {
		g.box.SetDescription(desc)
	}
	s.box.Append(g.box)
	s.groups = append(s.groups, g)
	return g
}

// addRow adds a row a search matches by its title and subtitle.
func (g *settingsGroup) addRow(w gtk.Widgetter, title, subtitle string) {
	g.addItem(w, func() string { return title + "\n" + subtitle })
}

// addItem adds anything to the group; text says what a search may find it by.
func (g *settingsGroup) addItem(w gtk.Widgetter, text func() string) {
	g.box.Add(w)
	g.items = append(g.items, settingsItem{w: w, text: text})
}

// shortcutCard is one prompt shortcut, all of it editable in place.
type shortcutCard struct {
	root   *gtk.Box
	name   *gtk.Entry
	mode   *gtk.DropDown
	prompt *gtk.TextView
}

// settingsView is the open dialog and everything it edits.
type settingsView struct {
	a      *App
	dialog *adw.Dialog

	search  *gtk.SearchEntry
	nav     *gtk.ListBox
	scroll  *gtk.ScrolledWindow
	content *gtk.Box
	empty   *gtk.Label

	sections []*settingsSection

	// save writes the config; typing applies the shortcuts and sysTyping the
	// system prompt, each after the person stops typing. The two are separate
	// so a pause in one does not cut short the other.
	save      *debouncer
	typing    *debouncer
	sysTyping *debouncer

	// commits apply what an entry row holds but has not yet applied, for the
	// close: a name typed and never confirmed is still what was meant.
	commits []func()

	cards    []*shortcutCard
	cardList *gtk.Box

	// scrolling is true while a click's scroll is under way, so the list's mark
	// does not flicker through every section on the way.
	scrolling bool
	anim      *adw.TimedAnimation
}

// showSettings opens the settings dialog.
func (a *App) showSettings() { a.showSettingsPage("") }

// showSettingsPage opens it scrolled to a named section. Everything but the
// screenshot tooling passes an empty string and gets the top.
func (a *App) showSettingsPage(page string) {
	want := settingsSectionFor(page)
	if v := openSettings[a]; v != nil {
		v.dialog.Present(a.win)
		v.goTo(want, true)
		return
	}

	v := &settingsView{a: a}
	openSettings[a] = v
	v.save = newDebouncer(settingsSaveDelay, func() {
		if err := storage.SaveConfig(a.cfg); err != nil {
			log.Printf("atlas-notes: save settings: %v", err)
		}
	})
	v.typing = newDebouncer(settingsTypingDelay, v.syncActions)

	v.dialog = adw.NewDialog()
	v.dialog.SetTitle("Settings")
	v.dialog.SetContentWidth(680)
	v.dialog.SetContentHeight(680)

	v.content = gtk.NewBox(gtk.OrientationVertical, 30)
	v.content.SetMarginTop(settingsPageMargin)
	v.content.SetMarginBottom(settingsPageMargin + 8)
	v.content.SetMarginStart(settingsPageMargin + 4)
	v.content.SetMarginEnd(settingsPageMargin + 4)
	v.content.AddCSSClass("atlas-settings-page")

	v.buildAppearance()
	v.buildNotes()
	v.buildAssistant()
	v.buildUpdates()
	v.buildAbout()

	v.empty = gtk.NewLabel("No setting matches that search.")
	v.empty.AddCSSClass("dim-label")
	v.empty.SetMarginTop(24)
	v.empty.SetVisible(false)
	v.content.Append(v.empty)

	v.scroll = pageScroll(v.content)

	navBox := v.buildNav()

	v.search = gtk.NewSearchEntry()
	v.search.SetPlaceholderText("Search settings")
	v.search.SetHExpand(true)
	v.search.ConnectSearchChanged(func() { v.filter(v.search.Text()) })
	// Escape clears a search first and closes the dialog second. The entry
	// takes the key otherwise, and a dialog that will not close on Escape
	// while the search is empty is a trap.
	v.search.ConnectStopSearch(func() {
		if v.search.Text() != "" {
			v.search.SetText("")
			return
		}
		v.dialog.Close()
	})
	searchBar := gtk.NewBox(gtk.OrientationHorizontal, 0)
	searchBar.SetMarginTop(8)
	searchBar.SetMarginBottom(8)
	searchBar.SetMarginStart(12)
	searchBar.SetMarginEnd(12)
	searchBar.Append(v.search)

	body := gtk.NewBox(gtk.OrientationHorizontal, 0)
	body.Append(navBox)
	body.Append(v.scroll)

	main := gtk.NewBox(gtk.OrientationVertical, 0)
	main.Append(searchBar)
	main.Append(gtk.NewSeparator(gtk.OrientationHorizontal))
	main.Append(body)

	tv := adw.NewToolbarView()
	tv.AddTopBar(adw.NewHeaderBar())
	tv.SetContent(main)
	// A breakpoint needs a floor to shrink to. The page's own minimum would do
	// with the list showing, but not once the list has gone.
	tv.SetSizeRequest(340, -1)
	v.dialog.SetChild(tv)

	// Under this width the dialog is a phone-sized sheet, and the list would
	// take a third of it. Everything is still there to scroll to.
	bp := adw.NewBreakpoint(adw.NewBreakpointConditionLength(
		adw.BreakpointConditionMaxWidth, 640, adw.LengthUnitPx))
	bp.ConnectApply(func() { navBox.SetVisible(false) })
	bp.ConnectUnapply(func() { navBox.SetVisible(true) })
	v.dialog.AddBreakpoint(bp)

	v.scroll.VAdjustment().ConnectValueChanged(func() {
		if !v.scrolling {
			v.syncNav()
		}
	})

	v.dialog.ConnectClosed(func() {
		for _, commit := range v.commits {
			commit()
		}
		v.sysTyping.Flush()
		v.typing.Flush()
		v.save.Flush() // last: the flushes above may each have changed something
		delete(openSettings, a)
	})

	v.dialog.SetFocus(v.search)
	v.dialog.Present(a.win)
	if len(v.sections) > 0 {
		v.nav.SelectRow(v.sections[0].navRow)
	}
	if want != "" && want != sectionAppearance {
		v.scrollWhenReady(want)
	}
}

// changed says a setting has changed and the config wants writing.
func (v *settingsView) changed() { v.save.Trigger() }

// newSection starts a section: its heading, and a place for its groups.
func (v *settingsView) newSection(id, title string) *settingsSection {
	s := &settingsSection{id: id, title: title}
	s.box = gtk.NewBox(gtk.OrientationVertical, 18)
	heading := gtk.NewLabel(title)
	heading.SetXAlign(0)
	heading.AddCSSClass("title-3")
	s.box.Append(heading)
	v.content.Append(s.box)
	v.sections = append(v.sections, s)
	return s
}

// buildNav is the list of sections down the left.
func (v *settingsView) buildNav() *gtk.Box {
	v.nav = gtk.NewListBox()
	v.nav.SetSelectionMode(gtk.SelectionSingle)
	v.nav.AddCSSClass("navigation-sidebar")
	v.nav.AddCSSClass("atlas-settings-nav")
	for _, s := range v.sections {
		label := gtk.NewLabel(s.title)
		label.SetXAlign(0)
		row := gtk.NewListBoxRow()
		row.SetChild(label)
		v.nav.Append(row)
		s.navRow = row
	}
	// Activated, not selected: the list's own mark moves as the page scrolls,
	// and a selection that scrolls the page would answer that by scrolling it.
	v.nav.ConnectRowActivated(func(row *gtk.ListBoxRow) {
		if i := row.Index(); i >= 0 && i < len(v.sections) {
			v.goTo(v.sections[i].id, true)
		}
	})

	box := gtk.NewBox(gtk.OrientationHorizontal, 0)
	box.SetSizeRequest(170, -1)
	v.nav.SetHExpand(true)
	box.Append(v.nav)
	box.Append(gtk.NewSeparator(gtk.OrientationVertical))
	return box
}

// section finds one by id.
func (v *settingsView) section(id string) *settingsSection {
	for _, s := range v.sections {
		if s.id == id {
			return s
		}
	}
	return nil
}

// goTo scrolls to a section, or to the top for an unknown or empty id.
func (v *settingsView) goTo(id string, animate bool) {
	s := v.section(id)
	if s == nil {
		if v.scroll != nil {
			v.scrollTo(0, animate)
		}
		return
	}
	rect, ok := s.box.ComputeBounds(v.content)
	if !ok {
		return
	}
	adj := v.scroll.VAdjustment()
	v.scrollTo(clampScroll(float64(rect.Y())-settingsPageMargin, adj.Upper(), adj.PageSize()), animate)
	if s.navRow != nil {
		v.nav.SelectRow(s.navRow)
	}
}

// scrollTo moves the page to y, gently, unless the person has asked their
// desktop for no animation, which adw honours by jumping.
func (v *settingsView) scrollTo(y float64, animate bool) {
	adj := v.scroll.VAdjustment()
	if v.anim != nil && v.scrolling {
		v.anim.Skip() // an earlier one still running is finished, not left fighting this one
	}
	v.anim = nil
	from := adj.Value()
	if !animate || from == y {
		v.scrolling = false
		adj.SetValue(y)
		return
	}
	v.scrolling = true
	target := adw.NewCallbackAnimationTarget(func(x float64) { adj.SetValue(x) })
	anim := adw.NewTimedAnimation(v.scroll, from, y, 220, target)
	anim.SetEasing(adw.EaseOutCubic)
	anim.ConnectDone(func() {
		v.scrolling = false
		v.syncNav()
	})
	v.anim = anim
	anim.Play()
}

// scrollWhenReady is goTo for a dialog that has only just been presented,
// where nothing has a size yet. It waits for the page to be laid out, and
// scrolls again a moment later because the text fields settle after the rest.
func (v *settingsView) scrollWhenReady(id string) {
	tries := 0
	coreglib.TimeoutAdd(60, func() bool {
		tries++
		adj := v.scroll.VAdjustment()
		if adj.Upper() <= adj.PageSize() && tries < 40 {
			return true
		}
		v.goTo(id, false)
		coreglib.TimeoutAdd(300, func() bool {
			v.goTo(id, false)
			return false
		})
		return false
	})
}

// syncNav marks in the list the section the page is scrolled to.
func (v *settingsView) syncNav() {
	tops := make([]float64, len(v.sections))
	shown := make([]bool, len(v.sections))
	for i, s := range v.sections {
		shown[i] = s.box.Visible()
		if r, ok := s.box.ComputeBounds(v.content); ok {
			tops[i] = float64(r.Y()) - settingsPageMargin
		}
	}
	adj := v.scroll.VAdjustment()
	if i := activeSection(tops, shown, adj.Value(), adj.PageSize(), adj.Upper()); i >= 0 {
		if row := v.sections[i].navRow; row != nil && !row.IsSelected() {
			v.nav.SelectRow(row)
		}
	}
}

// filter hides the rows a search rules out, then the groups and sections it
// has emptied. A group's title and its section's count as words of every row in
// them, so searching "assistant" shows the whole of that section.
func (v *settingsView) filter(query string) {
	anyShown := false
	for _, s := range v.sections {
		sectionShown := false
		for _, g := range s.groups {
			groupShown := false
			for _, it := range g.items {
				ok := matchesSearch(query, s.title, g.title, g.desc, it.text())
				gtk.BaseWidget(it.w).SetVisible(ok)
				groupShown = groupShown || ok
			}
			g.box.SetVisible(groupShown)
			sectionShown = sectionShown || groupShown
		}
		s.box.SetVisible(sectionShown)
		s.navRow.SetVisible(sectionShown)
		anyShown = anyShown || sectionShown
	}
	v.empty.SetVisible(!anyShown)
	v.scroll.VAdjustment().SetValue(0)
	// The list's mark follows the page, which has just changed shape.
	coreglib.IdleAdd(func() bool {
		v.syncNav()
		return false
	})
}

// ---- Appearance -------------------------------------------------------------

func (v *settingsView) buildAppearance() {
	a := v.a
	s := v.newSection(sectionAppearance, "Appearance")

	v.themePicker(s.addGroup("Theme", ""))

	g := s.addGroup("", "")

	// True translucency, where the desktop can show it. Elsewhere the choice
	// stays, greyed, with the reason beside it, and the saved level is kept for
	// a desktop that can.
	glassSub := "Lets the desktop show through the window's frame and page. Text stays solid."
	ok, why := TransparencyAvailable()
	if !ok {
		glassSub = why
	}
	glass := comboRow("Window transparency", glassSub, transparencyLabels,
		transparencyIndex(a.cfg.WindowTransparency), func(i int) {
			if i < 0 || i >= len(storage.TransparencyLevels) {
				return
			}
			a.cfg.WindowTransparency = storage.TransparencyLevels[i]
			a.applyTransparency()
			v.changed()
		})
	glass.SetSensitive(ok)
	g.addRow(glass, "Window transparency", glassSub)

	// Text rendering: the right choice depends on the screen, so it is a
	// setting rather than a guess. See internal/app/fonts.go.
	fontSub := "Automatic picks per screen. Takes effect on the next launch."
	fonts := comboRow("Text rendering", fontSub, []string{
		"Automatic (recommended)",
		"Crisp (best on 1080p)",
		"Smooth (best on HiDPI)",
	}, fontModeIndex(a.cfg.FontRendering), func(i int) {
		if i < 0 || i >= len(fontRenderingModes) {
			return
		}
		a.cfg.FontRendering = fontRenderingModes[i]
		v.changed()
	})
	g.addRow(fonts, "Text rendering", fontSub+" crisp smooth font")

	barSub := "Shows what the editor understands above each note. " +
		"Every command it offers also has a keyboard shortcut."
	bar := switchRow("Formatting toolbar", barSub, a.cfg.ShowFormatBar, func(on bool) {
		a.cfg.ShowFormatBar = on
		a.applyFormatBarVisibility()
		v.changed()
	})
	g.addRow(bar, "Formatting toolbar", barSub)
}

// themePicker is the ten circles, as in Atlas Monitor: a colour is chosen by
// looking at it, and a list of names makes you pick one to find out what it is.
// Each circle is split between the window's background and its accent, since a
// theme is those two decisions.
//
// There is no circle for following the desktop. It is where the app starts and
// it is not a palette, so it is a quiet button under them, marked while it is
// what is chosen.
func (v *settingsView) themePicker(g *settingsGroup) {
	a := v.a
	g.box.SetDescription(themeDescription(a.cfg.Theme))

	// Two rows of five rather than one of ten: ten circles and their names are
	// wider than the dialog can be at its narrowest, and a FlowBox wraps to
	// fewer per line there instead of clipping.
	flow := gtk.NewFlowBox()
	flow.SetSelectionMode(gtk.SelectionNone)
	flow.SetActivateOnSingleClick(false)
	flow.SetMaxChildrenPerLine(5)
	flow.SetMinChildrenPerLine(1)
	flow.SetHomogeneous(true)
	flow.SetColumnSpacing(18)
	flow.SetRowSpacing(12)
	flow.SetHAlign(gtk.AlignCenter)
	flow.SetMarginTop(6)
	flow.SetMarginBottom(6)

	follow := gtk.NewToggleButton()
	follow.SetLabel("Follow the desktop")
	follow.SetHAlign(gtk.AlignCenter)
	follow.AddCSSClass("flat")
	follow.AddCSSClass("atlas-quiet-button")
	follow.SetTooltipText("Use the desktop's light or dark setting, as Atlas Notes did before themes")

	// Every circle is held so that choosing one can clear the others. A
	// GtkCheckButton group would do that itself, but its indicator cannot be
	// styled into a disc.
	var swatches []*gtk.ToggleButton

	// syncing guards the toggled signal that setting Active fires: clearing the
	// others would otherwise re-enter through their own handlers.
	syncing := false
	sync := func() {
		syncing = true
		following := theme.IsFollowing(a.cfg.Theme)
		for i, b := range swatches {
			b.SetActive(!following && theme.Themes[i].ID == a.cfg.Theme)
		}
		follow.SetActive(following)
		syncing = false
		g.box.SetDescription(themeDescription(a.cfg.Theme))
	}
	choose := func(id string) {
		if a.cfg.Theme == id {
			return
		}
		a.cfg.Theme = id
		a.applyTheme()
		sync()
		v.changed()
	}

	words := []string{"theme colour color palette dark light"}
	for _, t := range theme.Themes {
		t := t
		words = append(words, t.Name, t.Summary)

		sw := gtk.NewToggleButton()
		// Centred, not filled: a button fills its cell, and the cell is as wide
		// as the name under it, so every longer name drew an oval.
		sw.SetHAlign(gtk.AlignCenter)
		sw.SetVAlign(gtk.AlignCenter)
		sw.AddCSSClass("atlas-swatch")
		sw.AddCSSClass(theme.SwatchClass(t.ID))
		sw.SetTooltipText(t.Name + ": " + t.Summary)
		// The button has no label, so without this a screen reader would
		// announce ten unnamed toggles.
		sw.UpdateProperty([]gtk.AccessibleProperty{gtk.AccessiblePropertyLabel},
			[]coreglib.Value{*coreglib.NewValue(t.Name)})

		name := gtk.NewLabel(t.Name)
		name.AddCSSClass("caption")

		cell := gtk.NewBox(gtk.OrientationVertical, 6)
		cell.SetHAlign(gtk.AlignCenter)
		cell.Append(sw)
		cell.Append(name)
		flow.Append(cell)
		// The FlowBox wraps each cell in a child that takes focus, which would
		// put two tab stops in front of every circle. Only the button should.
		if child := flow.ChildAtIndex(len(swatches)); child != nil {
			child.SetFocusable(false)
		}

		sw.ConnectToggled(func() {
			if syncing {
				return
			}
			if !sw.Active() {
				// Clicking the chosen one again would turn it off and leave
				// nothing chosen. It stays chosen.
				sw.SetActive(true)
				return
			}
			choose(t.ID)
		})
		swatches = append(swatches, sw)
	}
	follow.ConnectToggled(func() {
		if syncing {
			return
		}
		if !follow.Active() {
			follow.SetActive(true)
			return
		}
		choose(theme.Follow)
	})

	// A box of its own, because a group puts its rows above its other content
	// and a bare widget added to it would land wherever that leaves it.
	box := gtk.NewBox(gtk.OrientationVertical, 4)
	box.Append(flow)
	box.Append(follow)
	joined := strings.Join(words, "\n")
	g.addItem(box, func() string { return joined })
	sync()
}

// ---- Notes ------------------------------------------------------------------

func (v *settingsView) buildNotes() {
	a := v.a
	s := v.newSection(sectionNotes, "Notes")
	g := s.addGroup("", "")

	// The format belongs to the vault, not to this machine: it is saved in the
	// vault, so every device that syncs it writes notes the same way.
	formatSub := "Plain Markdown opens in any app, and is what most sync " +
		"and backup tools expect. The others take less space. Changing this converts " +
		"every note in the vault; protected notes stay encrypted either way."
	selected := 0
	if a.store != nil {
		selected = noteFormatIndex(a.store.Compression())
	}
	format := comboRow("Note files", formatSub, noteFormatLabels, selected, func(i int) {
		if a.store != nil && i >= 0 && i < len(noteFormats) {
			a.changeNoteFormat(noteFormats[i])
		}
	})
	format.SetSensitive(a.store != nil)
	g.addRow(format, "Note files", formatSub+" markdown compression zstd gzip xz")

	remindSub := "Notify me about checklist items that are due today or overdue"
	remind := switchRow("Reminders", remindSub, a.cfg.DueReminders, func(on bool) {
		a.cfg.DueReminders = on
		v.changed()
	})
	g.addRow(remind, "Reminders", remindSub)

	hoverSub := "Show a 1-sentence AI summary when you hover a note"
	hover := switchRow("Hover previews", hoverSub, a.cfg.EnableTreeSummaries, func(on bool) {
		a.cfg.EnableTreeSummaries = on
		if a.tree != nil {
			a.tree.SetSummariesEnabled(on)
		}
		v.changed()
	})
	g.addRow(hover, "Hover previews", hoverSub)

	// The one setting here that needs a dialog of its own: it asks for the old
	// password, then the new one, and nothing on this page could hold that.
	lockSub := "Right-click a note or folder in the vault panel to protect it. " +
		"One password covers everything you protect."
	lock := adw.NewActionRow()
	lock.SetTitle("Password protection")
	lock.SetSubtitle(lockSub)
	lock.SetUseMarkup(false)
	change := gtk.NewButtonWithLabel("Change Password…")
	change.SetVAlign(gtk.AlignCenter)
	change.SetSensitive(a.store != nil && a.store.HasPassword())
	if !change.Sensitive() {
		change.SetTooltipText("No password has been set yet.")
	}
	change.ConnectClicked(a.promptChangePassword)
	lock.AddSuffix(change)
	g.addRow(lock, "Password protection", lockSub+" change password")
}

// ---- Assistant --------------------------------------------------------------

func (v *settingsView) buildAssistant() {
	a := v.a
	s := v.newSection(sectionAssistant, "Assistant")

	names := s.addGroup("", "What the assistant is called in the side panel, and the Ollama model that answers.")
	name := v.entryRow("Assistant name", a.cfg.AssistantName, func(text string) string {
		text = strings.TrimSpace(text)
		if text == "" {
			text = storage.DefaultAssistantName
		}
		a.cfg.AssistantName = text
		if a.sidebar != nil {
			a.sidebar.SetName(text)
		}
		v.changed()
		return text
	})
	names.addRow(name, "Assistant name", "")
	model := v.entryRow("Ollama model", a.cfg.Model, func(text string) string {
		text = strings.TrimSpace(text)
		if text == "" {
			text = storage.DefaultModel
		}
		a.cfg.Model = text
		if a.ai != nil {
			a.ai.Model = text
		}
		if a.sidebar != nil {
			a.sidebar.SetModel(text)
		}
		v.changed()
		return text
	})
	names.addRow(model, "Ollama model", "")

	v.systemPrompt(s.addGroup("System prompt", "The standing instructions the assistant is given with every request."))
	v.shortcuts(s.addGroup("Prompt shortcuts", "{content} = note text   ·   {items} = checklist (Sort mode)"))
}

// systemPrompt is the prompt's field and the way back to the default.
func (v *settingsView) systemPrompt(g *settingsGroup) {
	a := v.a
	field, frame := multilineField(a.cfg.SystemPrompt, 6)
	frame.SetMarginTop(12)
	frame.SetMarginBottom(12)
	frame.SetMarginStart(12)
	frame.SetMarginEnd(12)
	// In a row of its own, so it sits above the reset button; a bare widget
	// added to a group goes below every row in it.
	holder := gtk.NewListBoxRow()
	holder.SetActivatable(false)
	holder.SetSelectable(false)
	holder.SetChild(frame)
	g.addItem(holder, func() string { return "system prompt instructions " + textViewText(field) })

	reset := adw.NewButtonRow()
	reset.SetTitle("Reset to default")
	g.addRow(reset, "Reset to default", "system prompt")
	dimReset := func() {
		reset.SetSensitive(strings.TrimSpace(textViewText(field)) != storage.DefaultSystemPrompt)
	}
	dimReset()

	v.sysTyping = newDebouncer(settingsTypingDelay, func() {
		a.cfg.SystemPrompt = strings.TrimSpace(textViewText(field))
		if a.ai != nil {
			a.ai.SystemPrompt = a.cfg.SystemPrompt
		}
		v.changed()
	})
	field.Buffer().ConnectChanged(func() {
		dimReset()
		v.sysTyping.Trigger()
	})
	flushOnLeave(field, v.sysTyping)
	reset.ConnectActivated(func() {
		field.Buffer().SetText(storage.DefaultSystemPrompt)
		v.sysTyping.Flush()
	})
}

// shortcuts lists every prompt shortcut as a card in the page itself, with the
// way to add one.
func (v *settingsView) shortcuts(g *settingsGroup) {
	v.cardList = gtk.NewBox(gtk.OrientationVertical, 10)
	for _, act := range v.a.cfg.Actions {
		v.addCard(act)
	}

	addContent := adw.NewButtonContent()
	addContent.SetIconName("atlasnotes-add-symbolic")
	addContent.SetLabel("Add shortcut")
	add := gtk.NewButton()
	add.SetChild(addContent)
	add.SetHAlign(gtk.AlignStart)
	add.ConnectClicked(func() {
		c := v.addCard(storage.AIAction{Name: "New shortcut", Mode: storage.ActionModeShow, Prompt: "{content}"})
		v.syncActions()
		c.name.GrabFocus()
		c.name.SelectRegion(0, -1)
	})

	// One box for the cards and the button, so their order is not the group's
	// to decide.
	holder := gtk.NewBox(gtk.OrientationVertical, 12)
	holder.Append(v.cardList)
	holder.Append(add)
	g.addItem(holder, func() string {
		var b strings.Builder
		b.WriteString("shortcuts actions prompts add")
		for _, c := range v.cards {
			b.WriteString("\n" + c.name.Text() + "\n" + textViewText(c.prompt))
		}
		return b.String()
	})
}

// addCard builds a shortcut's card and puts it at the end of the list. Its
// name is applied after a pause in typing, on Enter, or on leaving it; there is
// no apply button on a plain entry to wait for.
func (v *settingsView) addCard(act storage.AIAction) *shortcutCard {
	c := &shortcutCard{}
	c.root = gtk.NewBox(gtk.OrientationVertical, 8)
	c.root.AddCSSClass("card")
	c.root.AddCSSClass("atlas-shortcut")

	top := gtk.NewBox(gtk.OrientationHorizontal, 8)
	c.name = gtk.NewEntry()
	c.name.SetHExpand(true)
	c.name.SetPlaceholderText("Shortcut name")
	c.name.SetText(act.Name)
	c.mode = gtk.NewDropDownFromStrings(modeLabels)
	c.mode.SetSelected(modeIndex(act.Mode))
	c.mode.SetVAlign(gtk.AlignCenter)
	remove := gtk.NewButtonFromIconName("atlasnotes-trash-symbolic")
	remove.AddCSSClass("flat")
	remove.SetVAlign(gtk.AlignCenter)
	remove.SetTooltipText("Remove shortcut")
	top.Append(c.name)
	top.Append(c.mode)
	top.Append(remove)
	c.root.Append(top)

	var frame *gtk.Frame
	c.prompt, frame = multilineField(act.Prompt, 3)
	c.root.Append(frame)

	c.name.ConnectChanged(v.typing.Trigger)
	c.name.ConnectActivate(v.typing.Flush)
	flushOnLeave(c.name, v.typing)
	c.prompt.Buffer().ConnectChanged(v.typing.Trigger)
	flushOnLeave(c.prompt, v.typing)
	c.mode.NotifyProperty("selected", v.syncActions)
	remove.ConnectClicked(func() {
		v.cardList.Remove(c.root)
		kept := v.cards[:0]
		for _, x := range v.cards {
			if x != c {
				kept = append(kept, x)
			}
		}
		v.cards = kept
		v.syncActions()
	})

	v.cards = append(v.cards, c)
	v.cardList.Append(c.root)
	return c
}

// syncActions makes the config's shortcuts what the cards say. A card with no
// name is left out, as it was when Save read them, but it stays on the page so
// its name can be typed.
func (v *settingsView) syncActions() {
	var actions []storage.AIAction
	for _, c := range v.cards {
		name := strings.TrimSpace(c.name.Text())
		if name == "" {
			continue
		}
		actions = append(actions, storage.AIAction{
			Name:   name,
			Prompt: textViewText(c.prompt),
			Mode:   modeFromIndex(c.mode.Selected()),
		})
	}
	v.a.cfg.Actions = actions
	if v.a.sidebar != nil {
		v.a.sidebar.SetActions(actions)
	}
	v.changed()
}

// ---- Updates ----------------------------------------------------------------

func (v *settingsView) buildUpdates() {
	a := v.a
	s := v.newSection(sectionUpdates, "Updates")
	g := s.addGroup("", "Automatically check and install updates from GitHub.")

	status := adw.NewActionRow()
	status.SetTitle("Status")
	status.SetSubtitle(updateStatusLine(version, a.cfg.UpdateChannel))
	status.SetSubtitleSelectable(true)
	status.SetUseMarkup(false) // the text is a build's output, which may hold anything

	selected := 0
	if a.cfg.UpdateChannel == storage.ChannelBeta {
		selected = 1
	}
	chanSub := "Release is the stable main branch. Beta is the beta branch: the newest, and it may be unstable."
	channel := comboRow("Update channel", chanSub, []string{"Release", "Beta"}, selected, func(i int) {
		a.cfg.UpdateChannel = channelFromIndex(uint(i))
		status.SetSubtitle(updateStatusLine(version, a.cfg.UpdateChannel))
		v.changed()
	})
	g.addRow(channel, "Update channel", chanSub)

	checkSub := "Asks GitHub whether a newer version has been published. " +
		"Nothing about you or your notes is sent."
	check := switchRow("Check for updates when Atlas Notes starts", checkSub, a.cfg.CheckUpdates, func(on bool) {
		a.cfg.CheckUpdates = on
		v.changed()
	})
	g.addRow(check, "Check for updates when Atlas Notes starts", checkSub)

	g.addRow(status, "Status", "version channel")

	update := adw.NewButtonRow()
	update.SetTitle("Update & Restart")
	update.SetUseMarkup(false) // the ampersand is not markup
	update.AddCSSClass("suggested-action")
	update.ConnectActivated(func() {
		a.cfg.UpdateChannel = channelFromIndex(channel.Selected())
		// The update ends in a restart, so the choice goes to disk now rather
		// than after the usual wait.
		v.changed()
		v.save.Flush()
		update.SetSensitive(false)
		a.installUpdate(channelBranch(a.cfg.UpdateChannel), func(text string, done bool) {
			status.SetSubtitle(text)
			if done {
				update.SetSensitive(true)
			}
		})
	})
	g.addRow(update, "Update & Restart", "check now install")
}

// ---- About ------------------------------------------------------------------

func (v *settingsView) buildAbout() {
	a := v.a
	s := v.newSection(sectionAbout, "About")
	g := s.addGroup("", "Where Atlas Notes keeps things, for a bug report or a backup.")

	vault := a.cfg.VaultPath
	if a.store != nil {
		vault = a.store.VaultPath
	}
	if vault == "" {
		vault = storage.DefaultVaultPath()
	}
	for _, r := range []struct{ title, value string }{
		{"Version", "Atlas Notes v" + version},
		{"Vault", vault},
		{"Data", storage.DataDir()},
		{"Config", storage.ConfigPath()},
		{"Build", buildInfo()},
	} {
		g.addRow(infoRow(r.title, r.value), r.title, r.value)
	}
}

// infoRow is a title with a value under it that can be selected and copied.
// Markup is off: a path may hold an ampersand.
func infoRow(title, value string) *adw.ActionRow {
	row := adw.NewActionRow()
	row.SetTitle(title)
	row.SetSubtitle(value)
	row.SetSubtitleSelectable(true)
	row.SetUseMarkup(false)
	return row
}

// ---- Row builders -----------------------------------------------------------

// switchRow is a switch that applies the moment it is flipped.
func switchRow(title, subtitle string, on bool, apply func(on bool)) *adw.SwitchRow {
	row := adw.NewSwitchRow()
	row.SetTitle(title)
	row.SetSubtitle(subtitle)
	row.SetUseMarkup(false)
	row.SetActive(on)
	row.NotifyProperty("active", func() { apply(row.Active()) })
	return row
}

// comboRow is a dropdown that applies the moment something else is chosen. The
// handler is connected after the model and selection are set, which would
// otherwise each count as a choice.
func comboRow(title, subtitle string, labels []string, selected int, apply func(i int)) *adw.ComboRow {
	row := adw.NewComboRow()
	row.SetTitle(title)
	row.SetSubtitle(subtitle)
	row.SetUseMarkup(false)
	row.SetModel(gtk.NewStringList(labels))
	row.SetSelected(uint(selected))
	row.NotifyProperty("selected", func() { apply(int(row.Selected())) })
	return row
}

// entryRow is a text row that applies on its apply button or Enter, not on
// every keystroke: a model name typed halfway is not a model. apply returns the
// text as it kept it, which the row then shows (a blank name becomes the
// default, and the person sees that it did).
func (v *settingsView) entryRow(title, value string, apply func(text string) string) *adw.EntryRow {
	row := adw.NewEntryRow()
	row.SetTitle(title)
	row.SetText(value)
	row.SetShowApplyButton(true)
	applied := value
	commit := func() {
		applied = apply(row.Text())
		if row.Text() != applied {
			row.SetText(applied)
		}
	}
	row.ConnectApply(commit)
	v.commits = append(v.commits, func() {
		if row.Text() != applied {
			commit()
		}
	})
	return row
}

// flushOnLeave applies a pending change when focus leaves a field, so clicking
// away from it is as good as waiting.
func flushOnLeave(w gtk.Widgetter, d *debouncer) {
	fc := gtk.NewEventControllerFocus()
	fc.ConnectLeave(d.Flush)
	gtk.BaseWidget(w).AddController(fc)
}

// ---- Shared widgets ---------------------------------------------------------

// pageScroll wraps a settings page so it scrolls vertically only. Horizontal
// scrolling is switched off deliberately: with it on, the page is as wide as
// its widest unwrappable label, and anything past the dialog's edge is simply
// cut off rather than reachable. Held to the viewport's width, labels that can
// wrap do, and the page fits.
func pageScroll(child gtk.Widgetter) *gtk.ScrolledWindow {
	scroll := gtk.NewScrolledWindow()
	scroll.SetChild(child)
	scroll.SetVExpand(true)
	scroll.SetHExpand(true)
	scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	return scroll
}

// multilineField returns a bordered multi-line text field that grows to fit its
// content (the page scrolls), so prompts are never clipped. minLines sets the
// minimum height.
func multilineField(text string, minLines int) (*gtk.TextView, *gtk.Frame) {
	tv := gtk.NewTextView()
	tv.SetWrapMode(gtk.WrapWordChar)
	tv.SetLeftMargin(6)
	tv.SetRightMargin(6)
	tv.SetTopMargin(6)
	tv.SetBottomMargin(6)
	tv.SetSizeRequest(-1, minLines*24)
	tv.Buffer().SetText(text)

	frame := gtk.NewFrame("")
	frame.SetChild(tv)
	return tv, frame
}

func textViewText(tv *gtk.TextView) string {
	b := tv.Buffer()
	start, end := b.Bounds()
	return b.Text(start, end, true)
}
