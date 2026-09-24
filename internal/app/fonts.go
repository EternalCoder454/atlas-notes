package app

import (
	"log"
	"os"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-notes/internal/storage"
)

// Text rendering.
//
// GTK 4 renders glyphs unhinted with grayscale antialiasing by default, and it
// does not round font metrics. On a HiDPI screen (scale 2) that is invisible —
// there are enough pixels to hide it. At scale 1, which is what a 1080p display
// gets, the same settings make text look soft and unevenly spaced compared to
// Windows, where ClearType hints stems onto the pixel grid.
//
// So: on a low-density display, ask GTK to hint font metrics and to respect the
// desktop's own hinting/antialiasing preferences (GNOME defaults to slight
// hinting, which snaps stems without distorting shapes). On a HiDPI display the
// defaults already look right and hinting would only cost fidelity, so nothing
// is changed there.
//
// hidpiThreshold is the scale factor at and above which the display has enough
// pixels that unhinted rendering looks correct.
const hidpiThreshold = 2

// applyFontRendering tunes text rendering for the display Atlas Notes is on.
// cfg.FontRendering overrides the automatic choice.
func (a *App) applyFontRendering() {
	settings := gtk.SettingsGetDefault()
	if settings == nil {
		return
	}

	mode := a.cfg.FontRendering
	if mode == "" || mode == storage.FontRenderingAuto {
		if a.lowDensityWindow() {
			mode = storage.FontRenderingCrisp
		} else {
			mode = storage.FontRenderingSmooth
		}
	}
	if mode == a.fontMode {
		return // already applied; re-applying would churn every widget's layout
	}
	a.fontMode = mode

	switch mode {
	case storage.FontRenderingCrisp:
		// GTK 4.16+ decides hinting for itself unless font rendering is set to
		// manual; without this the xft settings below are ignored.
		setSetting(settings, "gtk-font-rendering", fontRenderingManual)
		// Hint stems onto the pixel grid and round font metrics, so glyphs and
		// line heights land on whole pixels.
		setSetting(settings, "gtk-hint-font-metrics", true)
		setSetting(settings, "gtk-xft-antialias", 1)
		setSetting(settings, "gtk-xft-hinting", 1)
		if style := desktopHintStyle(settings); style != "" {
			setSetting(settings, "gtk-xft-hintstyle", style)
		}
	case storage.FontRenderingSmooth:
		// Leave GTK's own defaults alone: unhinted, metrics unrounded.
		setSetting(settings, "gtk-font-rendering", fontRenderingAutomatic)
		setSetting(settings, "gtk-hint-font-metrics", false)
	}
	debugFontSettings(settings, mode)
}

// GtkFontRendering values (GTK 4.16+). gotk4 has no binding for the enum yet,
// so the numeric values are used directly.
const (
	fontRenderingAutomatic = 0
	fontRenderingManual    = 1
)

// debugFontSettings prints the resolved text-rendering settings when
// ATLAS_DEBUG_FONTS is set — the first thing to check on a report of soft or
// uneven text.
func debugFontSettings(settings *gtk.Settings, mode string) {
	if os.Getenv("ATLAS_DEBUG_FONTS") == "" {
		return
	}
	log.Printf("atlas-notes: font rendering mode=%s", mode)
	for _, name := range []string{
		"gtk-font-rendering", "gtk-hint-font-metrics", "gtk-xft-antialias",
		"gtk-xft-hinting", "gtk-xft-hintstyle", "gtk-xft-rgba", "gtk-xft-dpi",
		"gtk-font-name",
	} {
		log.Printf("  %-22s %v", name, settings.ObjectProperty(name))
	}
	if display := gdk.DisplayGetDefault(); display != nil {
		monitors := display.Monitors()
		for i := uint(0); i < monitors.NItems(); i++ {
			if m, ok := monitors.Item(i).Cast().(*gdk.Monitor); ok {
				g := m.Geometry()
				log.Printf("  monitor %d: %dx%d scale=%d (%.2f)", i, g.Width(), g.Height(), m.ScaleFactor(), m.Scale())
			}
		}
	}
}

// lowDensityWindow reports whether the screen the window is on needs hinted
// text. Once the window exists its own surface answers that exactly, which
// matters on a mixed setup: dragging the window from a HiDPI screen to a 1080p
// one re-runs this and switches rendering to match (see watchScaleChanges).
func (a *App) lowDensityWindow() bool {
	if a.win == nil {
		return lowDensityDisplay()
	}
	native := gtk.BaseWidget(a.win).Native()
	if native == nil {
		return lowDensityDisplay()
	}
	surface := native.Surface()
	if surface == nil {
		return lowDensityDisplay()
	}
	return gdk.BaseSurface(surface).ScaleFactor() < hidpiThreshold
}

// lowDensityDisplay reports whether any monitor is below the HiDPI threshold.
// It is the answer before there is a window to ask about.
func lowDensityDisplay() bool {
	display := gdk.DisplayGetDefault()
	if display == nil {
		return true
	}
	monitors := display.Monitors()
	n := monitors.NItems()
	if n == 0 {
		return true
	}
	for i := uint(0); i < n; i++ {
		obj := monitors.Item(i)
		if obj == nil {
			continue
		}
		monitor, ok := obj.Cast().(*gdk.Monitor)
		if !ok {
			continue
		}
		if monitor.ScaleFactor() < hidpiThreshold {
			return true
		}
	}
	return false
}

// watchScaleChanges re-applies the text settings when the window moves to a
// screen with a different pixel density.
func (a *App) watchScaleChanges() {
	if a.win == nil || a.cfg.FontRendering != storage.FontRenderingAuto {
		return
	}
	native := gtk.BaseWidget(a.win).Native()
	if native == nil {
		return
	}
	surface := native.Surface()
	if surface == nil {
		return
	}
	gdk.BaseSurface(surface).NotifyProperty("scale-factor", func() {
		a.applyFontRendering()
	})
}

// desktopHintStyle returns the hint style the desktop already asks for, so the
// app matches the rest of the session instead of imposing its own taste. It
// falls back to slight hinting, GNOME's default.
func desktopHintStyle(settings *gtk.Settings) string {
	if v, ok := settings.ObjectProperty("gtk-xft-hintstyle").(string); ok && v != "" && v != "hintnone" {
		return v
	}
	return "hintslight"
}

// setSetting writes a GtkSettings property, logging rather than failing when a
// GTK version doesn't have it.
func setSetting(settings *gtk.Settings, name string, value any) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("atlas-notes: font setting %s unavailable: %v", name, r)
		}
	}()
	settings.SetObjectProperty(name, value)
}
