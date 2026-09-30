package app

import (
	"errors"
	"log"
	"path"
	"strings"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"atlas-notes/internal/editor"
	"atlas-notes/internal/markup"
	"atlas-notes/internal/storage"
)

// A second note beside the first: a pane to the right of the editor, with its
// own title, its own editor and its own autosave.
//
// The app was written for one note at a time (currentNote, dirty, saveCurrent),
// and that main pane is left as it was. The side pane keeps the same state in a
// struct of its own, and joins the main pane at the few places where "the note
// that is open" has to mean both:
//
//   - flushDirty writes both, so every path that flushes before it does something
//     (opening another note, renaming, locking, restoring, quitting) covers both.
//   - renames, moves and deletes are reported to it (onRenamed, onDeleted), so it
//     follows a note that was renamed and lets go of one that was deleted.
//   - links rewritten on disk reload it, as they reload the main pane
//     (sideLinksChanged), and locking the vault closes it when its note is locked.
//
// One note is never open in both panes: two editors on one file would each save
// over the other. Asking for it in the pane that lacks it shows the other pane.

// sidePane is the second pane's state.
type sidePane struct {
	a     *App
	box   *gtk.Box
	title *gtk.Label
	dot   *gtk.Box
	ed    *editor.Editor

	rel      string
	dirty    bool
	gen      int  // bumped by every edit, so a waiting autosave can tell it was overtaken
	pending  bool // an autosave timer is in flight
	inFlight bool // an async save is running

	savedRel  string // the note savedHash belongs to
	savedHash uint64 // hash of what is on disk, to skip writes that change nothing
}

// allEditors are the editors on screen, main first.
func (a *App) allEditors() []*editor.Editor {
	var eds []*editor.Editor
	if a.editor != nil {
		eds = append(eds, a.editor)
	}
	if a.side != nil {
		eds = append(eds, a.side.ed)
	}
	return eds
}

// activeEditor is the editor a shortcut or toolbar button is meant for: the side
// pane's while the caret is in it, the main one otherwise.
func (a *App) activeEditor() *editor.Editor {
	if a.side != nil && a.side.ed.HasFocus() {
		return a.side.ed
	}
	return a.editor
}

// activeNote is the note activeEditor is showing.
func (a *App) activeNote() string {
	if a.side != nil && a.side.ed.HasFocus() {
		return a.side.rel
	}
	return a.currentNote
}

// sideOpen reports whether the side pane is showing a note.
func (a *App) sideOpen() bool { return a.side != nil && a.side.rel != "" }

// newSidePane builds the pane and its editor. It is not shown yet.
func (a *App) newSidePane() *sidePane {
	p := &sidePane{a: a}

	p.title = gtk.NewLabel("")
	p.title.SetXAlign(0)
	p.title.SetHExpand(true)
	p.title.SetEllipsize(pango.EllipsizeEnd)
	p.title.AddCSSClass("side-title")

	p.dot = gtk.NewBox(gtk.OrientationHorizontal, 0)
	p.dot.SetSizeRequest(8, 8)
	p.dot.SetVAlign(gtk.AlignCenter)
	p.dot.AddCSSClass("save-dot")
	p.dot.AddCSSClass("save-saved")

	closeBtn := findIconButton("atlasnotes-close-symbolic", "✕", "Close this pane", a.closeSide)

	head := gtk.NewBox(gtk.OrientationHorizontal, 8)
	head.AddCSSClass("side-header")
	head.Append(p.title)
	head.Append(p.dot)
	head.Append(closeBtn)

	e := editor.New()
	p.ed = e
	e.OnChanged = p.onChanged
	e.OnOpenNote = a.openLinkedNote
	e.OnOpenTag = a.showTagged
	e.OnOpenURL = a.openURL
	e.NoteNames = a.noteNames
	e.TagNames = a.tagNames
	e.OnImageError = func(err error) { a.toast(imageErrorText(err)) }
	e.SetSideHandlers(a.openLinkToSide, func() { a.splitSelection(e, p.rel) })

	clamp := newClamp(e)
	p.box = gtk.NewBox(gtk.OrientationVertical, 0)
	p.box.AddCSSClass("side-pane")
	p.box.SetHExpand(true)
	p.box.Append(head)
	p.box.Append(clamp)
	return p
}

// openToSide shows rel in the side pane, making the pane if there is none.
func (a *App) openToSide(rel string) {
	if a.store == nil || a.sidePaned == nil {
		return
	}
	if !a.noteOpen() {
		a.openNote(rel) // nothing to put it beside
		return
	}
	if rel == a.currentNote {
		a.toast("That note is already open")
		return
	}
	if a.side != nil && a.side.rel == rel {
		a.side.ed.Focus()
		return
	}
	a.flushDirty() // the pane it replaces, and the main one, before anything is read
	content, err := a.store.ReadNote(rel)
	if errors.Is(err, storage.ErrLocked) {
		a.ensureUnlocked(func() { a.openToSide(rel) })
		return
	}
	if err != nil {
		log.Printf("atlas-notes: open %q to the side: %v", rel, err)
		a.toast("Couldn't open that note: " + err.Error())
		return
	}
	first := a.side == nil
	if first {
		a.side = a.newSidePane()
		a.sidePaned.SetEndChild(a.side.box)
		a.syncEditorAccent() // the new editor draws its links in the theme's colour
	}
	a.side.load(rel, content)
	if first {
		// Half and half to begin with; the handle moves it from there.
		if w := a.sidePaned.AllocatedWidth(); w > 0 {
			a.sidePaned.SetPosition(w / 2)
		}
	}
	a.side.ed.Focus()
}

// openLinkToSide follows a [[link]] into the side pane. Unlike following it in
// the main pane it does not make a note that is missing: a second pane that
// filled up with empty notes by accident would be a nuisance.
func (a *App) openLinkToSide(target, _ string) {
	if a.store == nil {
		return
	}
	notes := a.noteNames()
	for _, t := range []string{target, strings.TrimSuffix(target, ".md")} {
		if rel := markup.Resolve(t, notes); rel != "" {
			a.openToSide(rel)
			return
		}
	}
	a.toast("There is no note called " + path.Base(target) + " yet. Click the link to make it")
}

// closeSide saves the side pane's note and closes the pane. If the save fails the
// pane stays, with its text, rather than losing it.
func (a *App) closeSide() {
	p := a.side
	if p == nil {
		return
	}
	p.flush()
	if p.dirty {
		a.toast("Couldn't save " + path.Base(p.rel) + ", so the pane was left open")
		return
	}
	a.dropSide()
}

// dropSide closes the side pane without saving it: for a note that is gone, or
// already saved.
func (a *App) dropSide() {
	p := a.side
	if p == nil {
		return
	}
	hadFocus := p.ed.HasFocus()
	a.side = nil // a waiting autosave sees this and does nothing
	p.rel, p.dirty = "", false
	if a.sidePaned != nil {
		a.sidePaned.SetEndChild(nil)
	}
	if hadFocus && a.editor != nil {
		a.editor.Focus()
	}
}

// load puts a note into the pane.
func (p *sidePane) load(rel, content string) {
	p.rel, p.dirty = rel, false
	p.remember(rel, content)
	p.bindImages()
	p.ed.SetContent(content)
	p.showName()
	p.setDot("save-saved")
}

// showName writes the note's name in the pane's title.
func (p *sidePane) showName() {
	p.title.SetText(path.Base(p.rel))
	p.title.SetTooltipText(p.rel)
}

// bindImages points the pane's pictures at its note, as bindImages does for the
// main editor.
func (p *sidePane) bindImages() {
	store, rel := p.a.store, p.rel
	p.ed.LoadImage = func(mdPath string) ([]byte, error) { return store.ReadAttachment(rel, mdPath) }
	p.ed.SaveImage = func(data []byte) (string, error) { return store.SaveAttachment(rel, data) }
}

func (p *sidePane) setDot(class string) {
	for _, c := range []string{"save-saved", "save-unsaved", "save-saving"} {
		p.dot.RemoveCSSClass(c)
	}
	p.dot.AddCSSClass(class)
	p.dot.SetTooltipText(map[string]string{"save-saved": "Saved", "save-unsaved": "Unsaved", "save-saving": "Saving…"}[class])
}

func (p *sidePane) remember(rel, content string) {
	p.savedRel, p.savedHash = rel, hashContent(content)
}

func (p *sidePane) unchanged(rel, content string) bool {
	return p.savedRel == rel && p.savedHash == hashContent(content)
}

// onChanged runs on every edit: mark the pane dirty and arm its autosave.
func (p *sidePane) onChanged() {
	p.dirty = true
	p.setDot("save-unsaved")
	p.schedule()
}

// schedule debounces the autosave the way scheduleAutosave does for the main
// pane: one timer, which re-arms itself when edits arrived while it waited.
func (p *sidePane) schedule() {
	p.gen++
	if p.pending {
		return
	}
	p.pending = true
	gen := p.gen
	coreglib.TimeoutAdd(autosaveDelayMs, func() bool {
		p.pending = false
		if p.a.side != p || !p.dirty {
			return false
		}
		if p.gen != gen {
			p.schedule()
			return false
		}
		p.save()
		return false
	})
}

// save writes the pane's note off the main thread. It shares the app's wait
// group with the main pane's saves, so flushDirty waits for both.
func (p *sidePane) save() {
	a := p.a
	if a.store == nil || p.rel == "" || !p.dirty || p.inFlight {
		return
	}
	rel, content := p.rel, p.ed.Content()
	if p.unchanged(rel, content) {
		p.dirty = false
		p.setDot("save-saved")
		return
	}
	p.dirty = false
	p.inFlight = true
	p.setDot("save-saving")
	a.saveWG.Add(1)
	go func() {
		defer a.saveWG.Done()
		err := a.store.WriteNote(rel, content)
		coreglib.IdleAdd(func() bool {
			p.inFlight = false
			if err != nil {
				log.Printf("atlas-notes: save %q: %v", rel, err)
			}
			if a.closing || a.side != p || p.rel != rel {
				return false // closed, or on another note: its state stands
			}
			if err != nil {
				p.dirty = true
				p.setDot("save-unsaved")
				return false
			}
			p.setDot("save-saved")
			p.remember(rel, content)
			if p.dirty {
				p.schedule()
			}
			return false
		})
	}()
}

// flush writes the pane's note now if it has unsaved changes, after any save in
// flight, and reports whether it wrote. See flushDirty.
func (p *sidePane) flush() bool {
	a := p.a
	if a.store == nil || p.rel == "" {
		return false
	}
	a.saveWG.Wait()
	if !p.dirty {
		return false
	}
	content := p.ed.Content()
	if p.unchanged(p.rel, content) {
		p.dirty = false
		return false
	}
	if err := a.store.WriteNote(p.rel, content); err != nil {
		log.Printf("atlas-notes: save %q: %v", p.rel, err)
		p.setDot("save-unsaved")
		return false
	}
	p.dirty = false
	p.remember(p.rel, content)
	p.setDot("save-saved")
	return true
}

// reload reads the note again when what is on disk is no longer what the pane
// shows: links in it were rewritten by a rename or a merge. Nothing is lost,
// because everything was saved first; a pane with edits it has not saved is left
// alone, so they are not thrown away.
func (p *sidePane) reload() {
	if p.rel == "" || p.dirty || p.a.store == nil {
		return
	}
	text, err := p.a.store.ReadNote(p.rel)
	if err != nil || text == p.ed.Content() {
		return
	}
	p.remember(p.rel, text)
	p.ed.SetContent(text)
}

// sideLinksChanged is onLinksChanged for the side pane.
func (a *App) sideLinksChanged() {
	if a.side != nil {
		a.side.reload()
	}
}

// saveAll is Ctrl+S: both panes, without waiting.
func (a *App) saveAll() {
	a.saveCurrent()
	if a.side != nil {
		a.side.save()
	}
}

// onRenamed follows a note or folder that was renamed or moved, for the side
// pane. The main pane is followed by onMoved, which the vault panel calls for
// the open note only.
func (a *App) onRenamed(oldRel, newRel string, isFolder bool) {
	p := a.side
	if p == nil {
		return
	}
	switch {
	case p.rel == oldRel:
		p.rel = newRel
	case isFolder && strings.HasPrefix(p.rel, oldRel+"/"):
		p.rel = newRel + strings.TrimPrefix(p.rel, oldRel)
	default:
		return
	}
	p.showName()
	p.bindImages() // its pictures are relative to it, wherever it is now
	p.savedRel = p.rel
}

// sideDeleted closes the side pane when its note, or a folder holding it, was
// deleted. It does not save: that would write the note back.
func (a *App) sideDeleted(rel string, isFolder bool) {
	if p := a.side; p != nil && (p.rel == rel || (isFolder && strings.HasPrefix(p.rel, rel+"/"))) {
		a.dropSide()
	}
}

// closeSideIfLocked closes the side pane when its note is protected, for locking
// the vault: its text must not stay on screen.
func (a *App) closeSideIfLocked() {
	if a.side != nil && a.store.IsNoteLocked(a.side.rel) {
		a.dropSide() // flushDirty has already saved it
	}
}
