package app

import (
	"runtime"
	"strings"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-notes/internal/storage"
	"atlas-notes/internal/ui"
)

// The window's frame, laid out the way Windows 11 draws its own apps (and the
// way Atlas Monitor draws itself since 0.12): the title bar and the two side
// panels are one surface, and the note sits on a page of its own above it,
// rounded where they meet. The colours are all the theme's: the frame is
// @sidebar_bg_color and the page @view_bg_color. See "The frame" in
// assets/style.css.

// appIconName is the icon the installed app ships (Makefile: ICONDIR).
const appIconName = "atlas-notes"

// brandBox is the app's name at the start of the title bar: its mark, the
// name, and the version, quieter, after it.
//
// The mark only where the title bar does not already show one. A desktop can
// ask for the window's icon at the start of every title bar (KDE does, with
// the decoration layout "icon:minimize,maximize,close"), and GTK then draws it
// itself, which would put two side by side. The layout can change while the
// app runs, so the choice is made again when it does. A build run from the
// source tree has not installed its icon; the orb from the home screen stands
// in for it there, since GTK's picture of a missing icon would be worse.
func brandBox() *gtk.Box {
	box := gtk.NewBox(gtk.OrientationHorizontal, 8)
	box.AddCSSClass("atlas-brand")

	var mark gtk.Widgetter
	if hasIcon(appIconName) {
		img := gtk.NewImageFromIconName(appIconName)
		img.SetPixelSize(18)
		mark = img
	} else {
		mark = ui.NewStaticOrb(18)
	}
	box.Append(mark)
	if st := gtk.SettingsGetDefault(); st != nil {
		sync := func() {
			layout, _ := st.ObjectProperty("gtk-decoration-layout").(string)
			gtk.BaseWidget(mark).SetVisible(!decorationShowsIcon(layout))
		}
		sync()
		st.NotifyProperty("gtk-decoration-layout", sync)
	}

	name := gtk.NewLabel("Atlas Notes")
	name.AddCSSClass("atlas-brand-name")
	box.Append(name)
	v := gtk.NewLabel("v" + version)
	v.AddCSSClass("atlas-brand-version")
	box.Append(v)
	return box
}

// decorationShowsIcon reports whether a GTK decoration layout puts the window's
// icon at the start of the title bar, where the app's name goes: "icon" among
// the buttons before the colon.
func decorationShowsIcon(layout string) bool {
	start, _, _ := strings.Cut(layout, ":")
	for _, b := range strings.Split(start, ",") {
		if strings.TrimSpace(b) == "icon" {
			return true
		}
	}
	return false
}

// navRow is one entry of the side panel's navigation: an icon and a word, the
// height and shape of a Task Manager row. current marks it as where you are,
// which the stylesheet draws as the accent pill at its left edge.
type navRow struct {
	row *gtk.ListBoxRow
}

func (n navRow) setCurrent(on bool) {
	if n.row == nil {
		return
	}
	if on {
		n.row.AddCSSClass("atlas-current")
	} else {
		n.row.RemoveCSSClass("atlas-current")
	}
}

// navList builds a list of navigation rows. Selection is not used: which row
// is current is the app's to say (the home screen, say, is current whenever it
// is showing, however it was reached), so rows only activate.
func navList(rows ...struct {
	icon, label, tooltip string
	activate             func()
}) (*gtk.ListBox, []navRow) {
	list := gtk.NewListBox()
	list.AddCSSClass("navigation-sidebar")
	list.AddCSSClass("atlas-nav")
	list.SetSelectionMode(gtk.SelectionNone)
	out := make([]navRow, len(rows))
	acts := make([]func(), len(rows))
	for i, r := range rows {
		box := gtk.NewBox(gtk.OrientationHorizontal, 12)
		img := gtk.NewImageFromIconName(r.icon)
		box.Append(img)
		label := gtk.NewLabel(r.label)
		label.SetXAlign(0)
		label.SetHExpand(true)
		box.Append(label)
		row := gtk.NewListBoxRow()
		row.SetChild(box)
		row.SetActivatable(true)
		if r.tooltip != "" {
			row.SetTooltipText(r.tooltip)
		}
		list.Append(row)
		out[i] = navRow{row: row}
		acts[i] = r.activate
	}
	list.ConnectRowActivated(func(row *gtk.ListBoxRow) {
		if i := row.Index(); i >= 0 && i < len(acts) && acts[i] != nil {
			acts[i]()
		}
	})
	return list, out
}

// transparencyAvailable says whether the window can be made see-through, and
// if not, why, in words the Settings row can show. Windows is ruled out; the
// Windows build's GTK has no compositor to hand the translucency to. Elsewhere
// it takes a display that composites: without one there is nothing behind the
// window for its see-through parts to show.
func transparencyAvailable(goos string, composited bool) (bool, string) {
	if goos == "windows" {
		return false, "Not available on Windows"
	}
	if !composited {
		return false, "Needs a desktop that composites windows"
	}
	return true, ""
}

// TransparencyAvailable is transparencyAvailable for this machine. No display
// at all counts as not compositing.
func TransparencyAvailable() (bool, string) {
	composited := false
	if d := gdk.DisplayGetDefault(); d != nil {
		composited = d.IsComposited()
	}
	return transparencyAvailable(runtime.GOOS, composited)
}

// glassClasses are the style classes window transparency uses: the general
// one and one per level. All are listed so a change of level can clear the old
// one without remembering it.
var glassClasses = []string{"atlas-glass", "atlas-glass-subtle", "atlas-glass-medium", "atlas-glass-strong"}

func glassLevelClass(level string) string {
	switch storage.NormalizeTransparency(level) {
	case storage.TransparencySubtle:
		return "atlas-glass-subtle"
	case storage.TransparencyMedium:
		return "atlas-glass-medium"
	case storage.TransparencyStrong:
		return "atlas-glass-strong"
	}
	return ""
}

// applyTransparency makes the window see-through to the chosen level, or
// opaque again. It is all style classes on the window, and the stylesheet says
// what each means. Where the display cannot show it nothing is applied, and
// the saved choice is left alone, to take effect on a desktop that can. It
// does nothing when the level has not changed: adding or removing a class
// restyles the whole window.
func (a *App) applyTransparency() {
	if a.win == nil {
		return
	}
	want := glassLevelClass(a.cfg.WindowTransparency)
	if ok, _ := TransparencyAvailable(); !ok {
		want = ""
	}
	if want == a.appliedGlass {
		return
	}
	a.appliedGlass = want
	for _, c := range glassClasses {
		a.win.RemoveCSSClass(c)
	}
	if want != "" {
		a.win.AddCSSClass("atlas-glass")
		a.win.AddCSSClass(want)
	}
}

// syncPageCorners rounds the page where it meets the frame and squares it
// where it meets the window's edge: with the vault hidden the page reaches the
// left edge, and a rounded corner there would show a notch of frame colour.
func (a *App) syncPageCorners() {
	if a.center == nil {
		return
	}
	set := func(class string, on bool) {
		if on {
			a.center.AddCSSClass(class)
		} else {
			a.center.RemoveCSSClass(class)
		}
	}
	set("atlas-page-left", a.left != nil && a.left.Visible())
	set("atlas-page-right", a.right != nil && a.right.Visible())
}
