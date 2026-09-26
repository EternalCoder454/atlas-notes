//go:build darwin

package storage

import (
	"os"
	"path/filepath"
)

// macOS keeps both under Application Support. Preferences is for plists that
// the system manages; a JSON file the app writes itself does not belong there.

const macAppDir = "Atlas Notes"

func platformDataDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Application Support", macAppDir)
}

func platformConfigDir() string { return platformDataDir() }
