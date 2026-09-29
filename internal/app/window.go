package app

import (
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-notes/internal/ui"
)

const (
	leftPanelWidth  = 260
	rightPanelWidth = 320
	leftMinWidth    = 180 // hard minimum so note names stay readable when dragged narrow
	// rightMinWidth is the assistant's floor. Without one, the pane was free to
	// squeeze it to nothing on the first layout, which happens before its
	// contents exist (see buildSidebar), and the app then saved that.
	rightMinWidth = 260
	// assistantMinWidth is the window width below which the assistant folds
	// away by itself: under it, three panels leave the note too little room,
	// and the note is what the window is for.
	assistantMinWidth = 1200
)

// buildWindow constructs the main window: a header bar plus three drag-resizable
// panes (vault · editor · assistant). Each side panel can be retracted from the
// header bar or with a keyboard shortcut.
func (a *App) buildWindow() {
	a.win = adw.NewApplicationWindow(&a.adw.Application)
	if fixedWindowTitle {
		a.win.SetTitle(devCaptureTitle)
	} else {
		a.win.SetTitle("Atlas Notes")
	}
	a.win.SetDefaultSize(a.cfg.WindowWidth, a.cfg.WindowHeight)
	a.win.AddCSSClass("atlas-window")

	a.registerActions()
	mark("actions")

	header := adw.NewHeaderBar()
	header.AddCSSClass("atlas-header")
	a.windowTitle = adw.NewWindowTitle("Atlas Notes", "")
	header.SetTitleWidget(a.windowTitle)

	a.leftToggle = gtk.NewToggleButton()
	a.leftToggle.SetIconName("atlasnotes-panel-left-symbolic")
	a.leftToggle.SetActive(true)
	a.leftToggle.SetTooltipText("Show or hide the vault (F9)")
	header.PackStart(a.leftToggle)

	newBtn := gtk.NewButtonFromIconName("atlasnotes-note-new-symbolic")
	newBtn.SetTooltipText("New note (Ctrl+N)")
	newBtn.ConnectClicked(a.actionNewNote)
	header.PackStart(newBtn)

	homeBtn := gtk.NewButtonFromIconName("atlasnotes-home-symbolic")
	homeBtn.SetTooltipText("Home screen (Ctrl+H)")
	homeBtn.ConnectClicked(a.showWelcome)
	header.PackStart(homeBtn)

	menuBtn := gtk.NewMenuButton()
	menuBtn.SetIconName("atlasnotes-menu-symbolic")
	menuBtn.SetTooltipText("Main menu")
	menuBtn.SetPrimary(true)
	menuBtn.SetMenuModel(a.buildMainMenu())
	header.PackEnd(menuBtn)

	// Settings is in the menu as well, but it is the one thing in there people
	// go looking for repeatedly, and two clicks behind a hamburger is not where
	// it belongs. The interface overhaul in 0.5.0 dropped this button and the
	// documentation went on describing it for five releases.
	//
	// The app's own gear is preferred when it is there; the system one is the
	// fallback, and it ships inside the Windows bundle's Adwaita theme.
	settingsBtn := gtk.NewButtonFromIconName(iconName("atlasnotes-settings-symbolic", "emblem-system-symbolic"))
	settingsBtn.SetTooltipText("Settings (Ctrl+,)")
	settingsBtn.ConnectClicked(a.showSettings)
	header.PackEnd(settingsBtn)

	a.rightToggle = gtk.NewToggleButton()
	// The assistant's own mark rather than a second sidebar arrow: the button
	// toggles the assistant, and the panel it opens carries the same shape.
	a.rightToggle.SetIconName(iconName("atlasnotes-assistant-symbolic", "atlasnotes-panel-right-symbolic"))
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
		a.tree.OnMessage = a.toast
		a.tree.OnExport = a.exportNote
		a.tree.OnMoved = a.onMoved
		a.tree.OnChanged = a.refreshWelcome
		a.tree.IsStarred = a.isStarred
		a.tree.OnToggleStar = a.toggleStar
		a.tree.OnLock = a.lockItem
		a.tree.OnUnlock = a.unlockItem
		a.tree.SetSummariesEnabled(a.cfg.EnableTreeSummaries) // fills the panel
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
	a.right.SetSizeRequest(rightMinWidth, -1)

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
	a.rightToggle.ConnectToggled(func() {
		a.right.SetVisible(a.rightToggle.Active())
		// A toggle the user pressed is what they want. One this code made to
		// fit the window is not, and must not overwrite it.
		if !a.fitting {
			a.wantAssistant = a.rightToggle.Active()
		}
	})
	a.watchWindowWidth()

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
	current.Append("Export…", "app.export")
	current.Append("Find a Note", "app.search")
	current.Append("Find in Note", "app.find")
	current.Append("Find and Replace", "app.find-replace")
	menu.AppendSection("", current)

	view := gio.NewMenu()
	view.Append("Lock Notes Now", "app.lock-now")
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
		if !fixedWindowTitle {
			a.win.SetTitle("Atlas Notes")
		}
		return
	}
	a.windowTitle.SetTitle(note)
	a.windowTitle.SetSubtitle("Atlas Notes")
	if fixedWindowTitle {
		return
	}
	a.win.SetTitle(note + " · Atlas Notes")
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

// watchWindowWidth folds the assistant away when the window gets too narrow
// for three panels, and brings it back when there is room, if it was open.
//
// It restores what 0.5.8 did and 0.5.9 took out. 0.5.9 took it out as a fix
// for the app closing itself, which it was not; the real cause was found and
// fixed in 0.5.10, and this was never put back, although the release notes
// went on describing it.
//
// Two things differ from before. It acts only when the width crosses the
// line, not on every change: open the assistant in a narrow window and it
// stays open until the window goes wide and comes back. And it reads the
// window's real size from its surface. The window's default-width, which is
// what it read before, does not change when a window is maximised, so a
// narrow window maximised onto a wide screen kept the assistant hidden.
func (a *App) watchWindowWidth() {
	if a.win == nil || a.rightToggle == nil {
		return
	}
	a.wantAssistant = a.rightToggle.Active()
	// Decided from the size the window is about to open at, before it is
	// shown, so a narrow window never draws the assistant only to fold it.
	a.fitAssistant(a.cfg.WindowWidth)
	a.win.ConnectRealize(func() {
		if surface := a.win.Surface(); surface != nil {
			gdk.BaseSurface(surface).ConnectLayout(func(width, _ int) { a.fitAssistant(width) })
		}
	})
}

// fitAssistant applies the rule for a window of the given width.
func (a *App) fitAssistant(width int) {
	if width <= 0 {
		return
	}
	narrow := width < assistantMinWidth
	if a.widthKnown && narrow == a.narrow {
		return // no line crossed; whatever the user chose stands
	}
	a.widthKnown, a.narrow = true, narrow
	show := a.wantAssistant && !narrow
	if show == a.rightToggle.Active() {
		return
	}
	a.fitting = true
	a.rightToggle.SetActive(show)
	a.fitting = false
}
