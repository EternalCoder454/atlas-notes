package app

import (
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-notes/internal/ui"
)

const (
	leftPanelWidth  = 260
	rightPanelWidth = 320
	leftMinWidth    = 180 // hard minimum so note names stay readable when dragged narrow
)

// buildWindow constructs the main window: a header bar plus three drag-resizable
// panes (vault · editor · assistant). Each side panel can be retracted from the
// header bar or with a keyboard shortcut.
func (a *App) buildWindow() {
	a.win = adw.NewApplicationWindow(&a.adw.Application)
	a.win.SetTitle("Atlas Notes")
	a.win.SetDefaultSize(a.cfg.WindowWidth, a.cfg.WindowHeight)
	a.win.AddCSSClass("atlas-window")

	a.registerActions()
	mark("actions")

	header := adw.NewHeaderBar()
	header.AddCSSClass("atlas-header")
	a.windowTitle = adw.NewWindowTitle("Atlas Notes", "")
	header.SetTitleWidget(a.windowTitle)

	a.leftToggle = gtk.NewToggleButton()
	a.leftToggle.SetIconName("sidebar-show-symbolic")
	a.leftToggle.SetActive(true)
	a.leftToggle.SetTooltipText("Show or hide the vault (F9)")
	header.PackStart(a.leftToggle)

	newBtn := gtk.NewButtonFromIconName("document-new-symbolic")
	newBtn.SetTooltipText("New note (Ctrl+N)")
	newBtn.ConnectClicked(a.actionNewNote)
	header.PackStart(newBtn)

	homeBtn := gtk.NewButtonFromIconName("go-home-symbolic")
	homeBtn.SetTooltipText("Home screen (Ctrl+H)")
	homeBtn.ConnectClicked(a.showWelcome)
	header.PackStart(homeBtn)

	menuBtn := gtk.NewMenuButton()
	menuBtn.SetIconName("open-menu-symbolic")
	menuBtn.SetTooltipText("Main menu")
	menuBtn.SetPrimary(true)
	menuBtn.SetMenuModel(a.buildMainMenu())
	header.PackEnd(menuBtn)

	a.rightToggle = gtk.NewToggleButton()
	a.rightToggle.SetIconName("sidebar-show-right-symbolic")
	a.rightToggle.SetActive(true)
	a.rightToggle.SetTooltipText("Show or hide the assistant (F10)")
	header.PackEnd(a.rightToggle)

	// Left panel: the vault browser.
	a.left = newPanel("left-panel")
	a.left.SetSizeRequest(leftMinWidth, -1)
	if a.store != nil {
		a.tree = ui.NewTree(a.store, a.win, a.ai)
		a.tree.OnOpenNote = a.openNote
		a.tree.OnDeleted = a.onDeleted
		a.tree.OnMoved = a.onMoved
		a.tree.OnChanged = a.refreshWelcome
		a.tree.SetSummariesEnabled(a.cfg.EnableTreeSummaries)
		a.left.Append(a.tree.Widget())
	} else {
		a.left.Append(placeholder("Vault unavailable"))
	}

	mark("header+tree")

	// Center: welcome screen / editor.
	a.center = a.buildCenter()
	mark("center")

	// Right panel: the assistant. Its contents are filled in just after the
	// window is on screen (see buildSidebar): the panel is a third of the
	// window to lay out and paint — including a Cairo-drawn orb — and none of
	// it is needed to show the note the user came back to.
	a.right = newPanel("right-panel")

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
	outer.SetPosition(panePosition(a.cfg.LeftPanelWidth, leftPanelWidth))

	right := panePosition(a.cfg.RightPanelWidth, rightPanelWidth)
	centerWidth := a.cfg.WindowWidth - outer.Position() - right
	if centerWidth < 360 {
		centerWidth = 360
	}
	inner.SetPosition(centerWidth)
	a.outerPaned, a.innerPaned = outer, inner

	a.leftToggle.ConnectToggled(func() { a.left.SetVisible(a.leftToggle.Active()) })
	a.rightToggle.ConnectToggled(func() { a.right.SetVisible(a.rightToggle.Active()) })

	a.toastOverlay = adw.NewToastOverlay()
	a.toastOverlay.SetChild(outer)

	toolbar := adw.NewToolbarView()
	toolbar.AddTopBar(header)
	toolbar.SetContent(a.toastOverlay)

	a.win.SetContent(toolbar)
}

// buildSidebar fills in the assistant panel. It runs from an idle callback
// after the first frame, so the window is interactive before the panel exists.
func (a *App) buildSidebar() {
	if a.sidebar != nil || a.right == nil {
		return
	}
	if a.ai == nil {
		a.right.Append(placeholder("Assistant unavailable"))
		return
	}
	a.sidebar = ui.NewSidebar(a.ai)
	a.sidebar.GetContent = a.editorContent
	a.sidebar.SetContent = a.applyAIContent
	a.sidebar.SetActions(a.cfg.Actions)
	a.sidebar.SetName(a.cfg.AssistantName)
	a.right.Append(a.sidebar.Widget())
	mark("sidebar")
}

// buildMainMenu is the primary (hamburger) menu: everything the app can do that
// isn't a one-click toolbar action, with its shortcut shown beside it.
func (a *App) buildMainMenu() *gio.Menu {
	menu := gio.NewMenu()

	notes := gio.NewMenu()
	notes.Append("New Note", "app.new-note")
	notes.Append("New Checklist", "app.new-checklist")
	notes.Append("New Folder", "app.new-folder")
	menu.AppendSection("", notes)

	current := gio.NewMenu()
	current.Append("Save Now", "app.save")
	current.Append("Rename…", "app.rename")
	current.Append("Find a Note", "app.search")
	menu.AppendSection("", current)

	view := gio.NewMenu()
	view.Append("Home Screen", "app.home")
	view.Append("Toggle Vault Panel", "app.toggle-vault")
	view.Append("Toggle Assistant", "app.toggle-assistant")
	menu.AppendSection("", view)

	app := gio.NewMenu()
	app.Append("Settings", "app.settings")
	app.Append("Keyboard Shortcuts", "app.shortcuts")
	app.Append("About Atlas Notes", "app.about")
	menu.AppendSection("", app)

	return menu
}

// panePosition falls back to a default when the stored width is unset or absurd.
func panePosition(stored, fallback int) int {
	if stored < 120 || stored > 900 {
		return fallback
	}
	return stored
}

// setWindowSubtitle shows the open note in the header bar, so the window title
// says what you are looking at instead of repeating the app's version.
func (a *App) setWindowSubtitle(note string) {
	if a.windowTitle == nil {
		return
	}
	if note == "" {
		a.windowTitle.SetTitle("Atlas Notes")
		a.windowTitle.SetSubtitle("")
		a.win.SetTitle("Atlas Notes")
		return
	}
	a.windowTitle.SetTitle(note)
	a.windowTitle.SetSubtitle("Atlas Notes")
	if fixedWindowTitle {
		return
	}
	a.win.SetTitle(note + " — Atlas Notes")
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
