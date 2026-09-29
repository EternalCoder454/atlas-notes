package app

import (
	"log"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-notes/internal/editor"
)

// checklistTemplate seeds a new checklist so the feature explains itself: the
// first line is already a task, and the markdown that produces one is visible.
const checklistTemplate = "- [ ] First task\n- [ ] Second task\n\n"

// registerActions wires every command to a GAction plus its accelerator. Going
// through the action map (rather than ad-hoc signal handlers) means the menu,
// the buttons, and the keyboard all trigger exactly the same code, and the
// shortcuts work no matter which panel has focus.
func (a *App) registerActions() {
	type binding struct {
		name   string
		accels []string
		fn     func()
	}
	bindings := []binding{
		{"new-note", []string{"<Control>n"}, a.actionNewNote},
		{"new-checklist", []string{"<Control>t"}, a.actionNewChecklist},
		{"new-folder", []string{"<Control><Shift>n"}, a.actionNewFolder},
		{"new-from-template", []string{"<Control><Alt>n"}, a.actionNewFromTemplate},
		{"today", []string{"<Control>d"}, a.actionToday},
		{"save", []string{"<Control>s"}, a.saveCurrent},
		{"rename", []string{"F2"}, a.actionRename},
		{"export", []string{"<Control><Shift>e"}, a.exportCurrent},
		{"history", []string{"<Control><Shift>h"}, a.showHistory},
		{"insert-image", []string{"<Control><Shift>i"}, a.insertImage},
		{"search", []string{"<Control>k", "<Control>p", "<Control><Shift>f"}, a.actionFocusSearch},
		{"find", []string{"<Control>f"}, a.actionFind},
		{"find-replace", []string{"<Control>r"}, a.actionFindReplace},
		{"find-next", []string{"<Control>g", "F3"}, func() { a.stepFind(true) }},
		{"find-previous", []string{"<Control><Shift>g", "<Shift>F3"}, func() { a.stepFind(false) }},
		{"home", []string{"<Control>h"}, a.showWelcome},
		{"toggle-vault", []string{"F9"}, func() { a.toggle(a.leftToggle) }},
		{"toggle-assistant", []string{"F10"}, func() { a.toggle(a.rightToggle) }},
		{"lock-now", []string{"<Control><Shift>l"}, a.lockNow},
		{"settings", []string{"<Control>comma"}, a.showSettings},
		{"shortcuts", []string{"<Control>question", "<Control>slash"}, a.showShortcuts},
		{"about", nil, a.showAbout},
		{"focus-assistant", []string{"<Control>l"}, a.focusAssistant},
		{"bold", []string{"<Control>b"}, func() { a.withEditor((*editor.Editor).ToggleBold) }},
		{"italic", []string{"<Control>i"}, func() { a.withEditor((*editor.Editor).ToggleItalic) }},
		{"code", []string{"<Control>e"}, func() { a.withEditor((*editor.Editor).ToggleCode) }},
		{"task", []string{"<Control><Shift>t"}, func() { a.withEditor((*editor.Editor).ToggleTask) }},
		{"heading1", []string{"<Control>1"}, func() { a.withEditor(func(e *editor.Editor) { e.SetHeading(1) }) }},
		{"heading2", []string{"<Control>2"}, func() { a.withEditor(func(e *editor.Editor) { e.SetHeading(2) }) }},
		{"body-text", []string{"<Control>0"}, func() { a.withEditor(func(e *editor.Editor) { e.SetHeading(0) }) }},
	}
	for _, b := range bindings {
		fn := b.fn
		act := gio.NewSimpleAction(b.name, nil)
		act.ConnectActivate(func(*glib.Variant) { fn() })
		if noteActionNames[b.name] {
			act.SetEnabled(false) // until a note is open; see syncNoteActions
			noteActions[a] = append(noteActions[a], act)
		}
		a.adw.AddAction(act)
		if len(b.accels) > 0 {
			a.adw.SetAccelsForAction("app."+b.name, b.accels)
		}
	}
}

// noteActionNames are the commands that act on the note on screen. They are
// disabled, not just ignored, while there is none, so their menu entries grey
// out and their shortcuts do nothing on the home screen.
var noteActionNames = map[string]bool{"history": true, "insert-image": true}

// noteActions holds each App's note commands, so that syncNoteActions can reach
// them: the action map hands back a generic action, which cannot be enabled or
// disabled.
var noteActions = map[*App][]*gio.SimpleAction{}

// syncNoteActions enables the note commands while a note is open. It runs
// whenever the center panel changes what it shows (see watchNoteOpen).
func (a *App) syncNoteActions() {
	open := a.noteOpen()
	for _, act := range noteActions[a] {
		act.SetEnabled(open)
	}
}

// watchNoteOpen keeps the note commands in step with the center panel: they
// follow the page it shows. openNote and showWelcome also sync them directly,
// which covers opening a note while the editor page is already showing, as it
// is when the app starts.
func (a *App) watchNoteOpen() {
	if a.centerStack != nil {
		a.centerStack.NotifyProperty("visible-child-name", a.syncNoteActions)
	}
	a.syncNoteActions()
}

// toggle flips a header-bar toggle, which in turn shows or hides its panel.
func (a *App) toggle(btn *gtk.ToggleButton) {
	if btn != nil {
		btn.SetActive(!btn.Active())
	}
}

// actionNewNote creates an untitled note in the folder currently selected in
// the vault, opens it, and puts the caret in the title field.
func (a *App) actionNewNote() { a.createNote("", "Untitled note") }

// actionNewChecklist creates a note that already contains tasks.
func (a *App) actionNewChecklist() { a.createNote(checklistTemplate, "Checklist") }

func (a *App) createNote(body, base string) {
	if a.store == nil {
		return
	}
	folder := ""
	if a.tree != nil {
		folder = a.tree.SelectedFolder()
	}
	rel := a.store.UniqueName(folder, base)
	if err := a.store.WriteNote(rel, body); err != nil {
		log.Printf("atlas-notes: new note: %v", err)
		a.toast("Could not create the note")
		return
	}
	if a.tree != nil {
		a.tree.ForceRefresh()
	}
	a.openNote(rel)
	if a.titleEntry != nil {
		a.titleEntry.GrabFocus()
		a.titleEntry.SelectRegion(0, -1) // the name is a placeholder: type over it
	}
}

// actionNewFolder asks for a name and creates the folder in the vault.
func (a *App) actionNewFolder() {
	if a.tree == nil {
		return
	}
	a.tree.PromptNewFolder()
}

// actionRename moves focus to the title field, which is where renaming happens.
func (a *App) actionRename() {
	if a.currentNote == "" || a.titleEntry == nil {
		return
	}
	a.titleEntry.GrabFocus()
	a.titleEntry.SelectRegion(0, -1)
}

// actionFocusSearch reveals the vault panel and focuses its search field.
func (a *App) actionFocusSearch() {
	if a.tree == nil {
		return
	}
	if a.leftToggle != nil && !a.leftToggle.Active() {
		a.leftToggle.SetActive(true)
	}
	a.tree.FocusSearch()
}

// actionFind opens the find bar in the open note. On the home screen there is
// no note to search, and Ctrl+F keeps the meaning it had before the bar
// existed: the vault's search.
func (a *App) actionFind() {
	if !a.noteOpen() {
		a.actionFocusSearch()
		return
	}
	a.openFind(false)
}

// actionFindReplace opens the find bar with its replace row showing.
func (a *App) actionFindReplace() {
	if !a.noteOpen() {
		a.toast("Open a note to find and replace in it")
		return
	}
	a.openFind(true)
}

// focusAssistant reveals the assistant panel and focuses its prompt field.
func (a *App) focusAssistant() {
	if a.sidebar == nil {
		return
	}
	if a.rightToggle != nil && !a.rightToggle.Active() {
		a.rightToggle.SetActive(true)
	}
	a.sidebar.FocusInput()
}

// toast shows a transient message in the window.
func (a *App) toast(text string) {
	if a.toastOverlay == nil {
		return
	}
	a.toastOverlay.AddToast(adw.NewToast(text))
}

// shortcutRow is one line in the keyboard shortcuts dialog.
type shortcutRow struct {
	keys, what string
}

// showShortcuts lists every keyboard shortcut, grouped the way the app is used.
func (a *App) showShortcuts() {
	dialog := adw.NewDialog()
	dialog.SetTitle("Keyboard Shortcuts")
	dialog.SetContentWidth(560)
	dialog.SetContentHeight(620)

	body := gtk.NewBox(gtk.OrientationVertical, 18)
	body.SetMarginTop(18)
	body.SetMarginBottom(18)
	body.SetMarginStart(18)
	body.SetMarginEnd(18)

	groups := []struct {
		title string
		rows  []shortcutRow
	}{
		{"Notes", []shortcutRow{
			{"Ctrl+N", "New note"},
			{"Ctrl+T", "New checklist"},
			{"Ctrl+Alt+N", "New note from a template"},
			{"Ctrl+Shift+N", "New folder"},
			{"Ctrl+D", "Open today's note"},
			{"Ctrl+S", "Save now"},
			{"F2", "Rename the open note"},
			{"Ctrl+Shift+H", "Version history of the open note"},
		}},
		{"Moving around", []shortcutRow{
			{"Ctrl+K / Ctrl+P / Ctrl+Shift+F", "Find a note"},
			{"Ctrl+H", "Home screen"},
			{"F9", "Show or hide the vault"},
			{"F10", "Show or hide the assistant"},
			{"Ctrl+L", "Ask the assistant"},
		}},
		{"Find in the open note", []shortcutRow{
			{"Ctrl+F", "Find"},
			{"Ctrl+R", "Find and replace"},
			{"Ctrl+G / F3", "Next match"},
			{"Ctrl+Shift+G / Shift+F3", "Previous match"},
			{"Enter", "Next match, in the find box"},
			{"Shift+Enter", "Previous match, in the find box"},
			{"Esc", "Close the find bar"},
		}},
		{"Formatting", []shortcutRow{
			{"Ctrl+B", "Bold"},
			{"Ctrl+I", "Italic"},
			{"Ctrl+E", "Inline code"},
			{"Ctrl+1 / Ctrl+2", "Heading / subheading"},
			{"Ctrl+0", "Plain text"},
			{"Ctrl+Shift+T", "Turn the line into a task"},
			{"Ctrl+Shift+I", "Insert an image from a file"},
		}},
		{"App", []shortcutRow{
			{"Ctrl+Shift+L", "Lock protected notes now"},
			{"Ctrl+,", "Settings"},
			{"Ctrl+?", "This list"},
		}},
	}

	for _, g := range groups {
		heading := gtk.NewLabel(g.title)
		heading.SetXAlign(0)
		heading.AddCSSClass("shortcut-heading")
		body.Append(heading)

		card := gtk.NewBox(gtk.OrientationVertical, 0)
		card.AddCSSClass("shortcut-card")
		for i, r := range g.rows {
			if i > 0 {
				card.Append(gtk.NewSeparator(gtk.OrientationHorizontal))
			}
			row := gtk.NewBox(gtk.OrientationHorizontal, 12)
			row.AddCSSClass("shortcut-row")
			what := gtk.NewLabel(r.what)
			what.SetXAlign(0)
			what.SetHExpand(true)
			row.Append(what)
			for _, key := range strings.Split(r.keys, " / ") {
				cap := gtk.NewLabel(key)
				cap.AddCSSClass("keycap")
				row.Append(cap)
			}
			card.Append(row)
		}
		body.Append(card)
	}

	scroll := gtk.NewScrolledWindow()
	scroll.SetChild(body)
	scroll.SetVExpand(true)

	header := adw.NewHeaderBar()
	tv := adw.NewToolbarView()
	tv.AddTopBar(header)
	tv.SetContent(scroll)
	dialog.SetChild(tv)
	dialog.Present(a.win)
}

// showAbout presents the standard libadwaita about dialog.
func (a *App) showAbout() {
	about := adw.NewAboutDialog()
	about.SetApplicationName("Atlas Notes")
	about.SetApplicationIcon(appID)
	about.SetVersion(version)
	about.SetDeveloperName("EternalHell")
	about.SetLicenseType(gtk.LicenseMITX11)
	about.SetComments("A fast, local-first notes and checklist app for Linux, with an optional local AI assistant. Your notes never leave this machine.")
	about.SetWebsite("https://github.com/EternalCoder454/atlas-notes")
	about.SetIssueURL("https://github.com/EternalCoder454/atlas-notes/issues")
	about.Present(a.win)
}
