package app

import (
	"context"

	"github.com/diamondburned/gotk4/pkg/gio/v2"
)

// trashFile moves a file or folder to the system Trash, which is what GIO
// calls the freedesktop Trash on Linux and the Recycle Bin on Windows, so a
// deleted note can be restored from wherever the file manager keeps them.
//
// A restored note comes back into the vault as a file, and the next launch's
// scan picks it up: nothing about the index has to be undone.
func trashFile(path string) error {
	return gio.NewFileForPath(path).Trash(context.Background())
}

// TrashFile is trashFile for the parts of the program outside the window, such
// as "atlas-notes mcp": GIO's Trash needs no display.
func TrashFile(path string) error { return trashFile(path) }
