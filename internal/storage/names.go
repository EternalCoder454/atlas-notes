package storage

import (
	"os"
	"path/filepath"
)

// CleanNoteName is a note name as the store will use it: forward slashes, no
// empty, "." or ".." segments, and no space around segments. Something that
// decides whether a note exists, or what to call a new one, before asking the
// store to write it has to clean the name the same way, or it can decide "no
// such note" about a name the store then writes over an existing note with.
func CleanNoteName(rel string) string { return normalizeRel(rel) }

// VaultSettingsExist reports whether the vault has its settings file. A
// shared folder that a sync app is still filling may have notes before it has
// the file that says what format they are in; converting them to the default
// then would fight the device that chose another.
func (s *Store) VaultSettingsExist() bool {
	info, err := os.Stat(filepath.Join(s.VaultPath, vaultFileName))
	return err == nil && info.Mode().IsRegular()
}
