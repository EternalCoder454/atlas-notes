// Package app wires the AdwApplication, main window, three-panel layout, and the
// storage / AI services together.
package app

import (
	"log"
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
	if devRun() {
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
	installIcons()
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
		if w := a.win.Width(); w > 0 {
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
