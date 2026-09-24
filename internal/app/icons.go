package app

import (
	"embed"
	"io/fs"
	"log"
	"os"
	"path/filepath"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-notes/internal/storage"
)

// Atlas Notes draws its own symbolic icons for the things the desktop's icon
// theme has no icon for — headings, inline code, a quote, a divider, and the
// assistant itself. Without them those buttons fell back to letters and
// punctuation, which sat next to the theme's icons at a different weight and
// size and made the toolbar look assembled from spare parts.
//
// They follow Adwaita's conventions (16px grid, one flat color, ~2px bars), so
// GTK recolors them with the rest and they match at any size.

//go:embed icons/*.svg
var iconFS embed.FS

// iconIndexTheme is the minimal theme description GTK needs to pick the icons
// out of the directory below.
const iconIndexTheme = `[Icon Theme]
Name=Atlas Notes
Comment=Symbolic icons bundled with Atlas Notes
Directories=scalable/actions

[scalable/actions]
Size=16
MinSize=8
MaxSize=512
Type=Scalable
Context=Actions
`

// iconCache memoizes icon-theme lookups. Each one is a query into GTK through
// cgo, and the toolbar and home screen ask about a dozen icons while the window
// is being built.
var iconCache = map[string]bool{}

// hasIcon reports whether the current icon theme can draw name.
func hasIcon(name string) bool {
	if known, ok := iconCache[name]; ok {
		return known
	}
	display := gdk.DisplayGetDefault()
	if display == nil {
		return false
	}
	known := gtk.IconThemeGetForDisplay(display).HasIcon(name)
	iconCache[name] = known
	return known
}

// iconName returns preferred when the icon theme has it, and fallback when it
// does not — bundled icons are unpacked at startup, but a theme can always
// surprise us.
func iconName(preferred, fallback string) string {
	if hasIcon(preferred) {
		return preferred
	}
	return fallback
}

// iconDir is where the bundled icons are unpacked: the app's own data
// directory, in the layout an icon theme has.
func iconDir() string { return filepath.Join(storage.DataDir(), "icons") }

// installIcons unpacks the bundled icons (once per version) and adds them to
// the icon theme, so the rest of the app can ask for them by name.
func installIcons() {
	dir := iconDir()
	if err := unpackIcons(dir); err != nil {
		log.Printf("atlas-notes: bundled icons: %v", err)
		return // the UI falls back to text labels for these buttons
	}
	display := gdk.DisplayGetDefault()
	if display == nil {
		return
	}
	gtk.IconThemeGetForDisplay(display).AddSearchPath(dir)
}

// unpackIcons writes the icons to disk when they are missing or stale. The
// stamp keeps an upgrade from rewriting files that are already current, so a
// normal launch costs one stat.
func unpackIcons(dir string) error {
	actions := filepath.Join(dir, "hicolor", "scalable", "actions")
	stamp := filepath.Join(dir, ".version")
	if current, err := os.ReadFile(stamp); err == nil && string(current) == version {
		return nil
	}
	if err := os.MkdirAll(actions, 0o755); err != nil {
		return err
	}
	entries, err := fs.ReadDir(iconFS, "icons")
	if err != nil {
		return err
	}
	for _, e := range entries {
		data, err := iconFS.ReadFile("icons/" + e.Name())
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(actions, e.Name()), data, 0o644); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "hicolor", "index.theme"), []byte(iconIndexTheme), 0o644); err != nil {
		return err
	}
	return os.WriteFile(stamp, []byte(version), 0o644)
}
