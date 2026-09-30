package app

import (
	"errors"
	"log"
	"path"
	"sort"
	"strings"
	"unicode"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"atlas-notes/internal/editor"
	"atlas-notes/internal/markup"
	"atlas-notes/internal/storage"
)

// Splitting a note in two and merging two into one, and opening a note to the
// side, from the app's side: the questions asked, the dialogs, and what has to be
// saved and reloaded around the store's work (see storage/merge.go).

// noteNameLimit is the longest name suggested for a new note.
const noteNameLimit = 60

// cleanNoteName makes text fit as a note's name: no path separators, which would
// put it in a folder, no control characters, and no dots or spaces at the start.
func cleanNoteName(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '/' || r == '\\':
			return '-'
		case unicode.IsControl(r):
			return -1
		}
		return r
	}, s)
	s = strings.TrimLeft(strings.TrimSpace(s), ". ")
	if r := []rune(s); len(r) > noteNameLimit {
		s = string(r[:noteNameLimit])
	}
	return strings.TrimSpace(s)
}

// noteNameFromText suggests a name for the note made from selected text: its
// first line that has words in it, without the Markdown around them.
func noteNameFromText(text string) string {
	plain := strings.NewReplacer("[[", "", "]]", "", "**", "", "__", "", "`", "", "~~", "")
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimLeft(strings.TrimSpace(line), "#>-*+ \t")
		line = strings.TrimPrefix(strings.TrimPrefix(line, "[ ] "), "[x] ")
		if name := cleanNoteName(plain.Replace(line)); name != "" {
			return name
		}
	}
	return ""
}

// toastAction shows a message with one button.
func (a *App) toastAction(text, label string, fn func()) {
	if a.toastOverlay == nil {
		return
	}
	t := adw.NewToast(text)
	t.SetButtonLabel(label)
	t.SetTimeout(7)
	t.ConnectButtonClicked(fn)
	a.toastOverlay.AddToast(t)
}

// askName asks for a name in a small dialog and calls onOK with what was typed,
// trimmed. icon, when the theme has it, sits in the field.
func (a *App) askName(title, body, ok, initial, icon string, onOK func(name string)) {
	dialog := adw.NewAlertDialog(title, body)
	entry := gtk.NewEntry()
	entry.SetHExpand(true)
	entry.SetText(initial)
	if hasIcon(icon) {
		entry.SetIconFromIconName(gtk.EntryIconPrimary, icon)
	}
	dialog.SetExtraChild(entry)
	dialog.AddResponse("cancel", "Cancel")
	dialog.AddResponse("ok", ok)
	dialog.SetResponseAppearance("ok", adw.ResponseSuggested)
	dialog.SetDefaultResponse("ok")
	dialog.SetCloseResponse("cancel")
	entry.SetActivatesDefault(true) // Enter answers the dialog
	dialog.ConnectResponse(func(response string) {
		if response != "ok" {
			return
		}
		if name := strings.TrimSpace(entry.Text()); name != "" {
			onOK(name)
		}
	})
	dialog.Present(a.win)
	entry.GrabFocus()
	entry.SelectRegion(0, -1)
}

// actionSplitNote is "Move selection to a new note" from the keyboard and the
// command box: it acts on the pane the caret is in.
func (a *App) actionSplitNote() {
	if !a.noteOpen() {
		a.toast("Open a note and select some text to move it")
		return
	}
	a.splitSelection(a.activeEditor(), a.activeNote())
}

// splitSelection asks for a name, makes a note of the text selected in ed (which
// shows the note rel), and puts a link to it where the text was. The link
// replaces the text as one step of ed's undo history, so a single Ctrl+Z brings
// the text back, though the new note stays.
func (a *App) splitSelection(ed *editor.Editor, rel string) {
	if a.store == nil || ed == nil || rel == "" {
		return
	}
	from, to, text, ok := ed.Selection()
	if !ok || strings.TrimSpace(text) == "" {
		a.toast("Select some text to move it to a new note")
		return
	}
	if a.store.IsNoteLocked(rel) {
		// The new note would be written in the clear, and the text would leave the
		// protection it is under.
		a.toast("Text can't be moved out of a protected note, because the new note would not be protected")
		return
	}
	initial := noteNameFromText(text)
	if initial == "" {
		initial = "New note"
	}
	a.askName("Move Selection to a New Note",
		"The selected text goes into a new note, and a link to it takes its place here.",
		"Move", initial, "atlasnotes-split-note-symbolic",
		func(name string) { a.finishSplit(ed, rel, from, to, text, name) })
}

// finishSplit does the split once the name is known.
func (a *App) finishSplit(ed *editor.Editor, rel string, from, to int, text, name string) {
	name = cleanNoteName(name)
	if name == "" {
		a.toast("That name can't be used")
		return
	}
	// The dialog was open while the note could have changed. If the selected text
	// is not where it was, nothing is made: a note whose text was not also
	// replaced would leave it in two places.
	if !ed.SpanIs(from, to, text) {
		a.toast("The note changed, so nothing was moved. Select the text again")
		return
	}
	folder := path.Dir(rel)
	if folder == "." {
		folder = ""
	}
	newRel := a.store.UniqueName(folder, name)
	// What is on screen but not yet on disk goes first, so history keeps the
	// text as it was before the split.
	a.flushDirty()

	body := "# " + path.Base(newRel) + "\n\n" + strings.Trim(text, "\n") + "\n"
	made, err := a.store.CreateNote(newRel, body)
	switch {
	case err != nil:
		log.Printf("atlas-notes: split into %q: %v", newRel, err)
		a.toast("Couldn't make the new note: " + err.Error())
		return
	case !made:
		a.toast("A note called " + path.Base(newRel) + " appeared just now. Nothing was moved")
		return
	}

	// A bare name when it says which note it means, the full path when another
	// note by that name is nearer the root.
	target := path.Base(newRel)
	if notes, err := a.store.ListNotes(); err == nil {
		paths := make([]string, len(notes))
		for i, n := range notes {
			paths[i] = n.Path
		}
		if !strings.EqualFold(markup.Resolve(target, paths), newRel) {
			target = newRel
		}
	}
	if !ed.ReplaceSpan(from, to, text, "[["+target+"]]") {
		a.toast("The note changed, so the text was copied to " + path.Base(newRel) + " but not removed")
	} else {
		a.flushDirty() // the source now has the link, not the text
		a.toastAction("Moved the text to “"+path.Base(newRel)+"”", "Open", func() { a.openNote(newRel) })
	}
	a.noteNamesCache = nil // the [[ suggestions should know the new note
	if a.tree != nil {
		a.tree.ForceRefresh()
	}
	a.refreshWelcome()
	a.refreshBacklinks()
}

// actionOpenSide asks which note to open beside the current one.
func (a *App) actionOpenSide() {
	if !a.noteOpen() {
		a.toast("Open a note first, then choose one to open beside it")
		return
	}
	a.pickNote("Open to the Side", "atlasnotes-split-right-symbolic",
		map[string]bool{a.currentNote: true}, a.openToSide)
}

// actionMerge is "Merge into…" from the keyboard and the command box, for the
// note in the pane the caret is in.
func (a *App) actionMerge() {
	if !a.noteOpen() {
		a.toast("Open a note to merge it into another")
		return
	}
	a.mergeInto(a.activeNote())
}

// mergeInto asks which note rel should be merged into, and then whether to go
// ahead.
func (a *App) mergeInto(rel string) {
	if a.store == nil || rel == "" {
		return
	}
	a.pickNote("Merge “"+path.Base(rel)+"” into…", "atlasnotes-merge-note-symbolic",
		map[string]bool{rel: true}, func(into string) { a.confirmMerge(rel, into) })
}

// confirmMerge says what a merge does, in full, before doing it.
func (a *App) confirmMerge(from, into string) {
	dialog := adw.NewAlertDialog("Merge into “"+path.Base(into)+"”?",
		"The text of “"+path.Base(from)+"” is added to the end of “"+path.Base(into)+
			"” under a heading with its name. Links to “"+path.Base(from)+"” will point to “"+
			path.Base(into)+"” instead, and “"+path.Base(from)+"” goes to the Trash, where you can restore it from.")
	dialog.AddResponse("cancel", "Cancel")
	dialog.AddResponse("merge", "Merge")
	dialog.SetResponseAppearance("merge", adw.ResponseSuggested)
	dialog.SetDefaultResponse("cancel")
	dialog.SetCloseResponse("cancel")
	dialog.ConnectResponse(func(response string) {
		if response == "merge" {
			a.mergeNotes(from, into)
		}
	})
	dialog.Present(a.win)
}

// mergeNotes merges from into into and brings the screen up to date. Both panes
// are saved first, since the merge reads and writes the notes' files, and the
// notes on screen are read again after: the target has more text, and other
// notes may have had links rewritten.
func (a *App) mergeNotes(from, into string) {
	if a.store == nil {
		return
	}
	lockedFrom, lockedInto := a.store.IsNoteLocked(from), a.store.IsNoteLocked(into)
	if (lockedFrom || lockedInto) && !a.store.IsUnlocked() {
		a.ensureUnlocked(func() { a.mergeNotes(from, into) })
		return
	}
	if lockedFrom && !lockedInto {
		// Its text would become readable without the password.
		a.toast("“" + path.Base(from) + "” is protected, so it can't be merged into a note that isn't")
		return
	}
	a.flushDirty()
	if (a.dirty && (a.currentNote == from || a.currentNote == into)) ||
		(a.side != nil && a.side.dirty && (a.side.rel == from || a.side.rel == into)) {
		a.toast("Couldn't save the note first, so nothing was merged")
		return
	}

	_, err := a.store.MergeNote(from, into)
	gone := !a.store.NoteExists(from)
	switch {
	case errors.Is(err, storage.ErrNoTrash):
		a.toast("Merging keeps the merged note in the Trash, and there is none here, so nothing was changed")
		return
	case errors.Is(err, storage.ErrLocked):
		a.ensureUnlocked(func() { a.mergeNotes(from, into) })
		return
	case err != nil && !gone:
		log.Printf("atlas-notes: merge %q into %q: %v", from, into, err)
		a.toast("Couldn't finish the merge: " + err.Error())
	case err != nil:
		log.Printf("atlas-notes: merge %q into %q: %v", from, into, err)
		a.toast("Merged, but not every link could be updated: " + err.Error())
	default:
		a.toast("Merged “" + path.Base(from) + "” into “" + path.Base(into) + "”. It is in the Trash")
	}
	if !gone {
		a.reloadOpen(into)
		return
	}

	a.forgetFavourite(from, false)
	mainAfter := a.currentNote
	if mainAfter == from {
		mainAfter = into
	}
	if p := a.side; p != nil && (p.rel == from || p.rel == into) {
		if mainAfter == into {
			a.dropSide() // the main pane shows the target; one note is never in both
		} else {
			p.rel = into
			p.showName()
			p.bindImages()
		}
	}
	if a.currentNote == from {
		a.dirty = false // nothing of it is left to save
		a.openNote(into)
	} else {
		a.onLinksChanged() // reads what is on screen again where the disk now says otherwise
		if a.currentNote == into {
			a.reloadOpen(into)
		}
	}
	if a.side != nil {
		a.side.reload()
	}
	a.noteNamesCache = nil
	if a.tree != nil {
		a.tree.ForceRefresh()
	}
	a.refreshWelcome()
}

// reloadOpen reads rel again in whichever pane shows it, when its text on disk
// is no longer what the pane holds.
func (a *App) reloadOpen(rel string) {
	if a.currentNote == rel && !a.dirty {
		if text, err := a.store.ReadNote(rel); err == nil && text != a.editorContent() {
			a.rememberSaved(rel, text)
			a.editor.SetContent(text)
		}
	}
	if a.side != nil && a.side.rel == rel {
		a.side.reload()
	}
}

// pickNote is a small dialog with a search field and the vault's notes, for
// choosing one. exclude leaves some out. onPick runs once the dialog has gone.
func (a *App) pickNote(title, icon string, exclude map[string]bool, onPick func(rel string)) {
	if a.store == nil {
		return
	}
	metas, err := a.store.ListNotes()
	if err != nil {
		a.toast("Couldn't list the notes: " + err.Error())
		return
	}
	sort.Slice(metas, func(i, j int) bool { return metas[i].ModifiedAt.After(metas[j].ModifiedAt) })
	var notes []string
	for _, m := range metas {
		if !exclude[m.Path] {
			notes = append(notes, m.Path)
		}
	}

	dialog := adw.NewDialog()
	dialog.SetTitle(title)
	dialog.SetContentWidth(460)
	dialog.SetContentHeight(420)

	entry := gtk.NewSearchEntry()
	entry.SetPlaceholderText("Find a note")
	entry.SetMarginTop(10)
	entry.SetMarginBottom(10)
	entry.SetMarginStart(12)
	entry.SetMarginEnd(12)

	list := gtk.NewListBox()
	list.SetSelectionMode(gtk.SelectionSingle)
	list.SetActivateOnSingleClick(true)
	list.AddCSSClass("atlas-palette-list")
	empty := gtk.NewLabel("No notes match")
	empty.AddCSSClass("dim-label")
	empty.SetMarginTop(24)
	empty.SetVisible(false)

	var shown []string
	refresh := func() {
		for c := list.FirstChild(); c != nil; c = list.FirstChild() {
			list.Remove(c)
		}
		shown = shown[:0]
		q := entry.Text()
		type hit struct {
			rel   string
			score int
		}
		var hits []hit
		for _, rel := range notes {
			s := paletteScore(q, path.Base(rel))
			if s2 := paletteScore(q, rel); s2 >= 0 && (s < 0 || s2+2 < s) {
				s = s2 + 2
			}
			if s >= 0 {
				hits = append(hits, hit{rel, s})
			}
		}
		if q != "" {
			sort.SliceStable(hits, func(i, j int) bool { return hits[i].score < hits[j].score })
		}
		if len(hits) > paletteListLimit {
			hits = hits[:paletteListLimit]
		}
		for _, h := range hits {
			shown = append(shown, h.rel)
			list.Append(pickRow(icon, h.rel))
		}
		empty.SetVisible(len(hits) == 0)
		if row := list.RowAtIndex(0); row != nil {
			list.SelectRow(row)
		}
	}
	choose := func(i int) {
		if i < 0 || i >= len(shown) {
			return
		}
		rel := shown[i]
		dialog.Close()
		coreglib.IdleAdd(func() bool { onPick(rel); return false })
	}
	entry.ConnectSearchChanged(refresh)
	entry.ConnectActivate(func() {
		if row := list.SelectedRow(); row != nil {
			choose(row.Index())
		}
	})
	list.ConnectRowActivated(func(row *gtk.ListBoxRow) { choose(row.Index()) })

	// The arrows move the selection while the caret stays in the field, as in
	// the command box.
	keys := gtk.NewEventControllerKey()
	keys.SetPropagationPhase(gtk.PhaseCapture)
	keys.ConnectKeyPressed(func(keyval, _ uint, _ gdk.ModifierType) bool {
		step := 0
		switch keyval {
		case gdk.KEY_Down:
			step = 1
		case gdk.KEY_Up:
			step = -1
		default:
			return false
		}
		if n := len(shown); n > 0 {
			i := 0
			if row := list.SelectedRow(); row != nil {
				i = row.Index()
			}
			list.SelectRow(list.RowAtIndex((i + step + n) % n))
		}
		return true
	})
	entry.AddController(keys)

	inner := gtk.NewBox(gtk.OrientationVertical, 0)
	inner.Append(list)
	inner.Append(empty)
	scroll := gtk.NewScrolledWindow()
	scroll.SetChild(inner)
	scroll.SetVExpand(true)
	scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)

	body := gtk.NewBox(gtk.OrientationVertical, 0)
	body.Append(entry)
	body.Append(gtk.NewSeparator(gtk.OrientationHorizontal))
	body.Append(scroll)
	tv := adw.NewToolbarView()
	tv.AddTopBar(adw.NewHeaderBar())
	tv.SetContent(body)
	dialog.SetChild(tv)
	dialog.SetFocus(entry)
	refresh()
	dialog.Present(a.win)
	entry.GrabFocus()
}

// pickRow is one note in the picker: its icon, its name, and its folder.
func pickRow(icon, rel string) *gtk.ListBoxRow {
	box := gtk.NewBox(gtk.OrientationHorizontal, 10)
	box.SetMarginTop(6)
	box.SetMarginBottom(6)
	box.SetMarginStart(12)
	box.SetMarginEnd(12)
	if !hasIcon(icon) {
		icon = "atlasnotes-note-symbolic"
	}
	box.Append(gtk.NewImageFromIconName(icon))
	name := gtk.NewLabel(path.Base(rel))
	name.SetXAlign(0)
	name.SetEllipsize(pango.EllipsizeEnd)
	box.Append(name)
	sub := gtk.NewLabel(strings.TrimPrefix(path.Dir(rel), "."))
	sub.SetXAlign(0)
	sub.SetHExpand(true)
	sub.SetEllipsize(pango.EllipsizeStart)
	sub.AddCSSClass("dim-label")
	sub.AddCSSClass("caption")
	box.Append(sub)
	row := gtk.NewListBoxRow()
	row.SetChild(box)
	return row
}
