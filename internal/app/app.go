// Package app wires the AdwApplication, main window, three-panel layout, and the
// storage / AI services together.
package app

import (
	"hash/fnv"
	"log"
	"os"
	"path"
	"strings"
	"sync"

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

	windowTitle *adw.WindowTitle
	leftToggle  *gtk.ToggleButton
	rightToggle *gtk.ToggleButton
	outerPaned  *gtk.Paned
	innerPaned  *gtk.Paned
	centerStack *gtk.Stack

	titleEntry    *gtk.Entry
	breadcrumb    *gtk.Label
	saveDot       *gtk.Box
	saveLabel     *gtk.Label
	statusLabel   *gtk.Label
	readTimeLabel *gtk.Label
	vaultLabel    *gtk.Label
	taskProgress  *gtk.Label

	fontMode        string // text-rendering mode currently applied (see fonts.go)
	reindexPending  bool   // the vault scan runs after the first frame
	welcomeBuilt    bool   // the home screen is constructed on first use
	recents         []*recentRow
	recentsHeading  *gtk.Label
	currentNote     string
	dirty           bool
	saveState       int
	autosaveGen     int
	autosavePending bool           // an autosave timer is in flight
	statsDirty      bool           // the footer counts are out of date
	statsPending    bool           // a footer refresh is scheduled
	savedNote       string         // note the hash below belongs to
	savedHash       uint64         // hash of what is on disk, to skip no-op writes
	saveInFlight    bool           // an async save is running
	saveWG          sync.WaitGroup // tracks the in-flight async save goroutine

	toastOverlay  *adw.ToastOverlay
	aiUndoContent string // note content before the last AI change (one-step undo)
	aiUndoNote    string // which note aiUndoContent belongs to
}

func init() { stopCPUProfile = startCPUProfile() }

// New constructs the application without starting the main loop. css is the
// embedded stylesheet applied at activation.
func New(css string) *App {
	// Atlas Notes is single-instance: launching it again raises the window that
	// is already open. Measurement and screenshot runs opt out, so they neither
	// hand off to — nor disturb — a copy the user happens to be using.
	flags := gio.ApplicationFlagsNone
	if benchMode != "" || traceOn || os.Getenv("ATLAS_DEV_VIEW") != "" {
		flags = gio.ApplicationNonUnique
	}
	return &App{
		adw: adw.NewApplication(appID, flags),
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
	mark("activate")
	cfg, err := storage.LoadConfig()
	if err != nil {
		log.Printf("atlas-notes: load config: %v", err)
	}
	a.cfg = cfg
	mark("config")

	store, err := storage.Open(cfg.VaultPath, "")
	if err != nil {
		log.Printf("atlas-notes: open storage: %v", err)
	} else {
		a.store = store
		mark("store-open")
		// The index is a cache of what is on disk. When it already has content
		// the window can be built from it immediately and the vault scan runs
		// after the first frame (see scheduleReindex) — that scan is the only
		// part of startup that grows with the size of the vault. An empty index
		// (first launch, or a vault copied in) has to be built up front.
		if a.store.IsIndexEmpty() {
			if err := a.store.Reindex(); err != nil {
				log.Printf("atlas-notes: reindex: %v", err)
			}
			if err := a.store.EnsureWelcome(); err != nil {
				log.Printf("atlas-notes: welcome note: %v", err)
			}
		} else {
			a.reindexPending = true
		}
		mark("reindex")
	}

	a.ai = ai.NewClient(cfg.Model, cfg.SystemPrompt)

	a.loadCSS()
	a.applyFontRendering()
	mark("css")
	a.buildWindow()
	mark("window-built")
	a.win.SetVisible(true)
	a.applyFontRendering() // now that the window's own screen is known
	a.watchScaleChanges()
	mark("window-shown")

	a.openInitialNote()
	mark("initial-note")

	// Everything below happens after the window is up.
	coreglib.IdleAdd(func() bool {
		a.buildSidebar()
		return false
	})
	a.scheduleReindex()
	a.runBench()
	a.runDevView()
	printTrace()
}

func (a *App) shutdown() {
	a.flushDirty()
	a.rememberLayout()
	if err := storage.SaveConfig(a.cfg); err != nil {
		log.Printf("atlas-notes: save config: %v", err)
	}
	if a.store != nil {
		if err := a.store.Close(); err != nil {
			log.Printf("atlas-notes: close storage: %v", err)
		}
	}
}

// scheduleReindex runs the deferred vault scan just after the window is up. It
// happens on an idle callback at low priority, so the first frame paints first;
// the tree is refreshed only if the scan actually changed something.
func (a *App) scheduleReindex() {
	if !a.reindexPending || a.store == nil {
		return
	}
	a.reindexPending = false
	coreglib.IdleAddPriority(coreglib.PriorityLow, func() bool {
		before, _ := a.store.CountNotes()
		if err := a.store.Reindex(); err != nil {
			log.Printf("atlas-notes: reindex: %v", err)
			return false
		}
		if err := a.store.EnsureWelcome(); err != nil {
			log.Printf("atlas-notes: welcome note: %v", err)
		}
		if after, _ := a.store.CountNotes(); after != before && a.tree != nil {
			a.tree.ForceRefresh()
			a.refreshWelcome()
		}
		return false
	})
}

// rememberLayout stores the window and panel sizes so the next launch opens the
// way the user left it.
func (a *App) rememberLayout() {
	if a.win != nil {
		if w, h := a.win.DefaultSize(); w > 0 && h > 0 {
			a.cfg.WindowWidth, a.cfg.WindowHeight = w, h
		}
	}
	if a.outerPaned != nil {
		a.cfg.LeftPanelWidth = a.outerPaned.Position()
	}
	if a.innerPaned != nil && a.win != nil {
		if w := a.win.AllocatedWidth(); w > 0 {
			a.cfg.RightPanelWidth = w - a.outerPaned.Position() - a.innerPaned.Position()
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

func (a *App) updateStats() {
	if a.editor != nil {
		a.statsDirty = false
		a.updateStatsWith(a.editor.Content())
	}
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
