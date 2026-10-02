package app

import (
	"log"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-notes/internal/storage"
	"atlas-notes/internal/theme"
	"atlas-notes/internal/ui"
)

// Settings is a page of the window, as Home and Tasks are, laid out the way
// Atlas Monitor's is: a heading per section, and under it each setting as a
// card of its own, with an icon, a title, one line saying what it does, and
// its control at the right-hand end. Options that belong to another, such as
// the model under the assistant's name, sit in the same card beneath it, lined
// up with its title. Nothing is hidden behind a second page, and nothing waits
// for a Save button: every control applies the moment it changes, and text
// applies on Enter, on leaving the field, or on leaving the page.

// Section ids, in the order the page shows them. The command box jumps to them.
const (
	sectionAppearance = "appearance"
	sectionNotes      = "notes"
	sectionAssistant  = "assistant"
	sectionClaude     = "claude"
	sectionUpdates    = "updates"
	sectionAbout      = "about"
)

// settingsSectionFor maps what the screenshot tooling passes (ATLAS_DEV_VIEW=
// settings=<name>) to a section id. The old page names still work, so scripts
// written for the three-page dialog keep reaching the same settings. An unknown
// or empty name is "", the top of the page.
func settingsSectionFor(name string) string {
	switch n := strings.ToLower(strings.TrimSpace(name)); n {
	case sectionAppearance, sectionNotes, sectionAssistant, sectionClaude, sectionUpdates, sectionAbout:
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
// change to save. Flush runs a pending fn now, which is what leaving the page
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
var noteFormatLabels = []string{"Markdown", "Zstandard", "Gzip", "XZ"}

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

// themeDescription is the line under the theme's title: which theme is chosen,
// or what following the desktop means.
func themeDescription(id string) string {
	if t, ok := theme.ByID(id); ok && !theme.IsFollowing(id) {
		return t.Name + ": " + t.Summary
	}
	return "Follows the desktop's light and dark setting"
}

// updateChannels are the channels Settings offers, each with what it is in one
// sentence.
var updateChannels = []struct{ Value, Label, Detail string }{
	{storage.ChannelRelease, "Release", "The stable version, from the main branch"},
	{storage.ChannelBeta, "Beta", "The newest features and fixes, before they reach Release. It may be unstable"},
}

// channelName is what a channel is called on the page.
func channelName(ch string) string {
	for _, c := range updateChannels {
		if c.Value == ch {
			return c.Label
		}
	}
	return "Release"
}

// updateStatusLine is what the version card says before anything has been
// tried: the channel it updates from.
func updateStatusLine(channel string) string {
	return "On the " + channelName(channel) + " channel"
}

// The waits before typing becomes a change, and before changes become a file.
// Typing gets longer than saving: a person mid-sentence should not have the
// assistant's prompt swapped under them, but the file only costs a write.
const (
	settingsTypingDelay = 600
	settingsSaveDelay   = 300
)

// settingIconSize and the margins round it decide where a card's titles start,
// and settingIndent is that position, for the rows and blocks beneath a card's
// first row that have no icon of their own but line up with its title. The 12
// and the 6 are libadwaita's own: a row's header box starts 12px in, and puts
// 6px between its prefixes and the title.
const (
	settingIconSize     = 20
	settingIconStart    = 6
	settingIconEnd      = 10
	settingIndent       = 12 + settingIconStart + settingIconSize + settingIconEnd + 6
	settingTextWidth    = 24 // characters, for the text fields at a row's end
	settingPromptHeight = 140
	settingsPageID      = "settings" // the page's name in the center stack
)

// shortcutCard is one prompt shortcut, all of it editable in place.
type shortcutCard struct {
	row    *gtk.ListBoxRow
	name   *gtk.Entry
	mode   *gtk.DropDown
	prompt *gtk.TextView
}

// settingsView is the page and everything it edits. It is built afresh each
// time the page is opened, so it never shows a value changed elsewhere since.
type settingsView struct {
	a       *App
	root    *gtk.ScrolledWindow
	content *gtk.Box

	// headings are the sections' headings, by id, for scrolling to one.
	headings map[string]*gtk.Label

	// save writes the config; typing applies the shortcuts and sysTyping the
	// system prompt, each after the person stops typing. The two are separate
	// so a pause in one does not cut short the other.
	save      *debouncer
	typing    *debouncer
	sysTyping *debouncer

	// commits apply what a text field holds but has not yet applied, for
	// leaving the page: a name typed and never confirmed is still what was
	// meant.
	commits []func()

	cards     []*shortcutCard
	shortcuts *gtk.ListBox

	anim *adw.TimedAnimation // a scroll to a section, while it runs
}

// showSettings opens the Settings page at the top.
func (a *App) showSettings() { a.showSettingsPage("") }

// showSettingsPage opens the Settings page at a named section, and moves to
// that section if the page is already open.
func (a *App) showSettingsPage(page string) {
	if a.centerStack == nil {
		return
	}
	defer a.diagSeeing("settings page")
	want := settingsSectionFor(page)
	if a.settingsShowing() && a.settings != nil {
		a.settings.goTo(want, true)
		return
	}
	if !a.leaveNote() {
		return
	}
	// A page left a moment ago may still be fading out, holding an edit it
	// applies only once it is gone. It applies it now, before the new page
	// reads the config.
	a.flushSettings()
	if old := a.centerStack.ChildByName(settingsPageID); old != nil {
		a.centerStack.Remove(old) // leaving it applied what it held
	}
	v := a.buildSettings()
	a.settings = v
	a.centerStack.AddNamed(v.root, settingsPageID)
	ui.ReplayChildren(v.content, "rise", 0)
	a.centerStack.SetVisibleChildName(settingsPageID)
	a.setWindowSubtitle("")
	a.syncNoteActions()
	if a.backlinksBar != nil {
		a.backlinksBar.SetVisible(false)
	}
	if want != "" {
		v.scrollWhenReady(want)
	}
}

// settingsShowing says whether the Settings page is what the center panel shows.
func (a *App) settingsShowing() bool {
	return a.centerStack != nil && a.centerStack.VisibleChildName() == settingsPageID
}

// flushSettings applies whatever the Settings page holds and has not yet
// applied, and writes it. Quitting needs it: the page may never be left.
func (a *App) flushSettings() {
	if a.settings != nil {
		a.settings.flush()
	}
}

// buildSettings builds the page.
func (a *App) buildSettings() *settingsView {
	v := &settingsView{a: a, headings: map[string]*gtk.Label{}}
	v.save = newDebouncer(settingsSaveDelay, func() {
		if err := storage.SaveConfig(a.cfg); err != nil {
			log.Printf("atlas-notes: save settings: %v", err)
		}
	})
	v.typing = newDebouncer(settingsTypingDelay, v.syncActions)

	v.content = gtk.NewBox(gtk.OrientationVertical, 6)
	v.content.AddCSSClass("settings-content")

	title := gtk.NewLabel("Settings")
	title.SetXAlign(0)
	title.AddCSSClass("settings-title")
	v.content.Append(title)

	v.section(sectionAppearance, "Appearance",
		v.themeCard(),
		v.transparencyCard(),
		settingsCard(v.fontRow()),
		settingsCard(v.formatBarRow()),
		settingsCard(v.introRow()))
	v.section(sectionNotes, "Notes",
		settingsCard(v.noteFormatRow()),
		settingsCard(v.remindersRow()),
		settingsCard(v.hoverRow()),
		settingsCard(v.passwordRow()))
	v.section(sectionAssistant, "Assistant",
		v.assistantCard(),
		v.promptCard(),
		v.shortcutsCard())
	v.section(sectionClaude, "Claude Code", v.claudeCards()...)
	v.section(sectionUpdates, "Updates", v.updateCards()...)
	v.section(sectionAbout, "About", v.aboutCards()...)

	// Wide enough for a title, its line and a text field side by side, but not
	// so wide on a maximised window that a title and its control end up a
	// screen apart.
	clamp := adw.NewClamp()
	clamp.SetMaximumSize(860)
	clamp.SetTighteningThreshold(640)
	clamp.SetChild(v.content)

	v.root = gtk.NewScrolledWindow()
	v.root.SetChild(clamp)
	v.root.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	v.root.SetHExpand(true)
	v.root.SetVExpand(true)
	v.root.AddCSSClass("settings-page")
	// Leaving the page, for a note or another page, applies whatever was being
	// typed, as leaving the field would have.
	v.root.ConnectUnmap(v.flush)
	return v
}

// flush applies everything pending and writes the config.
func (v *settingsView) flush() {
	for _, commit := range v.commits {
		commit()
	}
	if v.sysTyping != nil {
		v.sysTyping.Flush()
	}
	v.typing.Flush()
	v.save.Flush() // last: the flushes above may each have changed something
}

// changed says a setting has changed and the config wants writing.
func (v *settingsView) changed() { v.save.Trigger() }

// ---- Layout -----------------------------------------------------------------

// section appends a section to the page: its heading, then its cards.
func (v *settingsView) section(id, title string, cards ...gtk.Widgetter) {
	heading := gtk.NewLabel(title)
	heading.AddCSSClass("settings-heading")
	heading.SetXAlign(0)
	heading.SetMarginTop(18)
	heading.SetMarginBottom(4)
	v.content.Append(heading)
	v.headings[id] = heading
	for _, c := range cards {
		v.content.Append(c)
	}
}

// settingsCard is one card on the page: its rows joined on one surface.
func settingsCard(rows ...gtk.Widgetter) *gtk.ListBox {
	lb := gtk.NewListBox()
	lb.SetSelectionMode(gtk.SelectionNone)
	lb.AddCSSClass("boxed-list")
	lb.AddCSSClass("settings-card")
	for _, r := range rows {
		lb.Append(r)
	}
	return lb
}

// withIcon makes row the first row of a card: its icon at the start, and a
// taller row for a setting's title and description.
func withIcon(row *adw.ActionRow, icon string) {
	img := gtk.NewImageFromIconName(icon)
	img.SetPixelSize(settingIconSize)
	img.SetMarginStart(settingIconStart)
	img.SetMarginEnd(settingIconEnd)
	row.AddPrefix(img)
	row.AddCSSClass("setting")
}

// indented makes row one that belongs to the row above it: no icon, and its
// title lined up with that row's.
func indented(row *adw.ActionRow) {
	spacer := gtk.NewBox(gtk.OrientationHorizontal, 0)
	spacer.SetSizeRequest(settingIconSize, -1)
	spacer.SetMarginStart(settingIconStart)
	spacer.SetMarginEnd(settingIconEnd)
	row.AddPrefix(spacer)
}

// blockRow holds something that is not a row, such as the theme circles or a
// prompt's text, beneath a card's first row, starting where its title does.
func blockRow(child gtk.Widgetter) *gtk.ListBoxRow {
	row := gtk.NewListBoxRow()
	row.SetActivatable(false)
	// The row is only a frame. Whatever is inside it takes focus; the row
	// itself would be one more stop on the way there.
	row.SetFocusable(false)
	w := gtk.BaseWidget(child)
	w.SetMarginStart(settingIndent)
	w.SetMarginEnd(12)
	w.SetMarginTop(6)
	w.SetMarginBottom(12)
	row.SetChild(child)
	return row
}

// headRow is a card's first row, with nothing at its end yet.
func headRow(icon, title, subtitle string) *adw.ActionRow {
	row := adw.NewActionRow()
	row.SetUseMarkup(false) // before the text: a path may hold an ampersand
	row.SetTitle(title)
	row.SetSubtitle(subtitle)
	withIcon(row, icon)
	return row
}

// switchRow is a switch that applies the moment it is flipped.
func switchRow(icon, title, subtitle string, on bool, apply func(on bool)) *adw.SwitchRow {
	row := adw.NewSwitchRow()
	row.SetUseMarkup(false)
	row.SetTitle(title)
	row.SetSubtitle(subtitle)
	withIcon(&row.ActionRow, icon)
	row.SetActive(on)
	row.NotifyProperty("active", func() { apply(row.Active()) })
	return row
}

// comboRow is a dropdown that applies the moment something else is chosen. The
// handler is connected after the model and selection are set, which would
// otherwise each count as a choice.
func comboRow(icon, title, subtitle string, labels []string, selected int, apply func(i int)) *adw.ComboRow {
	row := adw.NewComboRow()
	row.SetUseMarkup(false)
	row.SetTitle(title)
	row.SetSubtitle(subtitle)
	withIcon(&row.ActionRow, icon)
	row.SetModel(gtk.NewStringList(labels))
	row.SetSelected(uint(selected))
	row.NotifyProperty("selected", func() { apply(int(row.Selected())) })
	return row
}

// textField is a text setting's entry. It applies itself on Enter and when focus
// leaves it; apply stores the trimmed text and returns what was stored, which
// the entry then shows: an emptied name comes back as the default, rather than
// staying blank while the default is what is used. Anything still pending when
// the page is left is applied then.
func (v *settingsView) textField(value string, apply func(text string) string) *gtk.Entry {
	e := gtk.NewEntry()
	e.SetText(value)
	e.SetWidthChars(settingTextWidth)
	e.SetVAlign(gtk.AlignCenter)
	applied := value
	commit := func() {
		if e.Text() == applied {
			return
		}
		applied = apply(strings.TrimSpace(e.Text()))
		if e.Text() != applied {
			e.SetText(applied)
		}
	}
	e.ConnectActivate(commit)
	focus := gtk.NewEventControllerFocus()
	focus.ConnectLeave(commit)
	e.AddController(focus)
	v.commits = append(v.commits, commit)
	return e
}

// textRow is a text setting beneath a card's first row: its title and
// description, and the field at the end, with anything in before just ahead of
// it.
func (v *settingsView) textRow(title, desc, value string, apply func(text string) string,
	before ...gtk.Widgetter) *adw.ActionRow {
	row := adw.NewActionRow()
	row.SetUseMarkup(false)
	row.SetTitle(title)
	row.SetSubtitle(desc)
	indented(row)
	for _, w := range before {
		row.AddSuffix(w)
	}
	field := v.textField(value, apply)
	row.AddSuffix(field)
	// A click anywhere on the row puts the cursor in the field.
	row.SetActivatableWidget(field)
	return row
}

// promptField is a multi-line text field in a frame of its own. It grows with
// its text up to three times its smallest height, and scrolls after that.
func promptField(text string, minHeight int) (*gtk.TextView, *gtk.ScrolledWindow) {
	tv := gtk.NewTextView()
	tv.SetWrapMode(gtk.WrapWordChar)
	tv.SetLeftMargin(8)
	tv.SetRightMargin(8)
	tv.SetTopMargin(8)
	tv.SetBottomMargin(8)
	tv.Buffer().SetText(text)

	scroll := gtk.NewScrolledWindow()
	scroll.SetChild(tv)
	scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	scroll.SetMinContentHeight(minHeight)
	scroll.SetMaxContentHeight(minHeight * 3)
	scroll.SetPropagateNaturalHeight(true)
	scroll.AddCSSClass("settings-field")
	return tv, scroll
}

// flushOnLeave applies a pending change when focus leaves a field, so clicking
// away from it is as good as waiting.
func flushOnLeave(w gtk.Widgetter, d *debouncer) {
	fc := gtk.NewEventControllerFocus()
	fc.ConnectLeave(d.Flush)
	gtk.BaseWidget(w).AddController(fc)
}

func textViewText(tv *gtk.TextView) string {
	b := tv.Buffer()
	start, end := b.Bounds()
	return b.Text(start, end, true)
}

// ---- Scrolling to a section -------------------------------------------------

// goTo scrolls to a section, or to the top for an unknown or empty id.
func (v *settingsView) goTo(id string, animate bool) {
	y := 0.0
	if h := v.headings[id]; h != nil {
		rect, ok := h.ComputeBounds(v.content)
		if !ok {
			return
		}
		y = float64(rect.Y()) - 8
	}
	adj := v.root.VAdjustment()
	v.scrollTo(clampScroll(y, adj.Upper(), adj.PageSize()), animate)
}

// scrollTo moves the page to y, gently, unless the person has asked their
// desktop for no animation, which adw honours by jumping.
func (v *settingsView) scrollTo(y float64, animate bool) {
	adj := v.root.VAdjustment()
	if v.anim != nil {
		anim := v.anim
		v.anim = nil
		anim.Skip() // an earlier one still running is finished, not left fighting this one
	}
	from := adj.Value()
	if !animate || from == y {
		adj.SetValue(y)
		return
	}
	target := adw.NewCallbackAnimationTarget(func(x float64) { adj.SetValue(x) })
	anim := adw.NewTimedAnimation(v.root, from, y, 260, target)
	anim.SetEasing(adw.EaseOutCubic)
	anim.ConnectDone(func() {
		if v.anim == anim {
			v.anim = nil
		}
	})
	v.anim = anim
	anim.Play()
}

// scrollWhenReady is goTo for a page that has only just been shown, where
// nothing has a size yet. It waits for the page to be laid out, and scrolls
// again a moment later because the text fields settle after the rest.
func (v *settingsView) scrollWhenReady(id string) {
	tries := 0
	coreglib.TimeoutAdd(60, func() bool {
		if v.a.settings != v {
			return false // replaced by a newer copy of the page
		}
		tries++
		adj := v.root.VAdjustment()
		if adj.Upper() <= adj.PageSize() && tries < 40 {
			return true
		}
		v.goTo(id, false)
		coreglib.TimeoutAdd(300, func() bool {
			if v.a.settings == v {
				v.goTo(id, false)
			}
			return false
		})
		return false
	})
}

// ---- Appearance -------------------------------------------------------------

// themeCard is the colour theme: one circle per theme, split between the
// window's background and its accent, since a theme is those two decisions.
// A colour is chosen by looking at it; a list of names would make you pick one
// to find out what it is.
//
// There is no circle for following the desktop. It is where the app starts and
// it is not a palette, so it is the button at the end of the row, which stays
// pressed while it is the choice.
func (v *settingsView) themeCard() *gtk.ListBox {
	a := v.a
	head := headRow("atlasnotes-theme-symbolic", "Theme", "")

	system := gtk.NewToggleButtonWithLabel("Use system setting")
	system.SetVAlign(gtk.AlignCenter)
	system.AddCSSClass("settings-choice")
	system.SetTooltipText("Use the desktop's light or dark setting")
	head.AddSuffix(system)

	// The circles in two halves, side by side where they fit and one above the
	// other where they do not: a line of ten, or two of five. Wrapping the ten
	// one by one would leave whatever did not fit alone on a second line.
	circles := gtk.NewFlowBox()
	circles.SetSelectionMode(gtk.SelectionNone)
	circles.SetActivateOnSingleClick(false)
	circles.SetMaxChildrenPerLine(2)
	circles.SetMinChildrenPerLine(1)
	circles.SetHomogeneous(true)
	circles.SetColumnSpacing(18)
	circles.SetRowSpacing(12)
	circles.SetHAlign(gtk.AlignStart)
	perHalf := (len(theme.Themes) + 1) / 2
	var halves [2]*gtk.Box
	for i := range halves {
		halves[i] = gtk.NewBox(gtk.OrientationHorizontal, 18)
		halves[i].SetHomogeneous(true)
		circles.Append(halves[i])
		// The FlowBox wraps each half in a child that takes focus, which would
		// put a stop that does nothing in front of the circles.
		if child := circles.ChildAtIndex(i); child != nil {
			child.SetFocusable(false)
		}
	}

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
		system.SetActive(following)
		syncing = false
		head.SetSubtitle(themeDescription(a.cfg.Theme))
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
	// keep puts back a toggle clicked while already chosen: turning it off
	// would leave nothing chosen.
	keep := func(b *gtk.ToggleButton) {
		syncing = true
		b.SetActive(true)
		syncing = false
	}

	for _, t := range theme.Themes {
		t := t
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
		cell.Append(sw)
		cell.Append(name)
		halves[min(len(swatches)/perHalf, 1)].Append(cell)

		sw.ConnectToggled(func() {
			if syncing {
				return
			}
			if !sw.Active() {
				keep(sw)
				return
			}
			choose(t.ID)
		})
		swatches = append(swatches, sw)
	}
	system.ConnectToggled(func() {
		if syncing {
			return
		}
		if !system.Active() {
			keep(system)
			return
		}
		choose(theme.Follow)
	})

	sync()
	return settingsCard(head, blockRow(circles))
}

// transparencyCard is true translucency, where the desktop can show it.
// Elsewhere the choice stays, greyed, with the reason as its line, and the
// saved level is kept for a desktop that can.
func (v *settingsView) transparencyCard() *gtk.ListBox {
	a := v.a
	sub := "Lets the desktop show through the window. Text stays solid"
	ok, why := TransparencyAvailable()
	if !ok {
		sub = why
	}
	row := comboRow("atlasnotes-opacity-symbolic", "Window transparency", sub, transparencyLabels,
		transparencyIndex(a.cfg.WindowTransparency), func(i int) {
			if i < 0 || i >= len(storage.TransparencyLevels) {
				return
			}
			a.cfg.WindowTransparency = storage.TransparencyLevels[i]
			a.applyTransparency()
			v.changed()
		})
	row.SetSensitive(ok)
	return settingsCard(row)
}

// fontRow is how text is drawn. The right choice depends on the screen, so it
// is a setting rather than a guess; see fonts.go.
func (v *settingsView) fontRow() *adw.ComboRow {
	a := v.a
	return comboRow("atlasnotes-text-symbolic", "Text rendering",
		"Crisp suits 1080p screens, Smooth suits HiDPI ones. Applies on the next launch",
		[]string{"Automatic", "Crisp", "Smooth"}, fontModeIndex(a.cfg.FontRendering), func(i int) {
			if i < 0 || i >= len(fontRenderingModes) {
				return
			}
			a.cfg.FontRendering = fontRenderingModes[i]
			v.changed()
		})
}

func (v *settingsView) formatBarRow() *adw.SwitchRow {
	a := v.a
	return switchRow("atlasnotes-toolbar-symbolic", "Formatting toolbar",
		"The formatting buttons above each note. Each has a keyboard shortcut too",
		a.cfg.ShowFormatBar, func(on bool) {
			a.cfg.ShowFormatBar = on
			a.applyFormatBarVisibility()
			v.changed()
		})
}

func (v *settingsView) introRow() *adw.SwitchRow {
	a := v.a
	return switchRow("atlasnotes-startup-symbolic", "Intro at startup",
		"Plays the Atlas logo when Atlas Notes opens. A click or any key skips it",
		a.cfg.ShowIntro, func(on bool) {
			a.cfg.ShowIntro = on
			v.changed()
		})
}

// ---- Notes ------------------------------------------------------------------

// noteFormatRow is how notes are stored. The format belongs to the vault, not
// to this machine: it is saved in the vault, so every device that syncs it
// writes notes the same way. Protected notes stay encrypted in any of them.
func (v *settingsView) noteFormatRow() *adw.ComboRow {
	a := v.a
	selected := 0
	if a.store != nil {
		selected = noteFormatIndex(a.store.Compression())
	}
	var row *adw.ComboRow
	putting := false
	row = comboRow("atlasnotes-file-format-symbolic", "Note files",
		"Markdown opens in any app; the others take less space. Changing it converts every note",
		noteFormatLabels, selected, func(i int) {
			if putting || a.store == nil || i < 0 || i >= len(noteFormats) {
				return
			}
			a.changeNoteFormat(noteFormats[i])
			// A change the vault refused leaves the format as it was, and the
			// row says so rather than showing the one that never applied.
			if now := noteFormatIndex(a.store.Compression()); now != i {
				putting = true
				row.SetSelected(uint(now))
				putting = false
			}
		})
	row.SetSensitive(a.store != nil)
	return row
}

func (v *settingsView) remindersRow() *adw.SwitchRow {
	a := v.a
	return switchRow("atlasnotes-alarm-symbolic", "Reminders",
		"Notify me about checklist items due today or overdue",
		a.cfg.DueReminders, func(on bool) {
			a.cfg.DueReminders = on
			v.changed()
		})
}

func (v *settingsView) hoverRow() *adw.SwitchRow {
	a := v.a
	return switchRow("atlasnotes-summarize-symbolic", "Hover previews",
		"A one-sentence AI summary when you hover a note in the vault panel",
		a.cfg.EnableTreeSummaries, func(on bool) {
			a.cfg.EnableTreeSummaries = on
			if a.tree != nil {
				a.tree.SetSummariesEnabled(on)
			}
			v.changed()
		})
}

// passwordRow is the one setting that needs a dialog of its own: it asks for
// the old password, then the new one, and nothing on this page could hold
// that.
func (v *settingsView) passwordRow() *adw.ActionRow {
	a := v.a
	row := headRow("atlasnotes-lock-symbolic", "Password protection",
		"One password covers everything you protect. Right-click a note or folder to protect it")
	change := gtk.NewButtonWithLabel("Change password…")
	change.SetVAlign(gtk.AlignCenter)
	change.SetSensitive(a.store != nil && a.store.HasPassword())
	if !change.Sensitive() {
		change.SetTooltipText("No password has been set yet")
	}
	change.ConnectClicked(a.promptChangePassword)
	row.AddSuffix(change)
	return row
}

// ---- Assistant --------------------------------------------------------------

// assistantCard is what the assistant is called, with the model that answers
// beneath it.
func (v *settingsView) assistantCard() *gtk.ListBox {
	a := v.a
	head := headRow("atlasnotes-assistant-symbolic", "Assistant name",
		"What the assistant is called in the side panel")
	name := v.textField(a.cfg.AssistantName, func(text string) string {
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
	head.AddSuffix(name)
	head.SetActivatableWidget(name)

	// The link goes before the field rather than after it, so that the fields'
	// right-hand edges still line up down the card.
	browse := gtk.NewLinkButtonWithLabel("https://ollama.com/library", "Browse models")
	browse.SetVAlign(gtk.AlignCenter)
	browse.SetTooltipText("The models Ollama can pull, on ollama.com")
	model := v.textRow("Model", "Any model Ollama has pulled", a.cfg.Model, func(text string) string {
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
	}, browse)

	return settingsCard(head, model)
}

// promptCard is the system prompt, in full, with the button that puts back
// the one Atlas Notes ships with.
func (v *settingsView) promptCard() *gtk.ListBox {
	a := v.a
	head := headRow("atlasnotes-sparkle-symbolic", "System prompt",
		"The standing instructions the assistant is given with every request")
	field, frame := promptField(a.cfg.SystemPrompt, settingPromptHeight)

	reset := gtk.NewButtonWithLabel("Reset to default")
	reset.SetVAlign(gtk.AlignCenter)
	head.AddSuffix(reset)
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
	reset.ConnectClicked(func() {
		field.Buffer().SetText(storage.DefaultSystemPrompt)
		v.sysTyping.Flush()
	})
	return settingsCard(head, blockRow(frame))
}

// shortcutsCard lists every prompt shortcut beneath its heading, each editable
// in place, with the way to add one at the heading's end.
func (v *settingsView) shortcutsCard() *gtk.ListBox {
	head := headRow("atlasnotes-prompts-symbolic", "Prompt shortcuts",
		"One click in the assistant panel. {content} is the note's text, {items} its checklist")

	addContent := adw.NewButtonContent()
	addContent.SetIconName("atlasnotes-add-symbolic")
	addContent.SetLabel("Add")
	add := gtk.NewButton()
	add.SetChild(addContent)
	add.SetVAlign(gtk.AlignCenter)
	add.SetTooltipText("Add a prompt shortcut")
	head.AddSuffix(add)

	v.shortcuts = settingsCard(head)
	for _, act := range v.a.cfg.Actions {
		v.addShortcut(act)
	}
	add.ConnectClicked(func() {
		c := v.addShortcut(storage.AIAction{Name: "New shortcut", Mode: storage.ActionModeShow, Prompt: "{content}"})
		v.syncActions()
		c.name.GrabFocus()
		c.name.SelectRegion(0, -1)
	})
	return v.shortcuts
}

// addShortcut puts a shortcut at the end of the card: its name, what it does
// with the answer, the way to remove it, and its prompt beneath. Its name and
// prompt are applied after a pause in typing, or on leaving them.
func (v *settingsView) addShortcut(act storage.AIAction) *shortcutCard {
	c := &shortcutCard{}

	c.name = gtk.NewEntry()
	c.name.SetHExpand(true)
	c.name.SetPlaceholderText("Shortcut name")
	c.name.SetText(act.Name)
	c.mode = gtk.NewDropDownFromStrings(modeLabels)
	c.mode.SetSelected(modeIndex(act.Mode))
	c.mode.SetVAlign(gtk.AlignCenter)
	c.mode.SetTooltipText("What happens to the answer")
	remove := gtk.NewButtonFromIconName("atlasnotes-trash-symbolic")
	remove.AddCSSClass("flat")
	remove.SetVAlign(gtk.AlignCenter)
	remove.SetTooltipText("Remove shortcut")

	top := gtk.NewBox(gtk.OrientationHorizontal, 8)
	top.Append(c.name)
	top.Append(c.mode)
	top.Append(remove)

	var frame *gtk.ScrolledWindow
	c.prompt, frame = promptField(act.Prompt, 64)

	box := gtk.NewBox(gtk.OrientationVertical, 8)
	box.Append(top)
	box.Append(frame)
	c.row = blockRow(box)
	box.SetMarginTop(10)

	c.name.ConnectChanged(v.typing.Trigger)
	c.name.ConnectActivate(v.typing.Flush)
	flushOnLeave(c.name, v.typing)
	c.prompt.Buffer().ConnectChanged(v.typing.Trigger)
	flushOnLeave(c.prompt, v.typing)
	c.mode.NotifyProperty("selected", v.syncActions)
	remove.ConnectClicked(func() {
		v.shortcuts.Remove(c.row)
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
	v.shortcuts.Append(c.row)
	return c
}

// syncActions makes the config's shortcuts what the page says. One with no
// name is left out, but it stays on the page so its name can be typed.
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

// ---- Claude Code ------------------------------------------------------------

// claudeCards are the switch that lets Claude Code use the vault, what it may
// do once it can, and the command that connects it. The connector itself is
// "atlas-notes mcp", a separate process that reads these settings on every
// call, so saving them is all it takes to change what it allows.
func (v *settingsView) claudeCards() []gtk.Widgetter {
	a := v.a
	access := &a.cfg.Claude

	sub := func(title, subtitle string, on bool, apply func(on bool)) *adw.SwitchRow {
		row := adw.NewSwitchRow()
		row.SetUseMarkup(false)
		row.SetTitle(title)
		row.SetSubtitle(subtitle)
		indented(&row.ActionRow)
		row.SetActive(on)
		row.SetSensitive(access.Enabled)
		row.NotifyProperty("active", func() { apply(row.Active()) })
		return row
	}
	read := sub("Read and search notes", "Lists, opens and searches your notes", access.Read, func(on bool) {
		access.Read = on
		v.changed()
	})
	write := sub("Create and edit notes",
		"Creates, edits, renames and moves notes and folders. Every edit is kept in Version History",
		access.Write, func(on bool) {
			access.Write = on
			v.changed()
		})
	del := sub("Delete notes", "Moves notes and folders to the Trash", access.Delete, func(on bool) {
		access.Delete = on
		v.changed()
	})
	master := switchRow("atlasnotes-sparkle-symbolic", "Let Claude Code use your notes",
		"Claude Code can work with this vault through the Atlas Notes connector. "+
			"Password-protected notes stay hidden from it",
		access.Enabled, func(on bool) {
			access.Enabled = on
			for _, r := range []*adw.SwitchRow{read, write, del} {
				r.SetSensitive(on)
			}
			v.changed()
		})

	cmd := claudeConnectCommand(connectBinary(), inFlatpak())
	connect := headRow("atlasnotes-link-symbolic", "Connect Claude Code",
		"Run this once in a terminal:\n"+cmd)
	connect.SetUseMarkup(false) // a path can hold an & or a <
	connect.SetSubtitleSelectable(true)
	copyBtn := gtk.NewButtonWithLabel("Copy")
	copyBtn.SetVAlign(gtk.AlignCenter)
	copyBtn.SetTooltipText("Copy the command")
	copyBtn.ConnectClicked(func() {
		copyBtn.Clipboard().SetText(cmd)
		a.toast("Command copied")
	})
	connect.AddSuffix(copyBtn)

	return []gtk.Widgetter{settingsCard(master, read, write, del), settingsCard(connect)}
}

// connectBinary is the running program, with symlinks resolved, for the
// command that registers it. Empty when it cannot be found.
func connectBinary() string {
	exe, _ := installedBinary()
	return exe
}

// claudeConnectCommand is the command that registers the connector with Claude
// Code. In a Flatpak the program is not on the host's PATH, so it is run
// through flatpak; otherwise it is the binary's own path.
func claudeConnectCommand(exe string, flatpak bool) string {
	const head = "claude mcp add --scope user atlas-notes -- "
	if flatpak {
		return head + "flatpak run --command=" + binaryName + " " + flatpakAppID + " mcp"
	}
	if exe == "" {
		exe = binaryName
	}
	return head + shellQuote(exe) + " mcp"
}

// shellQuote leaves a word alone when a shell would read it as one plain word,
// and wraps it in single quotes otherwise.
func shellQuote(s string) string {
	plain := s != ""
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("/._+-:@%=,", r):
		default:
			plain = false
		}
	}
	if plain {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ---- Updates ----------------------------------------------------------------

// updateCards are the version with its Update button, the channel it follows,
// and whether it checks by itself.
func (v *settingsView) updateCards() []gtk.Widgetter {
	a := v.a
	status := headRow("atlasnotes-update-symbolic", "Atlas Notes "+version, updateStatusLine(a.cfg.UpdateChannel))
	status.SetSubtitleSelectable(true) // a failed build's output, for a bug report

	update := gtk.NewButtonWithLabel("Update")
	update.SetVAlign(gtk.AlignCenter)
	update.AddCSSClass("suggested-action")
	update.SetTooltipText("Install the newest version from the chosen channel, and restart")
	status.AddSuffix(update)
	// An update runs on after the page is left and opened again, which builds
	// the page anew: the new one carries on showing it, and cannot start a
	// second build in the same clone.
	if a.updateStatus != "" {
		status.SetSubtitle(a.updateStatus)
	}
	update.SetSensitive(!a.updating)
	a.onUpdateStatus = func(text string, done bool) {
		status.SetSubtitle(text)
		update.SetSensitive(done)
	}
	update.ConnectClicked(func() {
		if a.updating {
			return
		}
		// The update ends in a restart, so the config goes to disk now rather
		// than after the usual wait.
		v.flush()
		a.updating = true
		a.updateStatus = ""
		update.SetSensitive(false)
		a.installUpdate(channelBranch(a.cfg.UpdateChannel), func(text string, done bool) {
			a.updating = !done
			a.updateStatus = text
			if a.onUpdateStatus != nil {
				a.onUpdateStatus(text, done)
			}
		})
	})

	// The channel is which Atlas Notes this copy is: moving to the other is
	// choosing it here and pressing Update.
	current := a.cfg.UpdateChannel
	labels := make([]string, len(updateChannels))
	selected := 0
	for i, c := range updateChannels {
		labels[i] = c.Label
		if c.Value == current {
			selected = i
		}
	}
	var channel *adw.ComboRow
	channel = comboRow("atlasnotes-branch-symbolic", "Update channel", updateChannels[selected].Detail,
		labels, selected, func(i int) {
			if i < 0 || i >= len(updateChannels) {
				return
			}
			c := updateChannels[i]
			a.cfg.UpdateChannel = c.Value
			channel.SetSubtitle(c.Detail)
			switch {
			case a.updating:
				// The status is the build's progress, not to be talked over.
			case c.Value == current:
				status.SetSubtitle(updateStatusLine(c.Value))
			default:
				status.SetSubtitle("Press Update to switch to " + c.Label)
			}
			v.changed()
		})

	check := switchRow("atlasnotes-recent-symbolic", "Check for updates on launch",
		"Asks GitHub once at startup. Nothing about you or your notes is sent",
		a.cfg.CheckUpdates, func(on bool) {
			a.cfg.CheckUpdates = on
			v.changed()
		})

	return []gtk.Widgetter{settingsCard(status), settingsCard(channel), settingsCard(check)}
}

// ---- About ------------------------------------------------------------------

// aboutCards are where Atlas Notes keeps things, and the build, for a bug
// report or a backup. Every value can be selected and copied.
func (v *settingsView) aboutCards() []gtk.Widgetter {
	a := v.a
	vault := a.cfg.VaultPath
	if a.store != nil {
		vault = a.store.VaultPath
	}
	if vault == "" {
		vault = storage.DefaultVaultPath()
	}
	where := headRow("atlasnotes-folder-symbolic", "Vault", vault)
	where.SetSubtitleSelectable(true)
	rows := []gtk.Widgetter{where}
	for _, r := range []struct{ title, value string }{
		{"Data", storage.DataDir()},
		{"Config", storage.ConfigPath()},
	} {
		row := adw.NewActionRow()
		row.SetUseMarkup(false)
		row.SetTitle(r.title)
		row.SetSubtitle(r.value)
		row.SetSubtitleSelectable(true)
		indented(row)
		rows = append(rows, row)
	}

	build := headRow("atlasnotes-info-symbolic", "Build", buildInfo())
	build.SetSubtitleSelectable(true)

	diagRow := switchRow("atlasnotes-property-list-symbolic", "Keep a diagnostic log",
		"Records what you click and what the window shows, for tracking down bugs. "+
			"It stays in a file on this computer and is never sent anywhere:\n"+storage.DiagnosticsLogPath(),
		a.cfg.DiagnosticsLog, func(on bool) {
			a.cfg.DiagnosticsLog = on
			a.applyDiagnostics()
			v.changed()
		})
	diagRow.SetSubtitleSelectable(true)
	return []gtk.Widgetter{settingsCard(rows...), settingsCard(build), settingsCard(diagRow)}
}
