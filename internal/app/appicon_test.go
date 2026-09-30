package app

import (
	"bytes"
	"os"
	"testing"
)

// The app's icon is embedded with the interface's own icons, so a build run
// from the source tree, which has not installed it, still shows its mark in
// the title bar. The copy must stay the icon the Makefile installs.
func TestEmbeddedAppIconMatchesTheInstalledOne(t *testing.T) {
	installed, err := os.ReadFile("../../assets/atlas-notes.svg")
	if err != nil {
		t.Fatal(err)
	}
	embedded, err := iconFS.ReadFile("icons/atlas-notes.svg")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(installed, embedded) {
		t.Fatal("internal/app/icons/atlas-notes.svg differs from assets/atlas-notes.svg; copy it over")
	}
}
