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

	// The title bar the way Windows 11 draws its apps: the name at the start
	// rather than a title in the middle, on the same surface as the side
	// panels, so the three read as one frame around the page. The note's name
	// is already at the top of the page, and the window title still carries it
	// for the taskbar (setWindowSubtitle).
	header := adw.NewHeaderBar()
	header.AddCSSClass("atlas-header")
	header.AddCSSClass("atlas-titlebar")
	header.SetTitleWidget(gtk.NewBox(gtk.OrientationHorizontal, 0))
	if hasIcon(appIconName) {
		a.win.SetIconName(appIconName) // what the title bar shows where it asks for one
	}

	// The start of the title bar is the app's name, as a Windows 11 title bar
	// has it; a desktop that draws the window's icon puts it just before. The
	// buttons are grouped at the end: the two panel toggles side by side, then
	// New note, then the menu. The vault's toggle used to sit before the name,
	// which on KDE left it wedged between the window's icon and the name.
	header.PackStart(brandBox())

	menuBtn := gtk.NewMenuButton()
	menuBtn.SetIconName("atlasnotes-menu-symbolic")
	menuBtn.SetTooltipText("Main menu")
	menuBtn.SetPrimary(true)
	menuBtn.SetMenuModel(a.buildMainMenu())
	header.PackEnd(menuBtn)

	// Settings is the last entry in the vault panel, where Windows 11 apps keep
	// it, rather than a gear up here. With the panel hidden it is still in the
	// menu and on Ctrl+,.

	newBtn := gtk.NewButtonFromIconName("atlasnotes-note-new-symbolic")
	newBtn.SetTooltipText("New note (Ctrl+N)")
	newBtn.ConnectClicked(a.actionNewNote)
	header.PackEnd(newBtn)

	a.rightToggle = gtk.NewToggleButton()
	// The assistant's own mark rather than a second sidebar arrow: the button
	// toggles the assistant, and the panel it opens carries the same shape.
	a.rightToggle.SetIconName(iconName("atlasnotes-assistant-symbolic", "atlasnotes-panel-right-symbolic"))
	a.rightToggle.SetActive(true)
	a.rightToggle.SetTooltipText("Show or hide the assistant (F10)")
	header.PackEnd(a.rightToggle)

	a.leftToggle = gtk.NewToggleButton()
	a.leftToggle.SetIconName("atlasnotes-panel-left-symbolic")
	a.leftToggle.SetActive(true)
	a.leftToggle.SetTooltipText("Show or hide the vault (F9)")
	header.PackEnd(a.leftToggle)

	// Left panel: Home at the top, the vault browser, and Settings at the
	// foot, as a Windows 11 app lays out its navigation.
	a.left = newPanel("left-panel")
	a.left.SetSizeRequest(leftMinWidth, -1)
	top, topRows := navList(struct {
		icon, label, tooltip string
		activate             func()
	}{"atlasnotes-home-symbolic", "Home", "Home screen (Ctrl+H)", a.showWelcome}, struct {
		icon, label, tooltip string
		activate             func()
	}{"atlasnotes-tasks-board-symbolic", "Tasks", "Every open task (Ctrl+J)", a.showTasks})
	a.homeNav, a.tasksNav = topRows[0], topRows[1]
	a.left.Append(top)
	if a.store != nil {
		a.tree = ui.NewTree(a.store, a.win, a.ai)
		a.tree.OnOpenNote = a.openNote
		a.tree.OnDeleted = a.onDeleted
		a.tree.OnMessage = a.toast
		a.tree.OnExport = a.exportNote
		a.tree.OnMoved = a.onMoved
		a.tree.OnRenamed = a.onRenamed
		a.tree.OnOpenSide = a.openToSide
		a.tree.OnMerge = a.mergeInto
		a.tree.OnBeforeRename = a.onBeforeRename
		a.tree.OnBeforeDelete = a.onBeforeDelete
		a.tree.OnLinksChanged = a.onLinksChanged
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
	foot, _ := navList(struct {
		icon, label, tooltip string
		activate             func()
	}{iconName("atlasnotes-settings-symbolic", "atlasnotes-menu-symbolic"), "Settings", "Settings (Ctrl+,)", a.showSettings})
	foot.AddCSSClass("atlas-nav-footer")
	a.left.Append(foot)

	mark("header+tree")

	// Center: welcome screen / editor, on the page layer.
	a.center = a.buildCenter()
	a.center.AddCSSClass("atlas-page")
	a.center.SetOverflow(gtk.OverflowHidden) // children are clipped to its rounded corners
	mark("center")
	a.watchNoteOpen() // needs the center stack and the header's title, both built by now

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

	inner.AddCSSClass("atlas-inner")

	outer := gtk.NewPaned(gtk.OrientationHorizontal)
	outer.AddCSSClass("atlas-shell")
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

	a.leftToggle.ConnectToggled(func() {
		a.left.SetVisible(a.leftToggle.Active())
		a.syncPageCorners()
	})
	a.rightToggle.ConnectToggled(func() {
		a.right.SetVisible(a.rightToggle.Active())
		a.syncPageCorners()
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
	toolbar.AddCSSClass("atlas-frame")
	toolbar.AddTopBar(header)
	toolbar.SetContent(a.toastOverlay)

	a.win.SetContent(toolbar)
	a.syncPageCorners()
	a.applyTransparency()
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
	notes.Append("New from Template…", "app.new-from-template")
	notes.Append("New Folder", "app.new-folder")
	notes.Append("Today's Note", "app.today")
	menu.AppendSection("", notes)

	current := gio.NewMenu()
	current.Append("Save Now", "app.save")
	current.Append("Rename…", "app.rename")
	current.Append("Version History…", "app.history")
	current.Append("Insert Image…", "app.insert-image")
	current.Append("Export…", "app.export")
	current.Append("Find a Note", "app.search")
	current.Append("Find in Note", "app.find")
	current.Append("Find and Replace", "app.find-replace")
	menu.AppendSection("", current)

	view := gio.NewMenu()
	view.Append("Lock Notes Now", "app.lock-now")
	view.Append("Home Screen", "app.home")
	view.Append("Tasks", "app.tasks")
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
	a.homeNav.setCurrent(note == "" && !a.tasksShowing())
	a.tasksNav.setCurrent(note == "" && a.tasksShowing())
	if a.win == nil || fixedWindowTitle {
		return
	}
	if note == "" {
		a.win.SetTitle("Atlas Notes")
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
