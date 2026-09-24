package app

import (
	"hash/fnv"
	"log"
	"path"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"

	"atlas-notes/internal/storage"
)

// Everything about the note that is currently open: loading it, following it
// through renames and deletions, marking it dirty, and getting it back onto
// disk. Saving is deliberately in one place — autosave, Ctrl+S, switching
// notes and shutting down all funnel through saveCurrent and flushDirty.

// openNote flushes any pending edits, then loads rel into the editor.
func (a *App) openNote(rel string) {
	if a.store == nil || a.editor == nil {
		return
	}
	a.flushDirty()
	content, err := a.store.ReadNote(rel)
	if err == storage.ErrLocked {
		// Not a failure: the note is protected and the password has not been
		// given yet. Ask, then open it.
		a.ensureUnlocked(func() { a.openNote(rel) })
		return
	}
	if err != nil {
		log.Printf("atlas-notes: open note %q: %v", rel, err)
		a.toast("Couldn't open that note: " + err.Error())
		return
	}
	a.currentNote = rel
	a.dirty = false
	a.cfg.LastNote = rel
	a.rememberSaved(rel, content) // what is on disk right now
	a.editor.SetContent(content)
	a.showEditorPage()
	a.refreshHeader()
	a.editor.Focus()
	if a.tree != nil {
		a.tree.SetCurrent(rel)
	}
}

// onDeleted clears the editor when the note currently open (or a folder
// containing it) is deleted, so a stale note isn't left on screen. Clearing
// currentNote also stops a pending autosave from re-creating the file.
func (a *App) onDeleted(rel string, isFolder bool) {
	a.forgetFavourite(rel, isFolder)
	affected := a.currentNote != "" &&
		(a.currentNote == rel || (isFolder && strings.HasPrefix(a.currentNote, rel+"/")))
	if !affected {
		return
	}
	if a.titleEntry != nil {
		a.titleEntry.Buffer().SetText("", -1)
	}
	a.setSaveState(saveSaved)
	a.updateStats()
	a.showWelcome()
}

// onMoved keeps the open note in sync after it's dragged into another folder.
func (a *App) onMoved(oldRel, newRel string) {
	if a.currentNote != oldRel {
		return
	}
	a.currentNote = newRel
	a.cfg.LastNote = newRel
	a.refreshHeader()
	if a.tree != nil {
		a.tree.SetCurrent(newRel)
	}
}

// openInitialNote reopens the note from last time. When there isn't one — a
// fresh install, or the note has been deleted — the home screen is shown
// instead of dropping the user into a document they never wrote.
func (a *App) openInitialNote() {
	if a.store == nil || a.editor == nil {
		return
	}
	target := a.cfg.LastNote
	if target != "" {
		if _, err := a.store.ReadNote(target); err != nil {
			target = ""
		}
	}
	if target == "" {
		a.showWelcome()
		return
	}
	a.openNote(target)
}

// onEditorChanged runs on every keystroke, so it stays cheap: mark dirty, flip
// the indicator, arm the autosave. The expensive document scans (word count, AI
// button state) are deferred to onEditorReparsed below.
func (a *App) onEditorChanged() {
	a.dirty = true
	a.setSaveState(saveUnsaved)
	a.scheduleAutosave()
}

// onEditorReparsed runs after each render pass. It only flags the footer as
// stale: recomputing it means pulling the whole document out of the text buffer
// and walking it twice, which measured as 82% of the cost of a keystroke on a
// 50 KB note. A word count does not need to be that fresh.
func (a *App) onEditorReparsed() {
	a.scheduleStats()
}

// statsDelayMs is how long the footer may lag behind the text.
const statsDelayMs = 400

// scheduleStats coalesces footer refreshes onto a single timer, re-armed while
// typing continues (see scheduleAutosave for why one timer rather than one per
// edit).
func (a *App) scheduleStats() {
	a.statsDirty = true
	if a.statsPending {
		return
	}
	a.statsPending = true
	coreglib.TimeoutAdd(statsDelayMs, func() bool {
		a.statsPending = false
		if a.statsDirty {
			a.statsDirty = false
			a.updateStats()
		}
		return false
	})
}

func (a *App) editorContent() string {
	if a.editor == nil {
		return ""
	}
	return a.editor.Content()
}

// applyAIContent replaces the editor content with AI output (Clean & Format,
// Sort Priorities), marks it dirty, and persists immediately.
func (a *App) applyAIContent(content string) {
	if a.editor == nil {
		return
	}
	a.aiUndoContent = a.editor.Content() // snapshot for one-step undo
	a.aiUndoNote = a.currentNote
	a.editor.SetContent(content)
	a.dirty = true
	if a.flushDirty() {
		a.setSaveState(saveSaved)
	}
	a.updateStats()
	a.showUndoToast()
}

// showUndoToast offers a one-step undo of the AI change just applied to the note.
func (a *App) showUndoToast() {
	if a.toastOverlay == nil {
		return
	}
	toast := adw.NewToast("Note updated by Atlas")
	toast.SetButtonLabel("Undo")
	toast.SetTimeout(6)
	toast.ConnectButtonClicked(a.undoAIContent)
	a.toastOverlay.AddToast(toast)
}

// undoAIContent restores the note to its content from just before the last AI
// change, as long as the same note is still open.
func (a *App) undoAIContent() {
	if a.editor == nil || a.currentNote != a.aiUndoNote {
		return
	}
	a.editor.SetContent(a.aiUndoContent)
	a.dirty = true
	if a.flushDirty() {
		a.setSaveState(saveSaved)
	}
	a.updateStats()
	a.aiUndoContent, a.aiUndoNote = "", ""
}

// scheduleAutosave debounces the autosave: the note is written once typing has
// paused for autosaveDelayMs.
//
// Only one timer is ever in flight. The obvious version — arm a timer per edit
// and let the newest one win — leaves a timer per keystroke pending, and the
// bindings keep every timer's callback alive for the life of the process, so a
// long writing session accumulated thousands of them. Instead the pending timer
// re-arms itself when edits arrived while it was waiting.
func (a *App) scheduleAutosave() {
	a.autosaveGen++
	if a.autosavePending {
		return // a timer is already running; it will see the newer generation
	}
	a.autosavePending = true
	gen := a.autosaveGen
	coreglib.TimeoutAdd(autosaveDelayMs, func() bool {
		a.autosavePending = false
		if !a.dirty {
			return false
		}
		if a.autosaveGen != gen {
			a.scheduleAutosave() // edits landed while waiting: wait again
			return false
		}
		a.saveCurrent()
		return false
	})
}

// saveCurrent persists the open note off the main thread. It snapshots the
// content (GTK access must stay on the main thread), flips the indicator to
// amber, then writes on a background goroutine via the thread-safe Store and
// marks the result green/red. Coalesced by saveInFlight so only one write runs
// at a time; edits arriving mid-write schedule another. Used by autosave and
// Ctrl+S. No tree refresh: a row's label is the filename, which a content save
// never changes (and refreshing would collapse expanded folders).
func (a *App) saveCurrent() {
	if a.store == nil || a.editor == nil || a.currentNote == "" || !a.dirty || a.saveInFlight {
		return
	}
	rel := a.currentNote
	content := a.editor.Content()
	if a.contentUnchanged(rel, content) {
		a.dirty = false
		a.setSaveState(saveSaved)
		return
	}
	a.dirty = false
	a.saveInFlight = true
	a.setSaveState(saveSaving)

	a.saveWG.Add(1)
	go func() {
		defer a.saveWG.Done()
		err := a.store.WriteNote(rel, content)
		coreglib.IdleAdd(func() bool {
			a.saveInFlight = false
			if err != nil {
				log.Printf("atlas-notes: save %q: %v", rel, err)
			}
			if a.currentNote != rel {
				return false // moved on to another note; its state stands
			}
			if err != nil {
				a.dirty = true
				a.setSaveState(saveUnsaved)
				return false
			}
			a.setSaveState(saveSaved)
			a.rememberSaved(rel, content)
			if a.dirty { // edits arrived while the write was in flight
				a.scheduleAutosave()
			}
			return false
		})
	}()
}

// flushDirty writes the open note synchronously when it has unsaved changes,
// returning whether a write happened. It first waits for any in-flight async
// save to finish, so the newest content always wins on disk. Used where the save
// must complete before the next step: switching notes, renaming, or shutting
// down.
func (a *App) flushDirty() bool {
	if a.store == nil || a.editor == nil || a.currentNote == "" {
		return false
	}
	a.saveWG.Wait() // let any background save complete before we (re)write
	if !a.dirty {
		return false
	}
	content := a.editor.Content()
	if a.contentUnchanged(a.currentNote, content) {
		a.dirty = false
		return false
	}
	if err := a.store.WriteNote(a.currentNote, content); err != nil {
		log.Printf("atlas-notes: save %q: %v", a.currentNote, err)
		a.setSaveState(saveUnsaved)
		return false
	}
	a.dirty = false
	a.rememberSaved(a.currentNote, content)
	return true
}

// contentUnchanged reports whether content is byte-for-byte what is already on
// disk for rel. Autosave fires on a timer, and editing that ends where it
// started — typing and deleting again, ticking a box twice — would otherwise
// recompress and rewrite the file, touch its modification time and disturb the
// vault index for no change at all.
func (a *App) contentUnchanged(rel, content string) bool {
	return a.savedNote == rel && a.savedHash == hashContent(content)
}

// rememberSaved records what is now on disk for rel.
func (a *App) rememberSaved(rel, content string) {
	a.savedNote, a.savedHash = rel, hashContent(content)
}

func hashContent(s string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(s))
	return h.Sum64()
}

// onTitleActivate renames the current note when the title entry is committed.
func (a *App) onTitleActivate() {
	if a.store == nil || a.currentNote == "" || a.titleEntry == nil {
		return
	}
	newName := strings.TrimSpace(a.titleEntry.Buffer().Text())
	if newName == "" {
		return
	}
	newRel := replaceLeaf(a.currentNote, newName)
	if newRel == a.currentNote {
		return
	}
	a.flushDirty()
	if err := a.store.RenameNote(a.currentNote, newRel); err != nil {
		log.Printf("atlas-notes: rename via title: %v", err)
		return
	}
	a.currentNote = newRel
	a.cfg.LastNote = newRel
	a.setSaveState(saveSaved)
	if a.tree != nil {
		a.tree.SetCurrent(newRel)
		a.tree.ForceRefresh()
	}
}

// setSaveState updates the red/amber/green save dot and its label.
func (a *App) setSaveState(state int) {
	if a.saveDot != nil {
		for _, c := range []string{"save-saved", "save-unsaved", "save-saving"} {
			a.saveDot.RemoveCSSClass(c)
		}
		switch state {
		case saveUnsaved:
			a.saveDot.AddCSSClass("save-unsaved")
		case saveSaving:
			a.saveDot.AddCSSClass("save-saving")
		default:
			a.saveDot.AddCSSClass("save-saved")
		}
	}
	if a.saveLabel != nil {
		a.saveLabel.SetText(saveStateText(state))
	}
}

func saveStateText(state int) string {
	switch state {
	case saveUnsaved:
		return "Unsaved"
	case saveSaving:
		return "Saving…"
	default:
		return "Saved"
	}
}

func replaceLeaf(rel, newLeaf string) string {
	dir := path.Dir(rel)
	if dir == "." || dir == "/" {
		return newLeaf
	}
	return dir + "/" + newLeaf
}
