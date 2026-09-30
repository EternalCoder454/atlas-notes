package app

import (
	"fmt"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-notes/internal/theme"
)

// Colour themes, the same ten Atlas Monitor has (internal/theme). The default
// follows the desktop; choosing one forces its light or dark scheme and
// overrides libadwaita's named colours, which is all the palette the
// stylesheet reads.

// loadThemeCSS installs the providers themes need: the pickers' circles, which
// never change, and the theme's own overrides, empty until applyTheme fills
// them. The overrides sit at APPLICATION+1, not USER: USER is where GTK loads
// the person's own gtk.css, which should still be able to override the app.
func (a *App) loadThemeCSS() {
	display := gdk.DisplayGetDefault()
	if display == nil {
		return
	}
	swatches := gtk.NewCSSProvider()
	swatches.LoadFromString(theme.SwatchCSS())
	gtk.StyleContextAddProviderForDisplay(display, swatches, gtk.STYLE_PROVIDER_PRIORITY_APPLICATION)

	a.themeCSS = gtk.NewCSSProvider()
	gtk.StyleContextAddProviderForDisplay(display, a.themeCSS, gtk.STYLE_PROVIDER_PRIORITY_APPLICATION+1)

	// The desktop's accent can change while the app runs, and a followed theme
	// takes it; the editor's links are drawn in it.
	if mgr := adw.StyleManagerGetDefault(); mgr != nil {
		mgr.NotifyProperty("accent-color-rgba", a.syncEditorAccent)
		mgr.NotifyProperty("dark", a.syncEditorAccent)
	}
}

// applyTheme puts the chosen theme into effect, live. It does nothing when the
// choice has not changed: reloading a provider restyles every widget in the
// window, whether or not a colour in it is different.
func (a *App) applyTheme() {
	mgr := adw.StyleManagerGetDefault()
	if mgr == nil || a.themeCSS == nil {
		return
	}
	want := a.cfg.Theme
	if theme.IsFollowing(want) {
		want = theme.Follow
	}
	if a.themeApplied && want == a.appliedTheme {
		return
	}
	a.themeApplied, a.appliedTheme = true, want

	css := ""
	switch {
	case theme.IsFollowing(want):
		mgr.SetColorScheme(adw.ColorSchemeDefault)
	default:
		t := theme.Resolve(want, mgr.Dark())
		if t.Dark {
			mgr.SetColorScheme(adw.ColorSchemeForceDark)
		} else {
			mgr.SetColorScheme(adw.ColorSchemeForceLight)
		}
		css = t.CSS()
	}
	a.themeCSS.LoadFromString(css)
	a.syncEditorAccent()
}

// syncEditorAccent gives the editor the accent to draw links, tags and web
// addresses in. They are text tags, which take a colour rather than a CSS
// name, so they are told: the chosen theme's accent, or the desktop's when the
// theme follows it.
func (a *App) syncEditorAccent() {
	if a.editor == nil {
		return
	}
	setColor := func(c string) {
		for _, e := range a.allEditors() {
			e.SetLinkColor(c)
		}
	}
	if t, ok := theme.ByID(a.cfg.Theme); ok && t.ID != "light" && t.ID != "dark" {
		setColor(t.LinkColor())
		return
	}
	mgr := adw.StyleManagerGetDefault()
	if mgr == nil {
		return
	}
	if c := mgr.AccentColorRGBA(); c != nil {
		setColor(fmt.Sprintf("#%02x%02x%02x",
			int(c.Red()*255+0.5), int(c.Green()*255+0.5), int(c.Blue()*255+0.5)))
	}
}
