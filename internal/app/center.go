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

// noteFileSuffix is the on-disk extension shown in the status bar.
const noteFileSuffix = ".md.zst"

// buildCenter assembles the center panel as a stack of two pages: the welcome
// home screen, and the note editor (title row · formatting toolbar · text ·
// status bar).
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
	page.Append(a.buildFormatBar())

	a.editor = editor.New()
	a.editor.OnChanged = a.onEditorChanged
	a.editor.OnReparsed = a.onEditorReparsed

	clamp := adw.NewClamp()
	clamp.SetMaximumSize(editorMaxWidth)
	clamp.SetTighteningThreshold(editorMaxWidth)
	clamp.SetChild(a.editor.Widget())
	clamp.SetVExpand(true)
	page.Append(clamp)

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
	a.titleEntry.SetTooltipText("The note's name is its filename — press Enter to rename")
	a.titleEntry.ConnectActivate(a.onTitleActivate)
	titles.Append(a.titleEntry)

	row.Append(titles)
	row.Append(a.buildSavePill())
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

// hasIcon reports whether the current icon theme can draw name.
func hasIcon(name string) bool {
	display := gdk.DisplayGetDefault()
	if display == nil {
		return false
	}
	return gtk.IconThemeGetForDisplay(display).HasIcon(name)
}

// buildFormatBar is the formatting toolbar. Everything it offers was previously
// available only by typing the Markdown by hand, which meant new users had no
// way to discover it.
func (a *App) buildFormatBar() *gtk.Box {
	bar := gtk.NewBox(gtk.OrientationHorizontal, 2)
	bar.AddCSSClass("format-bar")

	groups := [][]formatButton{{
		{"format-text-bold-symbolic", "B", "Bold (Ctrl+B)", func() { a.withEditor((*editor.Editor).ToggleBold) }},
		{"format-text-italic-symbolic", "I", "Italic (Ctrl+I)", func() { a.withEditor((*editor.Editor).ToggleItalic) }},
		{"format-text-strikethrough-symbolic", "S", "Strikethrough", func() { a.withEditor((*editor.Editor).ToggleStrike) }},
		{"format-text-code-symbolic", "</>", "Inline code (Ctrl+E)", func() { a.withEditor((*editor.Editor).ToggleCode) }},
	}, {
		{"", "H1", "Heading (Ctrl+1)", func() { a.withEditor(func(e *editor.Editor) { e.SetHeading(1) }) }},
		{"", "H2", "Subheading (Ctrl+2)", func() { a.withEditor(func(e *editor.Editor) { e.SetHeading(2) }) }},
		{"", "¶", "Plain text (Ctrl+0)", func() { a.withEditor(func(e *editor.Editor) { e.SetHeading(0) }) }},
	}, {
		{"view-list-symbolic", "•", "Bullet list", func() { a.withEditor((*editor.Editor).ToggleBullet) }},
		{"object-select-symbolic", "☑", "Task (Ctrl+Shift+T)", func() { a.withEditor((*editor.Editor).ToggleTask) }},
		{"format-justify-left-symbolic", "❝", "Quote", func() { a.withEditor((*editor.Editor).ToggleQuote) }},
		{"", "—", "Divider", func() { a.withEditor((*editor.Editor).InsertDivider) }},
	}}

	for i, group := range groups {
		if i > 0 {
			sep := gtk.NewSeparator(gtk.OrientationVertical)
			sep.AddCSSClass("format-sep")
			bar.Append(sep)
		}
		for _, b := range group {
			var btn *gtk.Button
			if b.icon != "" && hasIcon(b.icon) {
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

	spacer := gtk.NewBox(gtk.OrientationHorizontal, 0)
	spacer.SetHExpand(true)
	bar.Append(spacer)

	a.taskProgress = gtk.NewLabel("")
	a.taskProgress.AddCSSClass("task-progress")
	a.taskProgress.SetVisible(false)
	bar.Append(a.taskProgress)
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

// buildStatusBar is the footer: word and character counts, reading time, and
// how far through the note's checklist you are.
func (a *App) buildStatusBar() *gtk.Box {
	bar := gtk.NewBox(gtk.OrientationHorizontal, 12)
	bar.AddCSSClass("status-bar")

	a.statusLabel = gtk.NewLabel("0 words")
	a.statusLabel.SetXAlign(0)
	bar.Append(a.statusLabel)

	a.readTimeLabel = gtk.NewLabel("")
	a.readTimeLabel.AddCSSClass("status-dim")
	bar.Append(a.readTimeLabel)

	spacer := gtk.NewBox(gtk.OrientationHorizontal, 0)
	spacer.SetHExpand(true)
	bar.Append(spacer)

	a.vaultLabel = gtk.NewLabel("")
	a.vaultLabel.AddCSSClass("status-dim")
	a.vaultLabel.SetTooltipText("This note on disk, inside your vault folder")
	bar.Append(a.vaultLabel)
	return bar
}

// showWelcome switches the center panel to the home screen.
func (a *App) showWelcome() {
	if a.centerStack == nil {
		return
	}
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
}

// refreshWelcome brings the home screen up to date. The page itself is built
// once, the first time it is shown, and afterwards only its contents change —
// rebuilding the widgets each time would keep every previous copy resident.
func (a *App) refreshWelcome() {
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

// updateStatsWith refreshes the footer and the checklist progress from content
// that has already been fetched once. Words, characters and tasks are counted
// in a single walk of the document — this runs after every pause in typing.
func (a *App) updateStatsWith(content string) {
	words, chars := countWords(content)
	if a.statusLabel != nil {
		a.statusLabel.SetText(fmt.Sprintf("%s · %s", plural(words, "word"), plural(chars, "character")))
		if a.readTimeLabel != nil {
			a.readTimeLabel.SetText(readingTime(words))
		}
	}
	if a.taskProgress != nil {
		done, total := checklist.Progress(content)
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

// countWords returns the word and character counts of s in one pass, without
// the slice strings.Fields would allocate for every word in the note.
func countWords(s string) (words, chars int) {
	inWord := false
	for _, r := range s {
		chars++
		switch r {
		case ' ', '\t', '\n', '\r', '\v', '\f':
			inWord = false
		default:
			if !inWord {
				words++
				inWord = true
			}
		}
	}
	return words, chars
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
	if a.vaultLabel != nil {
		if a.currentNote == "" {
			a.vaultLabel.SetText("")
		} else {
			a.vaultLabel.SetText(a.currentNote + noteFileSuffix)
		}
	}
	a.setWindowSubtitle(name)
	a.updateStats()
	a.setSaveState(saveSaved)
}
