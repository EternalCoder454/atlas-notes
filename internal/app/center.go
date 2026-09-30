package app

import (
	"fmt"
	"path"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-notes/internal/checklist"
	"atlas-notes/internal/editor"
)

// editorMaxWidth clamps the text column: long lines are hard to read edge to
// edge on a wide monitor, so the document stays centered at a comfortable
// measure while the window itself can be any size.
const editorMaxWidth = 860

// buildCenter assembles the center panel as a stack of two pages: the welcome
// home screen, and the note editor (title row · formatting toolbar · find bar ·
// text · status bar).
func (a *App) buildCenter() *gtk.Box {
	center := gtk.NewBox(gtk.OrientationVertical, 0)
	center.SetHExpand(true)
	center.AddCSSClass("center-panel")

	a.centerStack = gtk.NewStack()
	a.centerStack.SetTransitionType(gtk.StackTransitionTypeCrossfade)
	a.centerStack.SetVExpand(true)
	// The home screen is built the first time it is shown. Launching straight
	// into a note — the common case — then costs nothing for a page the user
	// never sees, including the query behind its "recent notes" list.
	a.centerStack.AddNamed(gtk.NewBox(gtk.OrientationVertical, 0), "welcome")
	a.centerStack.AddNamed(a.buildEditorPage(), "editor")
	a.centerStack.SetVisibleChildName("editor")

	center.Append(a.centerStack)
	return center
}

// buildEditorPage is the note view itself.
func (a *App) buildEditorPage() *gtk.Box {
	page := gtk.NewBox(gtk.OrientationVertical, 0)
	page.Append(a.buildNoteHeader())
	a.formatBar = a.buildFormatBar()
	a.formatBar.SetVisible(a.cfg.ShowFormatBar)
	page.Append(a.formatBar)

	a.editor = editor.New()
	a.editor.OnChanged = a.onEditorChanged
	a.editor.OnReparsed = a.onEditorReparsed
	a.wireEditorLinks()
	page.Append(a.buildFindBar())

	clamp := adw.NewClamp()
	clamp.SetMaximumSize(editorMaxWidth)
	clamp.SetTighteningThreshold(editorMaxWidth)
	clamp.SetChild(a.editor.Widget())
	clamp.SetVExpand(true)
	page.Append(clamp)

	page.Append(a.buildBacklinks())
	page.Append(a.buildStatusBar())
	return page
}

// buildNoteHeader is the title row: the folder the note lives in, its name as a
// borderless title field, and the save state.
func (a *App) buildNoteHeader() *gtk.Box {
	row := gtk.NewBox(gtk.OrientationHorizontal, 10)
	row.AddCSSClass("note-header")

	titles := gtk.NewBox(gtk.OrientationVertical, 0)
	titles.SetHExpand(true)
	titles.SetVAlign(gtk.AlignCenter)

	a.breadcrumb = gtk.NewLabel("")
	a.breadcrumb.SetXAlign(0)
	a.breadcrumb.AddCSSClass("breadcrumb")
	a.breadcrumb.SetVisible(false)
	titles.Append(a.breadcrumb)

	a.titleEntry = gtk.NewEntry()
	a.titleEntry.SetHExpand(true)
	a.titleEntry.SetPlaceholderText("Untitled note")
	a.titleEntry.AddCSSClass("note-title")
	a.titleEntry.SetHasFrame(false)
	a.titleEntry.SetTooltipText("The note's name is its filename. Press Enter to rename")
	a.titleEntry.ConnectActivate(a.onTitleActivate)
	titles.Append(a.titleEntry)

	row.Append(titles)
	return row
}

// buildSavePill is the save indicator: a colored dot plus its state, styled as
// one quiet pill instead of two loose widgets.
func (a *App) buildSavePill() *gtk.Box {
	a.saveDot = gtk.NewBox(gtk.OrientationHorizontal, 0)
	a.saveDot.SetSizeRequest(8, 8)
	a.saveDot.SetVAlign(gtk.AlignCenter)
	a.saveDot.AddCSSClass("save-dot")
	a.saveDot.AddCSSClass("save-saved")

	a.saveLabel = gtk.NewLabel("Saved")
	a.saveLabel.AddCSSClass("save-indicator")

	pill := gtk.NewBox(gtk.OrientationHorizontal, 6)
	pill.SetVAlign(gtk.AlignCenter)
	pill.AddCSSClass("save-pill")
	pill.Append(a.saveDot)
	pill.Append(a.saveLabel)
	pill.SetTooltipText("Atlas Notes saves as you type · Ctrl+S saves now")
	return pill
}

// formatButton is one formatting-toolbar entry. label is used when the icon
// theme has no icon by that name, so the toolbar never shows broken images.
type formatButton struct {
	icon, label, tooltip string
	action               func()
}

// buildFormatBar is the formatting toolbar. Everything it offers was previously
// available only by typing the Markdown by hand, which meant new users had no
// way to discover it.
func (a *App) buildFormatBar() *gtk.Box {
	bar := gtk.NewBox(gtk.OrientationHorizontal, 2)
	bar.AddCSSClass("format-bar")

	groups := [][]formatButton{{
		{"atlasnotes-bold-symbolic", "B", "Bold (Ctrl+B)", func() { a.withEditor((*editor.Editor).ToggleBold) }},
		{"atlasnotes-italic-symbolic", "I", "Italic (Ctrl+I)", func() { a.withEditor((*editor.Editor).ToggleItalic) }},
		{"atlasnotes-strikethrough-symbolic", "S", "Strikethrough", func() { a.withEditor((*editor.Editor).ToggleStrike) }},
		{"atlasnotes-code-symbolic", "</>", "Inline code (Ctrl+E)", func() { a.withEditor((*editor.Editor).ToggleCode) }},
	}, {
		{"atlasnotes-heading1-symbolic", "H1", "Heading (Ctrl+1)", func() { a.withEditor(func(e *editor.Editor) { e.SetHeading(1) }) }},
		{"atlasnotes-heading2-symbolic", "H2", "Subheading (Ctrl+2)", func() { a.withEditor(func(e *editor.Editor) { e.SetHeading(2) }) }},
		{"atlasnotes-paragraph-symbolic", "¶", "Plain text (Ctrl+0)", func() { a.withEditor(func(e *editor.Editor) { e.SetHeading(0) }) }},
	}, {
		{"atlasnotes-bullet-list-symbolic", "•", "Bullet list", func() { a.withEditor((*editor.Editor).ToggleBullet) }},
		{"atlasnotes-task-symbolic", "☑", "Task (Ctrl+Shift+T)", func() { a.withEditor((*editor.Editor).ToggleTask) }},
		{"atlasnotes-quote-symbolic", "❝", "Quote", func() { a.withEditor((*editor.Editor).ToggleQuote) }},
		{"atlasnotes-divider-symbolic", "─", "Divider", func() { a.withEditor((*editor.Editor).InsertDivider) }},
	}}

	for i, group := range groups {
		if i > 0 {
			sep := gtk.NewSeparator(gtk.OrientationVertical)
			sep.AddCSSClass("format-sep")
			bar.Append(sep)
		}
		for _, b := range group {
			var btn *gtk.Button
			if hasIcon(b.icon) {
				btn = gtk.NewButtonFromIconName(b.icon)
			} else {
				btn = gtk.NewButtonWithLabel(b.label)
				btn.AddCSSClass("format-text-button")
			}
			btn.AddCSSClass("flat")
			btn.AddCSSClass("format-button")
			btn.SetTooltipText(b.tooltip)
			btn.SetCanFocus(false) // keep the caret in the document
			btn.ConnectClicked(b.action)
			bar.Append(btn)
		}
	}

	return bar
}

// withEditor runs a formatting command and returns focus to the document.
func (a *App) withEditor(fn func(*editor.Editor)) {
	if a.editor == nil {
		return
	}
	fn(a.editor)
	a.editor.Focus()
}

// findSearchDelayMs is how long the find entry waits after the last keystroke
// before it reports a new query. It is the debounce: a long note is searched
// once the typing pauses, not once per letter.
const findSearchDelayMs = 100

// findBar is the strip between the toolbar and the text for finding, and
// replacing, text in the open note. It is built once and shown or hidden.
type findBar struct {
	revealer      *gtk.Revealer
	entry         *gtk.SearchEntry
	counter       *gtk.Label
	matchCase     *gtk.ToggleButton
	replaceToggle *gtk.ToggleButton
	replaceReveal *gtk.Revealer // the second row
	replaceEntry  *gtk.Entry

	// holdCounter keeps "Replaced N" on the counter until the next search or
	// step. Without it the very next refresh would overwrite the answer with
	// how many matches are left, which is not what was just asked.
	holdCounter bool
}

// findBars holds each App's find bar, so that everything about it stays in this
// file.
var findBars = map[*App]*findBar{}

// buildFindBar makes the find bar, hidden until Ctrl+F (or the menu) asks for
// it. It reuses the toolbar's look, so it reads as part of the same chrome.
func (a *App) buildFindBar() *gtk.Revealer {
	fb := &findBar{}
	findBars[a] = fb

	fb.entry = gtk.NewSearchEntry()
	fb.entry.SetHExpand(true)
	fb.entry.SetPlaceholderText("Find in this note")
	fb.entry.SetSearchDelay(findSearchDelayMs)
	fb.entry.ConnectSearchChanged(a.runFind)
	// GtkSearchEntry binds Ctrl+G and Ctrl+Shift+G itself, and if it gets the key
	// before the application shortcut does, it must still go somewhere.
	fb.entry.ConnectNextMatch(func() { a.stepFind(true) })
	fb.entry.ConnectPreviousMatch(func() { a.stepFind(false) })
	// Enter is next and Shift+Enter previous. Handled while the key is still on
	// its way down, before the entry can treat it as "activate".
	fb.entry.AddController(enterKey(func(shift bool) { a.stepFind(!shift) }))

	fb.counter = gtk.NewLabel("")
	fb.counter.AddCSSClass("dim-label")
	fb.counter.AddCSSClass("numeric")
	fb.counter.SetWidthChars(10) // room for "No matches", so the bar does not shift as it changes
	fb.counter.SetXAlign(1)

	prev := findIconButton("atlasnotes-chevron-up-symbolic", "↑", "Previous match (Ctrl+Shift+G)", func() { a.stepFind(false) })
	next := findIconButton("atlasnotes-chevron-down-symbolic", "↓", "Next match (Ctrl+G)", func() { a.stepFind(true) })

	fb.matchCase = gtk.NewToggleButtonWithLabel("Match case")
	fb.matchCase.AddCSSClass("flat")
	fb.matchCase.SetFocusOnClick(false)
	fb.matchCase.SetTooltipText("Only match text with the same capitals")
	fb.matchCase.ConnectToggled(a.runFind)

	fb.replaceToggle = gtk.NewToggleButton()
	if hasIcon("atlasnotes-find-replace-symbolic") {
		fb.replaceToggle.SetIconName("atlasnotes-find-replace-symbolic")
	} else {
		fb.replaceToggle.SetLabel("⇄")
	}
	fb.replaceToggle.AddCSSClass("flat")
	fb.replaceToggle.SetFocusOnClick(false)
	fb.replaceToggle.SetTooltipText("Replace (Ctrl+R)")
	fb.replaceToggle.ConnectToggled(func() { fb.replaceReveal.SetRevealChild(fb.replaceToggle.Active()) })

	closeBtn := findIconButton("atlasnotes-close-symbolic", "✕", "Close (Esc)", func() { a.closeFind(true) })

	top := gtk.NewBox(gtk.OrientationHorizontal, 6)
	top.Append(fb.replaceToggle)
	top.Append(fb.entry)
	top.Append(fb.counter)
	top.Append(prev)
	top.Append(next)
	top.Append(fb.matchCase)
	top.Append(closeBtn)

	// The second row starts under the first row's entry: a spacer as wide as the
	// replace toggle stands in for it.
	spacer := gtk.NewBox(gtk.OrientationHorizontal, 0)
	widths := gtk.NewSizeGroup(gtk.SizeGroupHorizontal)
	widths.AddWidget(fb.replaceToggle)
	widths.AddWidget(spacer)

	fb.replaceEntry = gtk.NewEntry()
	fb.replaceEntry.SetHExpand(true)
	fb.replaceEntry.SetPlaceholderText("Replace with")
	fb.replaceEntry.AddController(enterKey(func(bool) { a.replaceOne() }))

	replace := gtk.NewButtonWithLabel("Replace")
	replace.SetFocusOnClick(false)
	replace.SetTooltipText("Replace this match and go to the next")
	replace.ConnectClicked(a.replaceOne)
	replaceAll := gtk.NewButtonWithLabel("Replace All")
	replaceAll.SetFocusOnClick(false)
	replaceAll.SetTooltipText("Replace every match. Undo puts them all back at once")
	replaceAll.ConnectClicked(a.replaceEvery)

	bottom := gtk.NewBox(gtk.OrientationHorizontal, 6)
	bottom.Append(spacer)
	bottom.Append(fb.replaceEntry)
	bottom.Append(replace)
	bottom.Append(replaceAll)

	fb.replaceReveal = gtk.NewRevealer()
	fb.replaceReveal.SetTransitionType(gtk.RevealerTransitionTypeSlideDown)
	fb.replaceReveal.SetChild(bottom)

	box := gtk.NewBox(gtk.OrientationVertical, 6)
	box.AddCSSClass("format-bar") // the toolbar's padding and rule underneath
	box.AddCSSClass("find-bar")
	box.Append(top)
	box.Append(fb.replaceReveal)

	// Escape closes the bar from either entry. It is taken on the way down, above
	// the entries, which have their own ideas about Escape.
	esc := gtk.NewEventControllerKey()
	esc.SetPropagationPhase(gtk.PhaseCapture)
	esc.ConnectKeyPressed(func(keyval, _ uint, _ gdk.ModifierType) bool {
		if keyval != gdk.KEY_Escape {
			return false
		}
		a.closeFind(true)
		return true
	})
	box.AddController(esc)

	fb.revealer = gtk.NewRevealer()
	fb.revealer.SetTransitionType(gtk.RevealerTransitionTypeSlideDown)
	fb.revealer.SetChild(box)

	a.editor.SetFindListener(fb.show)
	return fb.revealer
}

// findIconButton is a flat button for the find bar. label stands in when the
// icon theme has no icon by that name, as in the formatting toolbar.
//
// It does not take focus when clicked: the caret should stay in the entry it
// was in, so that stepping through matches does not cost the search box.
func findIconButton(icon, label, tooltip string, action func()) *gtk.Button {
	var btn *gtk.Button
	if hasIcon(icon) {
		btn = gtk.NewButtonFromIconName(icon)
	} else {
		btn = gtk.NewButtonWithLabel(label)
	}
	btn.AddCSSClass("flat")
	btn.SetFocusOnClick(false)
	btn.SetTooltipText(tooltip)
	btn.ConnectClicked(action)
	return btn
}

// enterKey makes a key controller that calls fn when Enter is pressed, telling
// it whether Shift was down, and keeps the entry from seeing the key.
func enterKey(fn func(shift bool)) *gtk.EventControllerKey {
	kc := gtk.NewEventControllerKey()
	kc.SetPropagationPhase(gtk.PhaseCapture)
	kc.ConnectKeyPressed(func(keyval, _ uint, state gdk.ModifierType) bool {
		switch keyval {
		case gdk.KEY_Return, gdk.KEY_KP_Enter, gdk.KEY_ISO_Enter:
			fn(state&gdk.ShiftMask != 0)
			return true
		}
		return false
	})
	return kc
}

// show puts where the search stands on the counter, and flags the entry when a
// query finds nothing.
func (fb *findBar) show(current, count int) {
	if fb.holdCounter {
		return
	}
	switch {
	case fb.entry.Text() == "":
		fb.counter.SetText("")
		fb.entry.RemoveCSSClass("error")
	case count == 0:
		fb.counter.SetText("No matches")
		fb.entry.AddCSSClass("error")
	default:
		fb.counter.SetText(fmt.Sprintf("%d of %d", current, count))
		fb.entry.RemoveCSSClass("error")
	}
}

// noteOpen reports whether a note is on screen, as opposed to the home screen.
func (a *App) noteOpen() bool {
	return a.currentNote != "" && a.centerStack != nil && a.centerStack.VisibleChildName() == "editor"
}

// openFind shows the find bar with the caret in its entry. Text selected on
// one line in the note becomes the query.
func (a *App) openFind(withReplace bool) {
	fb := findBars[a]
	if fb == nil || a.editor == nil {
		return
	}
	fb.revealer.SetRevealChild(true)
	if withReplace {
		fb.replaceToggle.SetActive(true)
	}
	if seed := a.editor.FindSeed(); seed != "" {
		fb.entry.SetText(seed)
	}
	fb.entry.GrabFocus()
	fb.entry.SelectRegion(0, -1)
	a.runFind() // closing the bar cleared the highlights; an old query brings them back
}

// closeFind hides the bar and takes the highlights off the note. refocus puts
// the caret back in the text.
func (a *App) closeFind(refocus bool) {
	fb := findBars[a]
	if fb == nil || !fb.revealer.RevealChild() {
		return
	}
	fb.revealer.SetRevealChild(false)
	fb.holdCounter = false
	if a.editor == nil {
		return
	}
	a.editor.ClearFind()
	if refocus {
		a.editor.Focus()
	}
}

// runFind searches for what the entry holds, under the Match case setting.
func (a *App) runFind() {
	fb := findBars[a]
	// The entry reports a changed query a moment after the keystroke, which can
	// be after Escape. A search then would light the note up under a closed bar.
	if fb == nil || a.editor == nil || !fb.revealer.RevealChild() {
		return
	}
	fb.holdCounter = false
	a.editor.SetFindQuery(fb.entry.Text(), fb.matchCase.Active())
}

// stepFind goes to the next or previous match. With the bar closed there is no
// search to step through, so it opens the bar.
func (a *App) stepFind(forward bool) {
	fb := findBars[a]
	if fb == nil || a.editor == nil || !a.noteOpen() {
		return
	}
	if !fb.revealer.RevealChild() {
		a.openFind(false)
		return
	}
	fb.holdCounter = false
	if forward {
		a.editor.FindNext()
	} else {
		a.editor.FindPrev()
	}
}

// replaceOne replaces the current match and moves to the next.
func (a *App) replaceOne() {
	fb := findBars[a]
	if fb == nil || a.editor == nil {
		return
	}
	fb.holdCounter = false
	a.editor.ReplaceCurrent(fb.replaceEntry.Text())
}

// replaceEvery replaces every match, and says how many there were.
func (a *App) replaceEvery() {
	fb := findBars[a]
	if fb == nil || a.editor == nil {
		return
	}
	fb.holdCounter = true // the editor reports the count left as it finishes; that is not the answer
	n := a.editor.ReplaceAll(fb.replaceEntry.Text())
	if n == 0 {
		fb.holdCounter = false
		fb.show(a.editor.CurrentMatch(), a.editor.FindCount())
		return
	}
	fb.counter.SetText(fmt.Sprintf("Replaced %d", n))
	fb.entry.RemoveCSSClass("error")
}

// buildStatusBar is the footer, and the one place the app reports on the note
// in front of you: word and character counts, reading time, how far through its
// checklist you are, where it lives, and whether it is saved.
//
// The save state and the task count used to sit up beside the title and in the
// formatting toolbar. They are status, not controls, and having them in three
// places meant the header carried information the eye had to hunt through on
// the way to the note's name.
func (a *App) buildStatusBar() *gtk.Box {
	bar := gtk.NewBox(gtk.OrientationHorizontal, 12)
	bar.AddCSSClass("status-bar")

	// Words, not words and characters. A character count is a thing a form
	// with a limit needs; a note does not. It stays available on hover rather
	// than taking a permanent place in the footer.
	a.statusLabel = gtk.NewLabel("0 words")
	a.statusLabel.SetXAlign(0)
	bar.Append(a.statusLabel)

	a.readTimeLabel = gtk.NewLabel("")
	a.readTimeLabel.AddCSSClass("status-dim")
	bar.Append(a.readTimeLabel)

	a.taskProgress = gtk.NewLabel("")
	a.taskProgress.AddCSSClass("task-progress")
	a.taskProgress.SetVisible(false)
	bar.Append(a.taskProgress)

	spacer := gtk.NewBox(gtk.OrientationHorizontal, 0)
	spacer.SetHExpand(true)
	bar.Append(spacer)

	bar.Append(a.buildSavePill())
	return bar
}

// showWelcome switches the center panel to the home screen.
func (a *App) showWelcome() {
	if a.centerStack == nil {
		return
	}
	a.closeFind(false) // the note it searched is going away
	// Typing from the last moment before the autosave would otherwise go
	// with the note: the editor is emptied below.
	a.flushDirty()
	a.backlinksGen++ // an answer for the note being left is no longer wanted
	a.welcomeBuilt = true
	a.currentNote = ""
	a.dirty = false
	if a.editor != nil {
		a.editor.SetContent("")
	}
	if a.tree != nil {
		a.tree.SetCurrent("")
	}
	a.refreshWelcome()
	a.centerStack.SetVisibleChildName("welcome")
	a.setWindowSubtitle("")
	a.syncNoteActions()
	if a.backlinksBar != nil {
		a.backlinksBar.SetVisible(false)
	}
}

// refreshWelcome brings the home screen up to date. The page itself is built
// once, the first time it is shown, and afterwards only its contents change —
// rebuilding the widgets each time would keep every previous copy resident.
func (a *App) refreshWelcome() {
	a.refreshTasksSoon() // every change to the vault comes through here
	if a.centerStack == nil || !a.welcomeBuilt {
		return
	}
	if a.centerStack.ChildByName("welcome") == nil || len(a.recents) == 0 {
		if old := a.centerStack.ChildByName("welcome"); old != nil {
			a.centerStack.Remove(old)
		}
		a.centerStack.AddNamed(a.buildWelcome(), "welcome")
		return
	}
	a.refreshRecents()
}

// showEditorPage switches the center panel to the note editor.
func (a *App) showEditorPage() {
	if a.centerStack != nil {
		a.centerStack.SetVisibleChildName("editor")
	}
}

func (a *App) updateStats() {
	if a.editor != nil {
		a.statsDirty = false
		a.updateStatsWith(a.editor.Content())
	}
}

// updateStatsWith refreshes the footer and the checklist progress from content
// that has already been fetched once. Words, characters and tasks are counted
// in a single walk of the document — this runs after every pause in typing.
func (a *App) updateStatsWith(content string) {
	words, chars, done, total := documentStats(content)
	if a.statusLabel != nil {
		a.statusLabel.SetText(plural(words, "word"))
		a.statusLabel.SetTooltipText(plural(chars, "character"))
		if a.readTimeLabel != nil {
			a.readTimeLabel.SetText(readingTime(words))
		}
	}
	if a.taskProgress != nil {
		if total == 0 {
			a.taskProgress.SetVisible(false)
		} else {
			a.taskProgress.SetVisible(true)
			a.taskProgress.SetText(fmt.Sprintf("%d of %d tasks", done, total))
			if done == total {
				a.taskProgress.AddCSSClass("task-complete")
			} else {
				a.taskProgress.RemoveCSSClass("task-complete")
			}
		}
	}
}

// documentStats counts everything the footer shows in a single walk of the
// note: words, characters, and how many of its tasks are done. It allocates
// nothing — strings.Fields would build a slice of every word in the note, and
// counting the tasks separately would mean a second pass.
func documentStats(s string) (words, chars, done, total int) {
	inWord := false
	lineStart := 0
	for i, r := range s {
		chars++
		switch r {
		case '\n':
			done, total = countTask(s[lineStart:i], done, total)
			lineStart = i + len("\n")
			inWord = false
		case ' ', '\t', '\r', '\v', '\f':
			inWord = false
		default:
			if !inWord {
				words++
				inWord = true
			}
		}
	}
	done, total = countTask(s[lineStart:], done, total)
	return words, chars, done, total
}

// countTask folds one line into the running task tally.
func countTask(line string, done, total int) (int, int) {
	checked, ok := checklist.TaskLine(line)
	if !ok {
		return done, total
	}
	if checked {
		done++
	}
	return done, total + 1
}

// readingTime is a rough estimate at 200 words per minute, shown only once a
// note is long enough for it to mean anything.
func readingTime(words int) string {
	if words < 50 {
		return ""
	}
	mins := (words + 199) / 200
	return fmt.Sprintf("~%d min read", mins)
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// refreshHeader syncs the title row, breadcrumb, footer, and window title with
// the open note.
func (a *App) refreshHeader() {
	name := path.Base(a.currentNote)
	if a.titleEntry != nil {
		a.titleEntry.Buffer().SetText(name, -1)
	}
	if a.breadcrumb != nil {
		folder := path.Dir(a.currentNote)
		if folder == "." || folder == "/" || a.currentNote == "" {
			a.breadcrumb.SetVisible(false)
		} else {
			a.breadcrumb.SetText(strings.ReplaceAll(folder, "/", " / "))
			a.breadcrumb.SetVisible(true)
		}
	}
	if a.titleEntry != nil {
		tip := "The note's name is its filename. Press Enter to rename"
		if a.currentNote != "" && a.store != nil {
			// The file as it is on disk: its extension depends on the vault's
			// format, and on whether the note is locked.
			tip += "\n\nIn your vault: " + a.store.NoteFileName(a.currentNote)
		}
		a.titleEntry.SetTooltipText(tip)
	}
	a.setWindowSubtitle(name)
	a.updateStats()
	a.setSaveState(saveSaved)
}

// applyFormatBarVisibility shows or hides the formatting toolbar to match the
// setting.
func (a *App) applyFormatBarVisibility() {
	if a.formatBar != nil {
		a.formatBar.SetVisible(a.cfg.ShowFormatBar)
	}
}
