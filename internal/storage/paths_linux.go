//go:build linux && !android

package storage

import (
	"os"
	"path/filepath"
)

// Desktop Linux follows the XDG base directory specification, which is what
// every existing install and every harness in this repository already uses.
// Nothing here changes where an existing vault lives.

func platformDataDir() string {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, AppName)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", AppName)
}

func platformConfigDir() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, AppName)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", AppName)
}
