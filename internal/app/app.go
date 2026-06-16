// Package app wires the AdwApplication, main window, three-panel layout, and the
// storage / AI services together.
package app

import (
	"fmt"
	"log"
	"path"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-notes/internal/ai"
	"atlas-notes/internal/editor"
	"atlas-notes/internal/storage"
	"atlas-notes/internal/ui"
)

const (
	// appID is all-lowercase so the Wayland app-id matches the .desktop file's
	// basename exactly (GNOME shows the dock name/icon from that match).
	appID = "io.github.atlasnotes"
	// autosaveDelayMs debounces autosave: the note is written this long after the
	// last edit (the timer resets on each keystroke), so saves are timely and the
	// indicator reflects real activity.
	autosaveDelayMs = 1500
)

// Save-indicator states, surfaced as a red/amber/green dot.
const (
	saveSaved   = iota // green: persisted
	saveUnsaved        // red: unsaved changes
	saveSaving         // amber: write in progress
)

// App is the top-level application controller. It owns the long-lived services
// and references to the three panels so later steps can wire them together.
type App struct {
	adw *adw.Application
	css string

	cfg   storage.Config
	store *storage.Store
	ai    *ai.Client

	win    *adw.ApplicationWindow
	left   *gtk.Box
	center *gtk.Box
	right  *gtk.Box

	tree    *ui.Tree
	editor  *editor.Editor
	sidebar *ui.Sidebar

	titleEntry  *gtk.Entry
	saveDot     *gtk.Box
	saveLabel   *gtk.Label
	statusLabel *gtk.Label

	currentNote  string
	dirty        bool
	saveState    int
	autosaveGen  int
	saveInFlight bool           // an async save is running
	saveWG       sync.WaitGroup // tracks the in-flight async save goroutine

	toastOverlay  *adw.ToastOverlay
	aiUndoContent string // note content before the last AI change (one-step undo)
	aiUndoNote    string // which note aiUndoContent belongs to
}

// New constructs the application without starting the main loop. css is the
// embedded stylesheet applied at activation.
func New(css string) *App {
	return &App{
		adw: adw.NewApplication(appID, gio.ApplicationFlagsNone),
		css: css,
	}
}

// Run initializes resources and runs the GTK main loop, returning the exit code.
func (a *App) Run(args []string) int {
	a.adw.ConnectActivate(a.activate)
	a.adw.ConnectShutdown(a.shutdown)
	return a.adw.Run(args)
}

func (a *App) activate() {
	cfg, err := storage.LoadConfig()
	if err != nil {
		log.Printf("atlas-notes: load config: %v", err)
	}
	a.cfg = cfg

	store, err := storage.Open(cfg.VaultPath, "")
	if err != nil {
		log.Printf("atlas-notes: open storage: %v", err)
	} else {
		a.store = store
		if err := a.store.Reindex(); err != nil {
			log.Printf("atlas-notes: reindex: %v", err)
		}
		if err := a.store.EnsureWelcome(); err != nil {
			log.Printf("atlas-notes: welcome note: %v", err)
		}
	}

	a.ai = ai.NewClient(cfg.Model, cfg.SystemPrompt)

	a.loadCSS()
	a.buildWindow()
	a.win.SetVisible(true)

	a.openInitialNote()
}

func (a *App) shutdown() {
	a.flushDirty()
	if err := storage.SaveConfig(a.cfg); err != nil {
		log.Printf("atlas-notes: save config: %v", err)
	}
	if a.store != nil {
		if err := a.store.Close(); err != nil {
			log.Printf("atlas-notes: close storage: %v", err)
		}
	}
}

// loadCSS applies the embedded stylesheet to the default display.
func (a *App) loadCSS() {
	if a.css == "" {
		return
	}
	provider := gtk.NewCSSProvider()
	provider.LoadFromString(a.css)
	if display := gdk.DisplayGetDefault(); display != nil {
		gtk.StyleContextAddProviderForDisplay(display, provider, gtk.STYLE_PROVIDER_PRIORITY_APPLICATION)
	}
}

// openNote flushes any pending edits, then loads rel into the editor.
func (a *App) openNote(rel string) {
	if a.store == nil || a.editor == nil {
		return
	}
	a.flushDirty()
	content, err := a.store.ReadNote(rel)
	if err != nil {
		log.Printf("atlas-notes: open note %q: %v", rel, err)
		return
	}
	a.currentNote = rel
	a.dirty = false
	a.cfg.LastNote = rel
	a.editor.SetContent(content)
	a.refreshHeader()
	if a.tree != nil {
		a.tree.SetCurrent(rel)
	}
}

// onDeleted clears the editor when the note currently open (or a folder
// containing it) is deleted, so a stale note isn't left on screen. Clearing
// currentNote also stops a pending autosave from re-creating the file.
func (a *App) onDeleted(rel string, isFolder bool) {
	affected := a.currentNote != "" &&
		(a.currentNote == rel || (isFolder && strings.HasPrefix(a.currentNote, rel+"/")))
	if !affected {
		return
	}
	a.currentNote = ""
	a.dirty = false
	if a.editor != nil {
		a.editor.SetContent("")
	}
	if a.titleEntry != nil {
		a.titleEntry.Buffer().SetText("", -1)
	}
	a.setSaveState(saveSaved)
	a.updateStats()
	if a.tree != nil {
		a.tree.SetCurrent("")
	}
}

// onMoved keeps the open note in sync after it's dragged into another folder.
func (a *App) onMoved(oldRel, newRel string) {
	if a.currentNote != oldRel {
		return
	}
	a.currentNote = newRel
	a.cfg.LastNote = newRel
	if a.titleEntry != nil {
		a.titleEntry.Buffer().SetText(path.Base(newRel), -1)
	}
	if a.tree != nil {
		a.tree.SetCurrent(newRel)
	}
}

// openInitialNote opens the last-used note, falling back to Welcome.
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
		target = "Welcome"
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

// onEditorReparsed runs on the editor's 50ms debounce (not per keystroke), so the
// word/character count and the sort-button state recompute once typing pauses.
// The document text is reconstructed once here and shared by both.
func (a *App) onEditorReparsed() {
	if a.editor == nil {
		return
	}
	a.updateStatsWith(a.editor.Content())
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

// scheduleAutosave debounces the autosave: every edit bumps a generation counter
// and arms a timer; only the latest timer performs the save, so the note is
// written once typing pauses for autosaveDelayMs. Idle CPU stays at zero.
func (a *App) scheduleAutosave() {
	a.autosaveGen++
	gen := a.autosaveGen
	coreglib.TimeoutAdd(autosaveDelayMs, func() bool {
		if gen == a.autosaveGen && a.dirty {
			a.saveCurrent()
		}
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
	if err := a.store.WriteNote(a.currentNote, a.editor.Content()); err != nil {
		log.Printf("atlas-notes: save %q: %v", a.currentNote, err)
		a.setSaveState(saveUnsaved)
		return false
	}
	a.dirty = false
	return true
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
		a.tree.Refresh()
	}
}

// refreshHeader syncs the title entry, stats, and save indicator with the open note.
func (a *App) refreshHeader() {
	if a.titleEntry != nil {
		a.titleEntry.Buffer().SetText(path.Base(a.currentNote), -1)
	}
	a.updateStats()
	a.setSaveState(saveSaved)
}

func (a *App) updateStats() {
	if a.editor != nil {
		a.updateStatsWith(a.editor.Content())
	}
}

// updateStatsWith sets the word/character counter from already-fetched content,
// so the debounced reparse reuses one reconstruction for both the counter and
// the AI button state instead of rebuilding the document text twice.
func (a *App) updateStatsWith(content string) {
	if a.statusLabel == nil {
		return
	}
	words := len(strings.Fields(content))
	chars := utf8.RuneCountInString(content)
	a.statusLabel.SetText(fmt.Sprintf("%d words · %d characters", words, chars))
}

// setSaveState updates the red/amber/green save dot and its label.
func (a *App) setSaveState(state int) {
	a.saveState = state
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
