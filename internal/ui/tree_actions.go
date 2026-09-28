package ui

import (
	"errors"
	"log"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-notes/internal/storage"
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

		star := "Add to Favourites"
		if n.starred {
			star = "Remove from Favourites"
		}
		add(star, false, func() {
			if t.OnToggleStar != nil {
				t.OnToggleStar(n.rel, n.isFolder)
			}
		})

		// Locking the vault root is not offered: it would be every note, and
		// "lock everything" is a decision that belongs in Settings, not in a
		// right-click on a row.
		switch {
		case n.locked && t.OnUnlock != nil:
			add("Remove Password…", false, func() { t.OnUnlock(n.rel, n.isFolder) })
		case !n.locked && t.OnLock != nil:
			add("Protect with Password…", false, func() { t.OnLock(n.rel, n.isFolder) })
		}

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
	// With a Trash, a delete can be undone from the file manager, and the
	// dialog says so instead of warning about something that is no longer
	// true. Without one it is permanent, and says that.
	if t.store.Trash != nil {
		body := "“" + n.name + "” will be moved to the Trash. You can restore it from there."
		if n.isFolder {
			body = "The folder “" + n.name + "” and everything in it will be moved to the Trash. You can restore it from there."
		}
		t.confirm("Move to the Trash?", body, "Move to Trash", func() { t.deleteNode(n, false) })
		return
	}
	what := "note"
	if n.isFolder {
		what = "folder and everything in it"
	}
	t.confirm("Delete?", "Delete the "+what+" “"+n.name+"”? This cannot be undone.", "Delete",
		func() { t.deleteNode(n, true) })
}

// deleteNode deletes a note or folder, to the Trash unless permanently is set.
func (t *Tree) deleteNode(n *node, permanently bool) {
	var err error
	switch {
	case n.isFolder && permanently:
		err = t.store.DeleteFolderPermanently(n.rel)
	case n.isFolder:
		err = t.store.DeleteFolder(n.rel)
	case permanently:
		err = t.store.DeleteNotePermanently(n.rel)
	default:
		err = t.store.DeleteNote(n.rel)
	}
	if errors.Is(err, storage.ErrTrashFailed) {
		// Some places have no Trash: a network share, a USB drive, a
		// filesystem that does not support one. Nothing was deleted, and
		// deleting it for good is a different thing to agree to, so ask.
		log.Printf("atlas-notes: trash: %v", err)
		t.confirm("Delete permanently?",
			"“"+n.name+"” couldn't be moved to the Trash, so it would be deleted for good. This cannot be undone.",
			"Delete Permanently", func() { t.deleteNode(n, true) })
		return
	}
	if err != nil {
		// This used to go only to the log, which left a delete that failed
		// looking like a click that did nothing.
		log.Printf("atlas-notes: delete: %v", err)
		t.message("Couldn't delete “" + n.name + "”: " + err.Error())
		return
	}
	if t.OnDeleted != nil {
		t.OnDeleted(n.rel, n.isFolder)
	}
	t.ForceRefresh()
	t.notifyChanged()
	if !permanently && t.store.Trash != nil {
		t.message("Moved “" + n.name + "” to the Trash")
	}
}

// message shows a short message through the app, when it has asked for them.
func (t *Tree) message(text string) {
	if t.OnMessage != nil {
		t.OnMessage(text)
	}
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
