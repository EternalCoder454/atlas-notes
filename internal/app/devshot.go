package app

import (
	"os"
	"strconv"
	"strings"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
)

// fixedWindowTitle keeps the window's title constant (ATLAS_DEV_TITLE=1) so the
// screenshot tooling can find the window by name while notes are open.
var fixedWindowTitle = os.Getenv("ATLAS_DEV_TITLE") != ""

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
			a.showSettings()
		case "shortcuts":
			a.showShortcuts()
		case "about":
			a.showAbout()
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
