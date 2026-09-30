package app

import (
	"os"
	"path/filepath"
	"strings"
)

// Inside a Flatpak sandbox the app cannot rebuild itself (there is no compiler
// and the files are read-only) and no package manager owns it. `flatpak` owns
// it, from a bundle the person installed, so the Update button says how to
// replace that bundle.

// flatpakInfoPath exists in every Flatpak sandbox and nowhere else. It is a
// variable so tests can point it at a file.
var flatpakInfoPath = "/.flatpak-info"

// flatpakAppID is the application id the bundle is installed under.
const flatpakAppID = "io.github.atlasnotes"

// flatpakAppDir is where a Flatpak mounts the application's own files. Also a
// variable for tests.
var flatpakAppDir = "/app"

// inFlatpak reports whether this process is running inside a Flatpak sandbox
// as the packaged app. /.flatpak-info alone is not enough: a different program
// started from inside a sandbox (a terminal in a dev runtime, say) sees the same
// file, and must not be told it is the installed Atlas Notes. So the running
// binary has to live under /app as well.
func inFlatpak() bool {
	if _, err := os.Stat(flatpakInfoPath); err != nil {
		return false
	}
	exe, err := installedBinary()
	if err != nil {
		return false
	}
	return strings.HasPrefix(exe, filepath.Clean(flatpakAppDir)+string(filepath.Separator))
}

// flatpakAdvice is what the Update button says in a Flatpak.
func flatpakAdvice() string {
	return "Atlas Notes is installed as a Flatpak, so it cannot update itself. " +
		"To install the newest version over this one, run this in a terminal:\n" +
		"curl -fsSL " + installerURL + " | bash -s -- --update\n" +
		"Or download atlas-notes-<version>.flatpak from\n" + releasesURL +
		"\nand run: flatpak install --user ./atlas-notes-<version>.flatpak\n" +
		"Your notes are kept."
}
