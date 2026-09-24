package ui

import (
	"log"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// The vault panel's editing side: the context menu and the dialogs behind it.
// Every path here changes what is on disk and then refreshes the panel from
// the vault (ForceRefresh), which is what keeps the cached copy in tree.go
// honest.

// showContextMenu pops up the create/rename/delete menu at (x,y) in parent's
// coordinate space. n is the row under the pointer, or nil for empty space.
func (t *Tree) showContextMenu(parent gtk.Widgetter, x, y float64, n *node) {
	pop := gtk.NewPopover()
	pop.SetAutohide(true)
	pop.SetHasArrow(false)
	rect := gdk.NewRectangle(int(x), int(y), 1, 1)
	pop.SetPointingTo(&rect)

	box := gtk.NewBox(gtk.OrientationVertical, 2)
	box.SetMarginTop(4)
	box.SetMarginBottom(4)
	box.SetMarginStart(4)
	box.SetMarginEnd(4)

	add := func(label string, destructive bool, fn func()) {
		b := gtk.NewButtonWithLabel(label)
		b.AddCSSClass("flat")
		b.SetHAlign(gtk.AlignFill)
		if l, ok := b.Child().(*gtk.Label); ok {
			l.SetXAlign(0)
		}
		if destructive {
			b.AddCSSClass("destructive-action")
		}
		b.ConnectClicked(func() {
			pop.Popdown()
			fn()
		})
		box.Append(b)
	}

	folder := folderFor(n)
	add("New Note", false, func() { t.promptNewNote(folder) })
	add("New Folder", false, func() { t.promptNewFolder(folder) })
	if n != nil {
		box.Append(gtk.NewSeparator(gtk.OrientationHorizontal))
		add("Rename", false, func() { t.promptRename(n) })
		add("Delete", true, func() { t.promptDelete(n) })
	}

	pop.SetChild(box)
	pop.SetParent(parent)
	pop.ConnectClosed(func() { pop.Unparent() })
	pop.Popup()
}

// folderFor returns the folder a new item should be created in for a context
// target: inside a folder, alongside a note, or at the root for empty space.
func folderFor(n *node) string {
	switch {
	case n == nil:
		return ""
	case n.isFolder:
		return n.rel
	default:
		return parentFolder(n.rel)
	}
}

func (t *Tree) promptNewNote(folder string) {
	t.promptText("New Note", "Create", "", func(name string) {
		rel := joinRel(folder, name)
		if err := t.store.WriteNote(rel, "# "+name+"\n\n"); err != nil {
			log.Printf("atlas-notes: new note: %v", err)
			return
		}
		t.ForceRefresh()
		t.notifyChanged()
		if t.OnOpenNote != nil {
			t.OnOpenNote(rel)
		}
	})
}

func (t *Tree) promptNewFolder(folder string) {
	t.promptText("New Folder", "Create", "", func(name string) {
		if err := t.store.CreateFolder(joinRel(folder, name)); err != nil {
			log.Printf("atlas-notes: new folder: %v", err)
			return
		}
		t.ForceRefresh()
	})
}

func (t *Tree) promptRename(n *node) {
	if n == nil {
		return
	}
	t.promptText("Rename", "Rename", n.name, func(newName string) {
		newRel := joinRel(parentFolder(n.rel), newName)
		var err error
		if n.isFolder {
			err = t.store.RenameFolder(n.rel, newRel)
		} else {
			err = t.store.RenameNote(n.rel, newRel)
		}
		if err != nil {
			log.Printf("atlas-notes: rename: %v", err)
			return
		}
		if !n.isFolder && t.currentRel == n.rel {
			if t.OnMoved != nil {
				t.OnMoved(n.rel, newRel) // keep the open note in sync
			}
		}
		t.ForceRefresh()
	})
}

// notifyChanged tells the app the vault's contents changed.
func (t *Tree) notifyChanged() {
	if t.OnChanged != nil {
		t.OnChanged()
	}
}

func (t *Tree) promptDelete(n *node) {
	if n == nil {
		return
	}
	what := "note"
	if n.isFolder {
		what = "folder and all its contents"
	}
	t.confirm("Delete?", "Delete the "+what+" \""+n.name+"\"? This cannot be undone.", "Delete", func() {
		var err error
		if n.isFolder {
			err = t.store.DeleteFolder(n.rel)
		} else {
			err = t.store.DeleteNote(n.rel)
		}
		if err != nil {
			log.Printf("atlas-notes: delete: %v", err)
		} else if t.OnDeleted != nil {
			t.OnDeleted(n.rel, n.isFolder)
		}
		t.ForceRefresh()
		t.notifyChanged()
	})
}

// promptText shows a single-entry dialog and calls onOK with the trimmed value.
func (t *Tree) promptText(title, okLabel, initial string, onOK func(string)) {
	dialog := adw.NewAlertDialog(title, "")
	entry := gtk.NewEntry()
	if initial != "" {
		entry.Buffer().SetText(initial, -1)
	}
	entry.SetHExpand(true)
	dialog.SetExtraChild(entry)
	dialog.AddResponse("cancel", "Cancel")
	dialog.AddResponse("ok", okLabel)
	dialog.SetResponseAppearance("ok", adw.ResponseSuggested)
	dialog.SetDefaultResponse("ok")
	dialog.SetCloseResponse("cancel")
	dialog.ConnectResponse(func(response string) {
		if response != "ok" {
			return
		}
		if name := strings.TrimSpace(entry.Buffer().Text()); name != "" {
			onOK(name)
		}
	})
	dialog.Present(t.parent)
}

// confirm shows a destructive confirmation dialog.
func (t *Tree) confirm(title, body, okLabel string, onOK func()) {
	dialog := adw.NewAlertDialog(title, body)
	dialog.AddResponse("cancel", "Cancel")
	dialog.AddResponse("ok", okLabel)
	dialog.SetResponseAppearance("ok", adw.ResponseDestructive)
	dialog.SetDefaultResponse("cancel")
	dialog.SetCloseResponse("cancel")
	dialog.ConnectResponse(func(response string) {
		if response == "ok" {
			onOK()
		}
	})
	dialog.Present(t.parent)
}
