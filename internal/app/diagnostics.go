package app

import (
	"log"
	"os"
	"strings"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-notes/internal/diag"
	"atlas-notes/internal/storage"
)

// The diagnostic log (see package diag): turning it on and off, and the
// window-wide watch on clicks and shortcuts that feeds it. The rest of the app
// adds its own events where something is decided, such as a note opening.

// applyDiagnostics starts or stops the log to match the setting. ATLAS_DIAG=1
// turns it on whatever the setting says.
func (a *App) applyDiagnostics() {
	if a.cfg.DiagnosticsLog || os.Getenv("ATLAS_DIAG") != "" {
		if err := diag.Start(storage.DiagnosticsLogPath()); err != nil {
			log.Printf("atlas-notes: diagnostic log: %v", err)
			return
		}
		diag.Event("app.version", "version", version)
		a.diagSeeing("diag on")
		return
	}
	diag.Stop()
}

// watchInput records every mouse press in the window and every shortcut, at
// the window before any widget sees them, so a click that comes to nothing is
// still in the log. It claims nothing. Typing is not recorded: a key counts
// only when it is held with Ctrl, Alt or Super, or is Escape or an F key.
func (a *App) watchInput() {
	click := gtk.NewGestureClick()
	click.SetButton(0) // every button
	click.SetPropagationPhase(gtk.PhaseCapture)
	click.ConnectPressed(func(n int, x, y float64) {
		if !diag.Enabled() {
			return
		}
		path, label := describeWidget(a.win.Pick(x, y, gtk.PickDefault))
		diag.Event("input.press", "button", click.CurrentButton(), "n", n,
			"x", int(x), "y", int(y), "label", label, "widget", path)
	})
	click.ConnectReleased(func(n int, x, y float64) {
		if diag.Enabled() {
			diag.Event("input.release", "button", click.CurrentButton(), "n", n, "x", int(x), "y", int(y))
		}
	})
	a.win.AddController(click)

	keys := gtk.NewEventControllerKey()
	keys.SetPropagationPhase(gtk.PhaseCapture)
	keys.ConnectKeyPressed(func(keyval, _ uint, state gdk.ModifierType) bool {
		if !diag.Enabled() {
			return false
		}
		mods := state & (gdk.ControlMask | gdk.AltMask | gdk.SuperMask)
		name := gdk.KeyvalName(keyval)
		if mods != 0 || name == "Escape" || (len(name) >= 2 && name[0] == 'F' && name[1] >= '1' && name[1] <= '9') {
			diag.Event("input.key", "key", name, "ctrl", state&gdk.ControlMask != 0,
				"alt", state&gdk.AltMask != 0, "shift", state&gdk.ShiftMask != 0, "super", state&gdk.SuperMask != 0)
		}
		return false
	})
	a.win.AddController(keys)

	a.win.NotifyProperty("is-active", func() {
		diag.Event("window.active", "active", a.win.IsActive())
	})
}

// describeWidget names what a click landed on: the chain of widgets from it
// outwards, and the nearest text a person would read as its name.
//
// What a note says must never reach the log, and labels can hold it: a row of
// the Tasks page is a button holding the task's text. So a button gives only
// its own label or tooltip, never the labels inside it, and a label or another
// widget's tooltip is taken only inside the app's own chrome, where it is a
// note's name at most: the vault panel's rows, a toast, the title bar, a menu.
func describeWidget(w gtk.Widgetter) (path, label string) {
	var chain []string
	var inner, button string
	for i := 0; w != nil && i < 12; i++ {
		base := gtk.BaseWidget(w)
		chain = append(chain, base.Name())
		switch v := w.(type) {
		case *gtk.Button:
			if button == "" {
				button = v.Label()
				if button == "" {
					button = base.TooltipText()
				}
			}
		case *gtk.Label:
			if inner == "" {
				inner = v.Label()
			}
		}
		if inner == "" {
			if _, isButton := w.(*gtk.Button); !isButton {
				inner = base.TooltipText()
			}
		}
		w = base.Parent()
	}
	path = strings.Join(chain, "<")
	label = button
	for _, own := range []string{"GtkTreeExpander", "AdwToastWidget", "AdwHeaderBar", "GtkPopoverMenu"} {
		if strings.Contains(path, own) && inner != "" {
			label = inner
			break
		}
	}
	if r := []rune(label); len(r) > 80 {
		label = string(r[:80])
	}
	return path, label
}

// diagSeeing records what the window is showing: the page, the note in each
// pane, and whether there are edits not yet on disk.
func (a *App) diagSeeing(why string) {
	if !diag.Enabled() {
		return
	}
	page := ""
	if a.centerStack != nil {
		page = a.centerStack.VisibleChildName()
	}
	side := ""
	if a.side != nil {
		side = a.side.rel
	}
	tree := ""
	if a.tree != nil {
		tree = a.tree.Current()
	}
	diag.Event("seeing", "why", why, "page", page, "note", a.currentNote, "side", side,
		"tree_current", tree, "dirty", a.dirty, "saving", a.saveInFlight)
}
