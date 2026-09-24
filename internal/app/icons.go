package app

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-notes/internal/storage"
)

// Atlas Notes ships every icon it uses, rather than asking the desktop's theme
// for some of them. A toolbar drawn half from Material Symbols and half from
// whatever Adwaita, Papirus or Breeze happens to provide is a toolbar of
// mismatched weights and sizes; owning the set is the only way it looks like
// one set. They are Material Symbols, imported by scripts/import-icons.sh —
// see NOTICE for the licence and what the import changes.
//
// They follow the symbolic conventions GTK expects (16px nominal size, a
// single filled path, no strokes), so GTK recolors them with the rest of the
// interface and they match at any size.

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

// unpackIcons writes the icons to disk when what is there is not what is
// embedded. The stamp keeps a normal launch down to one read and one compare.
//
// It is a digest of the icons themselves rather than the app's version. Keyed
// on the version, an icon that changed without a release going out would never
// reach disk — which is every icon change during development, and any release
// that redraws an icon without bumping the number.
func unpackIcons(dir string) error {
	actions := filepath.Join(dir, "hicolor", "scalable", "actions")
	stampPath := filepath.Join(dir, ".stamp")

	entries, err := fs.ReadDir(iconFS, "icons")
	if err != nil {
		return err
	}
	want, err := iconStamp(entries)
	if err != nil {
		return err
	}
	if current, err := os.ReadFile(stampPath); err == nil && string(current) == want {
		return nil
	}

	// Start from an empty directory so a renamed icon does not leave its old
	// name behind, still resolving, for the rest of the install's life.
	if err := os.RemoveAll(actions); err != nil {
		return err
	}
	if err := os.MkdirAll(actions, 0o755); err != nil {
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
	// Older versions stamped the app version in a file of another name.
	os.Remove(filepath.Join(dir, ".version"))
	return os.WriteFile(stampPath, []byte(want), 0o644)
}

// iconStamp digests every embedded icon's name and contents. embed.FS lists
// entries in sorted order, so the result is stable across builds.
func iconStamp(entries []fs.DirEntry) (string, error) {
	h := sha256.New()
	for _, e := range entries {
		data, err := iconFS.ReadFile("icons/" + e.Name())
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s:%d:", e.Name(), len(data))
		h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
