package app

import (
	"errors"
	"fmt"
	"log"
	"path"
	"strings"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-notes/internal/storage"
)

// Version history: the list of earlier states the store keeps for the open
// note, with a preview of each and a way to put one back. The store decides
// what is kept and when (see storage/history.go); this is only the window onto
// it.

// versionTime writes when a version was saved the way a person would say it:
// today and yesterday by name, anything older by date. It takes the clock as an
// argument so it can be tested, and reads both times in now's zone so a version
// saved at 23:50 does not slip into "Yesterday" because of where the process
// happens to run.
func versionTime(t, now time.Time) string {
	t = t.In(now.Location())
	clock := t.Format("15:04")
	ty, tm, td := t.Date()
	ny, nm, nd := now.Date()
	if ty == ny && tm == nm && td == nd {
		return "Today " + clock
	}
	// AddDate steps by calendar day, so it is right across a clock change,
	// where subtracting 24 hours is not.
	yy, ym, yd := now.AddDate(0, 0, -1).Date()
	if ty == yy && tm == ym && td == yd {
		return "Yesterday " + clock
	}
	return t.Format("2 Jan 2006") + " " + clock
}

// versionTimeInSentence is versionTime for the middle of a sentence, where "from
// Today 14:05" reads wrongly and "from today 14:05" does not.
func versionTimeInSentence(t, now time.Time) string {
	s := versionTime(t, now)
	for _, word := range []string{"Today", "Yesterday"} {
		if strings.HasPrefix(s, word+" ") {
			return strings.ToLower(word) + s[len(word):]
		}
	}
	return s
}

// humanSize writes a byte count in the units a note's size is usually read in.
func humanSize(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
}

// showHistory opens the version history of the note on screen.
func (a *App) showHistory() {
	if a.store == nil || !a.noteOpen() {
		a.toast("Open a note to see its history")
		return
	}
	rel := a.currentNote
	versions, err := a.store.History(rel)
	if err != nil {
		log.Printf("atlas-notes: history of %q: %v", rel, err)
		a.toast("Couldn't read the history of that note: " + err.Error())
		return
	}

	dialog := adw.NewDialog()
	dialog.SetTitle("Version History of " + path.Base(rel))
	dialog.SetContentWidth(760)
	dialog.SetContentHeight(520)

	toolbar := adw.NewToolbarView()
	toolbar.AddTopBar(adw.NewHeaderBar())

	if len(versions) == 0 {
		empty := adw.NewStatusPage()
		empty.SetIconName(iconName("atlasnotes-recent-symbolic", "atlasnotes-note-symbolic"))
		empty.SetDescription("No earlier versions yet. Versions are kept as you edit, at most one every ten minutes.")
		empty.SetVExpand(true)
		toolbar.SetContent(empty)
		toolbar.AddBottomBar(historyButtons(dialog, nil))
		dialog.SetChild(toolbar)
		dialog.Present(a.win)
		return
	}

	// Left: the versions, newest first as the store gives them.
	list := gtk.NewListBox()
	list.SetSelectionMode(gtk.SelectionSingle)
	list.AddCSSClass("navigation-sidebar")
	now := time.Now()
	for _, v := range versions {
		row := gtk.NewListBoxRow()
		box := gtk.NewBox(gtk.OrientationVertical, 2)
		box.SetMarginTop(8)
		box.SetMarginBottom(8)
		box.SetMarginStart(10)
		box.SetMarginEnd(10)

		when := gtk.NewLabel(versionTime(v.Time, now))
		when.SetXAlign(0)
		box.Append(when)

		size := humanSize(v.Size)
		if v.Locked {
			size += " · locked"
		}
		meta := gtk.NewLabel(size)
		meta.SetXAlign(0)
		meta.AddCSSClass("dim-label")
		meta.AddCSSClass("caption")
		box.Append(meta)

		row.SetChild(box)
		list.Append(row)
	}
	listScroll := gtk.NewScrolledWindow()
	listScroll.SetChild(list)
	listScroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	listScroll.SetSizeRequest(230, -1)

	// Right: the selected version's text, for reading only. It is proportional
	// type like the note itself, so a version looks like the note it was.
	preview := gtk.NewTextView()
	preview.SetEditable(false)
	preview.SetCursorVisible(false)
	preview.SetWrapMode(gtk.WrapWordChar)
	preview.SetLeftMargin(14)
	preview.SetRightMargin(14)
	preview.SetTopMargin(12)
	preview.SetBottomMargin(12)
	previewScroll := gtk.NewScrolledWindow()
	previewScroll.SetChild(preview)
	previewScroll.SetHExpand(true)
	previewScroll.SetVExpand(true)

	body := gtk.NewBox(gtk.OrientationHorizontal, 0)
	body.Append(listScroll)
	body.Append(gtk.NewSeparator(gtk.OrientationVertical))
	body.Append(previewScroll)
	toolbar.SetContent(body)

	restore := gtk.NewButtonWithLabel("Restore This Version")
	restore.AddCSSClass("suggested-action")
	restore.SetSensitive(false)
	toolbar.AddBottomBar(historyButtons(dialog, restore))

	var selected *storage.Version
	list.ConnectRowSelected(func(row *gtk.ListBoxRow) {
		selected = nil
		restore.SetSensitive(false)
		if row == nil || row.Index() < 0 || row.Index() >= len(versions) {
			preview.Buffer().SetText("")
			return
		}
		v := versions[row.Index()]
		text, err := a.store.ReadVersion(rel, v.ID)
		switch {
		case errors.Is(err, storage.ErrLocked):
			preview.Buffer().SetText("Unlock the vault to see this version.")
		case err != nil:
			log.Printf("atlas-notes: read version %q of %q: %v", v.ID, rel, err)
			preview.Buffer().SetText("Couldn't read this version: " + err.Error())
		default:
			preview.Buffer().SetText(text)
			selected = &v
			restore.SetSensitive(true)
		}
	})
	restore.ConnectClicked(func() {
		if selected != nil {
			a.confirmRestore(dialog, rel, *selected)
		}
	})

	dialog.SetChild(toolbar)
	dialog.Present(a.win)
	// The newest version is what people came to look at, and selecting it
	// fills the preview, so the dialog never opens with an empty right side.
	list.SelectRow(list.RowAtIndex(0))
}

// historyButtons is the bottom bar: Close, and Restore when there is a version
// to restore.
func historyButtons(dialog *adw.Dialog, restore *gtk.Button) *gtk.Box {
	bar := gtk.NewBox(gtk.OrientationHorizontal, 8)
	bar.SetHAlign(gtk.AlignEnd)
	bar.SetMarginTop(8)
	bar.SetMarginBottom(8)
	bar.SetMarginStart(12)
	bar.SetMarginEnd(12)
	closeBtn := gtk.NewButtonWithLabel("Close")
	closeBtn.ConnectClicked(func() { dialog.Close() })
	bar.Append(closeBtn)
	if restore != nil {
		bar.Append(restore)
	}
	return bar
}

// confirmRestore asks before replacing the note's text. The text it replaces is
// kept as a version by the store, and the question says so, because that is
// what makes the answer easy to give.
func (a *App) confirmRestore(dialog *adw.Dialog, rel string, v storage.Version) {
	now := time.Now()
	when := versionTimeInSentence(v.Time, now)
	alert := adw.NewAlertDialog("Restore this version?",
		"Restore the version from "+when+"? The current text is kept in history, so this can be undone.")
	alert.AddResponse("cancel", "Cancel")
	alert.AddResponse("restore", "Restore")
	alert.SetResponseAppearance("restore", adw.ResponseSuggested)
	alert.SetDefaultResponse("restore")
	alert.SetCloseResponse("cancel")
	alert.ConnectResponse(func(response string) {
		if response != "restore" {
			return
		}
		// What is on screen but not yet on disk goes to disk first, so the
		// store keeps it as the version this restore can be undone to.
		a.flushDirty()
		if a.dirty && a.currentNote == rel {
			// The save before the restore failed. Restoring now would replace
			// text that is only in the editor, with nothing kept of it.
			a.toast("Couldn't save the note first, so nothing was restored")
			return
		}
		if err := a.store.RestoreVersion(rel, v.ID); err != nil {
			log.Printf("atlas-notes: restore %q of %q: %v", v.ID, rel, err)
			if errors.Is(err, storage.ErrLocked) {
				a.toast("Unlock the vault to restore this version")
			} else {
				a.toast("Couldn't restore that version: " + err.Error())
			}
			return
		}
		a.openNote(rel) // reads the restored text back into the editor
		a.toast("Restored the version from " + when)
		dialog.Close()
	})
	alert.Present(dialog)
}
