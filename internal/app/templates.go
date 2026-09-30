package app

import (
	"errors"
	"log"
	"path"
	"strings"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"atlas-notes/internal/storage"
)

// The daily note and notes started from a template. Both are ordinary notes in
// ordinary folders (see storage/templates.go), so all this file does is ask the
// store for one and open it.

// actionToday opens today's note, making it first if there is none yet.
func (a *App) actionToday() {
	if a.store == nil {
		return
	}
	rel, _, err := a.store.DailyNote(time.Now())
	if err != nil {
		log.Printf("atlas-notes: daily note: %v", err)
		if errors.Is(err, storage.ErrLocked) {
			a.toast("Today's note belongs in a locked folder. Unlock the vault and try again.")
		} else {
			a.toast("Couldn't open today's note: " + err.Error())
		}
		return
	}
	if a.tree != nil {
		a.tree.ForceRefresh() // it may be new
	}
	a.openNote(rel)
}

// templateNameParts splits a template's path into the name it is offered under
// and the folders between "Templates/" and it, which say which kind it is when
// there are many. "Templates/Work/Standup" is "Standup" and "Work".
func templateNameParts(rel string) (name, folder string) {
	name = path.Base(rel)
	folder = path.Dir(rel)
	folder = strings.TrimPrefix(folder, storage.TemplatesFolder)
	folder = strings.Trim(folder, "/")
	return name, folder
}

// inTemplates reports whether a folder is the templates folder or inside it. A
// note made from a template lands beside the selection, and a new note in the
// templates folder would itself become a template, which nobody meant.
func inTemplates(folder string) bool {
	return folder == storage.TemplatesFolder || strings.HasPrefix(folder, storage.TemplatesFolder+"/")
}

// actionNewFromTemplate lists the templates and starts a note from the one
// chosen.
func (a *App) actionNewFromTemplate() {
	if a.store == nil {
		return
	}
	templates, err := a.store.Templates()
	if err != nil {
		log.Printf("atlas-notes: list templates: %v", err)
		a.toast("Couldn't list the templates: " + err.Error())
		return
	}

	dialog := adw.NewDialog()
	dialog.SetTitle("New from Template")
	dialog.SetContentWidth(420)
	dialog.SetContentHeight(440)

	toolbar := adw.NewToolbarView()
	toolbar.AddTopBar(adw.NewHeaderBar())

	if len(templates) == 0 {
		// With nothing to choose from, the dialog is the place to learn what a
		// template is, and to make the folder that holds them.
		empty := adw.NewStatusPage()
		empty.SetIconName(iconName("atlasnotes-note-symbolic", "atlasnotes-folder-symbolic"))
		empty.SetDescription("Any note in a folder called Templates can start a new note. " +
			"Placeholders such as {{date}}, {{time}} and {{title}} are filled in.")
		create := gtk.NewButtonWithLabel("Create Templates Folder")
		create.AddCSSClass("suggested-action")
		create.AddCSSClass("pill")
		create.SetHAlign(gtk.AlignCenter)
		create.ConnectClicked(func() {
			if err := a.store.CreateFolder(storage.TemplatesFolder); err != nil {
				log.Printf("atlas-notes: create templates folder: %v", err)
				a.toast("Couldn't create the Templates folder: " + err.Error())
				return
			}
			if a.tree != nil {
				a.tree.ForceRefresh()
			}
			dialog.Close()
			a.toast("Created the Templates folder. Add notes to it to use them as templates.")
		})
		empty.SetChild(create)
		empty.SetVExpand(true)
		toolbar.SetContent(empty)
		dialog.SetChild(toolbar)
		dialog.Present(a.win)
		return
	}

	list := gtk.NewListBox()
	list.SetSelectionMode(gtk.SelectionNone)
	list.SetActivateOnSingleClick(true)
	list.AddCSSClass("boxed-list")
	list.SetMarginTop(12)
	list.SetMarginBottom(12)
	list.SetMarginStart(12)
	list.SetMarginEnd(12)
	list.SetVAlign(gtk.AlignStart)
	for _, rel := range templates {
		name, folder := templateNameParts(rel)
		row := gtk.NewListBoxRow()
		box := gtk.NewBox(gtk.OrientationHorizontal, 10)
		box.SetMarginTop(10)
		box.SetMarginBottom(10)
		box.SetMarginStart(12)
		box.SetMarginEnd(12)

		title := gtk.NewLabel(name)
		title.SetXAlign(0)
		title.SetEllipsize(pango.EllipsizeEnd)
		box.Append(title)

		if folder != "" {
			where := gtk.NewLabel(folder)
			where.SetXAlign(0)
			where.SetHExpand(true)
			where.SetEllipsize(pango.EllipsizeStart)
			where.AddCSSClass("dim-label")
			box.Append(where)
		}
		row.SetChild(box)
		list.Append(row)
	}
	list.ConnectRowActivated(func(row *gtk.ListBoxRow) {
		i := row.Index()
		if i < 0 || i >= len(templates) {
			return
		}
		rel := templates[i]
		dialog.Close()
		a.askTemplateTitle(rel)
	})
	scroll := gtk.NewScrolledWindow()
	scroll.SetChild(list)
	scroll.SetVExpand(true)
	toolbar.SetContent(scroll)
	dialog.SetChild(toolbar)
	dialog.Present(a.win)
}

// askTemplateTitle asks what to call the new note, offering the template's own
// name, and makes the note.
func (a *App) askTemplateTitle(templateRel string) {
	name, _ := templateNameParts(templateRel)
	alert := adw.NewAlertDialog("New note from “"+name+"”", "What should the note be called?")
	entry := gtk.NewEntry()
	entry.Buffer().SetText(name, -1)
	entry.SetHExpand(true)
	entry.SetActivatesDefault(true) // Enter creates the note
	alert.SetExtraChild(entry)
	alert.AddResponse("cancel", "Cancel")
	alert.AddResponse("create", "Create")
	alert.SetResponseAppearance("create", adw.ResponseSuggested)
	alert.SetDefaultResponse("create")
	alert.SetCloseResponse("cancel")
	alert.ConnectResponse(func(response string) {
		if response != "create" {
			return
		}
		a.newFromTemplate(templateRel, strings.TrimSpace(entry.Buffer().Text()))
	})
	alert.Present(a.win)
	entry.GrabFocus()
	entry.SelectRegion(0, -1) // the name is a suggestion: type over it
}

// newFromTemplate makes the note beside the selection and opens it. An empty
// title is the template's name, which the store fills in.
func (a *App) newFromTemplate(templateRel, title string) {
	folder := ""
	if a.tree != nil {
		folder = a.tree.SelectedFolder()
	}
	if inTemplates(folder) {
		folder = ""
	}
	rel, err := a.store.NewFromTemplate(templateRel, folder, title, time.Now())
	if err != nil {
		log.Printf("atlas-notes: new from template %q: %v", templateRel, err)
		if errors.Is(err, storage.ErrLocked) {
			a.toast("Unlock the vault to use that template.")
		} else {
			a.toast("Couldn't create the note: " + err.Error())
		}
		return
	}
	if a.tree != nil {
		a.tree.ForceRefresh()
	}
	a.openNote(rel)
}
