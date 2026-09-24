package app

import (
	"io/fs"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"

	"atlas-notes/internal/update"
)

// fixedWindowTitle keeps the window's title constant (ATLAS_DEV_TITLE=1) so the
// screenshot tooling can find the window by name while notes are open. The
// title is deliberately not the app's own, so a capture can never pick up a
// copy of Atlas Notes the user happens to have open.
var fixedWindowTitle = os.Getenv("ATLAS_DEV_TITLE") != ""

// devCaptureTitle is the window title used during screenshot runs.
const devCaptureTitle = "Atlas Notes Dev Capture"

// devRun reports whether this process is a measurement or screenshot run. Those
// opt out of single-instance behaviour, so they neither hand off to nor disturb
// a copy of Atlas Notes the user is actually using.
func devRun() bool {
	if benchMode != "" || traceOn || fixedWindowTitle {
		return true
	}
	for _, v := range []string{"ATLAS_DEV_VIEW", "ATLAS_PPROF", "ATLAS_CPUPROF"} {
		if os.Getenv(v) != "" {
			return true
		}
	}
	return false
}

// runDevView implements ATLAS_DEV_VIEW, which opens a named UI surface shortly
// after startup. It exists so the screenshot tooling can capture dialogs
// (docs/screenshots) without a human driving the app; it is inert unless the
// variable is set.
func (a *App) runDevView() {
	view := strings.ToLower(os.Getenv("ATLAS_DEV_VIEW"))
	if view == "" {
		return
	}
	coreglib.TimeoutAdd(400, func() bool {
		name, arg, _ := strings.Cut(view, "=")
		switch name {
		case "settings":
			a.showSettingsPage(arg)
		case "shortcuts":
			a.showShortcuts()
		case "about":
			a.showAbout()
		case "icons":
			a.showIconSheet()
		case "home":
			a.showWelcome()
		case "locks":
			a.devLockState()
		case "update":
			a.showUpdateFound(&update.Release{
				Version: "0.5.5",
				Notes: []string{
					"Atlas Notes now tells you when a new version is out",
					"The toolbar icons all match each other now",
					"Checkboxes line up neatly with their text",
					"Big vaults open much faster, and searching them is instant",
					"Fixed a crash when a note contained certain punctuation",
				},
			})
		case "search":
			if a.tree != nil {
				a.tree.SetSearch(arg)
			}
		case "item-menu":
			// Open a task line's context menu (arg = line number) so the
			// screenshot tooling can capture it.
			if a.editor != nil {
				ln, _ := strconv.Atoi(arg)
				a.editor.ShowItemMenu(ln)
			}
		}
		return false
	})
}

// showIconSheet fills the window with every icon the app embeds, at a large
// size and under its own name, so a capture shows exactly what each name
// resolves to. The list comes from the embedded directory rather than being
// written out here, so it cannot fall behind what is actually shipped.
func (a *App) showIconSheet() {
	entries, err := fs.ReadDir(iconFS, "icons")
	if err != nil {
		log.Printf("atlas-notes: icon sheet: %v", err)
		return
	}
	grid := gtk.NewFlowBox()
	grid.SetSelectionMode(gtk.SelectionNone)
	grid.SetMaxChildrenPerLine(8)
	grid.SetRowSpacing(18)
	grid.SetColumnSpacing(18)
	grid.SetHAlign(gtk.AlignCenter)
	grid.SetVAlign(gtk.AlignCenter)
	grid.SetVExpand(true)
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), ".svg")
		cell := gtk.NewBox(gtk.OrientationVertical, 6)
		img := gtk.NewImageFromIconName(name)
		img.SetPixelSize(40)
		cell.Append(img)
		caption := gtk.NewLabel(strings.TrimSuffix(strings.TrimPrefix(name, "atlas-"), "-symbolic"))
		caption.AddCSSClass("caption")
		caption.AddCSSClass("dim-label")
		cell.Append(caption)
		grid.Append(cell)
	}
	row := grid
	if a.centerStack == nil {
		return
	}
	if old := a.centerStack.ChildByName("iconsheet"); old != nil {
		a.centerStack.Remove(old)
	}
	a.centerStack.AddNamed(row, "iconsheet")
	a.centerStack.SetVisibleChildName("iconsheet")
}

// devLockState sets a password, protects a note and a folder, and stars a
// couple of things, so a capture can show what the vault panel does with them.
// It exists for the screenshot tooling; nothing calls it in a normal run.
func (a *App) devLockState() {
	if a.store == nil {
		return
	}
	if !a.store.HasPassword() {
		if err := a.store.SetPassword("screenshot"); err != nil {
			log.Printf("atlas-notes: dev lock: %v", err)
			return
		}
	}
	notes, err := a.store.ListNotes()
	if err != nil || len(notes) < 4 {
		log.Printf("atlas-notes: dev lock: not enough notes (%v)", err)
		return
	}
	if err := a.store.LockNote(notes[1].Path); err != nil {
		log.Printf("atlas-notes: dev lock note: %v", err)
	}
	if folders, ferr := a.store.ListFolders(); ferr == nil && len(folders) > 0 {
		if err := a.store.LockFolder(folders[0]); err != nil {
			log.Printf("atlas-notes: dev lock folder: %v", err)
		}
		a.cfg.FavouriteFolders = []string{folders[0]}
	}
	a.cfg.FavouriteNotes = []string{notes[0].Path, notes[2].Path}
	if a.tree != nil {
		a.tree.ForceRefresh()
	}
}
