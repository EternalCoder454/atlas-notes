//go:build windows

package storage

import (
	"os"
	"path/filepath"
)

// Windows keeps a user's own data under AppData. The vault goes in the local
// half, because it is a working store that no roaming profile should be
// dragging between machines, and the settings file goes in the roaming half,
// which is what it is for.

// windowsAppDir is the folder name shown in Explorer, so it is written the way
// a person would read it rather than as the executable's name.
const windowsAppDir = "Atlas Notes"

func platformDataDir() string {
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		return filepath.Join(local, windowsAppDir)
	}
	if dir, err := os.UserCacheDir(); err == nil {
		return filepath.Join(dir, windowsAppDir)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "AppData", "Local", windowsAppDir)
}

func platformConfigDir() string {
	if roaming := os.Getenv("APPDATA"); roaming != "" {
		return filepath.Join(roaming, windowsAppDir)
	}
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, windowsAppDir)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "AppData", "Roaming", windowsAppDir)
}
