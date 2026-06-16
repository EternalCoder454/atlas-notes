package app

import (
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-notes/internal/ui"
)

const (
	leftPanelWidth  = 220
	rightPanelWidth = 280
	leftMinWidth    = 150 // hard minimum so note names stay readable when dragged narrow
)

// buildWindow constructs the main window: a header bar plus three drag-resizable
// panes (folder tree · editor · AI sidebar). Each side panel can be retracted
// from the header bar.
func (a *App) buildWindow() {
	a.win = adw.NewApplicationWindow(&a.adw.Application)
	a.win.SetTitle("Atlas Notes")
	a.win.SetDefaultSize(a.cfg.WindowWidth, a.cfg.WindowHeight)

	header := adw.NewHeaderBar()
	header.SetTitleWidget(adw.NewWindowTitle("Atlas Notes", "v"+version))

	leftToggle := gtk.NewToggleButton()
	leftToggle.SetIconName("sidebar-show-symbolic")
	leftToggle.SetActive(true)
	leftToggle.SetTooltipText("Toggle folder panel")
	header.PackStart(leftToggle)

	settingsBtn := gtk.NewButtonFromIconName("emblem-system-symbolic")
	settingsBtn.SetTooltipText("Settings")
	settingsBtn.ConnectClicked(a.showSettings)
	header.PackEnd(settingsBtn)

	rightToggle := gtk.NewToggleButton()
	rightToggle.SetIconName("sidebar-show-right-symbolic")
	rightToggle.SetActive(true)
	rightToggle.SetTooltipText("Toggle AI panel")
	header.PackEnd(rightToggle)

	// Left panel: folder tree.
	a.left = newPanel("left-panel")
	a.left.SetSizeRequest(leftMinWidth, -1)
	if a.store != nil {
		a.tree = ui.NewTree(a.store, a.win, a.ai)
		a.tree.OnOpenNote = a.openNote
		a.tree.OnDeleted = a.onDeleted
		a.tree.OnMoved = a.onMoved
		a.tree.SetSummariesEnabled(a.cfg.EnableTreeSummaries)
		a.left.Append(a.tree.Widget())
	} else {
		a.left.Append(placeholder("Folders"))
	}

	// Center: editor.
	a.center = a.buildCenter()

	// Right panel: AI sidebar.
	a.right = newPanel("right-panel")
	if a.ai != nil {
		a.sidebar = ui.NewSidebar(a.ai)
		a.sidebar.GetContent = a.editorContent
		a.sidebar.SetContent = a.applyAIContent
		a.sidebar.SetActions(a.cfg.Actions)
		a.sidebar.SetName(a.cfg.AssistantName)
		a.right.Append(a.sidebar.Widget())
	} else {
		a.right.Append(placeholder("AI Assistant"))
	}

	// Nested resizable panes: [ left | [ center | right ] ].
	inner := gtk.NewPaned(gtk.OrientationHorizontal)
	inner.SetStartChild(a.center)
	inner.SetEndChild(a.right)
	inner.SetResizeStartChild(true)
	inner.SetResizeEndChild(false)
	inner.SetShrinkStartChild(false)
	inner.SetShrinkEndChild(false)
	inner.SetWideHandle(true)

	outer := gtk.NewPaned(gtk.OrientationHorizontal)
	outer.SetStartChild(a.left)
	outer.SetEndChild(inner)
	outer.SetResizeStartChild(false)
	outer.SetResizeEndChild(true)
	outer.SetShrinkStartChild(false) // floor enforced by the left panel's min width
	outer.SetShrinkEndChild(false)
	outer.SetWideHandle(true)
	outer.SetPosition(leftPanelWidth)

	centerWidth := a.cfg.WindowWidth - leftPanelWidth - rightPanelWidth
	if centerWidth < 300 {
		centerWidth = 300
	}
	inner.SetPosition(centerWidth)

	leftToggle.ConnectToggled(func() { a.left.SetVisible(leftToggle.Active()) })
	rightToggle.ConnectToggled(func() { a.right.SetVisible(rightToggle.Active()) })

	a.toastOverlay = adw.NewToastOverlay()
	a.toastOverlay.SetChild(outer)

	toolbar := adw.NewToolbarView()
	toolbar.AddTopBar(header)
	toolbar.SetContent(a.toastOverlay)

	a.win.SetContent(toolbar)

	a.addSaveShortcut()
}

// addSaveShortcut binds Ctrl+S to an immediate save, complementing the 30s
// autosave. Managed scope routes the accelerator through the window, so it fires
// no matter which widget (editor, sidebar entry, tree) currently holds focus.
func (a *App) addSaveShortcut() {
	controller := gtk.NewShortcutController()
	controller.SetScope(gtk.ShortcutScopeManaged)
	controller.AddShortcut(gtk.NewShortcut(
		gtk.NewKeyvalTrigger(gdk.KEY_s, gdk.ControlMask),
		gtk.NewCallbackAction(func(gtk.Widgetter, *glib.Variant) bool {
			a.saveCurrent()
			return true
		}),
	))
	a.win.AddController(controller)
}

// newPanel returns a vertical box with the given CSS class. Width is governed by
// the surrounding GtkPaned, so panels can be dragged to resize.
func newPanel(cssClass string) *gtk.Box {
	box := gtk.NewBox(gtk.OrientationVertical, 0)
	box.AddCSSClass(cssClass)
	return box
}

// placeholder is fallback panel content when a service failed to initialize.
func placeholder(text string) *gtk.Label {
	l := gtk.NewLabel(text)
	l.SetVAlign(gtk.AlignCenter)
	l.SetHAlign(gtk.AlignCenter)
	l.SetVExpand(true)
	l.AddCSSClass("dim-label")
	return l
}
