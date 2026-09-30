package app

import "os"

// Inside a Flatpak sandbox the app cannot rebuild itself (there is no compiler
// and the files are read-only) and no package manager owns it. `flatpak` owns
// it, from a bundle the person installed, so the Update button says how to
// replace that bundle.

// flatpakInfoPath exists in every Flatpak sandbox and nowhere else. It is a
// variable so tests can point it at a file.
var flatpakInfoPath = "/.flatpak-info"

// flatpakAppID is the application id the bundle is installed under.
const flatpakAppID = "io.github.atlasnotes"

// inFlatpak reports whether this process is running inside a Flatpak sandbox.
func inFlatpak() bool {
	_, err := os.Stat(flatpakInfoPath)
	return err == nil
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
