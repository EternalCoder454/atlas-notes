package app

import (
	"os"
	"strconv"
	"strings"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
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
			a.showSettings()
		case "shortcuts":
			a.showShortcuts()
		case "about":
			a.showAbout()
		case "icons":
			a.showIconSheet()
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

// showIconSheet fills the window with every icon the UI uses, at a large size,
// so a capture shows exactly what the icon theme resolves each name to.
func (a *App) showIconSheet() {
	names := []string{
		"atlas-heading1-symbolic", "atlas-heading2-symbolic", "atlas-paragraph-symbolic",
		"atlas-code-symbolic", "atlas-quote-symbolic", "atlas-divider-symbolic",
		"atlas-bullet-list-symbolic", "atlas-task-symbolic", "atlas-assistant-symbolic",
		"format-text-bold-symbolic", "format-text-italic-symbolic",
		"format-text-strikethrough-symbolic", "view-list-symbolic",
		"document-edit-symbolic", "go-up-symbolic",
	}
	row := gtk.NewBox(gtk.OrientationHorizontal, 18)
	row.SetHAlign(gtk.AlignCenter)
	row.SetVAlign(gtk.AlignCenter)
	row.SetVExpand(true)
	for _, n := range names {
		img := gtk.NewImageFromIconName(n)
		img.SetPixelSize(48)
		row.Append(img)
	}
	if a.centerStack == nil {
		return
	}
	if old := a.centerStack.ChildByName("iconsheet"); old != nil {
		a.centerStack.Remove(old)
	}
	a.centerStack.AddNamed(row, "iconsheet")
	a.centerStack.SetVisibleChildName("iconsheet")
}
