// Package app wires the AdwApplication, main window, three-panel layout, and the
// storage / AI services together.
package app

import (
	"fmt"
	"log"
	"path"
	"strings"

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
	appID           = "io.github.atlasnotes"
	autosaveSeconds = 30
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
	saveLabel   *gtk.Label
	statusLabel *gtk.Label

	currentNote       string
	dirty             bool
	autosaveScheduled bool
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
	a.saveCurrent()
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
	a.saveCurrent()
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
		a.tree.SetCurrent(path.Base(rel))
	}
	if a.sidebar != nil {
		a.sidebar.UpdateState()
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
	a.setSaveState("")
	a.updateStats()
	if a.tree != nil {
		a.tree.SetCurrent("")
	}
	if a.sidebar != nil {
		a.sidebar.UpdateState()
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
		a.tree.SetCurrent(path.Base(newRel))
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
	a.setSaveState("Unsaved")
	a.scheduleAutosave()
}

// onEditorReparsed runs on the editor's 50ms debounce (not per keystroke), so the
// word/character count and the sort-button state recompute once typing pauses
// instead of reconstructing the whole document on every key press.
func (a *App) onEditorReparsed() {
	a.updateStats()
	if a.sidebar != nil {
		a.sidebar.UpdateState()
	}
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
	a.editor.SetContent(content)
	a.dirty = true
	a.saveCurrent()
	a.updateStats()
	if a.sidebar != nil {
		a.sidebar.UpdateState()
	}
}

// scheduleAutosave saves after 30s of inactivity via a one-shot timer (no
// polling loop), so idle CPU stays at zero.
func (a *App) scheduleAutosave() {
	if a.autosaveScheduled {
		return
	}
	a.autosaveScheduled = true
	coreglib.TimeoutSecondsAdd(autosaveSeconds, func() bool {
		a.autosaveScheduled = false
		a.saveCurrent()
		return false
	})
}

func (a *App) saveCurrent() {
	if a.store == nil || a.editor == nil || a.currentNote == "" || !a.dirty {
		return
	}
	if err := a.store.WriteNote(a.currentNote, a.editor.Content()); err != nil {
		log.Printf("atlas-notes: save %q: %v", a.currentNote, err)
		return
	}
	a.dirty = false
	a.setSaveState("Saved")
	// No tree refresh here: a row's label is the filename, which a content save
	// never changes. Skipping it keeps autosave off the tree-rebuild path (which
	// also collapses any expanded folders). Renames/moves refresh the tree
	// themselves.
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
	a.saveCurrent()
	if err := a.store.RenameNote(a.currentNote, newRel); err != nil {
		log.Printf("atlas-notes: rename via title: %v", err)
		return
	}
	a.currentNote = newRel
	a.cfg.LastNote = newRel
	if a.tree != nil {
		a.tree.SetCurrent(path.Base(newRel))
		a.tree.Refresh()
	}
}

// refreshHeader syncs the title entry, stats, and save indicator with the open note.
func (a *App) refreshHeader() {
	if a.titleEntry != nil {
		a.titleEntry.Buffer().SetText(path.Base(a.currentNote), -1)
	}
	a.updateStats()
	a.setSaveState("Saved")
}

func (a *App) updateStats() {
	if a.statusLabel == nil || a.editor == nil {
		return
	}
	words, chars := a.editor.Stats()
	a.statusLabel.SetText(fmt.Sprintf("%d words · %d characters", words, chars))
}

func (a *App) setSaveState(state string) {
	if a.saveLabel != nil {
		a.saveLabel.SetText(state)
	}
}

func replaceLeaf(rel, newLeaf string) string {
	dir := path.Dir(rel)
	if dir == "." || dir == "/" {
		return newLeaf
	}
	return dir + "/" + newLeaf
}
